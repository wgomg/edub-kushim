# Developer Guide — The PostgreSQL of edub-kushim

This guide explains the PostgreSQL features used across the edub-kushim
database layer — especially the ones you don't meet in everyday CRUD SQL — and
how they are used *here*, with real snippets and file references. It is aimed at
developers who know SQL basics but are new to PostgreSQL's more specialized
features (full-text search, partial indexes, generated columns, LISTEN/NOTIFY,
LATERAL joins, upserts, identity columns).

It complements the other docs:

| Document | What it answers |
|---|---|
| `docs/reference/database.md` | The schema and query inventory |
| `golang.md` | The Go side (including the sqlc layer) |
| `frontend.md` | The SvelteKit side |
| `task-system.md` | How the schema powers task claims, leases, and dedup |
| **`postgresql.md` (this)** | *The PostgreSQL features themselves* |

**Where the SQL lives**: migrations and seeds in
`internal/database/sql/schema/`, queries in `internal/database/sql/queries/`
(hand-written, compiled to Go by sqlc), plus hand-written dynamic SQL in
`internal/database/structured_search.go` and `dashboard.go`, and DDL-ish
operations in `connection.go` / `dbtest.go`.

---

## Table of contents

1. [Orientation](#1-orientation)
2. [Identity columns: `GENERATED ALWAYS AS IDENTITY`](#2-identity-columns-generated-always-as-identity)
3. [Partial indexes](#3-partial-indexes)
4. [Generated columns and full-text search](#4-generated-columns-and-full-text-search)
5. [Full-text search queries: `@@`, `ts_rank`, `ts_headline`](#5-full-text-search-queries--ts_rank-ts_headline)
6. [Upserts: `ON CONFLICT` and `excluded`](#6-upserts-on-conflict-and-excluded)
7. [`RETURNING`](#7-returning)
8. [The optimistic claim pattern](#8-the-optimistic-claim-pattern)
9. [`LATERAL` joins and conditional aggregation](#9-lateral-joins-and-conditional-aggregation)
10. [Subqueries: scalar, `IN`, `EXISTS`](#10-subqueries-scalar-in-exists)
11. [`UNION ALL` with literal rows](#11-union-all-with-literal-rows)
12. [JSONB: documents-in-a-column](#12-jsonb-documents-in-a-column)
13. [SQL functions as gates](#13-sql-functions-as-gates)
14. [LISTEN/NOTIFY: the database as message bus](#14-listennotify-the-database-as-message-bus)
15. [Time arithmetic and dynamic intervals](#15-time-arithmetic-and-dynamic-intervals)
16. [Type casts](#16-type-casts)
17. [Migrations with goose](#17-migrations-with-goose)
18. [Admin operations from application code](#18-admin-operations-from-application-code)
19. [Aggregation idioms](#19-aggregation-idioms)
20. [Notably absent features (and why)](#20-notably-absent-features-and-why)
21. [Feature → file quick reference](#21-feature--file-quick-reference)
22. [Gotchas](#22-gotchas)

---

## 1. Orientation

- **PostgreSQL 16+** (test containers use `postgres:17`).
- **Driver**: `pgx` v5, used through the `database/sql` interface
  (`_ "github.com/jackc/pgx/v5/stdlib"`) for the app, and directly
  (`pgxpool`) for the LISTEN/NOTIFY listener.
- **sqlc** compiles the query files into type-safe Go (`sqlc generate` after
  editing any `*.sql` under `internal/database/sql/queries/`). Query files use
  `$1`, `$2`, ... placeholders; sqlc annotations (`:one`, `:many`, `:exec`,
  `:execrows`) appear in a comment above each query.
- **Migrations** are SQL files run by goose
  (`-- +goose Up` / `-- +goose Down` markers).
- **Every table has `TIMESTAMPTZ` timestamps** (timezone-aware, stored as UTC)
  defaulting to `CURRENT_TIMESTAMP` — never `TIMESTAMP` (which silently drops
  the timezone) and never `now()` as a string.

---

## 2. Identity columns: `GENERATED ALWAYS AS IDENTITY`

Every surrogate key in the schema is an identity column, not a `SERIAL`
(`internal/database/sql/schema/migrations/00001_baseline.sql:3`):

```sql
id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
```

Why this matters:

- `SERIAL` is a legacy shim that silently creates a sequence named
  `tablename_colname_seq`; identity is the SQL-standard way and stores the
  sequence *inside* the column definition.
- `GENERATED ALWAYS` means an explicit value in an `INSERT` is rejected unless
  you also write `OVERRIDING SYSTEM VALUE` — the application can't accidentally
  stomp on the sequence. (The test harness deliberately uses
  `ALTER TABLE ... ALTER COLUMN id RESTART WITH 1`, see §18.)
- Contrast with `batch.id TEXT PRIMARY KEY` (`00001_baseline.sql:101`) —
  business keys that come from outside (Go-generated UUIDs) are plain `TEXT`
  primary keys. Rule of thumb in this schema: **database-invented IDs are
  identity, externally-invented IDs are TEXT PKs.**

Also note `id int PRIMARY KEY DEFAULT 1 CHECK (id = 1)` in the `backup_lock`
table (`00005_backup_lock.sql:3`) — the **single-row table** pattern: a `CHECK`
that only ever allows the row with `id = 1` turns the table into a
single-row document usable as a lock/flag store.

---

## 3. Partial indexes

PostgreSQL indexes can carry a `WHERE` clause — the index only contains rows
matching the predicate. This is the codebase's favorite trick, used three ways.

### 1. Indexing only the rows queries actually touch

The task table can hold millions of rows, but workers only ever look at
`status = 'pending'`:

```sql
CREATE INDEX idx_task_pending ON task(created_at) WHERE status = 'pending';
CREATE INDEX idx_task_pending_type ON task(task_type, created_at) WHERE status = 'pending';
```

(00001_baseline.sql:125-126). The index is a fraction of the table's size, and
the planner uses it precisely when the query has the same `WHERE status =
'pending'` predicate.

### 2. A partial index for soft-delete

```sql
CREATE INDEX idx_document_deleted_at ON document(deleted_at) WHERE deleted_at IS NOT NULL;
```

(00007_trash_soft_delete.sql:3). Queries on live documents add
`deleted_at IS NULL` and never touch the trash rows in this index.

### 3. A partial UNIQUE index for conditional deduplication

The interesting one (`00001_baseline.sql:127-128`):

```sql
CREATE UNIQUE INDEX idx_task_dedup ON task(task_type, dedup_key)
    WHERE status IN ('pending', 'processing') AND dedup_key IS NOT NULL;
```

Uniqueness applies **only while the task is still live**. Once a task
completes or fails, the row drops out of the index and the same `dedup_key`
becomes insertable again — so "re-run this document" creates a new task while
"queue this document twice right now" collapses to one. Dedup is enforced by
the database, not by application logic.

**Rule**: a partial index only helps queries whose `WHERE` clause *implies*
the index's predicate. sqlc's dedup checks (`GetConfigTaskByDedupKey`, the
`DedupKey` interface in the task system) all carry the same status filters.

---

## 4. Generated columns and full-text search

`00002_tsvector.sql` shows a **stored generated column** — a column whose value
is computed by the database on every write, then physically stored:

```sql
ALTER TABLE document ADD COLUMN text_search_vector tsvector
  GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(text_content, ''))
  ) STORED;
```

- `tsvector` is the full-text search type: a lexeme vector of
  (word → positions) pairs. `to_tsvector(config, text)` parses and normalizes
  text into one.
- `'simple'` is the text-search configuration: **no stemming, no stopwords** —
  deliberately chosen because documents arrive in many languages (the pipeline
  detects the language per document), and language-specific dictionaries would
  mangle everything else.
- `coalesce(title, '') || ' ' || coalesce(text_content, '')` — full-text search
  is fed the concatenation of title + body.
- `STORED` (vs `VIRTUAL`) makes the value materialized on disk, which is what
  allows it to be indexed.

The vector stays in sync automatically — the app never writes it. If the
formula changes, a new migration must rewrite the column
(`ALTER TABLE ... ADD COLUMN ... GENERATED ALWAYS AS ...` in a fresh migration
recomputes existing rows).

The index comes separately, in its own migration
(`00003_tsvector_index.sql`):

```sql
-- +goose NO TRANSACTION
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_document_tsv
  ON document USING GIN (text_search_vector);
```

- **GIN** (Generalized Inverted Index) is the index type for `tsvector`,
  `jsonb`, and arrays — inverted-index semantics, exactly what FTS needs.
- `CONCURRENTLY` builds it without locking the table against writes — but it
  **cannot run inside a transaction**, hence the `-- +goose NO TRANSACTION`
  marker (goose otherwise wraps each migration in one).

---

## 5. Full-text search queries: `@@`, `ts_rank`, `ts_headline`

The dynamic search (`internal/database/structured_search.go:126-133`) is where
the FTS features come together:

```sql
ts_rank(d.text_search_vector, plainto_tsquery('simple', $1)) as rank,
COALESCE(ts_headline('simple', d.text_content, plainto_tsquery('simple', $1),
    'StartSel=<b>, StopSel=</b>, MaxWords=64, MinWords=32'), '') as snippet
FROM document d
WHERE d.text_search_vector @@ plainto_tsquery('simple', $1) AND d.deleted_at IS NULL
```

Three pieces worth knowing:

1. **`@@`** — the match operator: `tsvector @@ tsquery`. It's what the GIN
   index serves. The *left* side is the stored vector, the *right* side is the
   query built per request.
2. **`plainto_tsquery('simple', $1)`** — takes plain user text ("the quick
   brown") and produces a `tsquery` (ANDed lexemes). It applies the same
   `'simple'` normalization as the column, which is essential: `@@` only
   matches if both sides used the *same* config. (The alternative
   `to_tsquery` expects operators in the input and would break on user text;
   `websearch_to_tsquery` is the middle ground.) All three `$1` references
   reuse one parameter.
3. **`ts_rank(...)`** — relevance scoring for `ORDER BY rank`
   (`structured_search.go:171`); **`ts_headline(config, text, query, options)`**
   — extracts the best matching sentences and wraps matches in `<b>` tags via
   `StartSel`/`StopSel`, producing the snippet the UI shows. `COALESCE(..., '')`
   guards against NULL `text_content`.

The generated column guarantees `text_search_vector` is never out of sync with
the text, so the query can be pure and index-friendly.

---

## 6. Upserts: `ON CONFLICT` and `excluded`

The "insert, but only if absent" pattern appears in every creation path —
seeds, tags, people, document tags, batch owners. The two forms:

**Do nothing** (`internal/database/sql/queries/tag.sql:17-20`):

```sql
INSERT INTO tag (name)
VALUES ($1)
ON CONFLICT (name) DO NOTHING
RETURNING id;
```

Note `RETURNING id` still fires on the *conflict* path too — but returns no
rows. That's how the Go layer detects "already existed" (`RowsAffected`/row
scan semantics via sqlc's `:one`).

**Do update** — the batch-owner takeover (`batch.sql:24-31`):

```sql
INSERT INTO batch_owner (batch_id, owner_id, pid, acquired_at, last_heartbeat)
VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(batch_id) DO UPDATE SET
  owner_id = excluded.owner_id,
  pid = excluded.pid,
  acquired_at = CURRENT_TIMESTAMP,
  last_heartbeat = CURRENT_TIMESTAMP;
```

`excluded` is the pseudo-table of the row that *would have been* inserted —
the idiomatic way to reference the incoming values inside `DO UPDATE`. The
multi-column composite key `(document_id, people_id, people_type_id)`
(`document_people.sql:24`) shows `ON CONFLICT` on composite keys works the
same way.

**Prerequisite**: `ON CONFLICT` only works against a unique index/constraint
matching the conflict target — which is why every target column is `UNIQUE`
or part of a PK. `DO NOTHING` without a target list (the seeds) just means
"any unique violation is fine, skip".

The seeds are the canonical idempotency pattern: `INSERT ... VALUES (…),
(…), … ON CONFLICT (name) DO NOTHING` — 100+ tags, 9 document types, 15
people types — safe to run on every boot (they are, via
`InitializeSchema`).

---

## 7. `RETURNING`

Every insert ends with `RETURNING id` (e.g. `document.sql:22`,
`task.sql:113`, `orphaned.sql:3`). This is the single round-trip "insert and
get the generated identity back" — no separate `SELECT`. sqlc maps it to
`(int64, error)`. With `:one` queries, no rows back (conflict path) is
reported as `sql.ErrNoRows` on the Go side — the documented way to detect a
no-op upsert.

`RETURNING` works with all DML, not just INSERT — handy in `DELETE ... RETURNING`
for audit trails (not used here, but legal).

---

## 8. The optimistic claim pattern

How do multiple worker processes claim tasks without double-processing? Not
with locks — with a **guarded UPDATE**, which is atomic at the row level
(`task.sql:99-104`):

```sql
-- name: ClaimTask :execrows
UPDATE task SET
    status = 'processing',
    started_at = CURRENT_TIMESTAMP
WHERE id = $1 AND status = 'pending';
```

The `WHERE ... AND status = 'pending'` is the lock: exactly one of N racing
workers can flip `pending → processing` for a given row; the others affect 0
rows. The Go side checks `RowsAffected` (that's what sqlc's `:execrows`
annotation is for) and treats 0 as "someone else took it".

The same pattern drives **batch ownership**: the queue daemon pre-inserts a
placeholder owner row, then the child worker takes over with a two-step
acquire — `TryInsertBatchOwner` (`INSERT ... ON CONFLICT DO NOTHING`) followed
by `UpdateBatchOwnerIfStale` (guarded UPDATE), and `AcquireBatchOwnerForce`
(the `excluded` upsert from §6) for forced takeover. Heartbeats keep the lease
alive (`HeartbeatBatchOwner` updates `last_heartbeat`); any process that stops
heartbeating for 15 seconds becomes a stale owner (`ListStaleBatchOwners`
uses `last_heartbeat < CURRENT_TIMESTAMP - INTERVAL '15 seconds'`) and gets
reclaimed. Compare this to `SELECT ... FOR UPDATE SKIP LOCKED` — see §20 for
why the codebase chose the UPDATE-guard instead.

---

## 9. `LATERAL` joins and conditional aggregation

The batch overview query (`batch.sql:137-166`) is the most advanced statement
in the codebase. It computes per-batch task statistics with a `LATERAL`
subquery:

```sql
SELECT
    b.id AS batch_id,
    b.source,
    ...
    COALESCE(sub.total, 0) AS total,
    COALESCE(sub.waiting, 0) AS waiting,
    ...
    bo.last_heartbeat AS owner_last_heartbeat,
    bo.pid AS owner_pid
FROM batch b
LEFT JOIN LATERAL (
    SELECT
        COUNT(*) AS total,
        SUM(CASE WHEN t.status = 'waiting'   THEN 1 ELSE 0 END) AS waiting,
        SUM(CASE WHEN t.status = 'pending'   THEN 1 ELSE 0 END) AS pending,
        SUM(CASE WHEN t.status = 'processing' THEN 1 ELSE 0 END) AS processing,
        SUM(CASE WHEN t.status = 'completed' THEN 1 ELSE 0 END) AS completed,
        SUM(CASE WHEN t.status = 'failed'    THEN 1 ELSE 0 END) AS failed,
        SUM(CASE WHEN t.status = 'cancelled' THEN 1 ELSE 0 END) AS cancelled,
        SUM(CASE WHEN t.status = 'discarded' THEN 1 ELSE 0 END) AS discarded,
        MIN(t.started_at) AS first_started_at,
        MAX(t.completed_at) AS last_completed_at
    FROM task t WHERE t.batch_id = b.id
) sub ON true
LEFT JOIN batch_owner bo ON bo.batch_id = b.id
ORDER BY b.created_at DESC
```

Three features in one query:

1. **`LATERAL`** — lets a subquery in `FROM` reference columns of preceding
   tables (`t.batch_id = b.id`). It runs *per batch row*, like a correlated
   subquery, but can return multiple rows/aggregates. This is the standard
   "per-parent summary" tool.
2. **`LEFT JOIN ... ON true`** — the `ON true` is the idiom for "always join,
   even if the lateral produced nothing" — batches with zero tasks still
   appear with `COALESCE(sub.total, 0)`.
3. **Conditional aggregation** — `SUM(CASE WHEN t.status = 'completed' THEN 1
   ELSE 0 END)` is the classic "count rows matching a condition inside a
   GROUP BY-less aggregate" pattern (a histogram in one pass over the batch's
   tasks). Newer PostgreSQL has a `FILTER (WHERE ...)` clause for the same
   job — see §20.

---

## 10. Subqueries: scalar, `IN`, `EXISTS`

The codebase uses all three subquery flavors:

**Scalar subqueries in the SELECT list** — three independent counts in one
round trip (`dashboard.go:242-245`):

```sql
SELECT
    (SELECT COUNT(*) FROM document WHERE (language = 'und' OR language = '') AND deleted_at IS NULL) AS missing_language,
    (SELECT COUNT(*) FROM document WHERE document_type_id = 1 AND deleted_at IS NULL) AS missing_type,
    (SELECT COUNT(*) FROM document d WHERE d.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.document_id = d.id)) AS missing_tags
```

**`IN (subquery)`** — the structured search's tag filter
(`structured_search.go:142-146`):

```sql
AND d.id IN (
    SELECT dt.document_id FROM document_tag dt
    JOIN tag t ON dt.tag_id = t.id
    WHERE t.name IN ($1, $2, ...)
)
```

**`NOT EXISTS` (correlated)** — "documents with no tags":

```sql
AND NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.document_id = d.id)
```

`EXISTS`/`NOT EXISTS` is preferred over `NOT IN (subquery)` here — correct
`NULL` semantics and early-exit evaluation. The batch cleanup shows the
correlated form in a `DELETE` (`batch.sql:71-80`):

```sql
DELETE FROM batch_owner WHERE batch_id IN (
  SELECT b.batch_id FROM batch_owner b
  WHERE NOT EXISTS (
    SELECT 1 FROM task t WHERE t.batch_id = b.batch_id
      AND t.status IN ('pending', 'processing', 'waiting')
  )
);
```

(Note the `b` alias inside the subquery to disambiguate the outer table.)

---

## 11. `UNION ALL` with literal rows

Merging heterogeneous sources into one feed: each `UNION ALL` branch selects
the same column shape, with **literal constant columns** filling in fields a
source doesn't have — the SQL-level equivalent of a discriminated union:

```sql
SELECT 'document_uploaded' AS event_type,
       d.created_at AS event_time,
       d.title AS title,
       '' AS payload_file_path,
       d.document_id AS ref_id,
       '' AS batch_id,
       '' AS task_id
FROM document d
WHERE d.deleted_at IS NULL

UNION ALL

SELECT CASE WHEN t.status = 'completed' THEN 'task_completed' ELSE 'task_failed' END,
       t.completed_at,
       COALESCE(t.payload->>'file_name', ''),
       ...
FROM task t
WHERE t.status IN ('completed', 'failed') AND t.completed_at IS NOT NULL

UNION ALL
-- batch_created events...

ORDER BY event_time DESC
LIMIT 30
```

- Each branch defines the same column shape; literals (`'document_uploaded'`,
  `''`) fill in columns a source doesn't have — the SQL-level equivalent of a
  discriminated union. The `CASE WHEN` in the second branch derives the event
  type from data.
- `UNION ALL` (not `UNION`) keeps duplicates — events are never deduplicated
  away.
- The trailing `ORDER BY ... LIMIT 30` applies to the *whole* union.

---

## 12. JSONB: documents-in-a-column

The `task` table stores arbitrary per-task data in a JSONB column
(`00001_baseline.sql:46-47`):

```sql
payload JSONB,
result JSONB,
```

The pipeline writes task-specific payloads (consume: file path + document id;
enrich: document id + LLM options) into the same table — JSONB is PostgreSQL's
validated, binary-stored, indexable JSON. What the SQL uses it for:

**Extraction with `->>`** (get a field as text; e.g. `RestoreDiscardedEnrichTasks`, `task.sql:206`):

```sql
AND (e.task_id = c.payload->>'on_completed' OR e.task_id = c.payload->>'on_completed_thumbnail');
```

`->>` returns text (NULL if missing); `->` returns JSON.
The Go side declares these columns as `*json.RawMessage` via the sqlc
override in `sqlc.yaml`, so handlers pass payloads through untouched and only
the SQL reads inside them.

JSONB is *not* used for search — text lives in `text_content` (full-text
indexed) — which keeps payloads for machine data only.

---

## 13. SQL functions as gates

Plain SQL functions (not triggers) are used to centralize business conditions
so they can be reused across queries.

The backup gate (`00005_backup_lock.sql:12-18` + `task.sql:35-40`):

```sql
CREATE OR REPLACE FUNCTION is_backup_running() RETURNS boolean AS $$
BEGIN
  RETURN EXISTS (
    SELECT 1 FROM backup_lock
    WHERE id = 1 AND running = true AND started_at > NOW() - INTERVAL '30 minutes'
  );
END;
$$ LANGUAGE plpgsql;
```

```sql
-- name: GetNextPendingTaskOfTypeWithGate :one
SELECT id FROM task
WHERE status = 'pending' AND task_type = $1
  AND NOT is_backup_running()
ORDER BY created_at LIMIT 1;
```

The function encodes the lock's *staleness rule* in one place; every worker
poll (and the backup task itself) consults it. The lock is acquired with a
conditional UPDATE on the single-row table (§2) — an atomic
compare-and-swap:

```sql
UPDATE backup_lock SET running = true, started_at = NOW()
WHERE id = 1 AND (NOT running OR started_at <= NOW() - INTERVAL '30 minutes');
```

Zero rows affected = the lock was already held — same `:execrows` discipline
as the task claim (§8).

---

## 14. LISTEN/NOTIFY: the database as message bus

The queue daemon doesn't poll Postgres blindly — it is woken up by the
database. Schema side (`00004_listen_notify.sql`, a PL/pgSQL trigger):

```sql
CREATE OR REPLACE FUNCTION notify_batch_queued()
RETURNS trigger AS $$
BEGIN
  IF NEW.status = 'queued' THEN
    PERFORM pg_notify('batch_queued', NEW.id);
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER batch_queued_notify
AFTER INSERT OR UPDATE ON batch
FOR EACH ROW EXECUTE FUNCTION notify_batch_queued();
```

- **`pg_notify(channel, payload)`** posts a notification; `PERFORM` is the
  PL/pgSQL way to call a function whose result you discard.
- **`AFTER INSERT OR UPDATE ... FOR EACH ROW EXECUTE FUNCTION`** — the modern
  trigger syntax (the old `EXECUTE PROCEDURE` is deprecated). The trigger
  fires for both new batches and requeues (`NEW.status = 'queued'` covers the
  UPDATE path).

Client side (`internal/commands/notify.go`) — a **dedicated single-connection
pool** (LISTEN state is per-connection, so it must never be shared):

```go
poolCfg.MinConns = 1
poolCfg.MaxConns = 1

conn.Exec(ctx, "LISTEN batch_queued")
// ...
_, err := conn.Conn().WaitForNotification(ctx)
```

`WaitForNotification` blocks until a notification arrives (or the context
cancels); the daemon then re-checks the database. Notifications are a *hint*,
not the source of truth — the Go side drops them when busy (non-blocking send
on a buffered channel, `notify.go:68-71`) and a 30-second safety timer in the
daemon re-polls regardless. If the listener dies, the loop reconnects with
backoff (`LISTEN` again after `Acquire`).

Caveats baked into the design: notifications are not delivered to
transactions, are lost when no listener is connected, and there is no
guaranteed ordering — hence "hint, not contract".

---

## 15. Time arithmetic and dynamic intervals

**Static intervals** — the staleness probes:

```sql
WHERE last_heartbeat < CURRENT_TIMESTAMP - INTERVAL '15 seconds'
WHERE completed_at >= CURRENT_TIMESTAMP - INTERVAL '7 days'
WHERE started_at > NOW() - INTERVAL '30 minutes'
```

`CURRENT_TIMESTAMP` and `NOW()` are the same transaction timestamp; both are
`timestamptz`, so comparisons across timezones just work.

**Parameterized intervals** — when the interval duration comes from config,
the codebase builds it with a cast of a concatenated string (`task.sql:233`):

```sql
-- name: CountRecentBackupTasks :one
SELECT COUNT(*) FROM task
WHERE task_type = 'backup' AND created_at > NOW() - ($1 || ' minutes')::INTERVAL;
```

`($1 || ' minutes')::INTERVAL` turns the bound parameter `'30'` into
`INTERVAL '30 minutes'`. The trash purge does the same with an explicit text
cast first: `($1::text || ' days')::INTERVAL` (`document.sql:149`). The
parameter stays a parameter — no string interpolation into SQL.

**Duration math** — average task duration in milliseconds
(`dashboard.go:274-276`):

```sql
CAST(COALESCE(AVG(
    EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000
), 0.0) AS INTEGER) AS avg_duration_ms
```

`EXTRACT(EPOCH FROM interval)` converts an interval to seconds (fractional);
timestamps subtract to an interval directly. This is the standard "elapsed
time" idiom.

---

## 16. Type casts

Casts appear in three flavors, all with the same meaning:

```sql
CAST(COALESCE(SUM(file_size), 0) AS BIGINT) AS total_bytes   -- document.sql:119
($1::text || ' days')::INTERVAL                              -- document.sql:149
```

- `CAST(x AS t)` — standard SQL.
- `x::t` — PostgreSQL's shorthand.
- `'literal'::t` — cast a literal; `$1::text` — cast a parameter.

Why they're needed: `SUM(bigint)` can overflow to `numeric`, and
`COALESCE(SUM(...), 0)` infers `numeric`; without the `AS BIGINT` cast the Go
scanner would receive a `numeric` string instead of an `int64`. `$1::text` is
needed because `||` between an unknown-typed parameter and a string literal
can be ambiguous.

---

## 17. Migrations with goose

Each migration is a file with `-- +goose Up` / `-- +goose Down` blocks
(annotations in SQL comments). The project uses the less common annotations
too:

```sql
-- +goose NO TRANSACTION          -- 00003_tsvector_index.sql:1
```

Required because `CREATE INDEX CONCURRENTLY` cannot run inside a transaction
(goose wraps each migration in one by default).

```sql
-- +goose StatementBegin          -- 00004_listen_notify.sql:4
CREATE OR REPLACE FUNCTION ...
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
```

Required for multi-statement bodies with semicolons inside (`$$ ... $$` is
dollar-quoting, which keeps the PL/pgSQL body free of quote-escaping — note
the function body contains `;`s that must not split the statement).

Migration hygiene in this repo: **never edit an applied migration** — add a
new numbered file (00007 renamed columns with `ALTER TABLE ... RENAME COLUMN`
instead of editing 00001; 00002 and 00003 split the generated column from its
index so the CONCURRENTLY build can run standalone). Every `Up` has a `Down`
that drops what was created, and `ALTER TABLE ... ADD COLUMN IF EXISTS`-style
guards appear where reruns are plausible.

---

## 18. Admin operations from application code

The app talks to the `postgres` system catalog and runs DDL at boot and in
tests — features you rarely see in application SQL.

**Auto-create the database** (`internal/database/connection.go:36-59`): connect
to the maintenance DB, check `pg_database`, create if missing:

```sql
SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)
CREATE DATABASE "name"          -- identifier quoted via quoteIdent()
```

`CREATE DATABASE` cannot be parameterized (it's DDL), so the name is
double-quote-escaped (`"` → `""`) before interpolation — the injection-safe
way to interpolate an identifier, contrasted with parameterizing *values*.

**Force-drop test databases** (`internal/database/dbtest.go:84`):

```sql
DROP DATABASE IF EXISTS edub_test_database WITH (FORCE)
```

`WITH (FORCE)` (PostgreSQL 13+) terminates any lingering connections to the
database and drops it — without it, a leaked connection makes the DROP fail
with "database is being accessed by other users". That's how the per-package
test harness guarantees cleanup.

**Reset identity sequences in tests** (`dbtest.go:178`):

```sql
ALTER TABLE tag ALTER COLUMN id RESTART WITH 1
```

Because the IDs are identity columns (§2), tests reset them with this instead
of fiddling with `setval()` on a separate sequence.

---

## 19. Aggregation idioms

The recurring aggregate shapes, all in `dashboard.go` and the count queries:

- **`COUNT(*)` with `GROUP BY`** — histograms: `SELECT status, COUNT(*) FROM task GROUP BY status`
  (task.sql:203), `GROUP BY original_type ORDER BY total_bytes DESC`.
- **`GROUP BY` on a column alias**: `GROUP BY day` where `day` is
  `date(created_at)` (`dashboard.go:48`) — Postgres allows grouping by
  output-column aliases, keeping the query readable.
- **`COUNT(DISTINCT x)`**: `COUNT(DISTINCT batch_id)` (task.sql:199).
- **`GROUP BY ... ORDER BY aggregate`**: `GROUP BY batch_id ORDER BY
  MAX(created_at) DESC` (task.sql:174) — "most recently active first".
- **`COUNT(*) ... LEFT JOIN ... ON ... AND`** — counting with a join-time
  predicate so the count only includes live documents
  (`document_type.sql:35-38`): `LEFT JOIN document d ON dt.id =
  d.document_type_id AND d.deleted_at IS NULL` then `COUNT(d.id)` — rows with
  no live document contribute 0, not 1.
- **`COALESCE(SUM(x), 0)`** everywhere — aggregates on empty sets return
  NULL, and the app's `int64` fields can't hold NULL.

---

## 20. Notably absent features (and why)

Knowing what *isn't* here is as instructive as what is:

| Feature | Why it's absent |
|---|---|
| `SELECT ... FOR UPDATE SKIP LOCKED` | The task claim uses an atomic guarded UPDATE (`WHERE status='pending'` + `RowsAffected`) instead — one statement, no row lock held, no explicit unlock, works identically across retries. See §8. |
| `pg_advisory_lock` | Backup mutual exclusion uses a **single-row table lock** (§13) instead — visible/queryable state, staleness rule in a SQL function, and no session-lifetime ownership to leak. |
| `pg_trgm` / `ILIKE` / `LIKE`-with-`%`-prefix | Name search is `LIKE $1` with the caller appending `%` (prefix match only) — an index-friendly pattern. Fuzzy/substring search would need trigram indexes, which aren't needed. |
| `FILTER (WHERE ...)` | The codebase predates it or simply prefers portability: `SUM(CASE WHEN ... THEN 1 ELSE 0 END)` is the classic form that works everywhere. |
| `gen_random_uuid()` | UUIDs are generated in Go (`google/uuid`) and passed in as TEXT PKs — keeps the DB layer free of pgcrypto and the IDs available before the insert. |
| Enum types (`CREATE TYPE ... AS ENUM`) | Statuses/roles are TEXT + `CHECK (col IN (...))` — adding a value is a plain migration, not an `ALTER TYPE` (which is awkwardly transactional in older versions). |
| `date_trunc` | Dashboard day-bucketing uses `date(created_at)` — enough for daily granularity. |
| Window functions (`OVER (...)`) | The batch overview's per-group aggregates fit `LATERAL` + `GROUP BY`; no running-total/partition-rank need has appeared. |
| Full-text search on `jsonb` | FTS targets `text_content` only; JSONB payloads are machine data. |
| `SERIAL` | Identity columns everywhere (§2). |
| `TIMESTAMP` (without time zone) | `TIMESTAMPTZ` everywhere — never store local-wall-clock. |

---

## 21. Feature → file quick reference

| Feature | Where |
|---|---|
| Identity columns, partial indexes, composite PKs, CHECKs | `sql/schema/migrations/00001_baseline.sql` |
| Generated tsvector column | `sql/schema/migrations/00002_tsvector.sql` |
| GIN + CONCURRENTLY + NO TRANSACTION | `sql/schema/migrations/00003_tsvector_index.sql` |
| Trigger + `pg_notify` | `sql/schema/migrations/00004_listen_notify.sql` + `internal/commands/notify.go` |
| Single-row lock table + SQL gate function | `sql/schema/migrations/00005_backup_lock.sql`, `sql/queries/backup_lock.sql`, `sql/queries/task.sql:35-40` |
| Soft delete + partial index | `sql/schema/migrations/00007_trash_soft_delete.sql` |
| Idempotent multi-row seeds with `ON CONFLICT` | `sql/schema/seed-*.sql` |
| Upsert + `excluded` | `sql/queries/batch.sql:24-31`, `tag.sql:17-20`, `people.sql`, `document_tag.sql` |
| Optimistic claim (`:execrows`) | `sql/queries/task.sql:99-104` |
| LATERAL + conditional aggregation | `sql/queries/batch.sql:137-166` |
| Scalar subqueries, `NOT EXISTS`, `UNION ALL` literals | `internal/database/dashboard.go:83-120,239-248` |
| JSONB `->>` extraction | `internal/database/dashboard.go:98-100` |
| FTS `@@`, `ts_rank`, `ts_headline`, dynamic WHERE building | `internal/database/structured_search.go` |
| Dynamic intervals `($1::text || ' days')::INTERVAL` | `sql/queries/task.sql:233`, `document.sql:149` |
| `EXTRACT(EPOCH ...)` duration | `internal/database/dashboard.go:271-284` |
| `pg_database` + `CREATE DATABASE` + quoting | `internal/database/connection.go:36-63` |
| `DROP DATABASE ... WITH (FORCE)`, `RESTART WITH 1` | `internal/database/dbtest.go:76-85,177-179` |
| goose annotations | `sql/schema/migrations/*.sql` (Up/Down, StatementBegin/End, NO TRANSACTION) |

---

## 22. Gotchas

- **`ON CONFLICT` requires a matching unique index.** `DO NOTHING` without a
  target works on any unique violation; `ON CONFLICT (cols)` fails at runtime
  if no unique constraint covers those columns.
- **Partial index predicates must be implied by the query.** An index on
  `task(created_at) WHERE status='pending'` is useless to a query without the
  `status = 'pending'` condition. Keep the two in lockstep.
- **`@@` requires matching configs.** `to_tsvector('simple', ...)` only
  matches `plainto_tsquery('simple', ...)` — mix `'english'` on one side and
  the match silently misses. Same rule for `ts_headline`/`ts_rank` configs.
- **`CREATE INDEX CONCURRENTLY` can't run in a transaction** (hence
  `-- +goose NO TRANSACTION`), and it can leave an `INVALID` index behind if
  the build is interrupted — that's why the migration uses
  `IF NOT EXISTS` plus the separate generated-column migration.
- **`SERIAL` and identity are not interchangeable in the catalog** — `ALTER
  COLUMN id RESTART WITH 1` works for identity; `setval('seq')` is the SERIAL
  way. The test harness uses the identity form.
- **`GENERATED ALWAYS` columns reject explicit inserts** (`OVERRIDING SYSTEM
  VALUE` needed) — by design, and a common "why is my insert failing" moment.
- **`DROP DATABASE` can't run inside a transaction** and fails on live
  connections — `WITH (FORCE)` handles the second, an autocommit Exec the
  first.
- **`RETURNING` on a conflicting upsert returns no row** — detect "already
  existed" via `sql.ErrNoRows`, not by parsing the conflict.
- **`NOW()`/`CURRENT_TIMESTAMP` are transaction-stable**: identical for the
  whole transaction, which is what the heartbeat/backup-lock logic relies on.
  For per-row "clock" time you'd need `clock_timestamp()`.
- **`||` with a parameter needs a cast** (`$1::text`) — the concat operator
  can't infer the parameter's type from the literal side.
- **`->>` yields NULL for missing keys** — wrap in `COALESCE` before
  concatenating or comparing.
- **`UNION` (without ALL) dedups** — the timeline uses `UNION ALL` because
  events are never duplicates.
- **Grouping by an alias** (`GROUP BY day`) is PostgreSQL-specific but legal;
  don't use it if you ever need the query to be portable.
- **sqlc placeholders are `$1`-based**, not `?` — and the dynamic builder in
  `structured_search.go` must count them carefully (`nextIndex()` walks
  `len(args)+1`) because the same filter query is assembled twice (rows +
  count) with identical numbering.
- **`EXECUTE FUNCTION` is the current trigger syntax**; `EXECUTE PROCEDURE`
  still parses but is deprecated in PostgreSQL 18+.

---

*Last verified against the tree: 2026-08-03. If code and doc disagree, code wins.*
