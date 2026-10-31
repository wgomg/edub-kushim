# edub-kushim — API Guide

The server listens on `http://localhost:3000` by default (`server.port` in the configuration).
Everything below is relative to that base URL.

**This document vs the generated reference.** [docs/api.md](../api.md) is the canonical,
generated endpoint reference: it is rendered from the OpenAPI specification and lists every
endpoint with its parameters, request/response schemas and status codes
([ADR 0007](../adr/0007-api-spec-follows-the-code.md); regenerate with `make api-docs`, never
edit by hand). This guide carries what generation cannot express: how to authenticate, how to
chain endpoints into workflows, and the conventions a client must honor. Look up *what exists*
in the generated reference; use this guide to *make it work*.

## Authentication

### Log in for a session token

```bash
curl -s http://localhost:3000/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username": "admin", "password": "secret123!"}'
```

Response `200`:

```json
{
  "token": "eyJhbGciOi…",
  "user": { "id": 1, "username": "admin", "role": "admin", "created_at": "2026-06-26T12:00:00Z" }
}
```

Send the token on every subsequent request:

```bash
curl -s http://localhost:3000/api/v1/me -H 'Authorization: Bearer eyJhbGciOi…'
```

A failed login answers `401` with `{"error": "invalid username or password"}`. The same
response also sets an `edub_token` HttpOnly cookie (`MaxAge` 86400, `SameSite=Lax`, `Secure`
outside development): when a request carries **no** `Authorization` header, the server falls
back to that cookie, which is how the browser session works. Scripts should prefer the header.

### Use a personal API key

API keys are long-lived credentials for scripts. They use the same `Authorization: Bearer`
header — the `ek_` prefix tells the server to validate them as keys instead of JWTs.

```bash
curl -s -X POST http://localhost:3000/api/v1/me/api-key \
  -H 'Authorization: Bearer <your-session-token>'
```

Response `201`:

```json
{
  "api_key": "ek_1f2e3d4c5b6a79887766554433221100ffeeddccbbaa99887766554433221100",
  "prefix": "ek_1f2e3d4c5",
  "message": "Save this API key — it will not be shown again"
}
```

The raw key is shown **once**; the server stores only its SHA-256 hash.

| Endpoint | Effect |
|---|---|
| `POST /api/v1/me/api-key` | Mint a key for the calling user (`201`) |
| `PUT /api/v1/me/api-key` | Rotate — atomically replaces the key, revoking the previous one (`200`) |
| `DELETE /api/v1/me/api-key` | Revoke (`204`) |
| `GET /api/v1/me/api-key` | Whether a key exists, its prefix and creation date |

Admins manage other users' keys under `/api/v1/users/{id}/api-key` (same verbs).

### Roles, public endpoints and auth-disabled mode

Role gating (viewer / editor / admin) and the schemes the server accepts are documented in the
generated reference's [Authentication intro](../api.md#authentication) — this guide does not
duplicate the matrix. What a script author additionally needs:

- Public without a credential: `GET /health`, everything under `/api/v1/auth/` (login, logout),
  `GET /wizard/bootstrap`, and the SPA itself.
- With `server.auth_enabled: false`, **every** request is treated as an authenticated admin —
  convenient for a trusted local instance, never for a reachable one.
- A missing or bad credential answers `401` with `{"error": "missing or invalid authorization
  header"}` or `{"error": "invalid or expired token"}`.

## Conventions

- **Error bodies** are JSON objects with a single `error` field. Which status means what
  (`404` absent, `409` conflict, `503` dependency down) is in the
  [generated intro](../api.md#errors); client-side, check `Content-Type` before parsing,
  because some handlers answer through `text/plain`.
- **Pagination**: listing endpoints take `limit` and `offset` (defaults per endpoint in the
  generated reference). The behavior generation cannot state is on search endpoints: an
  `offset` at or past the last result returns an empty page with the correct `total` instead
  of an error, and negative or oversized offsets are rejected with `400`.
- **`_links`**: enqueue responses point at the work they created, e.g. consume answers
  `"_links": { "tasks": "/api/v1/tasks?batch=<id>" }` — follow it to monitor the batch.
- **Search snippets are HTML**: `snippet` is HTML-escaped before it is returned; only the
  `<b>`/`</b>` highlight tags from `ts_headline` survive. Render it as HTML **only** if you
  honor that contract — treat everything else in it as untrusted text.
- **`422` on ingest** (`POST /api/v1/consume`, `/consume/upload`) means required external tools
  are missing: the body carries a `missing_tools` array with per-tool install hints. The same
  check runs on the CLI — see the
  [pre-flight tool check](cli.md#pre-flight-tool-check) for how to fix it. Nothing was
  enqueued.
- **`503` has three distinct recovery actions** (the status-code table in the generated intro
  says only "unavailable dependency"):

  | Body | Meaning | Recovery |
  |---|---|---|
  | `matcher unavailable — tag store is offline` | `kushim hugot` is not running | Start the matcher ([cli.md](cli.md#kushim-hugot)); tag *reads* keep working meanwhile |
  | `the model catalog failed to load; restore the default catalogs to recover` | LLM catalogs failed to load at startup | `POST /api/v1/llm/reset` installs fresh catalogs live — reads serve again without a restart |
  | `database not ready — retry after setup completes` | Setup has not claimed a database | Finish `kushim setup` |

- **Ingest has no API-side paused-batch guard**: while provider credits are broken, a consume
  batch enqueued through the API runs until the credit error and settles as `paused`
  (`POST /api/v1/consume` itself still enqueues). The refusal guard exists on the CLI and
  polling paths only ([cli.md](cli.md#paused-batch-guard)). Detect and recover the paused state
  in the [ingest workflow](#ingest-and-monitor) below.

## Workflows

### Ingest and monitor

Goal: get files from the inbox (or from a client upload) through the pipeline.

1. Enqueue — scan the consumption directory:

   ```bash
   curl -s -X POST http://localhost:3000/api/v1/consume \
     -H "Authorization: Bearer $TOKEN"
   ```

   Response `202`:

   ```json
   {
     "batch_id": "550e8400-e29b-41d4-a716-446655440000",
     "total_files": 5,
     "enqueued": 5,
     "_links": { "tasks": "/api/v1/tasks?batch=550e8400-e29b-41d4-a716-446655440000" }
   }
   ```

   An empty inbox answers `200` with `batch_id: null`. To push files from a client instead,
   `POST /api/v1/consume/upload` with `multipart/form-data` and one or more `files` parts; it
   answers `202` with `accepted`/`rejected` lists (`422` when nothing is supported or
   `max_files_per_batch` is exceeded). Duplicate content (same MD5) is skipped at enqueue time.

2. Poll the batch until it settles:

   ```bash
   curl -s "http://localhost:3000/api/v1/batches/550e8400-e29b-41d4-a716-446655440000" \
     -H "Authorization: Bearer $TOKEN"
   ```

   The batch summary carries per-status task counts, the lease owner (`owner_state`:
   `none`/`live`/`stale`) and `orphaned`. Per-task detail: `GET /api/v1/tasks?batch=<id>`
   (add `&status=failed` to see failures; `status=active` shows pending/processing/waiting).

3. React to terminal states:

   - `paused` — provider credit/balance error. Fix billing, then
     `POST /api/v1/batches/{id}/resume` (add `?retry_failed=true` to also re-run failed
     tasks), or `POST /api/v1/batches/{id}/cancel`.
   - `failed` — resume/retry as above; `POST` resume answers `202` with
     `{"resumed": true, "reset": N, "retried": M}`, or `200` with `resumed: false` and a
     `reason` when nothing can become active.
   - `cancelled` / `completed` — terminal.

   Endpoints: `GET/POST /api/v1/batches…` — see the
   [generated reference](../api.md#batches) for every parameter.

### Search

Full-text, optionally combined with metadata filters.

```bash
curl -s "http://localhost:3000/api/v1/documents/search?q=quarterly+budget&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

Response items add `rank` and `snippet` to the document shape (honor the escaping contract in
[Conventions](#conventions)). For metadata filters use the structured form:

```json
POST /api/v1/documents/search
{
  "query": "quarterly report",
  "tags": ["finance", "budget"],
  "people": [{ "name": "John Doe", "type": "author" }],
  "document_type": "invoice",
  "language": "eng",
  "date_created": { "from": "2026-01-01", "to": "2026-03-31" },
  "file_size": { "min": 0, "max": 10485760 },
  "sort_by": "created_at",
  "sort_order": "desc",
  "limit": 50,
  "offset": 0
}
```

- `tags` combine with **AND** — the document must carry every listed tag. Without `query`,
  `sort_by`/`sort_order` applies; with it, results are ranked by relevance first.
- `people[].type` is a relationship type (`author`, `sender`, …). The reserved value
  `"person"` matches the person across **all** their relationship types.
- Response: `{ "results": [...], "total": N }`.
- Autocomplete companions: `GET /api/v1/tags?q=`, `/api/v1/people?q=`,
  `/api/v1/people-types`, `/api/v1/document-types`, plus `GET /api/v1/filter-languages` and
  `GET /api/v1/supported-mime-types` for building filter UIs.

### Document management

- **List**: `GET /api/v1/documents?limit=50&offset=0&sort_by=created_at&sort_order=desc`
  (`sort_by`: `title`, `file_size`, `created_at`). Single document: `GET /api/v1/documents/{id}`
  (adds `tags` and `people` to the base shape). File bytes: `GET /api/v1/documents/{id}/file`
  (`?download=true` forces an attachment), thumbnail: `GET /api/v1/documents/{id}/thumbnail`.
- **Edit metadata**: `PUT /api/v1/documents/{id}` with `title`, `document_type_id`, `language`
  (and optionally `text_content`) → `204`; `404` when the document or type does not exist.
- **Tag in bulk**: `POST /api/v1/documents/batch-tags` with
  `{"document_ids": [...], "tag_ids": [1,2], "mode": "add" | "replace"}` — `add` appends,
  `replace` swaps the whole set atomically. Per-document failures come back in `failed[]`.
- **Type in bulk**: `POST /api/v1/documents/batch-type`.
- **Download**: `POST /api/v1/documents/download` with `{"document_ids": [...]}` →
  `application/zip`; capped by `server.max_download_files` and `server.max_download_size_mb`.
- **Delete** (single): `DELETE /api/v1/documents/{id}` → `204`. This is a **soft delete** —
  files move to `<storage_dir>/trash/<document_id>/`, the row gets `deleted_at`, and the
  document disappears from lists, search and duplicate detection until restored.

### Trash lifecycle

Trashed documents are excluded from every query and from MD5 duplicate detection, so
re-ingesting the same file later creates a new document.

1. List: `GET /api/v1/trash?limit=50&offset=0` (ordered `deleted_at` descending).
2. Restore: `POST /api/v1/trash/{id}/restore` → `204` (missing files are skipped — a partial
   restore still succeeds). Bulk: `POST /api/v1/trash/batch-restore`.
3. Permanent: `DELETE /api/v1/trash/{id}` → `204` (row, cascades, trash directory and any
   thumbnail removed). Bulk: `POST /api/v1/trash/batch-delete` — documents not in the trash
   are reported in `failed[]`.
4. Expired purge: `POST /api/v1/trash/purge` → `{"purged": N}` for rows older than
   `storage.trash.retention_days` (default 30). The same purge runs hourly in the background,
   also sweeping orphaned thumbnails.

### Tag review

The review loop over provisional tags and vocabulary hygiene. Tag **mutations** require the
matcher to be up (503 otherwise); the report endpoints are read-only against PostgreSQL and
always work.

1. **Queue**: `GET /api/v1/tags/provisional?limit=50&offset=0` lists provisional tags with
   their latest rubric verdict and `pending_identity_total`. Filter it with:

   - `criteria=key:state[,key:state…]` — keys `subject_matter`, `dimension`, `not_person`,
     `atomic`, `not_form_genre`, `defer`; states `pass`, `fail`, `absent`. Pairs combine with
     AND. **Send one comma-separated value**: the server reads a single value per key and
     splits on commas, so repeated `criteria=` keys silently keep only the first. Unknown keys
     or states answer `400 Invalid criteria filter`.
   - `verdict=none|admit|defer|reject` (unknown values → `400`).

2. **Decide**: `POST /api/v1/tags/{id}/promote` → `204` (already promoted → `409`). Rejecting
   uses `DELETE /api/v1/tags/{id}`, which blocklists the name. `POST /api/v1/tags/{id}/demote`
   withdraws a promoted tag's standing (seeded inventory cannot be demoted — `409`); its
   assignments stay and it returns to the queue.
3. **Identity**: `POST /api/v1/tags/identity/reevaluate` → `202` with
   `{"batch_id": "…", "already_queued": false}` — enqueue a pass over pending identity claims
   (also runs on its own daily schedule; `already_queued: true` means the call was a no-op).
4. **Coverage**: `POST /api/v1/tags/audit/reevaluate` enqueues an audit;
   `GET /api/v1/tags/audit/flags` lists open flags; `POST /api/v1/tags/audit/flags/apply` and
   `…/flags/dismiss` take `{"document_id": N, "tag_name": "…"}` → `204` (a dismiss is
   authoritative — the pair is not re-flagged while the text is unchanged);
   `…/flags/create-value` registers a missing name as `{"tag_name": "…"}` → `204`.
5. **Reports**: `GET /api/v1/tags/reconciliation/report` returns the latest completed
   `tagreconcile` run (`404` before the first run; `zero_work: true` marks a skipped run),
   and `GET /api/v1/tags/siblings?limit=50` lists the latest measurement's near-containment
   pairs (`gray: true` marks the review band; `absent: true` means no measurement exists yet).

### Re-enrichment

Re-classify already-ingested documents (no re-OCR):

```bash
curl -s -X POST http://localhost:3000/api/v1/documents/550e8400-e29b-41d4-a716-446655440000/reenrich \
  -H "Authorization: Bearer $TOKEN"
```

Response `202` with `{"batch_id": "…", "_links": {"tasks": "…"}}`; `404` for an unknown
document, `409` when a re-enrichment with the same dedup key is already queued — monitor the
batch as in [Ingest and monitor](#ingest-and-monitor). Bulk re-enrichment goes through batch
creation on the CLI (`kushim batch create enrich`) or the UI.

### Saved searches

Store named filter configurations (what the documents screen's saved-searches menu lists):

```json
POST /api/v1/saved-searches
{ "name": "Invoices from Q1", "filter": { "tags": ["finance"], "document_type": "invoice" } }
```

`201` with `{"id": 1}`; `GET /api/v1/saved-searches` lists them; `DELETE
/api/v1/saved-searches/{id}` → `204`. `filter` accepts the same shape as the structured
search body.

### LLM provider and model catalogs

Discovery is open to any authenticated caller:

```bash
curl -s http://localhost:3000/api/v1/llm/models -H "Authorization: Bearer $TOKEN"
```

Response: adapters grouped by family, and per-provider model lists with capability objects
(`max_input_tokens`, `reasoning_efforts`, `supports_temperature`, …) — enough to populate
cascading provider/model selectors.

Mutations require admin and answer with the **fresh catalog**, so a client refreshes in one
round trip:

| Endpoint | Effect |
|---|---|
| `POST /api/v1/llm/providers` | Create (name + adapter + absolute `http(s)` URL required; no `.` or `/` in the name) |
| `PUT /api/v1/llm/providers/{name}` | Update (name immutable) |
| `DELETE /api/v1/llm/providers/{name}?cascade=true` | Remove; `cascade` also deletes its models |
| `POST /api/v1/llm/models` / `PUT /api/v1/llm/models/{provider}/{model}` | Create / update a model |
| `DELETE /api/v1/llm/models/{provider}/{model}` | Remove a model |
| `POST /api/v1/llm/reset` | `{"providers": bool, "models": bool}` — restore embedded defaults |

Guard rejections answer `409`: a provider that still owns models needs `cascade=true`, and a
provider or model referenced by the enricher configuration (content analyzer, its fallbacks,
tag adjudicator, tag quality) is always refused. The same catalog is editable from the
settings UI and `kushim llm` ([cli.md](cli.md#kushim-llm)); changes apply without a restart.

### Settings apply

The settings UI and the setup wizard share the configuration API (admin role):

1. **Read**: `GET /wizard/config` returns the full configuration (including LLM tokens) plus
   `available_engines`. `GET /wizard/bootstrap` is public and returns only
   `{"auth_enabled": …, "missing_tools": […]}` — enough for a client to decide whether to
   show the login screen.
2. **Write**: `PUT /wizard/config` with a flat map of dot-notation keys
   (`"server.port": 3000`, `"consumer.ocr.languages": ["eng", "spa"]`):
   - `201` with `{"pending_tasks": N, "missing_tools": […]}` when asset downloads were
     enqueued (tessdata, model files), or `200` with `{"configured": true}` when everything is
     already present.
   - **Deferred changes**: when the body touches `database.*` or `storage.storage_dir` /
     `storage.consumption_dir`, the write is queued as a migration instead — `202` with
     `{"pending_tasks": 1, "message": "migration(s) queued — settings apply once the migration
     completes"}`. Everything else in the same request applies immediately; further changes of
     the same kind answer `409` while the migration runs. Both `edub` and `kushim queue`
     reconnect on their own once the new settings land.
   - A failed migration is retried automatically on the next save, or on demand via
     `POST /wizard/config/retry`; the copy is idempotent.
3. **Watch**: `GET /wizard/config/status` →
   `{"configured": …, "pending_tasks": N, "errors": [], "tools": […], "missing_tools": […]}`
   (the setup wizard polls it every 3 seconds).

Catalog edits are **not** part of `PUT /wizard/config`: the settings UI stages them, saves the
config first, then replays them against the `/api/v1/llm/*` endpoints in order — a failed
operation stays pending with the server's reason. Follow the same order when scripting:
config first, catalog operations second ([web-ui.md](web-ui.md) documents the staged-apply
flow end to end).

### Dashboard and monitoring

```bash
curl -s http://localhost:3000/api/v1/dashboard -H "Authorization: Bearer $TOKEN"
```

One call returns the operational picture: `recent_batches` (the 10 newest batch summaries with
per-status counts, owner state and duration), `inbox_files`, `originals_size_bytes`,
`processed_size_bytes`, and `running_tasks` — `{ "count": <all active>, "tasks": [<up to 25>,
processing-first>] }`.

For anything deeper:

- `GET /api/v1/tasks?status=active` / `?batch=<id>` / `?status=failed` — the task queue.
- `GET /api/v1/batches?status=queued&limit=10` and `?task_status=failed` — batch triage.
- `GET /health` — public liveness (`{"status": "healthy", "version": "…", "time": "…"}`).

The same data is presented in the UI ([web-ui.md](web-ui.md)) and the equivalent CLI verbs are
in [cli.md](cli.md).

## See also

- [docs/api.md](../api.md) — generated endpoint reference (schemas, parameters, statuses)
- [cli.md](cli.md) — the same capabilities from the terminal
- [web-ui.md](web-ui.md) — the same workflows through the web interface
- [configuration.md](configuration.md) — every key referenced above
