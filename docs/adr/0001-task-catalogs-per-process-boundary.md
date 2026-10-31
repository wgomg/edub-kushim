# 0001. Task catalogs per process boundary

**Status:** accepted

## Context

Every process that enqueues background work needs the task-type policy: which task types
exist, their queued and direct locks, dedup hooks, preconditions, batch-mode CLI surface,
and host placement.

The requirements pull against each other: the policy is a closed table that must be
declared exactly once, while each process needs only its own slice of hooks and workers.
Serving every boundary from one assembled catalog and enqueuer couples boundaries that
share nothing at runtime — edub never runs a consume worker, the wizard never sees a tag
batch — and forces `internal/task` to import producer, service, and ops packages to wire
that shared surface.

Rejected alternatives:

- **One shared catalog/enqueuer held by the `Container` type (`internal/ops/container`).**
  Removes the visible duplication but makes every process assemble every hook, and drags
  producer imports into `internal/task`.
- **A second catalog package** (e.g. `internal/taskdefinitions`) that would hold the policy
  while `internal/task` executes it. Splits one concept across two homes; the policy table
  is the definition, not a companion to it.
- **Per-producer task definitions.** Would duplicate the closed policy table (statuses,
  lock names, CLI surface) into each producer, which the enum and lock-table invariants
  forbid: there is exactly one declaration point.

## Decision

`internal/task` owns the static task catalog (`catalog.go`, `TypeDefinition` policy, `NewCatalog`)
and imports no producer, handler, service, ops, or api package — only `internal/types`.
Type definitions carry policy only; task-set building and schedules stay in the producers.
Every process boundary constructs its own catalog and enqueuer from the static table plus
the dedup hooks that boundary owns.

The shape to look for is one `task.NewCatalog` call per boundary: a map of the task types
that boundary enqueues, each wired with its producer-owned `DedupKey` (or
`task.ConstantDedupKey` for a fixed key) and optional preconditions; `NewCatalog(nil)` for a
boundary that enqueues nothing and needs only the static policy. For example:

- edub (`internal/api/server.go`): consume, enrich, thumbbackfill, both migrations, and
  the three tag-curation types, each wired with its dedup key, then
  `task.NewEnqueuer(client, catalog)`.
- The queue daemon (`internal/ops/queue/queue.go`): consume, backup, thumbbackfill, mirror
  (with its precondition), and the four tag types; built once in `queue.Run` and passed to
  the dispatcher.
- `kushim batch run` (`internal/ops/cli/batch.go`): `NewCatalog(nil)` — it launches an
  existing batch and enqueues nothing.

The wizard, the consume CLI, the tag maintenance chain, `kushim batch create`, and storage
operations construct theirs the same way.

## Consequences

Apparent duplication of catalog assembly across boundaries is deliberate; it is the cost of
keeping process boundaries independent. A change to task policy happens once in the static
table; a change to a producer's dedup or concurrency happens only at that producer.

Standing constraint: do not reintroduce one assembled catalog/enqueuer shared by every
process; do not move producer-owned dedup hooks, spec builders, or schedules into
`internal/task`; do not create a second package that mirrors the policy table.
