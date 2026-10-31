# 0003. Batch executor precedence

**Status:** accepted

## Context

A batch can carry more than one task type: an ingest batch holds consume tasks plus their
enrich and thumbnail children; a scheduled maintenance batch mixes tag task types. Exactly
one forked child must execute the whole batch and settle it — split execution would race
the batch's locks, drain, and heartbeat, and two children would fight over one lease.

The executor must therefore be chosen deterministically from the batch's task types. The
naive alternatives are dynamic negotiation between children, one child per task type, or
letting the first task's type win by accident of ordering.

## Decision

`resolveLaunchTarget` (`internal/ops/dispatch/launcher.go`) resolves one executor per
claimed batch:

- A pure batch (all tasks one type) resolves to that type's batch-mode CLI.
- A mixed ingest batch resolves by the fixed precedence `consume > enrich > thumbnail >
  thumbbackfill` — the command that owns the batch's root work executes it, because the
  other tasks in the batch are the roots' children.
- A batch made only of tag maintenance types (`tagreconcile`, `tagidentity`, `tagaudit`,
  `tagmeasure`) resolves to the chained `tagmaintenance` executor.
- Any other disagreement between types in a batch fails loudly at claim time.

The child adopts the launcher's lease exactly once, requiring the same execution token and
pid 0; a replayed or superseded token cannot re-adopt a live lease.

## Consequences

Executor selection is a pure function of the batch's declared types — testable, and stable
across restarts. One child owns the batch's locks, heartbeat, and settle for its whole
lifetime, so crash recovery reclaims one process, not a tree.

Standing constraint: do not split a batch's execution across processes, do not make the
executor choice dynamic or negotiated, and do not let a task type outside the precedence
list silently win a mixed batch — disagreement must fail loudly.
