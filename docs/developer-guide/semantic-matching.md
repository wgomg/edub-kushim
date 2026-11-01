# Semantic tag matching with Hugot

This guide explains how edub-kushim turns documents into tag suggestions using semantic
similarity: the embedding model, the in-memory embedding store, the chunking/mean-pooling
encoding strategy, cosine ranking, the daemon that exposes it all over a Unix socket, and
the sizing and operating knowledge needed to keep it healthy. The identity *decisions*
that consume the matcher's candidates — adjudication, the quality gate, promotion — are
documented in [consolidation](consolidation.md).

## 1. Orientation

Semantic matching happens at two points in the enrichment pipeline:

1. **Tag suggestions for the LLM** — before the LLM analyzes a document, the reduced text
   is embedded and matched against the known tags; the matches at or above the injection
   bar (`enricher.tagmatcher.min_suggestion_similarity`) are passed into the prompt as
   "prefer these tags if relevant".
2. **Post-LLM consolidation and identity** — the tags the LLM produced are re-embedded
   and ranked against canonical tag names; similarity ranks candidates and never replaces
   a name — the enricher owns the decision.

The core type is `*matcher.Hugot` (`internal/matcher/hugot.go`), an in-process embedding
model (Hugot + ONNX runtime or pure-Go backend) implementing the `Matcher` and `Embedder`
interfaces (`internal/matcher/adapter.go`). Two deployments exist:

- **In-process** (`kushim` binary, CGo build): `*Hugot` runs the model directly.
- **Remote** (`edub` binary, no CGo): `*matcher.MatcherClient` speaks HTTP over a Unix
  socket to the `kushim hugot` daemon — see [ADR 0004](../../adr/0004-matcher-is-operator-owned.md).

Both satisfy the same interfaces, so the rest of the system cannot tell them apart. The
whole flow is: **tags → embeddings (store) → document text → embedding → cosine rank →
top-N suggestions**.

## 2. The embedding model and backends

`NewHugot` builds a Hugot feature-extraction pipeline from a Hugging Face model directory
(`hugot.NewPipeline` with `pipelines.WithNormalization()`). The default model is
**`BAAI/bge-m3`** (1024-dim, multilingual), downloaded on first start (by the setup flow)
to `<config_dir>/tagmatcher/hugot/models/<model>`. L2 normalization of every embedding
means cosine similarity collapses to a plain dot product — `cosineSimilarity` is the
element-wise sum with a length guard.

`getBackendSession` switches on `Hugot.Backend`:

- **`"ort"`** — ONNX Runtime via `hugot.NewORTSession`, with `WithCPUMemArena` and
  `WithMemPattern` mirroring `Hugot.CpuMemArena`/`MemPattern` (both default false — see
  §8). The ORT backend needs `libonnxruntime.so`, downloaded on first use: the pinned
  archive from GitHub, **verified against a hardcoded SHA-256**, extracted into
  `<config-dir>/tagmatcher/hugot/libs/`. The digest and URL are pinned together —
  updating one without the other breaks the download — and the first run needs `curl` and
  internet.
- **anything else (`"go"`)** — pure Go, no runtime downloads.

## 3. The embedding store

Matched against *every* tag for *every* document — so tag embeddings are cached in
memory: `matcher.TagEmbeddingStore` (`internal/matcher/embedding_store.go`), a
`map[string][]float32` behind its own `sync.RWMutex`. Reads take `RLock`, writes take
`Lock`, and `Entries()` returns a deep copy so callers never race the lock.
`Hugot.AddToStore`/`RemoveFromStore` mutate through `Add`/`Remove`, and `BuildTagCache`
swaps the complete map under the same lock.

The store is *authoritative for matching*: `Match` iterates whatever is in `entries` —
the cache **is** the tag universe. If a tag is missing from the store, it can never be
suggested.

## 4. Bootstrapping the store

`matcher.BuildTagCache` rebuilds the store from the `tag` table:

1. Loads the **promoted** tag names (`promoted_at IS NOT NULL`). Store membership is the
   trust boundary: a provisional tag is excluded from every injection point — never
   suggested, never a consolidation target, never part of the all-names fallback
   vocabulary. Promotion adds the name incrementally (`curation.Tag.AddToStore`); a
   re-run of `BuildTagCache` after a restart converges the store with the table either
   way. Demotion is the inverse: it clears `promoted_at` and removes the name, so the
   same restart rebuild drops it too.
2. Embeds them **verbatim** — no per-key transformation. Tag names are already canonical:
   `curation.Tag.Create`/`Update` normalize every name through `textnorm.NormalizeTag`
   before it reaches the DB, so the stored name is the embedding key and the matcher
   never re-derives a different form.
3. Encodes in batches of 32, then **swaps the whole map under the store's lock** — an
   atomic publish; readers never see a half-built store.

The store is rebuilt at daemon startup and incrementally updated on tag CRUD: a create
adds names only for trusted births, a rename re-embeds the new name only when the renamed
tag was promoted, and the enricher adds promoted names after the promotion check commits.

## 5. Encoding: short vs long texts

`Encode` handles two very different inputs with one API: tag names (a few words) and
document text (thousands of tokens).

- **Short texts** (≤ chunk size) are batched into a single pipeline call — this is how
  the 32-at-a-time tag batches stay fast.
- **Long texts** take the chunked path (`encodeChunked`): tokenize → slice token IDs into
  overlapping windows → decode each window back to text → embed each window → **mean-pool**
  the window embeddings into one vector.

### The chunk size comes from the model

`NewHugot` derives the effective chunk size from the model's `config.json`:
`max_position_embeddings` minus a 12-token safety margin; the configured `chunk_size`
(default 4096) is clamped into that range (0 means "use the model max"), and the overlap
is 10% of the chunk size. `kushim hugot` logs the resolved values on startup
(`hugot: chunk_size=4096 overlap=409 topN=15 min_sim=0.40`).

### Input memoization

Every encode consults the cache before spending inference: a long input's chunked encode
costs two to four inference calls of up to `enricher.tagmatcher.chunk_size` tokens each,
a short input one batched call, and the same texts recur — the completeness audit
re-matches the reduced document text later, daemon starts re-encode the promoted tag
names, identity ranking re-encodes its query names. All of it is memoized in the
[text-embedding cache](#10-the-text-embedding-cache), so a repeat input is a single
indexed read.

## 6. Matching and ranking

**`Match`** is the document→tags query: encode the input, compute the dot-product
similarity against every cached tag embedding, keep those `>= min_similarity` (the
doc→tag threshold, model-derived), sort descending, cap at `top_n`. The top scores are
logged for debugging — useful when tuning thresholds.

**`Rank`** is the tag→tag query for consolidation: it encodes the query tag names (short,
batched path), uses the separate higher **identity-candidate floor**
(`identity_candidate_similarity` — tag-name-to-tag-name similarity is inherently sparser
than document-to-tag), and returns **candidates with scores** for up to two neighbours.
If the floor is 0 or the store is empty, queries pass through with no candidates.

The decisions these outputs feed — identity resolution, the same-intent gate, the quality
gate — live in [consolidation](consolidation.md) §2. `Runner.RankTags` wraps the call
with the configured timeout; the enricher applies the identity pipeline locally with
candidate generation from `policy.Evaluate`.

## 7. Interfaces and RPC

The interfaces (`internal/matcher/adapter.go`):

```go
type Matcher interface {
	Match(ctx context.Context, docId, input string) ([]Candidate, error)
	Rank(ctx context.Context, docId string, queries []string) ([]RankResult, error)
	Close()
	Name()
}

type Embedder interface {
	Encode(ctx context.Context, docId string, texts []string) ([][]float32, error)
	AddToStore(ctx context.Context, names []string) error
	RemoveFromStore(ctx context.Context, names []string) error
	Close()
	Name()
}
```

- `*Hugot` implements both (in-process, CGo build).
- `*matcher.MatcherClient` implements both over the Unix socket — used by `edub`. The RPC
  surface is `Match`, `Rank`, `AddToStore`, `RemoveFromStore`, `Health` served as
  body-size-capped HTTP handlers; `Rank` returns its results verbatim (candidates only —
  the daemon makes no replacement decision).
- The client wraps every call in `ErrMatcherUnavailable` when the socket daemon is down —
  which is what turns tag CRUD into 503. The client imposes no client-level timeout of its
  own; the caller's context deadline (`enricher.tagmatcher.timeout`) is the sole bound,
  since encoding large documents can take a while.

The `EmbeddingStore` interface (`Add`/`Remove`/`Entries`) is implemented by
`matcher.TagEmbeddingStore` and by the test double in `internal/testutil`. Interface
changes must update both implementations plus the test doubles.

## 8. The matcher daemon

`kushim hugot` (`internal/ops/cli/hugot.go`) loads the model once and serves RPC over a
Unix socket (`<config-dir>/kushim-hugot.sock`).

- **Startup**: removes a stale socket, builds the tag cache, attaches the text-embedding
  cache and clears a stale generation, opens the listener, serves HTTP with a 30s
  read/idle timeout and a `WriteTimeout` mirrored from `enricher.tagmatcher.timeout` (0 =
  no deadline), read once at startup.
- **`--bg`** re-execs itself detached with stdio nulled (same pattern as `kushim queue --bg`).
- **Duplicate detection**: a live daemon makes `net.Listen("unix", ...)` fail with
  `EADDRINUSE`.
- **systemd readiness**: `notifyReady` writes `READY=1` to the socket named by
  `NOTIFY_SOCKET`.
- **Shutdown**: SIGTERM/SIGINT → `server.Shutdown` with a 5s budget → remove the socket.

### Operating

- **Start order and degradation**: the matcher must be running before `edub` starts for
  full functionality (the operator owns its lifecycle — ADR 0004). If it is unreachable,
  tag CRUD still succeeds (embedding-store sync is skipped with a logged error) and
  enrichment falls back to LLM-only tags — the pipeline degrades, it does not fail.
- **Memory capping (systemd)**: even with the sane default, an explicit cap protects the
  rest of the host from a future misconfiguration — a drop-in with `MemoryHigh` (throttle)
  and `MemoryMax` (hard cap: systemd OOM-kills only the matcher).
- **Observability**: health via `curl --unix-socket <config_dir>/kushim-hugot.sock /health`;
  logs at `<config_dir>/logs/hugot.log` or `journalctl -u kushim-hugot.service`; the
  startup line shows the effective settings; expect ~2.2–2.5 GB RSS at idle with the
  defaults. `TimeoutStartSec=120` covers model load (~10 s) plus tag-cache build (~31 s
  per 10k tags); raise it for 100k+ tag stores.

## 9. Configuration, memory, and sizing

All settings live under `enricher.tagmatcher`:

```yaml
enricher:
  tagmatcher:
    timeout: 120            # RPC deadline in seconds; 0 = no artificial deadline
    reduce_target_words: 4000
    chunk_size: 4096        # max tokens per inference chunk; 0 = model max
    min_suggestion_similarity: 0.52  # injection bar for suggestions; 0 = per-model default
    top_n: 15              # suggestions above the injection bar sent to the analyzer (minimum 3)
    hugot:
      model: BAAI/bge-m3
      backend: ort          # ort (ONNX Runtime) | go
```

| Key | Default | Meaning |
| --- | ------- | ------- |
| `timeout` | `120` | Deadline for matcher RPC calls in seconds, applied as a client context deadline and mirrored into the server's `WriteTimeout`. `0` disables it — not recommended for production. |
| `reduce_target_words` | `4000` | Word target for TextRank reduction applied *before* tag matching. Also drives the RPC request body cap (`× 24` bytes, clamped to 256 KiB–4 MiB). |
| `chunk_size` | `4096` | Maximum tokens per inference window (clamped to the model max − 12; see §5). |
| `min_suggestion_similarity` | `0.52` (bge-m3, derived) | Injection bar: only matched tags at or above it reach the emission prompt; when no match clears it the suggestion list is empty (no fallback). Explicit `0` reverts to the per-model default; negative values are rejected at load. |
| `top_n` | `15` | Suggestions above the injection bar that reach the analyzer's prompt. Validation requires `≥ 3`. |
| `hugot.model` | `BAAI/bge-m3` | Model identifier; the path is derived under `<config_dir>/tagmatcher/hugot/models/`. |
| `hugot.backend` | `ort` | Inference backend: `ort` or `go` (§2). |

Several knobs are intentionally **not** YAML-settable because they are auto-derived or
internal: the doc→tag threshold `min_similarity` (bge-m3 → 0.40), the identity-candidate
floor `identity_candidate_similarity` (bge-m3 → 0.80), and the ORT memory flags
(`CpuMemArena`/`MemPattern`, both false). The thresholds are derived per model from its
embedding dimension — higher-dimensional models cluster tighter, and tag-name similarity
is sparser than document similarity, so the identity floor sits well above the doc→tag
cut:

| Model | `min_similarity` | `identity_candidate_similarity` | `min_suggestion_similarity` |
| --- | --- | --- | --- |
| `bge-m3` (1024-dim) | 0.40 | 0.80 | 0.52 |
| `all-mpnet-base-v2` (768-dim) | 0.30 | 0.75 | 0.45 |
| `all-MiniLM-L6-v2` (384-dim) | 0.25 | 0.70 | 0.40 |
| default | 0.30 | 0.75 | 0.45 |

If you add a model to the catalog, add it to every derivation switch in
`internal/config/config.go`.

### `chunk_size` — the memory lever

`chunk_size` is the single most important memory control: texts of ≤ `chunk_size` tokens
embed in one call; longer texts split into overlapping windows whose embeddings are
mean-pooled (quality loss is negligible for tag matching). Attention memory grows roughly
quadratically with sequence length, so a full-context request (0 → 8180 tokens for
BGE-M3) can briefly need several times the memory of a 4096-token request. The default of
4096 bounds per-request peaks to ~4–6 GB while keeping idle usage (~2.2–2.5 GB) unchanged
— model weights dominate idle RSS regardless of `chunk_size`.

| Configuration | Idle RSS | Per-request peak | Notes |
| ------------- | -------- | ---------------- | ----- |
| `chunk_size: 4096` (default) + ORT defaults | ~2.2–2.5 GB | ~4–6 GB | Recommended for hosts with ≤ 16 GB RAM |
| `chunk_size: 0` (full context, 8180 tokens) | ~2.2–2.5 GB | can exceed 10 GB | OOM risk on 8 GB hosts |
| `chunk_size: 4096` + arena/pattern enabled | up to ~4–5 GB | higher | ~10–20% lower per-request latency |

ORT's CPU memory arena and shape-pattern pre-allocation are **disabled by default**: ORT
releases intermediate tensor buffers after each inference instead of retaining them in a
growing pool, capping idle RSS at ~2.2–2.5 GB. The cost is ~10–20% more per-inference
latency from re-allocation — dwarfed by text extraction, OCR, and LLM calls in the
enrichment pipeline. These flags are internal; flip them in `DefaultConfig` only if you
prefer lower latency over memory headroom.

### Sizing guidance (BGE-M3, `ort` backend)

| Host RAM | Recommended `chunk_size` | Notes |
| -------- | ------------------------ | ----- |
| 8 GB | `2048`–`4096` | Never `0`. Consider `2048` if other services run on the host. |
| 16 GB | `4096` (default) | Sweet spot: safe peaks with margin for the OS and Postgres. |
| 32 GB+ | `4096`, or `0` for full-context quality | `0` removes all chunking; peaks are the only cost. |

Numbers assume a single matcher process. If you raise `enricher.workers` or
`consumer.workers`, concurrent RPC requests arrive at the matcher at once and per-request
peaks can overlap — account for that in your headroom. The matcher is a single process
and inference is CPU-bound (BGE-M3 is a 570M-parameter encoder; a 4096-token encode is
hundreds of milliseconds to a couple of seconds depending on core count); both backends
use all available cores by default. `reduce_target_words: 4000` keeps typical match
payloads around 4000 words (~100 KB, bounded by the 4 MiB RPC cap), so in normal
operation most documents embed in a single 4096-token call and only long documents take
the chunked path.

| Question | Answer |
| -------- | ------ |
| My 8 GB host OOMs during enrichment — what now? | Set `enricher.tagmatcher.chunk_size: 2048` and restart `kushim hugot`. Idle RSS drops toward ~2 GB, peaks to ~3–4 GB. |
| I want the best matching quality and have 32 GB+. | `chunk_size: 0` — full 8180-token context, no chunking. |
| Why is the matcher using 2.2 GB when idle? | That is the model resident in RAM — loaded once, hot for the process's lifetime. |
| I switched `hugot.backend`, the model, or `chunk_size` — what happens to cached embeddings? | The matcher detects the stack change on its first start after it and clears its text-embedding cache; texts re-embed on demand. |
| Does the web UI expose these settings? | The Settings → Enricher → Tag matcher section edits `timeout`, `reduce_target_words`, `chunk_size`, model, and backend. |

## 10. The text-embedding cache

Encoding is not free — a long input's chunked encode costs multiple inference calls, a
short input one batched call — and the same text is embedded more than once (the
enricher matches at consume time; the completeness audit re-matches that same reduced
text later; tag names re-encode at every daemon start). The daemon caches one pooled
vector per distinct input text in `matcher_text_embedding`, so a re-match is a single
indexed read plus a cosine rank over the tag vectors instead of an encoding pass.

### Key and value

A row is keyed by `(fingerprint, input_hash)`:

- **input_hash** — sha256 of the exact input text the daemon received. Every upstream
  change that alters the text — reducer behavior, `reduce_target_words`, the
  reduction-failure fallback to raw text, body-cap truncation — produces a different key,
  so the text side of invalidation needs no wiring.
- **fingerprint** — sha256 over the model's `model.onnx` bytes (streamed once per daemon
  start), the backend name, the effective chunk size and overlap, the pinned ORT archive
  hash for the `ort` backend, and the `embeddingCacheGeneration` constant. Everything the
  embedding depends on and the code can hash is in the key.

The value is the pooled vector as little-endian float32 `bytea` — ~4 KB for BGE-M3's 1024
dimensions.

### Ownership and lifecycle

`matcher.TextEmbeddingCache` owns the table access; the daemon wiring builds it from the
same DB client `BuildTagCache` runs on. `Encode` consults the cache once per input:
short misses are batched into one pipeline call and stored, long misses are chunked,
pooled and stored; `Match` and `Rank` are unchanged, which is the load-bearing part: the
ranking always runs against the live tag store, so only the input vector is memoized and
any store change is reflected on the next match without cache interaction.

The table carries a generation marker in `system_signal`. `ClearStaleGeneration` runs
once at daemon startup: a steady-state start (marker equal to the current fingerprint) is
a single indexed read; a missing or different marker truncates the table and records the
new fingerprint. After a stack change every input re-embeds on demand and the cache
refills as documents and tag traffic flow through the daemon. Bump `embeddingCacheGeneration`
whenever the embedding runtime changes in a way the fingerprint cannot see — a hugot or
tokenizer upgrade, a pipeline-options change.

### Failure and backup behavior

The cache is fail-open: a read or write error, a malformed row, or an empty vector is
logged and treated as a miss, so a match outcome never depends on the cache answering; a
cache left unwired disables it entirely. Because the fingerprint is part of the row key,
even an error inside the startup clear leaves nothing reachable from the old stack —
stale rows are dead weight, never stale answers. Backups skip the table's rows: they are
rebuildable data (the migration recreates the empty table and the daemon repopulates it).

## 11. Where it plugs into the pipeline

1. **Tag CRUD** (`internal/tags/curation`): creates add the new names when the birth is
   trusted, deletes call `RemoveFromStore`, and a rename re-embeds the new name only when
   the renamed tag was promoted.
2. **Daemon startup**: `BuildTagCache` seeds the store from the promoted names, so
   provisional tags stay out across restarts; a warm start finds those names' vectors
   already in the text-embedding cache and encodes only the ones it has not seen.
3. **Enrichment** (`internal/enrichment`): `Runner.MatchTags` before the analysis call
   (matches above the injection bar become `tagSuggestions`; on error or zero matches the
   enricher falls back to the promoted-name list); `Runner.RankTags` after the LLM pass,
   feeding the identity pipeline ([consolidation](consolidation.md) §2); the judge calls
   (`JudgeTagIdentity`, `AdjudicateTags`, `JudgeTagQuality`) ride the same runner and
   timeout discipline.
4. **API tag creation** triggers a store refresh through the same service.

## 12. Gotchas

- **Tag names are canonical before they reach the store** — `textnorm.NormalizeTag` runs
  at every tag-name write site; the cache and the matcher use names verbatim, so store
  keys always equal DB names.
- **The store is the tag universe.** Tags not in `entries` can never be suggested, and
  `Match` returns nothing when the store is empty — even if the model works. With the
  quality layer on, this exclusion is deliberate for provisional tags.
- **Chunk size is clamped by the model**, not just by config; raising `chunk_size` beyond
  the model limit is silently ignored.
- **BGE-M3's 1024-dim vectors** mean `Entries()` deep copies ~4 KB per tag per call —
  fine at hundreds of tags, wasteful at hundreds of thousands.
- **Mean pooling ≠ the model's own pooling.** `encodeChunked` averages window
  embeddings; the result is an approximation of the full-document embedding. The 10%
  overlap exists precisely to soften window-boundary effects.
- **ORT first run needs `curl` + internet** and a correct pinned SHA-256; the download
  is path-sensitive (`--strip-components=2`, renamed to `libonnxruntime.so`).
- **The daemon must be running before `edub` starts**; the client wraps all failures in
  `ErrMatcherUnavailable`, and tag endpoints 503 while it is down. `kushim hugot` does
  not auto-start from `edub`.
- **`h == nil` guards**: every `Hugot` method starts with a "tag matcher not initialized"
  error — the matcher can be absent when unconfigured; callers must treat that as a
  graceful failure, not a panic.
- **`topN == 0` or an empty store short-circuits** `Match`; a config can no longer
  disable suggestions (`top_n` validates ≥ 3 at load).
- **Short texts are cached too** — store builds, tag CRUD re-embeds and identity-rank
  queries ride the batched short path, and their vectors land in `matcher_text_embedding`
  alongside document texts, so a repeat short input (a tag name re-embedded at CRUD or at
  daemon start) is a cache read, not an inference call.
- **The ORT memory flags and the three thresholds are derived, not settable** — they live
  in `DefaultConfig` and the per-model derivation switches; a config that sets
  `identity_candidate_similarity: 0` disables candidate generation entirely (the
  documented "disable consolidation candidates" knob).

## See also

- [Consolidation](consolidation.md) — identity decisions over the matcher's candidates
- [Tag specs](../specs/tags/overview.md) — the normative definition and pipeline
- [CLI](../manual/cli.md) / [API](../manual/api.md) usage and the
  [configuration reference](../manual/configuration.md)
- [Architecture](../architecture.md) — matcher server design and process model
