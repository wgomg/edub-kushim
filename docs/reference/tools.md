# Tools Framework (`internal/tools/`)

## `runner.go`

### Struct

`Runner`

- **Fields**: `logger`, `config`, `textExtractor`, `ocr`, `pdfOptimizer`, `tagMatcher tagmatcher.Matcher`, `contentAnalyzer`, `tagAdjudicator contentanalyzer.ContentAnalyzer` (built from `cfg.Enricher.TagAdjudicator` when enabled; logs an error and stays nil when construction fails), `fallbackAnalyzers []contentanalyzer.ContentAnalyzer` + `fallbackMeta []fallbackMeta` (one entry per enabled `enricher.contentanalyzer.fallbacks` entry, empty when none are configured), `textReducer`
- **Functions**:
  - `NewRunner(logger, cfg, tools []string) *Runner` — Initializes only the listed tool adapters (e.g., `["textextractor","ocr","pdfoptimizer"]` for consumer). Loads the LLM model catalog from `<configDir>/model_catalog.json` via `llm.NewRegistry`. No longer creates a tagmatcher internally. Content analyzer is created via the new adapter-based factory: `contentanalyzer.NewContentAnalyzer(logger, cfg.ToolConfig, &cfg.Enricher.ContentAnalyzer.Llm, cfg.Enricher.ContentAnalyzer.PromptTemplate, reg)`. For each **enabled** entry in `cfg.Enricher.ContentAnalyzer.Fallbacks`, an analyzer is built from that entry's `Llm` block with the same factory and appended to `fallbackAnalyzers` (with its provider/model recorded in `fallbackMeta`); entries whose factory fails are skipped with a logged error.
  - `NewRunnerWithMatcher(logger, cfg, tools, matcher tagmatcher.Matcher) *Runner` — Calls `NewRunner` then conditionally sets `r.tagMatcher` if `matcher != nil` and `"tagmatcher"` is in the tools list.
- **Methods**: `ExtractText`, `OCR(ctx, docId, path)`, `OptimizePdf(ctx, docId, path)`, `ReduceContent`, `MatchTags(ctx, docId, input)`, `AnalyzeContent`, `AnalyzeDocType` — the two analyzer methods retry through `fallbackAnalyzers` in order when the primary error passes `isProviderError` (provider-attributable failure and at least one fallback is configured), returning the first success or the last error; request-side and lifecycle errors never retry. `AdjudicateTags(ctx, pairs []contentanalyzer.TagPairEvidence) ([]contentanalyzer.TagVerdictResult, error)` — classifies ambiguous tag pairs via `tagAdjudicator.Adjudicate` under the `enricher.tag_adjudicator.timeout`; returns "tag adjudicator not configured" when the adjudicator is disabled or failed to construct. `AdjudicateTagSplits(ctx, tags []string) ([]contentanalyzer.TagSplitResult, error)` — decides keep/split for conjunction-joined tags via `tagAdjudicator.AdjudicateTagSplits`, same timeout and error contract. `HasTagAdjudicator() bool` — whether an adjudicator adapter was built at construction time.
- **Result types**: `TextExtractionResult`, `OCRResult`, `PdfOptimizationResult`, `TextReducerResult` (with Text, WordCount, CharCount, TargetWordCount), `TagMatchResult`, `ContentAnalysisResult` (with People)
- **Helper**: `runWithTimeout[T](ctx, fn) (T, error)` — Generic goroutine wrapper with context cancellation. `isProviderError(err) bool` — true for provider-attributable failures (network, HTTP status, credits); false for `ContentTooLargeError`, `TokenLimitError`, `context.Canceled`, `context.DeadlineExceeded`.
- **Timeout behavior**: `ExtractText`, `OCR`, `ReduceContent`, and `MatchTags` each read their component's configured `Timeout` and only wrap the context with `context.WithTimeout` when the value is > 0. A timeout of 0 means no artificial deadline — parent context cancellation still propagates via `runWithTimeout`. The same guarded pattern is used by `OptimizePdf`. Config validation rejects negative timeout values at load time.

---

## `adapters/contentanalyzer/adapter.go`

### Interface

```go
type ContentAnalyzer interface {
    Analyze(ctx, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error)
    AnalyzeDocType(ctx, prevResult, headTailText, docTypes, metadata DocMetadata) (string, error)
    Adjudicate(ctx, pairs []TagPairEvidence) ([]TagVerdictResult, error)
    AdjudicateTagSplits(ctx, tags []string) ([]TagSplitResult, error)
    Name() string
}
```

### Structs

- `AnalysisResult` — `Title`, `DocType`, `Tags []string` (canonical downstream field), `TagDetails []TagDetail` (typed mirror of `Tags`, consumed once by the enricher's facet gate, `omitempty` in JSON), `People []PeopleResult`, `Language`, `Stats *json.RawMessage`, `Prompt`, `PassContext *json.RawMessage` (`json:"-"`, the replayed prompts for the doc-type refinement call)
- `TagDetail` — `Term string`, `Category string`, `IsStandardTerm bool` (`is_standard_term`). `Category` is one of the exported `TagCategory*` constants (`topic`, `form_genre`, `place`, `person`, `work`, `other`); the parse folds case/hyphen/space variants and maps anything missing or unknown to `other`. String-shaped tag entries are `other`; `IsStandardTerm` is `false` unless the model explicitly set it.
- `DocMetadata` — `WordCount int32`, `PageCount int32`, `MimeType string`. Returned by `Format()` as a human-readable string (e.g. `"15234 total words, 42 pages, application/pdf"`). Returns empty string when all fields are zero/empty.
- `PeopleResult` — `Name`, `NameRomanized` (optional, for non-Latin names), `Type`, `NormalizedName` (pre-populated by enricher via `canonicalPersonName` + `NormalizeForDB`, used by `FilterTags`, excluded from JSON serialization via `json:"-"`)
- `TagPairEvidence` — `Query`, `Target`, `Sim`, `RunnerUpSim` (query-side runner-up similarity), `TargetRunnerUpTag`/`TargetRunnerUpSim` (target's nearest other store tag). Fed to `Adjudicate`'s prompt.
- `TagVerdictResult` — `Index int`, `Verdict types.TagVerdict` (`same`/`variant`/`related`; zero verdict = ambiguous entry, caller keeps).
- `TagSplitResult` — `Verdict TagSplitVerdict` (`keep`/`split`; zero verdict = ambiguous entry, caller keeps), `Components []string` (the split components, present only for a `split` verdict).

### Factory

`NewContentAnalyzer(logger, cfg, llmCfg, promptTemplate, minTags, maxTags, registry)` — Selects adapter by `llmCfg.Adapter` using a capability registry. The adapter is chosen from the `llmCfg.Adapter` field: `anthropic` dispatches to `NewLlmAnthropic`, anything else to the `openai-compatible` adapter (`NewLlmOpenAiCompatible`). Capabilities are looked up from the `registry` via `registry.Lookup(llmCfg.Provider, llmCfg.Model)` and passed to each adapter for dynamic request building (stripping unsupported parameters). `promptTemplate` is a custom Go `text/template` string; empty/missing means use the built-in default. `minTags`/`maxTags` feed the prompt's tag-range wording; the tag adjudicator is built with `0, 0` (it never runs `Analyze`).

---

## `adapters/contentanalyzer/shared.go`

### Constants

- `SystemMessage` (exported) — `"You are a helpful assistant specialized in document analysis and metadata extraction"`, used by both adapter implementations.
- `maxTagWords` (3) — Word-count cap enforced by `TagFilter.Accept`/`FilterTags`.
- `maxTagLength` (64) — Character cap enforced by `TagFilter.Accept`/`FilterTags`.
- `TagCategoryTopic`/`TagCategoryFormGenre`/`TagCategoryPlace`/`TagCategoryPerson`/`TagCategoryWork`/`TagCategoryOther` (exported) — the closed category set of a `TagDetail`.
- `TagSplitSystemMessage` (exported) — `"You are a tag-taxonomy classifier. Decide whether a tag denotes one concept or several distinct concepts."`

### Types

- `ContentTooLargeError` — Returned by `checkContentTooLarge` when estimated tokens exceed the model's `MaxInputTokens`. Fields: `EstimatedTokens`, `MaxInputTokens`.
- `TokenLimitError` — Returned by `parseTokenLimitError` when the provider error body matches the context-length exceeded regex. Fields: `MaxTokens`, `RequestedTokens`, `RawBody`.
- `TagFilter` — Token sets built by `NewTagFilter(people, knownPeopleNormalized, title, docTypeNames)`; `Accept(tag string) bool` applies the same shape rules and token checks `FilterTags` uses (normalizes its input first). Shared so split components get the same per-document rules as emitted tags.
- `TagDetail` — `Term string`, `Category string`, `IsStandardTerm bool`. The model's own classification of one tag; filled by `ParseAnalysisResponse`.
- `FacetDrop` — `Term string`, `Category string`, `Reason string`. One tag dropped by `SeparateFacets`; `Reason` records why: `matches_document_type` / `model_self_classification` for `form_genre`, `people_facet` for `person`, `forbidden_category` for `work`, `non_whitelisted_place` for a `place` that is not a whitelisted country or continent.
- `TagSplitVerdict` — `TagSplitKeep`/`TagSplitSplit`; zero verdict = ambiguous, callers keep.

### Functions

- `checkContentTooLarge(caps *llm.ModelCapability, fullPrompt string) error` — Estimates tokens via `utils.EstimateTokens` and returns a `ContentTooLargeError` if the estimate exceeds `caps.MaxInputTokens`. Returns nil if `caps` is nil or `MaxInputTokens ≤ 0`.
- `parseTokenLimitError(body []byte) error` — Matches provider error bodies against the regex `maximum context length is (\d+) tokens.*?you requested (?:about )?(\d+) tokens` and returns a `TokenLimitError` with the parsed counts. Returns nil on no match or zero values. Covers OpenAI, DeepSeek, and compatible providers; for Anthropic (different error format) it returns nil and the error propagates as generic API failure.
- `BuildPrompt(text, docTypes, peopleTypes, tagSuggestions, customTemplate, minTags, maxTags) string` — Builds the system prompt with JSON output instructions including people types. Prompts the LLM to provide a `name_romanized` field for any name containing non-Latin characters (Korean, Arabic, Cyrillic, Hebrew, etc.). The default template asks for "Between `minTags` and `maxTags`" tags and forbids merging concepts with a conjunction; it also states the facet rules (tags describe subject matter, forms/genres belong in the document type, places only for countries and continents) and the typed tag contract — each tag is an object with `term`, `category`, `is_standard_term` — with one-line negative exemplars (`literary anthology`, `racial scapegoating`, `newfoundland`, `bail spring replacement`). When `customTemplate` is non-empty (after trimming whitespace), it is used as a Go `text/template` with placeholders `{{.DocTypePrompt}}`, `{{.TagsPrompt}}`, `{{.PeoplePrompt}}`, `{{.Text}}`, `{{.MinTags}}`, `{{.MaxTags}}`. On parse or execution error, falls back silently to the hardcoded default template. The rendered prompt is captured in `AnalysisResult.Prompt` for debugging.
- `ParseAnalysisResponse(content string) (*AnalysisResult, error)` — Decodes one analysis response, accepting both tag shapes: a plain-string array (each entry becomes a tag with `category: other`) and an object array (`term`, `category`, `is_standard_term`), mixed arrays included. Elements that are neither a string nor an object with a non-empty `term` are skipped; `category` is folded via `normalizeTagCategory` (case, hyphens, spaces; unknown or missing → `other`). A missing or `null` `tags` key yields empty slices; a `tags` value that is not an array returns an error. Both adapters' `Analyze` and the missing-`type` fallback of `AnalyzeDocType` parse through this helper.
- `normalizeTagCategory(raw string) string` — Lowercases and trims the raw value, folds `-` and spaces to `_`, and returns the matching `TagCategory*` constant or `TagCategoryOther`.
- `NormalizeTags(raw []string) []string` — Converts LLM-extracted tags to canonical form via `utils.NormalizeTag` (NFKC, lowercase, dash family → space, accent folding, keeps letters of any Unicode script, digits, and `+ # .`, strips everything else, collapses whitespace), then deduplicates and rejects empty/whitespace-only results.
- `SeparateFacets(tags []string, details []TagDetail, docTypeNames []string) ([]string, []FacetDrop)` — Deterministic facet gate. Drops typed tags by their `TagDetail.Category`: `form_genre` tags (they belong in the document type) are dropped with `Reason` = `matches_document_type` when the normalized term is an existing `document_type` name, `model_self_classification` otherwise; `person` (`people_facet`) and `work` (`forbidden_category`) tags are dropped; `place` survives only when the term is a whitelisted country or continent (`non_whitelisted_place` otherwise). `topic` and `other` always survive, as do tags with no matching detail row. Returns `tags` unchanged when there are no details (untyped emissions gate nothing). Runs after `NormalizeTags` and before `FilterTags`; the enricher logs every drop.
- `IsWhitelistedPlace(term string) bool` — Reports whether the normalized term is a whitelisted country or continent. The embedded list covers the continents, UN member and observer states, contested or partially recognized states (`palestine`, `taiwan`, `kosovo`, `western sahara`, …), and common short or alternate names (`usa`, `uk`, `uae`, `drc`, `burma`, `holland`, …). Both sides of the lookup are normalized with `normalizeCore`, and a leading `the ` is ignored.
- `FilterTags(tags, people, knownPeopleNormalized, title, docTypeNames, maxTags) []string` — Post-normalization deterministic tag cleaner. Drops tags with more than 3 tokens or more than 64 chars, tags sharing any token with a person's `NormalizedName` (pre-populated by the enricher via `canonicalPersonName` + `NormalizeForDB`), multi-word tags (≥2 tokens) whose full token set is a strict subset of a known person's normalized name (from the full `people` table, passed via `knownPeopleNormalized`), tags matching a doc-type name token, and tags whose token set is a subset of the document title's token set. Caps survivors at `maxTags` (`enricher.contentanalyzer.max_tags`, default 5) in emit order. Operates after `NormalizeTags` and before rank-based consolidation.
- `SplitComponents(raw []string, filter *TagFilter) []string` — Normalizes adjudicated split components (`NormalizeTags`) and drops names that fail the shape rules or the filter's token checks; callers require 2..`max_tags` survivors.
- `buildTokenUsageStats(prompt, completion, total int) *json.RawMessage` — Creates token usage stats JSON
- `AdjudicationSystemMessage` (exported) — `"You are a tag-taxonomy classifier. Decide whether pairs of tag terms denote the same concept."`
- `BuildAdjudicationPrompt(pairs []TagPairEvidence) string` — Builds the JSON-verdict prompt: per pair, the two terms, their embedding similarity, the query-side runner-up similarity, and the target's nearest other store tag.
- `ParseAdjudicationResponse(content string, n int) ([]TagVerdictResult, error)` — Parses `{"pairs":[{"index":0,"verdict":"same"},...]}` (code-fenced or plain) into one result per pair; unknown verdict strings, out-of-range indices, and short slices leave the affected entries with a zero verdict (ambiguous → caller keeps). Invalid JSON returns an error.
- `BuildTagSplitPrompt(tags []string) string` — Builds the JSON-verdict prompt for joint tags: per tag, decide `keep`/`split`, with the split components listed when splitting.
- `ParseTagSplitResponse(content string, n int) ([]TagSplitResult, error)` — Parses `{"splits":[{"index":0,"verdict":"split","components":[...]},...]}` (code-fenced or plain) into one result per tag; unknown verdicts, out-of-range indices, and splits with fewer than two non-empty components leave the entry ambiguous (caller keeps). Invalid JSON returns an error.

### Adapter integration

Both `Analyze()` and `AnalyzeDocType()` call `checkContentTooLarge` with their constructed prompt before making any HTTP request. If it returns an error, the adapter returns the `ContentTooLargeError` directly — no API call is made, saving time and credits. Both adapter `doRequest()` methods (OpenAI's shared `doRequest`, Anthropic's shared `doRequest`) also call `parseTokenLimitError` on non-200 responses, catching provider-side context-length errors at response time. `Adjudicate()` follows the same path: `checkContentTooLarge` → request (temperature 0) → `doRequest` → `ParseAdjudicationResponse`.

---

## `adapters/contentanalyzer/openai_compatible.go`

### Struct

`LlmOpenAiCompatible` — OpenAI-compatible `/chat/completions` API (absorbs former OpenAI, DeepSeek, Mistral, Groq, etc.)

- Messages: system + user with `json_object` response format when `caps.SupportsResponseSchema`
- Headers: `Authorization: Bearer {token}`
- Dynamic request building based on `*ModelCapability`:
  - Strips `reasoning_effort` if `!caps.SupportsReasoning`
  - Strips `temperature` if `!caps.SupportsSamplingParams`
  - Uses `response_format: {type: "json_object"}` only when `caps.SupportsResponseSchema`
- Default provider URL from the registry's `ProviderDefaultURL(provider)`
- Token usage from `usage` field
- `Adjudicate` — reuses `buildRequestBody`/`doRequest` with temperature 0 and the `json_object` response format when `caps.SupportsResponseSchema`

---

## `adapters/contentanalyzer/llm_anthropic.go`

### Struct

`LlmAnthropic` — Anthropic `/messages` API

- System message via `system` field, user message via `messages`
- Headers: `x-api-key`, `anthropic-version: 2023-06-01`
- Dynamic request building based on `*ModelCapability`:
  - `llmCfg.Reasoning == false` → sets `thinking: {type: "disabled"}`
  - Reasoning enabled without model effort levels → `thinking: {type: "enabled"}` with a computed token budget
  - Reasoning enabled with effort levels → `output_config: {effort: ...}`; `thinking: {type: "adaptive"}` unless the model is in `anthropicManualThinkingModels`, which gets explicit `thinking: {type: "enabled"}` with budget
  - `max_tokens` fixed at `256` for the no-generation doc-type refinement pass, else `caps.MaxOutputTokens` (fallback 4096 when unset)
- Token usage from `usage.input_tokens` + `usage.output_tokens`
- Shared transport: `doRequest(ctx, reqBody, errLabel)` marshals, sends, and decodes one `/messages` call (headers, status/error parsing, text-block extraction); `Analyze`, `AnalyzeDocType`, and `Adjudicate` all go through it. `Adjudicate` uses temperature 0 and `AdjudicationSystemMessage`.

---

## `adapters/tagmatcher/adapter.go`

### Interfaces

```go
type Matcher interface {
    Match(ctx, docId, input string) ([]string, error)
    Rank(ctx, docId string, queries []string) ([]RankResult, error)
    SimilarPairs(ctx context.Context) ([]SimilarPair, error)
    Close()
    Name() string
}

type Embedder interface {
    Encode(ctx, docId *string, texts []string) ([][]float32, error)
    AddToStore(ctx context.Context, names []string) error
    RemoveFromStore(ctx context.Context, names []string) error
    Close()
    Name() string
}

type Candidate struct {
    Tag        string  `json:"tag"`
    Similarity float64 `json:"similarity"`
}

type RankResult struct {
    KeptName   string      `json:"kept_name"`
    Candidates []Candidate `json:"candidates"`
}

type SimilarPair struct {
    A          string  `json:"a"`
    B          string  `json:"b"`
    Similarity float64 `json:"similarity"`
}

type EmbeddingStore interface {
    Add(key string, embedding []float32)
    Remove(key string)
    Entries() map[string][]float32
}
```

The `Matcher` interface is used by the Runner for document-to-tag matching
(`Match`), post-LLM tag ranking (`Rank`), and by `TagMerge.Suggestions` for
the merge-review pair list (`SimilarPairs`). The `Embedder` interface is used
by TagService for encoding and store management; `AddToStore`/`RemoveFromStore`
delegate store management to the adapter (encoding + adding to the embedding
store, or removing from it). The `EmbeddingStore` interface provides read/write
access to the shared tag embedding cache. `RankResult` carries the name to keep
when no candidate is chosen (`KeptName`) plus up to two ranked candidates with
scores; the replacement decision lives in the enricher's
`applyConsolidationPolicy`. `SimilarPair` is an unordered pair of store names
with their cosine similarity, canonicalized so each pair is stored once.

The composition root builds a single `*Hugot` (for the `kushim` CLI) or uses a `*tagmatch.MatcherClient` (for the `edub` API server) — both satisfy the `Matcher` and `Embedder` interfaces. The `MatcherClient` forwards all calls over a Unix socket to a standalone `kushim hugot` process.

---

## `adapters/tagmatcher/hugot.go`

### Struct

`Hugot` — Hugot session with `FeatureExtractionPipeline`

- **Backend**: `"ort"` (default — ONNX Runtime, auto-downloads `libonnxruntime.so`) or `"go"` (pure Go with `libtokenizers.a`)
- **Chunked encoding**: Texts exceeding the effective chunk size (configured `chunk_size`, default `4096`; `0` = the model's `max_position_embeddings` minus a 12-token safety margin, 8180 for BGE-M3) are split into overlapping token chunks, mean-pooled. The default bounds per-request memory — see [Tag Matcher guide](../tag-matcher.md)
- **Ranking**: Cosine similarity on L2-normalized embeddings (dot product); separate `minSimilarity` (doc→tag), `consolidationSim` (tag→tag consideration floor) and the enricher-side `auto_replace_similarity` (tag→tag auto-replace cutoff)
- **Config**: `TopN`, `MinSimilarity`, `ConsolidationSimilarity`, `ChunkSize` (default 4096; `0` = model max, clamped to `max_position_embeddings` − 12), `ChunkOverlap` (10% of chunk size)
- **Fields**: `store EmbeddingStore` — shared reference to the tag embedding cache; `pairs` — mutex-guarded map of unordered `similarPairKey` → cosine similarity holding every pair of store entries at or above `consolidationSim`
- **Methods**:
  - `Match(ctx, docId, input)` — Reads all entries from the store, encodes the input, ranks by cosine similarity, returns top-N matches
  - `Rank(ctx, docId, queries []string)` — Reads all entries from the store internally, encodes the queries verbatim (they are already canonical — `contentanalyzer.NormalizeTags` output), ranks against the store embeddings, and returns up to two candidates per query with scores plus the kept name
  - `SimilarPairs(ctx)` — Snapshots the pair cache under the mutex and returns it as a slice of `SimilarPair`
  - `Encode(ctx, *docId, texts)` — Batch embedding with chunked encoding for long inputs. Does NOT normalize input — shared with document text matching where punctuation is meaningful.
  - `AddToStore(ctx, names)` — Encodes names verbatim (canonical DB names from `service.Tag.Create`/`Update`) in batches of 32 (`embedBatchSize`), adds them to the store, and maintains the pair cache: one `Entries()` snapshot per batch, prune-then-insert per name (re-encodes drop stale pairs), fresh batch vectors used for intra-batch pairs
  - `RemoveFromStore(ctx, names)` — Removes names from the store and drops every pair mentioning them
  - `SetStore(s EmbeddingStore)` — Injects the shared store reference after construction and rebuilds the pair cache from the store's entries (N²/2 cosine pass, skipped when `consolidationSim` is 0)
  - `Close()` — Idempotent (nil-safe, sets session to nil after destroy)
- **Nil-receiver guards**: `Match`, `Rank`, `SimilarPairs`, `Encode` all return an error if `h == nil`, preventing typed-nil interface panics
- **Helpers**: `meanPool`, `rankMatches`, `cosineSimilarity`, `tokenize`, `encodeChunked`, `readMaxPositionEmbeddings`, `downloadLib`, `getBackendSession`, `canonicalPairKey`, `rebuildPairs`, `addPairsFor`, `removePairsFor`

---

---

## `internal/llm/` package (Capability Registry)

### Data source

A manually maintained `model_catalog.json` file committed to the repository at
`internal/llm/model_catalog.json` and embedded via `//go:embed`. At startup the
registry tries to load `<configDir>/model_catalog.json` first (so deployments can
override the catalog without rebuilding); if that file is absent it falls back to
the embedded copy. There is no runtime refresh mechanism — updating the catalog
requires replacing the file and restarting, or rebuilding the binary.

### Key types

```go
type ModelCapability struct {
    MaxInputTokens          int
    MaxOutputTokens         int
    SupportsReasoning       bool
    SupportsFunctionCalling bool
    SupportsResponseSchema  bool
    SupportsSamplingParams  bool
    SupportsPromptCaching   bool
    SupportsVision          bool
    SupportsPDFInput        bool
    SupportsOutputConfig    bool
    ReasoningEffortLevels   []string
    AdaptiveThinking        bool
    InputCostPerToken       float64
    OutputCostPerToken      float64
}

type Registry struct { ... }
```

### Registry API

| Method | Description |
|--------|-------------|
| `NewRegistry(path)` | Loads JSON catalog from disk (falls back to embedded copy), builds index |
| `Lookup(provider, model)` | Returns `*ModelCapability` for a given provider+model |
| `Adapters()` | Returns adapter names present in the catalog (currently `["anthropic", "openai-compatible"]`) |
| `ProvidersForAdapter(adapter)` | Returns provider keys for a given adapter |
| `ModelsForProvider(provider)` | Returns model entries with capabilities |

### Provider→Adapter mapping

The mapping is data-driven: each entry in `model_catalog.json` has an `adapter` field (`"openai-compatible"`, `"anthropic"`, or `"custom"`). The `ProviderAdapter(provider)` method returns the adapter from the catalog, falling back to `"custom"` for unknown providers. The `"custom"` adapter itself is not listed by `Adapters()` unless a catalog entry uses it.

### Loading

The catalog is loaded at startup from `<configDir>/model_catalog.json`, falling back to the embedded copy (`internal/llm/model_catalog.json`, compiled via `//go:embed`) when the file doesn't exist. There is no runtime refresh mechanism — updating the catalog requires replacing the file and restarting, or rebuilding the binary for embedded updates.

---

## `adapters/textreducer/adapter.go`

### Interface

```go
type TextReducer interface {
    Reduce(ctx, content string, chunkSize, targetWordCount int) (*string, error)
    Name() string
}
```

### Factory

`NewTextReducer(logger, cfg)` — Returns `TextRank` adapter (default)

---

## `adapters/textreducer/textrank.go`

### Struct

`TextRank` — Extractive summarization via graph-based ranking

### Algorithm

1. Sentence-aware chunking (split on `[.!?]\s+` and `\n\n`)
2. TF-IDF scoring with inverse document frequency across all chunks
3. Weighted PageRank on a Jaccard similarity graph (`damping=0.85`, `maxIterations=100`, `tolerance=0.0001`)
4. Position scoring (cosine or decay bias auto-detected from text density)
5. Diversity penalty (Jaccard similarity penalty on `FinalScore` for high-overlap chunks)

**Final score**: `TFWeight(0.4) * NormalizedTF + GraphWeight(0.4) * NormalizedGraphScore + PositionWeight(0.2) * NormalizedPositionScore`

**Reduction**: Greedy selection (highest scoring) until `targetWordCount` is reached, overlapping sentences re-assembled with `[...]` separators for non-adjacent chunks.

---

## `adapters/ocr/adapter.go`

### Interface

`OCR` — `Process(ctx, docId, path)`, `CanHandle`, `Name`

### Factory

`NewOCR`

---

## `adapters/ocr/tesseract_link.go`

### CGo preamble

Links Tesseract, Leptonica, and libpng statically. Includes the Leptonica include path in `#cgo CFLAGS` and exposes a Go function `suppressLeptonicaStderr()` that registers a no-op stderr handler via `leptSetStderrHandler()`. All Leptonica `lept_stderr()` calls in the process are silently dropped. Genuine errors still propagate through Tesseract's C API return codes, which Go handles via `GetBoundingBoxes()` and `SetImageFromBytes()`.

## `adapters/ocr/gosseract.go`

### Struct

`Gosseract` — Tesseract + MuPDF CGo adapter. The `Process()` method forks `kushim internal-ocr` as a subprocess to run the OCR pipeline (MuPDF render at 200 DPI → Tesseract recognition → fpdf searchable PDF assembly with text rendering mode 3). The parent waits on `exec.CommandContext` which yields the Go scheduler via `entersyscall`, preventing CGo heartbeat starvation. Optimization runs in the parent after the child exits.

### Error handling

When the subprocess exits with a non-zero code, the adapter checks three conditions before treating the failure as fatal:

1. The error is an `exec.ExitError` (process ran but exited abnormally — covers exit code 2 from worker goroutine panics)
2. The output file exists on disk (the PDF was fully written before the exit)
3. The captured stderr contains known non-fatal Leptonica patterns (`pixReadMemTiff: function not present`, `font pixa not made`, `bmfCreate`)

If all three hold, the error is logged as a warning and processing continues — the output PDF is passed to the optimizer. Otherwise, the output is removed and the error is returned as before. This recovers files that would otherwise be quarantined due to exit code 2 from harmless Leptonica TIFF warnings during bitmap font creation (Tesseract's `api->Init()` calls `pixReadMem` with embedded TIFF font data, which fails through the `--without-libtiff` stub).

## `adapters/ocr/standalone.go`

### Function

`RunStandalone(inputPath, outputPath, languages, dataDir, ocrWorkers)` — Self-contained OCR pipeline called by the `internal-ocr` subcommand. Detects input file type via `mimetype.DetectFile` at the top of the function:
- **PDF** (`application/pdf`): Opens via MuPDF, renders pages at 200 DPI, downscales to 150 DPI via nearest-neighbor, OCRs with Tesseract, assembles a searchable PDF.
- **Image** (`image/*`): Decodes with Go standard library (`image.Decode` for PNG/JPEG, `golang.org/x/image/tiff` for TIFF), alpha-composites on white, same OCR pipeline as PDF. Rejects images larger than 50MP. Single-page only (multi-page TIFF reads first frame).

When `numPages > 1`, parallelizes Tesseract calls across `ocrWorkers` goroutines (0 = auto, resolves to `runtime.NumCPU()`). No context or logger — parent handles cancellation and logging. Shared helpers (`downscaleRGB`, `samplesToRGBA`, `encodePNG`, `encodeJPEG`, `computePage`, `addPDFPage`, `newOCRClient`, `imageToRGB`) used across PDF and image paths.

Calls `suppressLeptonicaStderr()` at the top of the function to suppress Leptonica diagnostic output (TIFF warnings from bitmap font creation) before any Tesseract or Leptonica operations run. The handler is set once per subprocess lifetime and is global to the process — harmless since the subprocess exits after a single OCR task.

<table>
<thead><tr><th>CLI flag</th><th>Config key</th><th>Description</th></tr></thead>
<tbody>
<tr><td><code>--ocr-workers</code></td><td><code>consumer.ocr.ocr_workers</code></td><td>Parallel OCR goroutine count; 0 = auto, resolved in the parent to <code>max(1, NumCPU / (max_concurrent_batches × consumer.workers))</code>, max is capped at <code>min(workers, pages, CPU*2)</code>; the child runs with <code>OMP_THREAD_LIMIT=1</code> so Tesseract adds no extra threads</td></tr>
</tbody>
</table>

---

## `adapters/textextractor/adapter.go`

### Interface

`TextExtractor` — `Extract`, `CanHandle`, `Name`

### Factory

`NewTextExtractor`

### Implementations

- `MuPDF` (default, CGo) — Page-by-page extraction
- `Gopdf` (pure Go, no CGo)
- `Pdftotext` (external tool)
- `Docx` — Pure Go DOCX text extraction via `archive/zip` + `encoding/xml` parsing of `word/document.xml`. Always active as part of the CompositeExtractor.
- `Odt` — Pure Go ODT text extraction via `archive/zip` + `encoding/xml` parsing of `content.xml`. Always active as part of the CompositeExtractor.
- `CompositeExtractor` — Wraps the configured PDF extractor + Docx + Odt, dispatches by MIME type via `CanHandle`. Created automatically by `NewTextExtractor`; no config changes needed.

---

## `adapters/pdfoptimizer/adapter.go`

### Interface

`PdfOptimizer` — `Optimize(ctx, docId, path)`, `Name`

### Factory

`NewPdfOptimizer`

### Implementations

- `MuPDF` (default, CGo) — `pdf_clean_file`
- `Ghostscript` (external tool)

---

## `adapters/mupdf_wrapper.go`

MuPDF 1.28.0 CGo wrapper. 6 C helpers: document open/close, page rendering, text extraction, `pdf_clean_file`.

### Render limit guard (`MUPDF_MAX_RENDER_PIXELS`)

A compile-time constant (`100,000,000` pixels) caps the total pixel area of any
single rendered page. The render pipeline rejects pages whose `width × height`
(at the configured DPI) exceeds this limit **before** calling
`fz_new_pixmap_with_bbox`, preventing an unbounded allocation that would
otherwise let a malicious or malformed PDF with an extreme MediaBox exhaust
process memory. Page dimensions come directly from untrusted document input and
are not bounded upstream. The 100 MP ceiling allows generous large-format pages
(e.g. a ~40×60 in poster at 200 DPI) while keeping a single RGB buffer under
~300 MB.

---

## See Also

- [Tag Matcher Guide](../tag-matcher.md) — Matcher config, memory, and CPU tuning
- [Pipeline](pipeline.md) — Consumer and Enricher that use the Runner
- [Config & Utils](config-and-utils.md) — Tool configuration (engines, timeouts, models)
- `internal/llm/` — LLM capability registry (model catalog, adapter/provider/model discovery)
