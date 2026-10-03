# Developer Guide — Semantic Tag Matching with Hugot

This guide explains how edub-kushim turns documents into tag suggestions using
semantic similarity: the embedding model, the in-memory embedding store, the
chunking/mean-pooling encoding strategy, cosine ranking, post-LLM
consolidation, and the daemon that exposes it all over a Unix socket.

It is the internal-mechanics companion to `docs/tags/matcher.md` (which is
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
   reduced text is embedded and matched against the known tags; the matches at
   or above the injection bar (`enricher.tagmatcher.min_suggestion_similarity`,
   §10) are passed into the prompt as "prefer these tags if relevant"
   (`internal/enrichment/enricher.go:132-151`).
2. **Post-LLM consolidation and identity** — the tags the LLM produced are
   re-embedded and mapped onto the canonical tag names ("concept → existing
   tag") by identity — house form, alias lookup, key equality — and by the
   same-intent gate, which adjudicates candidate neighbours with an LLM when
   `enricher.tag_adjudicator.enabled` is true (§7). Similarity ranks
   candidates; it never replaces a name.

The core type is `*matcher.Hugot` (`internal/matcher/hugot.go`),
an in-process embedding model (Hugot + ONNX runtime or pure-Go backend) that
implements the `Matcher` and `Embedder` interfaces
(`internal/matcher/adapter.go:39-68`). Two deployments exist:

- **In-process** (`kushim` binary, cgo build): `*Hugot` runs the model directly.
- **Remote** (`edub` binary, no cgo): `*matcher.MatcherClient` speaks HTTP over
  a Unix socket to the `kushim hugot` daemon (§9).

Both satisfy the same interfaces, so the rest of the system cannot tell them
apart. The tag service (`internal/tags/curation/tag.go`) holds whichever one was
wired in as its `embedder`.

The whole flow is: **tags → embeddings (store) → document text → embedding →
cosine rank → top-N suggestions**.

---

## 2. The embedding model and backends

### Model and pipeline

`NewHugot` builds a Hugot feature-extraction pipeline from a Hugging Face
model directory (`hugot.go:68-122`):

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
  similarity collapses to a plain dot product — `cosineSimilarity`
  (`hugot.go:563-572`) is just the sum of element-wise products, with a
  length guard:

```go
// Since both vectors are L2-normalized (enforced by pipeline.WithNormalization()),
// this is equivalent to a simple dot product.
```

### Backend session: ORT vs Go

`getBackendSession` (`hugot.go:592-624`) switches on `Hugot.Backend`:

- **`"ort"`** — ONNX Runtime via `hugot.NewORTSession`, with two options
  that matter for memory (the comment at `hugot.go:602-614` is the definitive
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
  downloads. The XLA backend is commented out (`hugot.go:594-595`).

### The ONNX runtime download

The ORT backend needs `libonnxruntime.so`, which is downloaded on first use if
missing (`downloadLib`, `hugot.go:631-663`): `curl` the pinned
`onnxruntime-linux-x64-1.26.0.tgz` from GitHub, **verify its SHA-256 against a
hardcoded digest** (`onnxruntimeTgzSHA256`, `hugot.go:629`), extract only the
`.so` with `tar --strip-components=2`, and rename it into
`<config-dir>/tagmatcher/hugot/libs/`.

**Gotcha**: the digest and URL are pinned together — updating one without the
other breaks the build or the download. Also note this requires `curl` and
internet at first run of the ORT backend (`AGENTS.md` flags this).

---

## 3. The embedding store

Matched against *every* tag for *every* document — so tag embeddings are
cached in memory: `matcher.TagEmbeddingStore` (`internal/matcher/embedding_store.go:9`).

```go
type TagEmbeddingStore struct {
	mu      sync.RWMutex
	entries map[string][]float32
}
```

It owns its `sync.RWMutex` directly: reads take `RLock`, writes take `Lock`,
and `Entries()` returns a deep copy so callers never race the lock (the
discipline is covered in `golang.md` §10). `Hugot.AddToStore` and
`RemoveFromStore` mutate it through `Add`/`Remove`, and `BuildTagCache` swaps
the complete map under the same lock.

The store is *authoritative for matching*: `Match` iterates whatever is in
`entries` — the cache **is** the tag universe. If a tag is missing from the
store, it can never be suggested.

---

## 4. Bootstrapping the store

`matcher.BuildTagCache` (`internal/matcher/bootstrap.go:17`) rebuilds the store
from the `tag` table:

1. Loads the **promoted** tag names (`ListPromotedTagNames`, i.e.
   `promoted_at IS NOT NULL`). Store membership is the trust boundary: a
   provisional tag (`promoted_at` NULL — a quarantined newborn, a
   zero-assignment legacy row) is excluded from every injection point, so it is
   never suggested, never a consolidation target, and never part of the
   all-names fallback vocabulary. Promotion adds the name incrementally
   (`curation.Tag.AddToStore` after the promotion check); a re-run of
   `BuildTagCache` after a restart converges the store with the table either
   way.
2. Embeds them **verbatim** — no per-key transformation. Tag names are
   already canonical: `curation.Tag.Create`/`Update` normalize every name
   through `textnorm.NormalizeTag` (`internal/textnorm/tagname.go`) before it
   reaches the DB, so the stored name is the embedding key and the matcher
   never re-derives a different form.
3. Encodes in batches of `embedBatchSize = 32`, then **swaps the whole map
   under the store's lock** (`bootstrap.go:42-44`), an atomic publish: readers
   never see a half-built store.

The store is rebuilt at `kushim hugot` daemon startup, and incrementally
updated on tag CRUD (`curation.Tag.encodeAndAddBatch` via `AddToStore`,
`service/tag.go:275-279`): a create adds names only for trusted births, a rename
re-embeds the new name only when the renamed tag was promoted, and the enricher
adds promoted names after the promotion check commits. `AddToStore`,
`RemoveFromStore`, `Consolidate` and `Rank` in `hugot.go` likewise use their
inputs verbatim — the canonical form is guaranteed upstream (DB names on the
store side, `analyzer.NormalizeTags` on the query side), so any per-call
normalization would only introduce drift.

---

## 5. Encoding: short vs long texts

`Encode` (`hugot.go:400-461`) handles two very different inputs with one API:
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
  (`hugot.go:465-508`): tokenize → slice token IDs into overlapping windows →
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
size (`hugot.go:100-106`):

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

`Match` (`hugot.go:281-319`) is the document→tags query:

```go
entries := h.store.Entries()
if len(entries) == 0 || h.topN == 0 {
	return nil, nil
}
embeddings, err := h.Encode(ctx, docId, []string{input})
...
inputEmb := embeddings[0]
matches := h.rankMatches(inputEmb, entries, h.minSimilarity)
topN := min(h.topN, len(matches))
result := make([]Candidate, topN)
for i := range topN {
	result[i] = Candidate{Tag: matches[i].tag, Similarity: matches[i].similarity}
}
```

`rankMatches` (`hugot.go:546-558`) computes the dot-product similarity against
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
(`hugot.go:307-316`) — useful when tuning thresholds.

---

## 7. Consolidation: tag-to-tag

`Rank` (`hugot.go:358-390`) ranks LLM-produced tag names against canonical
existing tags — the "the LLM said 'machine learning' but the store
has 'machine-learning'" fix. Differences from `Match`:

- It encodes the query tag names (not document text) — each is short, so it
  rides the batched path.
- It uses a **separate, higher threshold** (`consolidationSim`), because
  tag-name-to-tag-name similarity is inherently sparser than
  document-to-tag (see §10).
- It returns **candidates with scores**; the identity decision is the
  enricher's:

```go
for i, qEmb := range out.Embeddings {
	results[i].KeptName = normalized[i]
	matches := h.rankMatches(qEmb, entries, h.consolidationSim)
	if len(matches) > rankCandidates {
		matches = matches[:rankCandidates]
	}
	for _, m := range matches {
		results[i].Candidates = append(results[i].Candidates,
			Candidate{Tag: m.tag, Similarity: m.similarity})
	}
}
```

- `KeptName` is the **normalized** query after a completed ranking, but the
  **original** query when ranking short-circuits (empty store,
  `consolidationSim == 0.0`, or an embedding-count mismatch).
- If `consolidationSim == 0.0` (disabled) or the store is empty, queries pass
  through unchanged with no candidates.

`Runner.RankTags` (`internal/tools/runner.go`) wraps the call with the
configured timeout, and the enricher applies the identity pipeline locally
(`internal/tags/admission/identity.go`) with candidate generation from
`policy.Evaluate` (`internal/tags/policy/policy.go`).
Per query the policy runs, in order: short-slice and empty-candidates
passthrough, an identity fast path (exact equality with a store tag →
apply the existing label), and then candidate generation — every remaining
candidate neighbour is kept as a **gate subject** with its similarity and
runner-up recorded as adjudication evidence. Similarity never replaces or
keeps on its own; gates and identity do. Each disposition is logged as
`identity[v4]: apply|alias|defer|separate …`. Fallback results from the legacy
daemon (no candidates) pass through `KeptName` verbatim. Style variants and
aliases never reach the policy: M1 house form, M3 key equality, and the alias
resolver apply the canonical value before ranking output is used.

### Identity adjudication

Identity has two LLM paths, both under `enricher.tag_adjudicator.enabled`:

1. **Folds** — a mechanically proposed lexical fold flags a candidate: the
   lemmatizer and a conservative plural strip for tokens the dictionary does
   not know (a plural whose base form exists folds unless it is the established
   name of the sense), the store-anchored derivational transforms for
   relational adjectives (`chilean` → `chile`, `political` → `politics`), and
   the bounded morphographic proposal for single-token names (`mexican` →
   `mexico`, at most two edits; two distinct store values in reach make the
   candidate stand). A proposal only stands when its target already exists in
   the store. The flagged candidate is judged by one batched identity-judge
   call (`JudgeTagIdentity`) answering three binary criteria: fold to base form
   (J1), fold preserves denotation (J2), conventional written form (J5). The
   fold applies only when all three confirm; a candidate whose criteria lack a
   judge answer stays provisional with the three criteria recorded as pending
   evidence. A refused fold whose proposal targeted a live store value holds
   the birth for review: the registration is provisional and the
   evidence-derived promotion hold blocks inline and sweep promotion until an
   operator resolves it.
2. **Same-intent pairs** — every gate subject is adjudicated:
   - **Pair cache** — the latest `same_intent` row per `(candidate, partner)`
     at `IdentityRuleSetVersion` from `tag_identity_event` resolves the pair
     with no LLM call, carrying the direction in its criteria payload.
   - **Evidence** — one batched `RankTags` call runs target-as-query for the
     remaining pairs (a rank failure drops the evidence, not the pair).
   - **Verdict** — one `AdjudicateTags` call per batch (`ContentAnalyzer.Adjudicate`,
     temperature 0, structured output where the provider supports it)
     classifies each pair against the same-intent test — can either term stand
     for the other without loss or change of meaning? — as `same` / `variant` /
     `related`, plus a direction for `same` (`query`, `target`, or `tie`; a tie
     falls back to lexical order). `same` **aliases** the losing name onto the
     winner (a losing store label becomes the alias, its assignments are
     re-pointed, and its own aliases are retargeted); `variant`/`related`
     register both names. Any error, empty result, or ambiguous entry leaves
     those pairs pending, with the candidates born provisional and a pending
     `same_intent` row written.

Decided pairs write an identity-evidence row (criteria + direction + model) and,
when the emission carries a document, also an append-only `tag_verdict_event`
row: fresh verdicts record the judging model, cache-applied ones store NULL
`verdict_model`. A partial unique index on `(query, target, policy_version)
WHERE verdict_model IS NOT NULL` deduplicates fresh document-anchored verdicts
across concurrent workers: the losing insert re-reads the cached verdict and
applies it as cache-applied. Pending identity evidence is resolved later by the
triggered run or the reconciler's identity sweep; whichever runs first settles
the claim for both. When the adjudicator is unavailable (disabled, or failed to
construct), both paths are skipped: candidates register provisionally and every
claim waits as pending evidence.

### Rubric judge for novel tags

Consolidation decides what is a duplicate; the admission decision decides what
deserves to be born. With `enricher.tag_quality.enabled: true` (default
`false`), the enricher filters the consolidated list **before persist**: only
tags whose normalized names are absent from the tag table are candidates (a tag
that already exists was born long ago and is never re-judged in flight; a
provisional name with no verdict row waits for the scheduled sweep or the
review queue instead).

Each candidate's features are resolved by the pinned rule set
(`tags.DecideAdmission`):

- the judge's five rubric answers — `subject_matter`, `dimension`,
  `not_person_place_work`, `atomic`, `not_form_genre` — plus its optional
  `defer` abstention, from the verdict cache or a fresh judge call;
- the emission's deterministic signals: a detail annotated `other`, and a best
  store similarity below the internal `no_anchor_similarity` (0.60) cut.

A judge abstention defers; two or more failing signals reject; exactly one
defers to review (the gray band — no single signal decides); none admits. A
rejection drops the tag for that document, and `Plan.DropTagNames` removes its
registration and pending identity evidence before persist, so the name is never
stored; it logs `tagquality[v3]: reject … (reason=corroborated_failures …)`.
Defers are kept and logged too. A missing detail or similarity is absence of
evidence, not a failing signal, so a well-judged string-shaped emission can
still admit.

The judge input resolves in two steps:

1. **Verdict cache** — `LatestTagQualityEventsForNames` reads the latest row
   per name at the current `TagQualityVersion` from `tag_quality_event`.
   Cached verdicts apply with no LLM call; the decision re-runs over the
   stored rubric with the current emission's deterministic signals, so a
   cached row is evidence, not a frozen verdict.
2. **Verdict** — one temperature-0 `JudgeTagQuality` call per document for
   the uncached names. The prompt carries the reduced document text (the same
   text the generator saw) and one line per candidate with its term, typed
   category, and — for whitelisted countries/continents — an explicit note
   that the tag must not be rejected for being a place.

Fresh verdicts write a row with the judging model in `verdict_model` and the
recorded outcome (`reject`/`defer`/`admit`); the partial unique index on
`(tag_name, policy_version) WHERE verdict_model IS NOT NULL` deduplicates
concurrent workers, and a losing insert re-reads the cached verdict, applies
it, and writes a cache-applied row with NULL `verdict_model`. Rows predating
the outcome column carry NULL and fall back to `pass`, and rows predating the
rubric fall back to their `pass` flag (admit/reject) until the store reset
retires them. A judge error, timeout, or incomplete response retries once and
then keeps every novel tag with a defer decision and an error log — under the
quality gate those kept names are born **provisional** (no verdict row), so
they stay out of the vocabulary and await the reconciliation's re-judge.

The judge LLM and timeout default to the content analyzer's
(`ResolvedQualityLlm`/`ResolvedQualityTimeout`, resolved at use time); config
validation logs a warning when the resolved judge equals the generator, since
a shared model biases every verdict with the generator's own failure modes.
The decision thresholds (`no_anchor_similarity`, the 10-document promotion
floor) are pinned v1 constants, not config keys.

### Promotion out of quarantine

Under the same gate, a newborn is attached and searchable from birth but
provisional: `tag.promoted_at` is NULL and the name is excluded from every
vocabulary injection point. `promoteEligible` runs inside the enricher's persist
transaction, after the tag assignments are written and before the commit, over
the provisional rows the document just touched (fresh `Created` rows and
`Conflict` rows whose `promoted_at` is invalid; a re-emitted trusted tag is
skipped). A name promotes when both hold:

- the **admission decision admits it** — the emission's own decision for names
  judged this run, or, for a provisional already in the store (which the judge
  skips), its cached verdict row mapped through the same decision with the
  judge answers alone; and
- assignments on **≥ 10 distinct live documents**
  (`CountLiveTagDocumentsByTagIDs` — the `document_tag` `(document_id, tag_id)`
  PK makes the count distinct, so a single document re-enriching itself stays
  at 1, and carriers whose document row is trashed do not count).

`PromoteTags` sets `promoted_at = now()` under `WHERE promoted_at IS NULL`, so
concurrent promotions are idempotent, and the promoted names enter the store
via `AddToStore` only after the commit (`tagquality[v3]: promote %q
(docs=%d reason=%s rule_set=decision-v3 floor=10)`). Deferred and rejected
provisionals never promote here —
they wait for review — and neither does an identity-held name (a refused fold
beside a live store value): the hold is derived from the latest
`base_form`/`fold_gate` evidence and skips the candidate with a DEBUG
(`identity hold %q (anchor=%q) — left for review`).
Verdict-less provisionals — judge outages and re-emitted legacy rows — are not
re-judged in flight: a name already present in the tag table is not novel, so
`Adjudicator.ApplyTagQuality` never sees it again and no verdict row appears. Two surfaces
resolve them afterwards:

- The **scheduled reconciliation sweep** (`internal/tags/reconcile`)
  re-checks provisional tags older than
  `enricher.tag_reconcile.provisional_grace_days` (default 30): a stored
  verdict the decision re-derives as admit plus ≥ 10 live documents promotes
  without a judge call, while defer and reject verdicts log `left for review
  (outcome=… reason=…)` and stay; verdict-less names are re-judged
  oldest-first under `max_rejudge` — the
  tag's first live document (`FirstDocumentIDForTag`, `GetDocumentTextForTagQuality`)
  reduced with `matcher.TargetWordCount`, one judge call per name, one
  retry on failure — with the verdict recorded through the enricher's
  fresh-verdict path (`InsertTagQualityEvent` with `DO NOTHING`, so a
  concurrent fresh verdict is kept) and an admitted name promoted immediately at
  ≥ 10 live documents. Identity-held names are counted as skipped with an INFO
  naming the anchor and never promote from the sweep. Deferrals, rejections,
  missing judges, and tags with no live document text stay queued.
- The **review queue** (`GET /api/v1/tags/provisional`,
  `POST /api/v1/tags/{id}/promote`, and the tags page's editor-only "New
  tags" tab) is the operator path: it lists provisional names with their
  live `document_count` and latest verdict — `null` for a name with no row,
  labelled from the recorded outcome (`admit`/`defer`/`reject`) with a rubric
  fallback for rows predating the outcome column — narrowed by rubric-state
  and verdict-class filters and paged over the filtered total; it promotes one
  through the same `curation.Tag.Promote` method the sweep
  uses. Rows whose refused fold targeted a live store value carry the anchor
  as `identity_hold` and render an "identity hold" pill. A rejection is a
  normal tag delete, so the `deleted_tag` blocklist keeps the name from being
  reborn.

---

## 8. Interfaces: local and remote implementations

The interfaces (`internal/matcher/adapter.go`):

```go
type Matcher interface {
	Match(ctx context.Context, docId, input string) ([]Candidate, error)
	Rank(ctx context.Context, docId string, queries []string) ([]RankResult, error)
	Close()
	Name() string
}

type Embedder interface {
	Encode(ctx context.Context, docId string, texts []string) ([][]float32, error)
	AddToStore(ctx context.Context, names []string) error
	RemoveFromStore(ctx context.Context, names []string) error
	Close()
	Name() string
}
```

- `*Hugot` implements both (in-process, cgo build).
- `*matcher.MatcherClient` (`internal/matcher/client.go:76`) implements both
  over the Unix socket — used by `edub`, which cannot load the model. The RPC
  surface is `Match`, `Rank`, `AddToStore`, `RemoveFromStore`, `Health`
  (`internal/ops/cli/hugot.go:88-94`). `Rank` prefers `/rpc/v1/rank` and
  falls back to the legacy `/rpc/v1/consolidate` on 404 (old daemon), wrapping
  each returned name as a `RankResult{KeptName: name}` with no candidates so
  the enricher keeps it verbatim.
- The client wraps every call in `ErrMatcherUnavailable` when the socket
  daemon is down — which is what turns tag CRUD into 503.

The `EmbeddingStore` interface (`adapter.go:72-79`: `Add`/`Remove`/`Entries`)
is implemented by `matcher.TagEmbeddingStore` and by the test double
`testutil.MockEmbedder`.

---

## 9. The matcher daemon

`kushim hugot` (`internal/ops/cli/hugot.go`) is the daemon that loads the
model once and serves RPC over a Unix socket (`<config-dir>/kushim-hugot.sock`).

- **Startup**: removes a stale socket, builds the tag cache
  (`matcher.BuildTagCache`), opens the listener, serves HTTP with a 30s
  read/idle timeout and a `WriteTimeout` mirrored from
  `enricher.tagmatcher.timeout` (0 = no deadline), read once at startup.
- **`--bg`** re-execs itself detached with stdio nulled (same pattern as
  `kushim queue --bg`).
- **Duplicate detection**: `net.Listen("unix", ...)` fails with `EADDRINUSE`
  if the daemon is already running — detected via `errors.As` to
  `syscall.Errno` (`hugot.go:97-104`).
- **systemd readiness**: `notifyReady` (`hugot.go:147-165`) writes
  `READY=1` to the socket named by `NOTIFY_SOCKET` (unixgram; abstract
  sockets with a leading `@` become `\x00`-prefixed).
- **Shutdown**: SIGTERM/SIGINT → `server.Shutdown(ctx)` with a 5s budget →
  `os.Remove(socketPath)`.

The HTTP endpoints are body-size-capped RPC handlers (`bodyCap`). The client
imposes no client-level timeout of its own — the caller's context deadline
(`enricher.tagmatcher.timeout`, applied by `Runner.MatchTags` and
`Runner.RankTags`) is the sole bound, since encoding large documents
can take a while.

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
| `min_suggestion_similarity` | model-derived (`0.55` bge-m3) | injection bar: only matched tags at or above it reach the emission prompt; an explicit `0` reverts to the model default |
| `consolidation_similarity` | model-derived | tag→tag consideration floor (candidates below it never reach the identity pipeline) |
| `auto_replace_similarity` | model-derived | formerly the tag→tag auto-replace cutoff; still derived but no decision consumes it |
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

// defaultAutoReplaceSimilarity ... the similarity at or above which a
// guard-free non-identity candidate replaces the emitted tag; each value is
// the consolidation default + 0.15 ...
func defaultAutoReplaceSimilarity(modelShortName string) float64 {
	switch modelShortName {
	case "bge-m3":
		return 0.95
	case "all-mpnet-base-v2":
		return 0.90
	case "all-MiniLM-L6-v2":
		return 0.85
	default:
		return 0.90
	}
}

// defaultMinSuggestionSimilarity ... the bar at or above which a matcher
// suggestion is injected into the emission prompt; each value is the
// document-match default + 0.15, and only the bge-m3 value is dump-verified
func defaultMinSuggestionSimilarity(modelShortName string) float64 {
	switch modelShortName {
	case "bge-m3":
		return 0.55
	case "all-mpnet-base-v2":
		return 0.45
	case "all-MiniLM-L6-v2":
		return 0.40
	default:
		return 0.45
	}
}
```

Rules of thumb: raise `min_similarity` if false positives appear; keep
`consolidation_similarity` well above it. If you add a model to the
catalog/derivation, add it to all four switch
statements.

---

## 11. Where it plugs into the pipeline

1. **Tag CRUD** (`service/tag.go`): creates add the new names when the birth is
   trusted (`Create`/`CreateAndUnblock` promote what they create; a quarantined
   `CreatePlannedTx` birth stays provisional), deletes call `RemoveFromStore`, and a
   rename re-embeds the new name only when the renamed tag was promoted
   (`encodeAndAddBatch`, `service/tag.go:275-279`).
2. **Daemon startup** (`internal/ops/cli/hugot.go`): `BuildTagCache` seeds the store
   from the promoted names (`ListPromotedTagNames`), so provisional tags stay
   out across restarts.
3. **Enrichment** (`internal/enrichment/enricher.go`):
   - `runner.MatchTags(ctx, docId, reducedText)` → the matches at or above the
     injection bar become `tagSuggestions` (below-bar matches are dropped,
     yielding an empty list rather than a fallback); on error
     or zero matches, falls back to the promoted-name list (the enricher's
     `tagsNames`, composed from the promoted subset of `ListAll`).
   - `runner.RankTags(ctx, docId, analysis.Tags)` after the LLM pass;
     `Adjudicator.PlanIdentity` applies identity and candidate generation via
     `policy.Evaluate` (identity + gate subjects) with the promoted-only
     store set, so quarantined names are not targets. Style variants and
     aliases are applied before this point through M1/M3 and the alias
     resolver.
   - `runner.JudgeTagIdentity(ctx, candidates)` confirms proposed folds (J1/J2/J5)
     and `runner.AdjudicateTags(ctx, pairs)` resolves the same-intent gate when
     `enricher.tag_adjudicator.enabled` is true (cache + evidence + batched LLM
     calls, §7); unresolved claims stay pending as identity evidence.
   - `runner.JudgeTagQuality(ctx, reducedText, candidates)` feeds the
     consolidated novel set when `enricher.tag_quality.enabled` is true
     (cache → one rubric call per document → the pinned admission decision,
     §7).
   - `promoteEligible` runs inside the persist transaction, after the tag
     assignments and before the commit: an admitted decision (the emission's,
     or the cached verdict row for provisionals already in the store) plus
     assignments on ≥ 10 distinct live documents → `PromoteTags`, and the
     promoted names enter the store after the commit
     (§7).
4. **API tag creation** triggers a store refresh through the same service.

---

## 12. Gotchas

- **Tag names are canonical before they reach the store** — `textnorm.NormalizeTag`
  (`internal/textnorm/tagname.go`) runs at every tag-name write site: the DB save
  chokepoints (`curation.Tag.Create`/`Update`), the plan application's tag and
  verdict writes, alias writes, merge dismissals, and measurement operands;
  the cache and the matcher use names
  verbatim, so store keys always equal DB names.
- **The store is the tag universe.** Tags not in `entries` can never be
  suggested, and `Match` returns nothing when the store is empty — even if
  the model works. With the quality layer on this exclusion is deliberate for
  provisional tags: they are attached and searchable from birth but invisible
  to the vocabulary until promotion.
- **Chunk size is clamped by the model**, not just by config: `config.json`
  `max_position_embeddings` minus a 12-token safety margin. Raising
  `chunk_size` beyond the model limit is silently ignored.
- **BGE-M3's 1024-dim vectors** mean `Entries()` deep copies ~4 KB per tag
  per call — fine at hundreds of tags, wasteful at hundreds of thousands.
- **Mean pooling ≠ the model's own pooling.** `encodeChunked` averages window
  embeddings; the result is an approximation of the full-document embedding.
  Overlap (10%) exists precisely to soften window-boundary effects.
- **ORT first run needs `curl` + internet** and a *correct* pinned SHA-256
  (`hugot.go:629`); the download is extracted with `--strip-components=2`
  and renamed to `libonnxruntime.so` — path-sensitive.
- **The daemon must be running before `edub` starts** (AGENTS.md); the client
  wraps all failures in `ErrMatcherUnavailable`, and tag endpoints 503 while
  it's down. `kushim hugot` does NOT auto-start from `edub`.
- **`h == nil` guards**: every `Hugot` method starts with
  `if h == nil { return fmt.Errorf("tag matcher not initialized") }` — the
  matcher can be absent when unconfigured; callers must treat that as a
  graceful no-op, not a panic.
- **`topN == 0` or empty store short-circuits** (`hugot.go:286-288`): a
  zero `top_n` disables suggestions entirely.
- **Consolidation with `consolidationSim == 0.0` passes through** — that's
  the documented "disable consolidation" knob, not a bug.
- **The adjudicator is disabled by default** — folds are not applied and
  same-intent claims wait as pending evidence (candidates born provisional)
  until `enricher.tag_adjudicator.enabled: true` or the triggered run/sweep
  resolves them, and an explicit `llm` block is only
  needed when deviating from the content analyzer's LLM (an empty
  `provider`/`model` copies the analyzer's `llm` wholesale).
- **The quality judge is disabled by default** — novel tags are born
  untouched until `enricher.tag_quality.enabled: true`. Its decision
  thresholds are pinned v1 constants (`no_anchor_similarity = 0.60`, the
  10-document promotion floor), deliberately not operator-tunable.
- **Verdicts are cache-scoped to `policy.Version`** — bumping the policy
  version invalidates every cached verdict at once (old rows stay in the event
  table for analysis, they are simply never read for the new version).

---

*Last verified against the tree: 2026-10-01. If code and doc disagree, code wins.*
