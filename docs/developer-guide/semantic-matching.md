# Developer Guide — Semantic Tag Matching with Hugot

This guide explains how edub-kushim turns documents into tag suggestions using
semantic similarity: the embedding model, the in-memory embedding store, the
chunking/mean-pooling encoding strategy, cosine ranking, post-LLM
consolidation, and the daemon that exposes it all over a Unix socket.

It is the internal-mechanics companion to `docs/tag-matcher.md` (which is
user-facing). Audience: developers who want to understand, tune, or change how
tags get suggested.

---

## Table of contents

1. [Orientation](#1-orientation)
2. [The embedding model and backends](#2-the-embedding-model-and-backends)
3. [The embedding store](#3-the-embedding-store)
4. [Bootstrapping the store](#4-bootstrapping-the-store)
5. [Encoding: short vs long texts](#5-encoding-short-vs-long-texts)
6. [Matching: cosine ranking against the store](#6-matching-cosine-ranking-against-the-store)
7. [Consolidation: tag-to-tag](#7-consolidation-tag-to-tag)
8. [Interfaces: local and remote implementations](#8-interfaces-local-and-remote-implementations)
9. [The matcher daemon](#9-the-matcher-daemon)
10. [Thresholds and configuration](#10-thresholds-and-configuration)
11. [Where it plugs into the pipeline](#11-where-it-plugs-into-the-pipeline)
12. [Gotchas](#12-gotchas)

---

## 1. Orientation

Semantic matching happens at two points in the enrichment pipeline:

1. **Tag suggestions for the LLM** — before the LLM analyzes a document, the
   reduced text is embedded and matched against the known tags; the top
   matches are passed into the prompt as "prefer these tags if relevant"
   (`internal/enrichment/enricher.go:106-118`).
2. **Post-LLM consolidation** — the tags the LLM produced are re-embedded and
   mapped onto the canonical tag names ("concept → existing tag") when
   similarity is high enough (`enricher.go:230-237`).

The core type is `*tagmatcher.Hugot` (`internal/tools/adapters/tagmatcher/hugot.go`),
an in-process embedding model (Hugot + ONNX runtime or pure-Go backend) that
implements the `Matcher` and `Embedder` interfaces
(`internal/tools/adapters/tagmatcher/adapter.go:5-24`). Two deployments exist:

- **In-process** (`kushim` binary, cgo build): `*Hugot` runs the model directly.
- **Remote** (`edub` binary, no cgo): `*tagmatch.MatcherClient` speaks HTTP over
  a Unix socket to the `kushim hugot` daemon (§9).

Both satisfy the same interfaces, so the rest of the system cannot tell them
apart. The tag service (`internal/service/tag.go`) holds whichever one was
wired in as its `embedder`.

The whole flow is: **tags → embeddings (store) → document text → embedding →
cosine rank → top-N suggestions**.

---

## 2. The embedding model and backends

### Model and pipeline

`NewHugot` builds a Hugot feature-extraction pipeline from a Hugging Face
model directory (`hugot.go:50-103`):

```go
pipeline, err := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
	ModelPath:    modelPath,
	OnnxFilename: "model.onnx",
	Name:         pipeName,
	Options: []backends.PipelineOption[*pipelines.FeatureExtractionPipeline]{
		pipelines.WithNormalization(),
	},
})
```

- The default model is **`BAAI/bge-m3`** (1024-dim, multilingual) — see
  `internal/config/config.go` `DefaultConfig` (`TagMatcher.Hugot.Model`).
  The model is downloaded separately (by the setup flow / `kushim setup`),
  not by the matcher itself.
- `WithNormalization()` L2-normalizes every embedding. That is why cosine
  similarity collapses to a plain dot product (`hugot.go:418-430`):

```go
// Since both vectors are L2-normalized (enforced by pipeline.WithNormalization()),
// this is equivalent to a simple dot product.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}
```

### Backend session: ORT vs Go

`getBackendSession` (`hugot.go:450-482`) switches on `Hugot.Backend`:

- **`"ort"`** — ONNX Runtime via `hugot.NewORTSession`, with two options
  that matter for memory (the comment at `hugot.go:460-472` is the definitive
  rationale):

```go
return hugot.NewORTSession(context.Background(),
	options.WithOnnxLibraryPath(tmCfg.Hugot.BackendLibPath),
	options.WithCPUMemArena(tmCfg.Hugot.CpuMemArena),
	options.WithMemPattern(tmCfg.Hugot.MemPattern),
)
```

  `WithCPUMemArena(false)` + `WithMemPattern(false)` (the defaults) cap idle
  RSS at ~2.2–2.5 GB with BGE-M3 instead of ~4–5 GB, at the cost of ~10–20%
  per-inference latency. Toggle them only if latency matters more than memory.
- **anything else (`"go"`)** — `hugot.NewGoSession`, pure Go, no runtime
  downloads. The XLA backend is commented out (`hugot.go:452-453`).

### The ONNX runtime download

The ORT backend needs `libonnxruntime.so`, which is downloaded on first use if
missing (`downloadLib`, `hugot.go:489-521`): `curl` the pinned
`onnxruntime-linux-x64-1.26.0.tgz` from GitHub, **verify its SHA-256 against a
hardcoded digest** (`onnxruntimeTgzSHA256`, `hugot.go:487`), extract only the
`.so` with `tar --strip-components=2`, and rename it into
`<config-dir>/tagmatcher/hugot/libs/`.

**Gotcha**: the digest and URL are pinned together — updating one without the
other breaks the build or the download. Also note this requires `curl` and
internet at first run of the ORT backend (`AGENTS.md` flags this).

---

## 3. The embedding store

Matched against *every* tag for *every* document — so tag embeddings are
cached in memory: `cache.EmbeddingStore` (`internal/cache/embedding_store.go:3`).

```go
type EmbeddingStore struct {
	storeBase
	entries map[string][]float32
}
```

- It embeds `storeBase` (`internal/cache/cache.go:16`), which owns the
  `sync.RWMutex` (`myu`) and the `Attr/Attrs` map (used for metadata like
  `dim`, `model`, `normalized`).
- Reads (`Keys`, `Len`, `Entries`) take `RLock`; writes (`Add`, `Remove`)
  take `Lock`.
- **`Entries()` returns a deep copy** — both the map and every vector
  (`embedding_store.go:49-58`). Callers like `Match` iterate the copy
  lock-free; the cost is a copy per call, which is fine at tag-store scale.
- A named-store registry (`cache.Cache`, `cache.go:37`) maps names
  (`"tags"`) to stores with a second RWMutex.

The store is *authoritative for matching*: `Match` iterates whatever is in
`entries` — the cache **is** the tag universe. If a tag is missing from the
store, it can never be suggested.

---

## 4. Bootstrapping the store

`cache.BuildTagCache` (`internal/cache/bootstrap.go:16`) rebuilds the store
from the `tag` table:

1. Loads all tag names (`ListAllTagsNames`).
2. Normalizes each name for embedding:

```go
spaceRE := regexp.MustCompile(` +`)
// normalizeForEmbedding counterpart exists in internal/tools/adapters/tagmatcher/hugot.go — keep in sync.
for i, name := range tagNames {
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	name = spaceRE.ReplaceAllString(name, " ")
	tagNames[i] = strings.TrimSpace(name)
}
```

   Note the explicit **"keep in sync"** contract: the same normalization must
   be applied at add/remove/consolidate time in `hugot.go:116-121`
   (`normalizeForEmbedding`). If the two drift, a tag stored as
   `machine-learning` won't match a query normalized to `machine learning`.
3. Encodes in batches of `batchSize = 32`, then **swaps the whole map under
   one lock** (`bootstrap.go:59-61`) — an atomic publish; readers never see a
   half-built store.

The store is rebuilt at `kushim hugot` daemon startup, and incrementally
updated on tag CRUD (`service.Tag.encodeAndAddBatch` via `AddToStore`,
`service/tag.go:275-279`).

---

## 5. Encoding: short vs long texts

`Encode` (`hugot.go:258-319`) handles two very different inputs with one API:
tag names (a few words) and document text (thousands of tokens).

```go
for i, text := range texts {
	ids, err := h.tokenize(text, tk)
	...
	if len(ids) <= h.chunkSize {
		items[i] = item{index: i, short: true}
		shortTexts = append(shortTexts, text)
		shortIdx = append(shortIdx, i)
	} else {
		items[i] = item{index: i, short: false}
	}
}

// Batch-encode all short texts at once.
if len(shortTexts) > 0 {
	out, err := h.pipeline.RunPipeline(ctx, shortTexts)
	...
}
```

- **Short texts** (≤ chunk size) are batched into a single pipeline call —
  this is how the 32-at-a-time tag batches stay fast.
- **Long texts** take the chunked path, `encodeChunked`
  (`hugot.go:323-366`): tokenize → slice token IDs into overlapping windows →
  decode each window back to text (`backends.Decode`) → embed each window →
  **mean-pool** the window embeddings into one vector:

```go
step := h.chunkSize - h.chunkOverlap
...
for i := 0; i < n; i += step {
	end := min(i+h.chunkSize, n)
	chunkIDs := tokenIDs[i:end]
	chunkText, err := backends.Decode(chunkIDs, tk)
	...
	out, err := h.pipeline.RunPipeline(ctx, []string{chunkText})
	...
}
return meanPool(allEmbeddings), nil
```

### The chunk size comes from the model

`NewHugot` reads the model's `config.json` and derives the effective chunk
size (`hugot.go:74-88`):

```go
safetyMargin := 12
effectiveChunkSize := tmCfg.ChunkSize
if effectiveChunkSize <= 0 || effectiveChunkSize > maxPos-safetyMargin {
	effectiveChunkSize = maxPos - safetyMargin
}
chunkOverlap := effectiveChunkSize / 10
```

`max_position_embeddings` is the model's hard context limit; the config value
(`ChunkSize`, default 4096) is clamped into `[1, maxPos-12]` — the 12-token
margin accounts for special tokens. Overlap is 10% of the chunk size
(`chunkOverlap := effectiveChunkSize / 10`), so a 4096-token chunk slides by
~3686 tokens per step.

---

## 6. Matching: cosine ranking against the store

`Match` (`hugot.go:174-212`) is the document→tags query:

```go
entries := h.store.Entries()
if len(entries) == 0 || h.topN == 0 {
	return nil, nil
}
embeddings, err := h.Encode(ctx, &docId, []string{input})
...
inputEmb := embeddings[0]
matches := h.rankMatches(inputEmb, entries, h.minSimilarity)
topN := min(h.topN, len(matches))
result := make([]string, topN)
for i := range topN {
	result[i] = matches[i].tag
}
```

`rankMatches` (`hugot.go:404-416`) computes the dot-product similarity against
every cached tag embedding, keeps those `>= minSim`, and sorts descending:

```go
for tag, tagEmb := range entries {
	sim := cosineSimilarity(queryEmb, tagEmb)
	if sim >= minSim {
		matches = append(matches, match{tag: tag, similarity: sim})
	}
}
sort.Slice(matches, func(i, j int) bool {
	return matches[i].similarity > matches[j].similarity
})
```

The result is capped at `TopN` (15). The top scores are logged for debugging
(`hugot.go:200-209`) — useful when tuning thresholds.

---

## 7. Consolidation: tag-to-tag

`Consolidate` (`hugot.go:214-249`) maps LLM-produced tag names onto canonical
existing tags — the "the LLM said 'machine learning' but the store has
'machine-learning'" fix. Differences from `Match`:

- It encodes the query tag names (not document text) — each is short, so it
  rides the batched path.
- It uses a **separate, higher threshold** (`consolidationSim`), because
  tag-name-to-tag-name similarity is inherently sparser than
  document-to-tag (see §10):

```go
for i, qEmb := range out.Embeddings {
	matches := h.rankMatches(qEmb, entries, h.consolidationSim)
	if len(matches) > 0 {
		result[i] = matches[0].tag     // replace with the canonical tag
	} else {
		result[i] = queries[i]         // keep the LLM's name
	}
}
```

- If `consolidationSim == 0.0` (disabled) or the store is empty, queries pass
  through unchanged (`hugot.go:219-221`).

`service.Tag.Consolidate` (`internal/service/tag.go:271-273`) is a thin
delegation to the embedder, called by the enricher after `FilterTags`
(`enricher.go:230-237`).

---

## 8. Interfaces: local and remote implementations

The interfaces (`internal/tools/adapters/tagmatcher/adapter.go`):

```go
type Matcher interface {
	Match(ctx context.Context, docId, input string) ([]string, error)
	Close()
	Name() string
}

type Embedder interface {
	Encode(ctx context.Context, docId *string, texts []string) ([][]float32, error)
	Consolidate(ctx context.Context, docId string, queries []string) ([]string, error)
	AddToStore(ctx context.Context, names []string) error
	RemoveFromStore(ctx context.Context, names []string) error
	Close()
	Name() string
}
```

- `*Hugot` implements both (in-process, cgo build).
- `*tagmatch.MatcherClient` (`internal/tagmatch/client.go:44`) implements both
  over the Unix socket — used by `edub`, which cannot load the model. The RPC
  surface is `Match`, `Consolidate`, `AddToStore`, `RemoveFromStore`,
  `Health` (`internal/commands/hugot.go:95-99`).
- The client wraps every call in `ErrMatcherUnavailable` when the socket
  daemon is down — which is what turns tag CRUD into 503.

The `EmbeddingStore` interface (`adapter.go:20-24`: `Add`/`Remove`/`Entries`)
is implemented by `cache.EmbeddingStore` and by the test double
`testutil.MockEmbedder`.

---

## 9. The matcher daemon

`kushim hugot` (`internal/commands/hugot.go`) is the daemon that loads the
model once and serves RPC over a Unix socket (`<config-dir>/kushim-hugot.sock`).

- **Startup**: removes a stale socket, builds the tag cache
  (`cache.BuildTagCache`), opens the listener, serves HTTP with generous
  timeouts (`ReadTimeout: 30s`, `WriteTimeout: 120s`, `IdleTimeout: 30s`).
- **`--bg`** re-execs itself detached with stdio nulled (same pattern as
  `kushim queue --bg`).
- **Duplicate detection**: `net.Listen("unix", ...)` fails with `EADDRINUSE`
  if the daemon is already running — detected via `errors.As` to
  `syscall.Errno` (`hugot.go:104-108`).
- **systemd readiness**: `notifyReady` (`hugot.go:149-167`) writes
  `READY=1` to the socket named by `NOTIFY_SOCKET` (unixgram; abstract
  sockets with a leading `@` become `\x00`-prefixed).
- **Shutdown**: SIGTERM/SIGINT → `server.Shutdown(ctx)` with a 5s budget →
  `os.Remove(socketPath)`.

The HTTP endpoints are body-size-capped RPC handlers (`bodyCap`), and the
client always has a 120s timeout since encoding large documents can take a
while.

---

## 10. Thresholds and configuration

All knobs live under `enricher.tagmatcher` in the config
(`internal/config/config.go`):

| Key | Default | Meaning |
|---|---|---|
| `hugot.model` | `BAAI/bge-m3` | HF model id; `ModelPath` derived as `<config-dir>/tagmatcher/hugot/models/<short-name>` |
| `hugot.backend` | `ort` | `ort` or `go` |
| `hugot.chunk_size` | `4096` | max tokens per embedding chunk (clamped to model limit − 12) |
| `hugot.cpu_mem_arena` / `mem_pattern` | `false` | ORT memory knobs (§2) |
| `top_n` | `15` (derived) | max suggestions returned |
| `min_similarity` | model-derived | document→tag threshold |
| `consolidation_similarity` | model-derived | tag→tag threshold |
| `timeout` | `120` (s) | per-call budget |
| `reduce_target_words` | `4000` | text is reduced to ≤ this many words before embedding |

The thresholds are **derived from the model's embedding dimension**
(`config.go:504-508`), because higher-dimensional models cluster tighter:

```go
// defaultMinSimilarity ... Larger models with higher-dimensional embeddings
// tend to produce tighter clusters, requiring a higher threshold ...
func defaultMinSimilarity(modelShortName string) float64 {
	switch modelShortName {
	case "bge-m3":
		return 0.40
	case "all-mpnet-base-v2":
		return 0.30
	case "all-MiniLM-L6-v2":
		return 0.25
	default:
		return 0.30
	}
}

// defaultConsolidationSimilarity ... must be higher than the document-to-tag
// threshold because single tag names have much sparser semantic signal ...
func defaultConsolidationSimilarity(modelShortName string) float64 {
	switch modelShortName {
	case "bge-m3":
		return 0.80 // 1024-dim; reduced from 0.82 due to SentencePiece tokenization variance
	case "all-mpnet-base-v2":
		return 0.75 // 768-dim
	case "all-MiniLM-L6-v2":
		return 0.70 // 384-dim, compressed distribution
	default:
		return 0.75
	}
}
```

Rules of thumb: raise `min_similarity` if false positives appear; keep
`consolidation_similarity` well above it. If you add a model to the
catalog/derivation, add it to both switch statements.

---

## 11. Where it plugs into the pipeline

1. **Tag CRUD** (`service/tag.go`): create/delete tags call
   `AddToStore`/`RemoveFromStore` to keep the live store in sync
   (`encodeAndAddBatch`, `service/tag.go:275-279`).
2. **Daemon startup** (`commands/hugot.go`): `BuildTagCache` seeds the store.
3. **Enrichment** (`internal/enrichment/enricher.go`):
   - `runner.MatchTags(ctx, docId, reducedText)` → `tagSuggestions`; on error
     or zero matches, falls back to the full tag-name list
     (`enricher.go:106-118`).
   - `service.Tag.Consolidate` after the LLM pass (`enricher.go:230-237`).
4. **API tag creation** triggers a store refresh through the same service.

---

## 12. Gotchas

- **`normalizeForEmbedding` has two copies** — `hugot.go:116-121` and
  `bootstrap.go:32-38` — with a "keep in sync" comment on each. Change one,
  change both.
- **The store is the tag universe.** Tags not in `entries` can never be
  suggested, and `Match` returns nothing when the store is empty — even if
  the model works.
- **Chunk size is clamped by the model**, not just by config: `config.json`
  `max_position_embeddings` minus a 12-token safety margin. Raising
  `chunk_size` beyond the model limit is silently ignored.
- **BGE-M3's 1024-dim vectors** mean `Entries()` deep copies ~4 KB per tag
  per call — fine at hundreds of tags, wasteful at hundreds of thousands.
- **Mean pooling ≠ the model's own pooling.** `encodeChunked` averages window
  embeddings; the result is an approximation of the full-document embedding.
  Overlap (10%) exists precisely to soften window-boundary effects.
- **ORT first run needs `curl` + internet** and a *correct* pinned SHA-256
  (`hugot.go:487`); the download is extracted with `--strip-components=2`
  and renamed to `libonnxruntime.so` — path-sensitive.
- **The daemon must be running before `edub` starts** (AGENTS.md); the client
  wraps all failures in `ErrMatcherUnavailable`, and tag endpoints 503 while
  it's down. `kushim hugot` does NOT auto-start from `edub`.
- **`h == nil` guards**: every `Hugot` method starts with
  `if h == nil { return fmt.Errorf("tag matcher not initialized") }` — the
  matcher can be absent when unconfigured; callers must treat that as a
  graceful no-op, not a panic.
- **`topN == 0` or empty store short-circuits** (`hugot.go:179-181`): a
  zero `top_n` disables suggestions entirely.
- **Consolidation with `consolidationSim == 0.0` passes through** — that's
  the documented "disable consolidation" knob, not a bug.

---

*Last verified against the tree: 2026-08-03. If code and doc disagree, code wins.*
