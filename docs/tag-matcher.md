# Tag Matcher (Hugot) — Configuration, Memory & CPU

The tag matcher is a standalone process (`kushim hugot`) that embeds document
text and tag names with a Hugot feature-extraction model (default
`BAAI/bge-m3`) and ranks documents against the tag store by cosine similarity.
It runs over a Unix domain socket (`<config_dir>/kushim-hugot.sock`) and is
used by both `edub` (RPC via `MatcherClient`) and `kushim` workers.

Because it loads a full transformer model into RAM and runs CPU-bound
inference per request, a misconfigured matcher is the most likely cause of
memory pressure (or outright OOM) on small hosts. This page explains every
`enricher.tagmatcher` setting, what the matcher costs in memory and CPU, and
how to size it for your machine.

- [Config reference](#config-reference)
- [`chunk_size` — the memory lever](#chunk_size--the-memory-lever)
- [Memory usage by configuration](#memory-usage-by-configuration)
- [CPU usage and concurrency](#cpu-usage-and-concurrency)
- [Backends: `ort` vs `go`](#backends-ort-vs-go)
- [Operating the matcher](#operating-the-matcher)

---

## Config reference

All settings live under `enricher.tagmatcher` in `config.yaml`:

```yaml
enricher:
  tagmatcher:
    timeout: 120            # RPC deadline in seconds; 0 = no artificial deadline
    reduce_target_words: 4000
    chunk_size: 4096        # max tokens per inference chunk; 0 = model max
    hugot:
      model: BAAI/bge-m3
      backend: ort          # ort (ONNX Runtime) | go
```

| Key | Default | Meaning |
| --- | ------- | ------- |
| `timeout` | `120` | Deadline for matcher RPC calls (match/consolidate) in seconds, applied as a context deadline on the client and mirrored into the matcher server's `WriteTimeout` at startup. `0` disables the deadline — not recommended for production. |
| `reduce_target_words` | `4000` | Word target for TextRank reduction applied *before* tag matching. Also drives the RPC request body cap (`reduce_target_words × 24` bytes, clamped to 256 KiB–4 MiB), bounding how much text can reach the matcher. |
| `chunk_size` | `4096` | Maximum number of tokens per inference window. See [below](#chunk_size--the-memory-lever). |
| `hugot.model` | `BAAI/bge-m3` | Model identifier. Downloaded on first start to `<config_dir>/tagmatcher/hugot/models/<model>` (requires internet). |
| `hugot.backend` | `ort` | Inference backend: `ort` (ONNX Runtime) or `go` (pure Go). See [Backends](#backends-ort-vs-go). |

Several knobs are intentionally **not** YAML-settable because they are
auto-derived or internal:

| Internal setting | Default | Notes |
| ---------------- | ------- | ----- |
| `top_n` | `15` | Max tags returned per document. |
| `min_similarity` | `0.40` (bge-m3) | Doc→tag threshold, tuned per model. |
| `consolidation_similarity` | `0.80` (bge-m3) | Tag→tag threshold for post-LLM consolidation. |
| `CpuMemArena` / `MemPattern` | `false` / `false` | ORT memory behavior; see [Memory usage](#memory-usage-by-configuration). Toggle only via `DefaultConfig` (code), not YAML. |

---

## `chunk_size` — the memory lever

`chunk_size` is the maximum number of tokens the model processes in a single
inference call. Its relationship to memory is the single most important thing
to understand about the matcher:

- Texts of **≤ `chunk_size` tokens** are embedded in one batched call.
- Longer texts are split into **overlapping windows** (10% overlap, i.e. the
  step is `chunk_size × 0.9`) and the per-chunk embeddings are **mean-pooled**
  into the document embedding. Quality loss from chunking is negligible for
  tag matching.

### Resolution and clamping

The effective chunk size is computed at startup from the model's
`config.json`:

1. `chunk_size: 0` (explicit) → the model's `max_position_embeddings`
   minus a 12-token safety margin — **8180 tokens for BGE-M3**.
2. `chunk_size` larger than the model max → clamped down to the same value
   (small models such as `all-MiniLM-L6-v2` with 512 max positions are
   unaffected by the 4096 default).
3. Anything else → used as-is.

`kushim hugot` logs the resolved values on startup:
`hugot: chunk_size=4096 overlap=409 topN=15 min_sim=0.40`.

### Why 4096 is the default

Prior to v2.9 the default was `0` — i.e. **full context (8180 tokens)**.
Attention memory grows roughly quadratically with sequence length, so a
full-context request can briefly need several times the memory of a 4096-token
request (BGE-M3 has 24 layers and 16 attention heads). On a machine with 8 GB
of RAM, a long document hitting full context is a realistic OOM trigger.

The default was changed to `4096`, which bounds per-request peaks to
~4–6 GB with BGE-M3 while keeping idle usage (~2.2–2.5 GB) unchanged — the
model weights dominate idle RSS regardless of `chunk_size`. A value of `0`
remains available for hosts where embedding quality at full context is worth
the memory cost (see the sizing table).

### Memory usage by configuration

Measured with the default `BAAI/bge-m3` model and the `ort` backend:

| Configuration | Idle RSS | Per-request peak | Notes |
| ------------- | -------- | ---------------- | ----- |
| `chunk_size: 4096` (default) + ORT defaults | ~2.2–2.5 GB | ~4–6 GB | Recommended for hosts with ≤ 16 GB RAM |
| `chunk_size: 0` (full context, 8180 tokens) | ~2.2–2.5 GB | can exceed 10 GB | OOM risk on 8 GB hosts |
| `chunk_size: 4096` + arena/pattern enabled | up to ~4–5 GB | higher | ~10–20% lower per-request latency; see below |

ORT's CPU memory arena and shape-pattern pre-allocation are **disabled by
default** (`CpuMemArena: false`, `MemPattern: false`). This makes ORT release
intermediate tensor buffers after each inference instead of retaining them in
a growing pool, capping idle RSS at ~2.2–2.5 GB. The cost is ~10–20% more
per-inference latency from re-allocation — dwarfed by text extraction, OCR,
and LLM calls in the enrichment pipeline. These flags are internal (not
YAML-settable); flip them in `DefaultConfig` only if you prefer lower latency
over memory headroom.

### Sizing guidance (BGE-M3, `ort` backend)

| Host RAM | Recommended `chunk_size` | Notes |
| -------- | ------------------------ | ----- |
| 8 GB | `2048`–`4096` | Never `0`. Consider `2048` if other services run on the host. |
| 16 GB | `4096` (default) | Sweet spot: safe peaks with margin for the OS and Postgres. |
| 32 GB+ | `4096`, or `0` for full-context quality | `0` removes all chunking; peaks are the only cost. |

Numbers above assume a single matcher process. If you raise
`enricher.workers` or `consumer.workers`, concurrent RPC requests can arrive
at the matcher at once and per-request peaks can overlap — account for that
in your headroom.

---

## CPU usage and concurrency

- The matcher is a **single process** and inference is CPU-bound. BGE-M3 is a
  570M-parameter encoder; a 4096-token encode is on the order of hundreds of
  milliseconds to a couple of seconds depending on core count.
- Both backends use all available CPU cores by default (ORT sizes its
  intra-op thread pool from the CPU count).
- Requests arrive via RPC from enrichment workers (`enricher.workers`,
  default 1), queue-daemon workers, tag CRUD (re-embedding), and re-enrich
  operations. Multiple concurrent requests are handled in parallel — memory
  peaks add up, which is why `chunk_size` matters most under load.
- `reduce_target_words: 4000` keeps typical match payloads around 4000 words
  (~100 KB, bounded by the 4 MiB RPC cap), so in normal operation
  most documents embed in a single 4096-token call and only long documents
  take the chunked path.

---

## Backends: `ort` vs `go`

| | `ort` (default) | `go` |
| --- | --- | --- |
| Runtime | ONNX Runtime via `libonnxruntime.so` | Pure Go (`libtokenizers.a`) |
| First-run download | ONNX Runtime (SHA-256 pinned, ~tens of MB) to `<config_dir>/tagmatcher/hugot/libs/` + the model | The model only |
| Startup | Slightly slower (library load) | Slightly faster |
| Memory behavior | Controllable via `CpuMemArena`/`MemPattern` | N/A — no arena |
| Typical use | Production | Minimal-dependency fallback |

The model itself (~2.2 GB on disk for BGE-M3) is downloaded once on first
start for both backends and requires internet access at that point.

---

## Operating the matcher

### Start order and degradation

`kushim hugot` must be running before `edub` starts for full functionality.
If it is unreachable, tag CRUD still succeeds (embedding-store sync is
skipped with a logged error) and enrichment falls back to LLM-only tags —
the pipeline degrades, it does not fail.

### Memory capping (systemd)

Even with the sane default, an explicit cap on the matcher unit protects the
rest of the host from a future misconfiguration. Add a drop-in for the
`kushim-hugot` service:

```ini
[Service]
MemoryHigh=6G   # throttle under pressure before the hard cap
MemoryMax=8G    # hard cap: systemd kills the matcher, not your other services
```

When the matcher exceeds `MemoryMax`, systemd OOM-kills only the matcher
process; the rest of the system keeps running. Adjust the values to your
`chunk_size` choice (see the sizing table above).

### Observability

- Health: `curl --unix-socket <config_dir>/kushim-hugot.sock /health` → `{"ok":true}`
- Logs: `<config_dir>/logs/hugot.log`, or `journalctl -u kushim-hugot.service`
- Startup line to verify effective settings:
  `hugot: chunk_size=4096 overlap=409 topN=15 min_sim=0.40`
- RSS: `ps -o pid,rss,cmd -C kushim | grep hugot` — expect ~2.2–2.5 GB idle
  with the defaults.
- `TimeoutStartSec=120` covers model load (~10 s) plus tag-cache build
  (~31 s per 10k tags). Raise it for 100k+ tag stores.

### Quick answers

| Question | Answer |
| -------- | ------ |
| My 8 GB host OOMs during enrichment — what now? | Set `enricher.tagmatcher.chunk_size: 2048` and restart `kushim hugot`. Idle RSS drops toward ~2 GB, peaks to ~3–4 GB. |
| I want the best matching quality and have 32 GB+. | `chunk_size: 0` — full 8180-token context, no chunking. |
| Why is the matcher using 2.2 GB when idle? | That is the model resident in RAM — it is loaded once and stays hot for the lifetime of the process. |
| Does the web UI expose these settings? | The Settings → Enricher → Tag matcher section edits `timeout`, `reduce_target_words`, `chunk_size`, model, and backend. |

---

## See Also

- [User Manual](user-manual.md) — CLI/API reference, full config reference
- [Architecture](architecture.md) — matcher server design and process model
- [Code Reference: tools](reference/tools.md) — `adapters/tagmatcher/hugot.go` internals
