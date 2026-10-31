# 0002. Database as queue

**Status:** accepted

## Context

Work routed through the queue — document consumption, enrichment, thumbnails, backups,
mirrors, migrations, and tag maintenance — must be durable and exclusive: a queued unit is
never lost, an interrupted unit is reclaimed and re-run rather than abandoned, no unit
executes twice concurrently, and every unit's status is queryable by the API and UI. The
obvious alternatives are an in-memory job queue, Redis, or a message broker with a worker
pool on top of it.

Rejected alternatives:

- **In-memory queue.** Work dies with the process; no crash recovery; two edub instances
  cannot coordinate.
- **Redis / message broker.** Adds an external service the operator must install, run, and
  keep in sync with the database's view of batches, splits one transactional story across
  two stores, and duplicates the durability, status, and dedup machinery that the `task`
  table already provides.

## Decision

PostgreSQL is the queue. Tasks are rows in `task`; workers claim rows with guarded UPDATEs
under a batch lease (`batch_owner` with database-clock heartbeats); the queue daemon
(`kushim queue`) watches queued batches via `LISTEN batch_queued` with a safety poll,
claims each one in a guarded transaction, forks the batch's owning task CLI in executor
mode, and reclaims anything that dies. Deduplication, successor activation, and settle all
happen in the same database as the work state.

## Consequences

No extra infrastructure and no dual-write: enqueue, claim, heartbeat, and settle are
ordinary transactions, and crash recovery is lease expiry plus the reaper rather than a
broker redelivery. Status for the API/UI is a query, not a projection. The cost is that
queue throughput is bounded by the database, which is acceptable at this system's scale.

Standing constraint: do not introduce a broker, Redis, or an in-memory queue for
application work; new background work joins the `task` table and the batch machinery
instead of growing a parallel path.
