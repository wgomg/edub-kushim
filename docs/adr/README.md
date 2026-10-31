# Architecture Decision Records

One standing decision per record, frozen once accepted: a later change is a new ADR that
supersedes this one, never an edit. Context states the problem and the alternatives that
were rejected; Consequences records what the decision costs and the standing constraint it
protects against.

| # | Record | Status |
|---|---|---|
| 0001 | [Task catalogs per process boundary](0001-task-catalogs-per-process-boundary.md) | accepted |
| 0002 | [Database as queue](0002-database-as-queue.md) | accepted |
| 0003 | [Batch executor precedence](0003-batch-executor-precedence.md) | accepted |
| 0004 | [Matcher is operator-owned](0004-matcher-is-operator-owned.md) | accepted |
| 0005 | [Two binaries; edub never links CGo](0005-two-binaries-edub-never-links-cgo.md) | accepted |
| 0006 | [CGo work runs in child processes](0006-cgo-work-runs-in-child-processes.md) | accepted |
| 0007 | [The API spec follows the code](0007-api-spec-follows-the-code.md) | accepted |
| 0008 | [Search is PostgreSQL tsvector](0008-search-is-postgresql-tsvector.md) | accepted |
| 0009 | [LLM catalogs: seed once, user edits authoritative](0009-llm-catalogs-seed-once-user-edits-authoritative.md) | accepted |
| 0010 | [Document type: described candidates, head/tail refinement](0010-document-type-described-candidates-head-tail-refinement.md) | accepted |
| 0011 | [Intake dedup by content hash](0011-intake-dedup-by-content-hash.md) | accepted |

Living docs that depend on these decisions link here instead of restating them.
