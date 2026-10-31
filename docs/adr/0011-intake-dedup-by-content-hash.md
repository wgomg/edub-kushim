# 0011. Intake dedup by content hash

**Status:** accepted

## Context

The inbox receives re-downloads, copies, and re-submissions of the same file, often under
different names. Detection must happen before a document row or a stored copy exists, and
two concurrent workers must not ingest the same bytes at once.

Rejected alternatives:

- **Filename or path matching.** Renamed copies slip through; identical names with
  different content collide.
- **A database table of rejected duplicates.** Schema and UI cost for records nobody
  queries — a duplicate is not a document.

## Decision

Content identity is the file's MD5. A document row stores `md5_checksum` (and
`sha512_checksum`); `Consumer.Process` opens with a duplicate check against those
checksums and, on a hit, parks the file under `errors/duplicated/` with a fresh UUID
prefix and no document row; the inbox scan parks its duplicates the same way, and the
errored-files surface lists them. At the enqueue layer the same hash forms the dedup key
`consume:<md5>`, guarded by the unique `(task_type, dedup_key)` index (`idx_task_dedup`),
so two workers cannot enqueue identical content concurrently.

## Consequences

Detection is exact and name-independent; duplicates are inspectable on disk and never
become rows; concurrent double-enqueue is impossible at the index level. The accepted
cost: duplicate history is a filesystem directory, not a queryable table.

Standing constraint: duplicates never become documents or database rows; identity is the
content hash, never the path or filename; `errors/duplicated/` remains the on-disk
quarantine surface for them.
