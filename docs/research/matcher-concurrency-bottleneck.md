# Matcher Concurrency Bottleneck Under Concurrent Enrichment

## Context

The tag matcher is a local Unix-socket HTTP server (`kushim hugot`) that serves
`/rpc/v1/match`, `/rpc/v1/encode`, and `/rpc/v1/consolidate`. The `edub` side
calls it through `MatcherClient` (`internal/tagmatch/client.go`), which wraps
every call in a configurable context deadline via `runWithTimeout`
(`internal/tools/runner.go:96-113`, applied in `MatchTags` at
`internal/tools/runner.go:419-436`).

A recent fix removed the stacked deadlines that masked this problem: the client
had a hardcoded `http.Client.Timeout: 120 * time.Second`
(`internal/tagmatch/client.go:59`) independent of the configurable
`enricher.tagmatcher.timeout`, silently overriding any config change beyond
120s and producing `Client.Timeout exceeded while awaiting headers` errors
under concurrent load. The server mirrored this with a hardcoded
`WriteTimeout: 120 * time.Second` (`internal/commands/hugot.go:114`). Both are
now driven by the config value: the client relies solely on the context
deadline (client `Timeout` removed), and the server's `WriteTimeout` is derived
from `enricher.tagmatcher.timeout` (`internal/commands/hugot.go:111-117`,
`0` = no deadline).

Removing the stacked deadlines surfaced the underlying issue this report
documents: under concurrent enrichment, per-request latency inflates enough to
occasionally exhaust the timeout budget.

## Architecture: No Queue, No Worker Pool, No Locking

The matcher server is a plain `http.Server` (`internal/commands/hugot.go:111-117`)
with standard `net/http` dispatch. Each request runs in its own goroutine:

```
edub ── MatchTags ── runWithTimeout(ctx) ── HTTP POST /rpc/v1/match
                                                   │
                                                   ▼
                              http.Server (goroutine per request)
                                                   │
                                                   ▼
                              handleMatch ── h.Match ── Encode ── RunPipeline
                              (no queue, no worker pool, no handler-level mutex)
```

There is no request queue, no worker pool, and no handler-level locking —
`handleMatch` (`internal/commands/hugot.go:199-222`) decodes the body and calls
`h.Match` directly. The same holds for the adapter: `Match`
(`internal/tools/adapters/tagmatcher/hugot.go:174-212`), `Encode`
(`internal/tools/adapters/tagmatcher/hugot.go:258-319`), and `encodeChunked`
(`internal/tools/adapters/tagmatcher/hugot.go:323-366`) contain no mutex.

## Library Internals: Inference Is Not Serialized

Hugot v0.7.5 does not serialize inference at the library level either:

- `pipelineLock`/`modelLock` (`hugot.go:28-31` in the library) are only taken
  inside `NewPipeline` (`hugot.go:194-196`) — pipeline *creation*, which happens
  once at server startup. They are never touched during inference.
- `runORTSessionOnBatch` (`backends/model_ort.go:531-581`) calls
  `Session.Run` with no mutex. ONNX Runtime's C API is thread-safe for
  concurrent `Session.Run` calls on the same session, so concurrent requests
  genuinely execute in parallel — there is no hidden serialization.
- `RunPipeline` (`pipelines/featureExtraction.go:289-306`) is likewise
  lock-free: preprocess → forward → postprocess with no mutex.

## The Real Bottleneck: CPU Contention

Because nothing serializes inference, concurrent requests do not queue up —
they *contend*. The concurrency source is the queue daemon: `kushim queue` forks
up to `server.max_concurrent_batches` `kushim consume --batch <id>` processes,
each creating its own `MatcherClient` (`internal/commands/container.go:133`) and
running up to `enricher.workers` (default 1) enrichment workers. So concurrent
matcher requests = `max_concurrent_batches × enricher.workers`. (The `edub`
process also has a `MatcherClient` at `internal/api/server.go:56`, but uses it
only for tag CRUD — encode/consolidate — not enrichment matching.)

The specific mechanism behind the contention is **thread oversubscription**.
`getBackendSession` (`internal/tools/adapters/tagmatcher/hugot.go:450-482`)
creates the ORT session without calling `options.WithIntraOpNumThreads()`. ORT's
default is to use **all available cores** for a single `Session.Run`'s intra-op
parallelism. When N concurrent calls each spawn threads across all cores, you get
N×cores threads competing for cores cores — context-switching overhead on top of
the FLOPs contention. BGE-M3 (24 layers, 1024-dim) is compute-bound, so this
oversubscription directly inflates wall time.

| Factor | Cost |
|---|---|
| Thread oversubscription (N × cores threads on cores cores) | Context-switching overhead on top of FLOPs contention |
| Concurrent `Session.Run` on shared cores | Per-request wall time inflates proportionally; throughput does not multiply |
| Memory bandwidth saturation (1024-dim activations) | Additional slowdown beyond pure FLOPs contention |
| No queueing, no fairness | Latency spikes are unbounded and load-dependent |

So the matcher behaves like a CPU-bound service with no admission control:
total throughput is roughly constant (bounded by cores), and per-request
latency grows linearly with concurrency. With the config timeout now applied
as the sole deadline, a burst of concurrent enrichments can push individual
requests past the budget.

## Within-Document Serialization Compounds the Problem

`encodeChunked` (`internal/tools/adapters/tagmatcher/hugot.go:323-366`)
processes overlapping chunks in a sequential `for` loop — one `RunPipeline`
per chunk. A large document near the 4MB/4000-word cap produces multiple
sequential inference passes, each of which also pays the decode→re-encode
round-trip documented in
[double-tokenization-chunked-encoding.md](double-tokenization-chunked-encoding.md).
Under concurrent load, each of those sequential passes contends with every
other request's passes, so the per-document wall time scales linearly with
concurrency (M chunks × N× per-chunk contention), multiplying the base cost.

## Abandoned Inference Wastes CPU on the Server Side

`runWithTimeout` (`internal/tools/runner.go:96-113`) returns immediately when
the context deadline fires, but the goroutine it spawned keeps running:

```go
ch := make(chan result, 1)
go func() {
    v, e := fn()
    ch <- result{v, e}
}()
select {
case <-ctx.Done():
    return zero, ctx.Err()   // goroutine keeps running!
case r := <-ch:
    return r.val, r.err
}
```

On the **client side** this is transient: `c.client.Do()` respects the request
context (`http.NewRequestWithContext` at `internal/tagmatch/client.go:73`), so
the HTTP call returns within milliseconds with a context error and the goroutine
exits.

The real waste is **server side**. `handleMatch` passes `r.Context()` to
`h.Match`, and `runORTSessionOnBatch` checks it via a `select`
(`backends/model_ort.go:535-540`). When the client disconnects, `r.Context()`
cancels and the Go wrapper returns early — but the C-level `Session.Run` **cannot
be stopped**. The library's own comment at `backends/model_ort.go:548` says:
*"C code does not support context, so cancelling a context and/or session will
usually trigger a segfault(panic)."* The abandoned C inference runs to completion,
consuming CPU that the server does not reclaim. Timed-out requests do not free
capacity; they add to the contention that makes subsequent requests slower,
amplifying effective queue depth exactly when the system is already overloaded.

### Panic recovery under context cancellation

The same `runORTSessionOnBatch` wraps `Session.Run` in a `recover()`
(`backends/model_ort.go:550`) that catches segfaults triggered by context
cancellation during C execution. Under timeout-heavy conditions the server may be
silently experiencing and recovering from C-level segfaults on abandoned
requests. This is a risk of relying on timeouts to bound matcher behavior: the
timeout mechanism itself can destabilize the ORT session's internal state.

## Candidate Mitigations (Future Work)

None of these are in scope for the timeout fix; they are listed for the
backlog:

1. **Set `IntraOpNumThreads`** — call `options.WithIntraOpNumThreads()` in
   `getBackendSession` to limit per-inference thread count (e.g.
   `runtime.NumCPU() / expected_concurrency`). Eliminates thread
   oversubscription without adding any queuing or batching infrastructure.
   Simplest and most targeted fix.
2. **Server-side inference semaphore** — bound concurrent `h.Match` calls with
   a semaphore in `handleMatch` (or a wrapper around the adapter). Trades
   throughput for latency predictability: requests queue at the server instead
   of contending, and the queue depth is bounded.
3. **Cross-request batching** — collect inputs from pending requests and run
   them through a single `RunPipeline` call. ORT handles batches efficiently
   in one forward pass, so this could recover most of the lost throughput.
   Requires a batching layer between the HTTP handlers and the adapter.
4. **Reduce `chunk_size`** — lower per-inference latency at the cost of more
   chunks per document (and more decode→re-encode overhead, see the
   double-tokenization report). A stopgap, not a fix.

## Code References

- `MatchTags` — `internal/tools/runner.go:419-436` (config timeout as context deadline)
- `runWithTimeout` — `internal/tools/runner.go:96-113` (returns on ctx.Done, goroutine continues)
- `NewMatcherClient` — `internal/tagmatch/client.go:49-63` (client `Timeout` removed; context deadline is the sole deadline)
- Matcher server — `internal/commands/hugot.go:111-117` (`WriteTimeout` from config)
- `handleMatch` — `internal/commands/hugot.go:199-222` (no locking, standard HTTP dispatch)
- `getBackendSession` — `internal/tools/adapters/tagmatcher/hugot.go:450-482` (ORT session created without `WithIntraOpNumThreads`)
- `Match` — `internal/tools/adapters/tagmatcher/hugot.go:174-212` (no mutex)
- `Encode` — `internal/tools/adapters/tagmatcher/hugot.go:258-319` (no mutex)
- `encodeChunked` — `internal/tools/adapters/tagmatcher/hugot.go:323-366` (sequential per-chunk inference)
- Hugot v0.7.5: `hugot.go:28-31` (locks map), `hugot.go:194-196` (lock scope = creation only), `backends/model_ort.go:531-581` (`runORTSessionOnBatch`, no mutex, C-level `Session.Run` unstoppable + panic recovery at line 550), `pipelines/featureExtraction.go:289-306` (`RunPipeline`, no mutex)