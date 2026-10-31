# 0004. Matcher is operator-owned

**Status:** accepted

## Context

Tag matching runs the Hugot embedding model (ONNX runtime, several GB of RSS) over a single
`EmbeddingStore` that must be the sole owner of tag-embedding dedup. Several consumers need
it — the API server, forked workers, the standalone CLI — but building a model per consumer
costs gigabytes of RSS in each process, and a second store would diverge from the first and
pollute the tag database with redundant tags.

The alternatives are embedding the matcher in edub, letting each forked worker build its
own Hugot, or having edub spawn and supervise the matcher as a child.

Rejected alternatives:

- **Embed in edub.** Ties the API process's RSS and CGo lifetime to the model; a worker
  model or an edub restart cannot share one store.
- **Per-worker Hugot.** Divergent stores; the single-store dedup invariant breaks.
- **edub spawns/supervises the matcher.** Couples the API's lifecycle to a heavyweight
  model process: a matcher restart becomes an edub restart, supervision and reaping logic
  leak into the server, and standalone CLI use would still need its own lifecycle.

## Decision

The matcher (`kushim hugot`) is a sibling process the operator starts before edub
or any standalone CLI use (systemd/launchd/manual). edub, forked workers, and the CLI are
pure socket clients of `<config-dir>/kushim-hugot.sock`; none builds Hugot in-process.
edub probes the socket at startup with a short-timeout health check and continues serving
either way: tag CRUD degrades to 503, enrich falls back to LLM-assigned tags. Restarting a
crashed matcher is the operator's responsibility.

## Consequences

One store, one model, one RSS spike, and a flat-RSS server; process lifecycles are
independent; the degradation contract (CRUD 503, enrich fallback) is explicit per endpoint
rather than a whole-process failure.

Standing constraint: edub must never spawn, supervise, or reap the matcher; no worker or
CLI path may build an in-process Hugot — matching goes through the socket client; the
matcher remains the single owner of the embedding store.
