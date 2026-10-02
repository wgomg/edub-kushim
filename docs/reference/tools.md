# Tools Framework (`internal/tools/` + adapter packages)

## `runner.go`

### Struct

`Runner`

- **Fields**: `logger`, `config`, `textExtractor`, `ocr`, `pdfOptimizer`, `tagMatcher matcher.Matcher`, `contentAnalyzer`, `tagAdjudicator analyzer.ContentAnalyzer` (built from `cfg.Enricher.TagAdjudicator` when enabled; logs an error and stays nil when construction fails), `tagQualityJudge analyzer.ContentAnalyzer` (built from `cfg.Enricher.TagQuality` when enabled, same construction-failure contract), `fallbackAnalyzers []analyzer.ContentAnalyzer` + `fallbackMeta []fallbackMeta` (one entry per enabled `enricher.contentanalyzer.fallbacks` entry, empty when none are configured), `textReducer`
- **Functions**:
  - `NewRunner(logger, cfg, tools []string) *Runner` — Initializes only the listed tool adapters (e.g., `["textextractor","ocr","pdfoptimizer"]` for consumer). The accepted capability keys are `"textextractor"`, `"ocr"`, `"converter"`, `"pdfoptimizer"`, `"thumbnail"`, `"textreducer"`, `"contentanalyzer"`, and `"tagmatcher"`. Construction never fails: a requested adapter whose constructor fails is logged with its cause and left nil (its method then fails fast with "not configured"), and an unknown capability name is logged and skipped. Loads the LLM model catalog from `<configDir>/model_catalog.json` via `llm.NewRegistry`; a load failure is logged, leaving the analyzer adapters unconfigured. Does not create a tagmatcher internally. Content analyzer is created via the adapter-based factory: `analyzer.NewContentAnalyzer(logger, cfg.ToolConfig, &cfg.Enricher.ContentAnalyzer.Llm, cfg.Enricher.ContentAnalyzer.PromptTemplate, reg)`. For each **enabled** entry in `cfg.Enricher.ContentAnalyzer.Fallbacks`, an analyzer is built from that entry's `Llm` block with the same factory and appended to `fallbackAnalyzers` (with its provider/model recorded in `fallbackMeta`); entries whose factory fails are skipped with a logged error.
  - `NewRunnerWithMatcher(logger, cfg, tools, matcher matcher.Matcher) *Runner` — Calls `NewRunner` then conditionally sets `r.tagMatcher` if `matcher != nil` and `"tagmatcher"` is in the tools list.
- **Methods**: `ExtractText`, `OCR(ctx, docId, path)`, `OptimizePdf(ctx, docId, path)`, `ReduceContent`, `MatchTags(ctx, docId, input)`, `AnalyzeContent`, `AnalyzeDocType` — the two analyzer methods retry through `fallbackAnalyzers` in order when the primary error passes `isProviderError` (provider-attributable failure and at least one fallback is configured), returning the first success or the last error; request-side and lifecycle errors never retry. `AdjudicateTags(ctx, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error)` — classifies same-intent tag pairs via `tagAdjudicator.Adjudicate` under the `enricher.tag_adjudicator.timeout`; returns "tag adjudicator not configured" when the adjudicator is disabled or failed to construct. `JudgeTagIdentity(ctx, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error)` — answers the three binary canonical-form criteria for flagged candidates via `tagAdjudicator.JudgeTagIdentity` under the same timeout and error contract. `HasTagIdentityJudge() bool` — whether an identity adapter was built at construction time (the same adapter as the adjudicator). `AdjudicateTagSplits(ctx, tags []string) ([]analyzer.TagSplitResult, error)` — decides keep/split for compound tags via `tagAdjudicator.AdjudicateTagSplits`, same timeout and error contract. `HasTagAdjudicator() bool` — whether an adjudicator adapter was built at construction time. `JudgeTagQuality(ctx, text string, candidates []analyzer.TagQualityCandidate) ([]analyzer.TagQualityResult, error)` — runs the tag-quality rubric judge over the novel candidates under `enricher.tag_quality.timeout`; returns "tag quality judge not configured" when the layer is disabled or the adapter failed to construct. `HasTagQualityJudge() bool` — whether a tag-quality adapter was built at construction time.
- **Result types**: `TextExtractionResult`, `OCRResult`, `PdfOptimizationResult`, `TextReducerResult` (with Text, WordCount, CharCount, TargetWordCount), `TagMatchResult`, `ContentAnalysisResult` (with People)
- **Helper**: `runWithTimeout[T](ctx, fn) (T, error)` — Generic goroutine wrapper with context cancellation. `isProviderError(err) bool` — true for provider-attributable failures (network, HTTP status, credits); false for `ContentTooLargeError`, `TokenLimitError`, `context.Canceled`, `context.DeadlineExceeded`.
- **Timeout behavior**: `ExtractText`, `OCR`, `ReduceContent`, and `MatchTags` each read their component's configured `Timeout` and only wrap the context with `context.WithTimeout` when the value is > 0. A timeout of 0 means no artificial deadline — parent context cancellation still propagates via `runWithTimeout`. The same guarded pattern is used by `OptimizePdf`. Config validation rejects negative timeout values at load time.

---

## `internal/llm/analyzer/adapter.go`

### Interface

```go
type ContentAnalyzer interface {
    Analyze(ctx, text string, docTypes []TypeRef, peopleTypes []TypeRef, tagSuggestions []string) (*AnalysisResult, error)
    AnalyzeDocType(ctx, prevResult, headTailText, docTypes, metadata DocMetadata) (string, error)
    Adjudicate(ctx, pairs []TagPairEvidence) ([]TagVerdictResult, error)
    AdjudicateTagSplits(ctx, tags []string) ([]TagSplitResult, error)
    JudgeTagQuality(ctx, text string, candidates []TagQualityCandidate) ([]TagQualityResult, error)
    JudgeTagIdentity(ctx, candidates []TagIdentityCandidate) ([]TagIdentityResult, error)
    Name() string
}
```

### Structs

- `TypeRef` — `Name string`, `Description string`. One entry of a type list offered to the model: the type's name and the description the prompt renders beside it; the enricher maps its loaded document-type and people-type rows to these refs before calling `Analyze`/`AnalyzeDocType`.
- `AnalysisResult` — `Title`, `DocType`, `Tags []string` (canonical downstream field), `TagDetails []TagDetail` (typed mirror of `Tags`, consumed once by the enricher's facet gate, `omitempty` in JSON), `People []PeopleResult`, `Language`, `Stats *json.RawMessage`, `Prompt`, `PassContext *json.RawMessage` (`json:"-"`, the replayed prompts for the doc-type refinement call)
- `PeopleResult` — `Name`, `NameRomanized` (optional, for non-Latin names), `Type`, `NormalizedName` (pre-populated by enricher via `canonicalPersonName` + `NormalizeForDB`, used by `FilterTags`, excluded from JSON serialization via `json:"-"`)

### Factory

`NewContentAnalyzer(logger, cfg, llmCfg, promptTemplate, minTags, maxTags, registry)` — Selects adapter by `llmCfg.Adapter` using a capability registry. The adapter is chosen from the `llmCfg.Adapter` field: `anthropic` dispatches to `NewLlmAnthropic`, anything else to the `openai-compatible` adapter (`NewLlmOpenAiCompatible`). Capabilities are looked up from the `registry` via `registry.Lookup(llmCfg.Provider, llmCfg.Model)` and passed to each adapter for dynamic request building (stripping unsupported parameters). `promptTemplate` is a custom Go `text/template` string; empty/missing means use the built-in default. `minTags`/`maxTags` feed the prompt's tag-range wording; the tag adjudicator is built with `0, 0` (it never runs `Analyze`).

---

## `internal/llm/analyzer/gates.go`

### Types

- `ContentTooLargeError` — Returned by `checkContentTooLarge` when estimated tokens exceed the model's `MaxInputTokens`. Fields: `EstimatedTokens`, `MaxInputTokens`.
- `TokenLimitError` — Returned by `parseTokenLimitError` when the provider error body matches the context-length exceeded regex. Fields: `MaxTokens`, `RequestedTokens`, `RawBody`.

### Functions

- `checkContentTooLarge(caps *llm.ModelCapability, fullPrompt string) error` — Estimates tokens via the analyzer's `estimateTokens` and returns a `ContentTooLargeError` if the estimate exceeds `caps.MaxInputTokens`. Returns nil if `caps` is nil or `MaxInputTokens ≤ 0`.
- `parseTokenLimitError(body []byte) error` — Matches provider error bodies against the regex `maximum context length is (\d+) tokens.*?you requested (?:about )?(\d+) tokens` and returns a `TokenLimitError` with the parsed counts. Returns nil on no match or zero values. Covers OpenAI, DeepSeek, and compatible providers; for Anthropic (different error format) it returns nil and the error propagates as generic API failure.
- `buildTokenUsageStats(prompt, completion, total int) *json.RawMessage` — Creates token usage stats JSON.

### Adapter integration

Both `Analyze()` and `AnalyzeDocType()` call `checkContentTooLarge` with their constructed prompt before making any HTTP request. If it returns an error, the adapter returns the `ContentTooLargeError` directly — no API call is made, saving time and credits. Both adapter `doRequest()` methods also call `parseTokenLimitError` on non-200 responses, catching provider-side context-length errors at response time. The four judged steps share one path per adapter (the generic `judgedOpenAI`/`judgedAnthropic` helper): `checkContentTooLarge` → request (temperature 0) → labeled `doRequest` → empty-content check → bare-array drift log → the step's parser; a parse failure is returned carrying the model name and the reply text (`(model <model>)` plus a `raw:` line).

---

## `internal/llm/analyzer/prompt.go`

### Constants

- `SystemMessage` (exported) — `"You are a helpful assistant specialized in document analysis and metadata extraction"`, used by both adapter implementations.

### Types

- `DocMetadata` — `WordCount int32`, `PageCount int32`, `MimeType string`. Returned by `Format()` as a human-readable string (e.g. `"15234 total words, 42 pages, application/pdf"`). Returns empty string when all fields are zero/empty.

### Functions

- `BuildPrompt(text, docTypes, peopleTypes, tagSuggestions, customTemplate, minTags, maxTags) string` — Builds the system prompt with JSON output instructions including people types. Prompts the LLM to provide a `name_romanized` field for any name containing non-Latin characters (Korean, Arabic, Cyrillic, Hebrew, etc.). The default template asks for "At least `minTags`" tags naming the document's broad subjects (the upper bound is not rendered), forbids merging concepts with a conjunction, and instructs compound decomposition — when the parts of a phrase each name a subject that would work as a tag on its own, the parts are emitted instead of the phrase (`russian history` → `russia` + `history`), while fixed names and set phrases stay whole; it also states the facet rules (tags describe subject matter, forms/genres belong in the document type, places only for countries and continents) and the typed tag contract — each tag is an object with `term`, `category` — with one-line negative exemplars (`literary anthology`, `racial scapegoating`, `newfoundland`). When `customTemplate` is non-empty (after trimming whitespace), it is used as a Go `text/template` with placeholders `{{.DocTypePrompt}}`, `{{.TagsPrompt}}`, `{{.PeoplePrompt}}`, `{{.Text}}`, `{{.MinTags}}`, `{{.MaxTags}}`. On parse or execution error, falls back silently to the hardcoded default template. The rendered prompt is captured in `AnalysisResult.Prompt` for debugging.

---

## `internal/llm/analyzer/tagfilter.go`

### Constants

- `maxTagWords` (3) — Word-count cap enforced by `TagFilter.Accept`/`FilterTags`.
- `maxTagLength` (64) — Character cap enforced by `TagFilter.Accept`/`FilterTags`.

### Types

- `TagFilter` — Token sets built by `NewTagFilter(people, knownPeopleNormalized, title, docTypeNames)`; `Accept(tag string) bool` applies the same shape rules and token checks `FilterTags` uses (normalizes its input first). Shared so split components get the same per-document rules as emitted tags.

### Functions

- `NormalizeTags(raw []string) []string` — Converts LLM-extracted tags to canonical form via `textnorm.NormalizeTag` (NFKC, lowercase, dash family → space, accent folding, keeps letters of any Unicode script, digits, and `+ # .`, strips everything else, collapses whitespace), then deduplicates and rejects empty/whitespace-only results.
- `FilterTags(tags []string, filter *TagFilter) []string` — Post-normalization deterministic tag cleaner. Drops tags with more than 3 tokens or more than 64 chars, tags sharing any token with a person's `NormalizedName` (pre-populated by the enricher via `canonicalPersonName` + `NormalizeForDB`), multi-word tags (≥2 tokens) whose full token set is a strict subset of a known person's normalized name (from the full `people` table, passed via `knownPeopleNormalized`), tags matching a doc-type name token, and tags whose token set is a subset of the document title's token set. Applies no count cap — the emission size follows the document's dimensions. Operates after `NormalizeTags` and before rank-based consolidation.
- `SplitComponents(raw []string, filter *TagFilter) []string` — Normalizes adjudicated split components (`NormalizeTags`) and drops names that fail the shape rules or the filter's token checks; callers require 2..`MaxCompoundWords` (3) survivors.

---

## `internal/llm/analyzer/places.go`

### Functions

- `IsWhitelistedPlace(term string) bool` — Reports whether the normalized term is a whitelisted country or continent. The embedded list covers the continents, UN member and observer states, contested or partially recognized states (`palestine`, `taiwan`, `kosovo`, `western sahara`, …), and common short or alternate names (`usa`, `uk`, `uae`, `drc`, `burma`, `holland`, …). Both sides of the lookup are normalized with `normalizeCore`, and a leading `the ` is ignored.

---

## `internal/llm/analyzer/facets.go`

### Constants

- `TagCategoryTopic`/`TagCategoryFormGenre`/`TagCategoryPlace`/`TagCategoryPerson`/`TagCategoryWork`/`TagCategoryOther` (exported) — the closed category set of a `TagDetail`.

### Types

- `TagDetail` — `Term string`, `Category string` (`category`). `Category` is one of the exported `TagCategory*` constants (`topic`, `form_genre`, `place`, `person`, `work`, `other`); the parse folds case/hyphen/space variants and maps anything missing or unknown to `other`. Only object-shaped entries carry a detail; a plain-string entry yields a tag with no detail row, so consumers see an unannotated emission rather than a defaulted category.
- `FacetDrop` — `Term string`, `Category string`, `Reason string`. One tag dropped by `SeparateFacets`; `Reason` records why: `matches_document_type` / `model_self_classification` for `form_genre`, `people_facet` for `person`, `forbidden_category` for `work`, `non_whitelisted_place` for a `place` that is not a whitelisted country or continent.

### Functions

- `ParseAnalysisResponse(content string) (*AnalysisResult, error)` — Decodes one analysis response, accepting both tag shapes: a plain-string array (each entry becomes a tag with no detail row) and an object array (`term`, `category`), mixed arrays included. Elements that are neither a string nor an object with a non-empty `term` are skipped; `category` is folded via `normalizeTagCategory` (case, hyphens, spaces; unknown or missing → `other`). A missing or `null` `tags` key yields empty slices; a `tags` value that is not an array returns an error. Both adapters' `Analyze` and the missing-`type` fallback of `AnalyzeDocType` parse through this helper.
- `normalizeTagCategory(raw string) string` — Lowercases and trims the raw value, folds `-` and spaces to `_`, and returns the matching `TagCategory*` constant or `TagCategoryOther`.
- `SeparateFacets(tags []string, details []TagDetail, docTypeNames []string) ([]string, []FacetDrop)` — Deterministic facet gate. Drops typed tags by their `TagDetail.Category`: `form_genre` tags (they belong in the document type) are dropped with `Reason` = `matches_document_type` when the normalized term is an existing `document_type` name, `model_self_classification` otherwise; `person` (`people_facet`) and `work` (`forbidden_category`) tags are dropped; `place` survives only when the term is a whitelisted country or continent (`non_whitelisted_place` otherwise). `topic` and `other` always survive, as do tags with no matching detail row. Returns `tags` unchanged when there are no details (untyped emissions gate nothing). Runs after `NormalizeTags` and before `FilterTags`; the enricher logs every drop.

---

## `internal/llm/analyzer/adjudication.go`

### Constants

- `AdjudicationSystemMessage` (exported) — `"You are a tag-taxonomy classifier. Decide whether pairs of tag terms denote one value."`

### Types

- `TagPairEvidence` — `Query`, `Target`, `Sim`, `RunnerUpSim` (query-side runner-up similarity), `TargetRunnerUpTag`/`TargetRunnerUpSim` (target's nearest other store tag). Fed to `Adjudicate`'s prompt.
- `TagVerdictResult` — `Index int`, `Verdict types.TagVerdict` (`same`/`variant`/`related`; zero verdict = ambiguous entry, caller keeps), `Direction string` (for `same`: which term is the precise one — `query`/`target`/`tie`; empty when absent).

### Functions

- `BuildAdjudicationPrompt(pairs []TagPairEvidence) string` — Builds the JSON-verdict prompt: per pair, the two terms, their embedding similarity, the query-side runner-up similarity, and the target's nearest other store tag.
- `ParseAdjudicationResponse(content string, n int) ([]TagVerdictResult, error)` — Parses `{"pairs":[{"index":0,"verdict":"same"},...]}` (code-fenced or plain) into one result per pair; a bare top-level array is accepted as the envelope's value, and an entry without `index` is placed by its array position. Unknown verdict strings, out-of-range indices, and short slices leave the affected entries with a zero verdict (ambiguous → caller keeps). Invalid JSON returns an error.

---

## `internal/llm/analyzer/tag_split.go`

### Constants

- `TagSplitVersion` (exported) — `"v1"`; pins the split gate's criteria text, carried in the admission driver's split/keep/skip log lines and in every `tag_composition_event` row's `criteria_version`. v1 names the generic-head split class, asks one reading per family, keeps named units whole, and states that store existence is not the judge's criterion; the verdict and response shape are unchanged, and split verdicts are in-process only, so no cached verdict is invalidated.
- `TagSplitSystemMessage` (exported) — `"You are a tag-taxonomy classifier. Decide whether a tag's words each name a subject a searcher would filter by on its own, and the tag is just their combination."`

### Types

- `TagSplitVerdict` — `TagSplitKeep`/`TagSplitSplit`; zero verdict = ambiguous, callers keep.
- `TagSplitResult` — `Verdict TagSplitVerdict` (`keep`/`split`; zero verdict = ambiguous entry, caller keeps), `Components []string` (the split components, present only for a `split` verdict).

### Functions

- `BuildTagSplitPrompt(tags []string) string` — Builds the JSON-verdict prompt for compound tags: per tag, its content words are the parts; `split` when every part names a subject that works as a tag on its own and the phrase is just their combination — a modifier before a generic head splits too (`labor movement`, `bike repair`) — `keep` when the phrase names one established subject the parts cannot compose; compounds sharing a head and shape take one reading, store existence is not the criterion, and unsettled readings keep, never force a split. Split components are listed when splitting.
- `ParseTagSplitResponse(content string, n int) ([]TagSplitResult, error)` — Parses `{"splits":[{"index":0,"verdict":"split","components":[...]},...]}` (code-fenced or plain) into one result per tag; a bare top-level array is accepted as the envelope's value, and an entry without `index` is placed by its array position. Unknown verdicts, out-of-range indices, and splits with fewer than two non-empty components leave the entry ambiguous (caller keeps). Invalid JSON returns an error.

---

## `internal/llm/analyzer/tag_identity.go`

### Types

- `TagIdentityCandidate` — `Index int`, `Term string`, `ProposedFold string` (the mechanically proposed fold, empty when none). Fed to `JudgeTagIdentity`'s prompt.
- `TagIdentityResult` — `Index int`, `FoldToBase`, `FoldPreservesDenotation`, `ConventionalForm bool` (the three binary criteria); `CriteriaRecord(proposedFold)` is the persisted evidence payload.

---

## `internal/llm/analyzer/openai_compatible.go`

### Struct

`LlmOpenAiCompatible` — OpenAI-compatible `/chat/completions` API (absorbs former OpenAI, DeepSeek, Mistral, Groq, etc.)

- Messages: system + user (plus the assistant turn on the doc-type refinement call), assembled by `buildRequestMessages`
- Headers: `Authorization: Bearer {token}`; the catalog-declared session header when the provider has one
- Shared request assembly: `buildRequestMessages` → `buildRequestBody` → `buildRequest`, which sets `model`/`messages`/`stream` and applies the capability envelope
- Dynamic request building based on `*ModelCapability`:
  - Adds `temperature`/`top_p` only when `caps.SupportsTemperature`
  - Adds `response_format: {type: "json_object"}` only when `caps.SupportsStructuredOutput`
  - Adds the thinking config only when `caps.SupportsReasoning`: provider-specific knobs (`thinking` for deepseek/zhipu, `enable_thinking` for qwen, `reasoning_effort` otherwise), with the disabled forms when `llmCfg.Reasoning` is false
- Default provider URL from the registry's `ProviderDefaultURL(provider)`
- Token usage from `usage` field
- The four judged steps (`Adjudicate`, `AdjudicateTagSplits`, `JudgeTagQuality`, `JudgeTagIdentity`) share `judgedOpenAI`: size gate → temperature-0 request → labeled `doRequest` → empty-response check → bare-array drift log → the step's parser; a parse failure carries the model name and the raw reply

---

## `internal/llm/analyzer/llm_anthropic.go`

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
- Shared request assembly: `buildRequest(system, messages, maxTokens, temp)` sets the envelope (model, max tokens, system, `stream: false`, ephemeral cache control) and applies the reasoning and temperature gates; `Analyze`, `AnalyzeDocType`, and the judged-step helper all go through it
- Shared transport: `doRequest(ctx, reqBody, errLabel)` marshals, sends, and decodes one `/messages` call (headers, status/error parsing, text-block extraction)
- The four judged steps share `judgedAnthropic`: size gate → temperature-0 request → labeled `doRequest` → bare-array drift log → the step's parser; a parse failure carries the model name and the cleaned reply text

---

## `internal/matcher/adapter.go`

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
    Encode(ctx, docId string, texts []string) ([][]float32, error)
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
scores; the identity decision lives in the admission pipeline
(`Adjudicator.PlanIdentity`). `SimilarPair` is an unordered pair of store names
with their cosine similarity, canonicalized so each pair is stored once.

The composition root builds a single `*Hugot` (for the `kushim` CLI) or uses a `*matcher.MatcherClient` (for the `edub` API server) — both satisfy the `Matcher` and `Embedder` interfaces. The `MatcherClient` forwards all calls over a Unix socket to a standalone `kushim hugot` process.

---

## `internal/matcher/hugot.go`

### Struct

`Hugot` — Hugot session with `FeatureExtractionPipeline`

- **Backend**: `"ort"` (default — ONNX Runtime, auto-downloads `libonnxruntime.so`) or `"go"` (pure Go with `libtokenizers.a`)
- **Chunked encoding**: Texts exceeding the effective chunk size (configured `chunk_size`, default `4096`; `0` = the model's `max_position_embeddings` minus a 12-token safety margin, 8180 for BGE-M3) are split into overlapping token chunks, mean-pooled. The default bounds per-request memory — see [Tag Matcher guide](../tags/matcher.md)
- **Ranking**: Cosine similarity on L2-normalized embeddings (dot product); separate `minSimilarity` (doc→tag) and `consolidationSim` (tag→tag consideration floor) — similarity ranks candidates and never decides identity
- **Config**: `TopN`, `MinSimilarity`, `ConsolidationSimilarity`, `ChunkSize` (default 4096; `0` = model max, clamped to `max_position_embeddings` − 12), `ChunkOverlap` (10% of chunk size)
- **Fields**: `store EmbeddingStore` — shared reference to the tag embedding cache; `pairs` — mutex-guarded map of unordered `similarPairKey` → cosine similarity holding every pair of store entries at or above `consolidationSim`
- **Methods**:
  - `Match(ctx, docId, input)` — Reads all entries from the store, encodes the input, ranks by cosine similarity, returns top-N matches
  - `Rank(ctx, docId, queries []string)` — Reads all entries from the store internally, encodes the queries verbatim (they are already canonical — `analyzer.NormalizeTags` output), ranks against the store embeddings, and returns up to two candidates per query with scores plus the kept name
  - `SimilarPairs(ctx)` — Snapshots the pair cache under the mutex and returns it as a slice of `SimilarPair`
  - `Encode(ctx, docId, texts)` — Batch embedding with chunked encoding for long inputs (an empty `docId` means no log correlation). Does NOT normalize input — shared with document text matching where punctuation is meaningful.
  - `AddToStore(ctx, names)` — Encodes names verbatim (canonical DB names from `curation.Tag.Create`/`Update`) in batches of 32 (`embedBatchSize`), adds them to the store, and maintains the pair cache: one `Entries()` snapshot per batch, prune-then-insert per name (re-encodes drop stale pairs), fresh batch vectors used for intra-batch pairs
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
    SupportsReasoning        bool
    ReasoningEffortLevels    []string
    SupportsTemperature      bool
    SupportsPromptCaching    bool
    SupportsStructuredOutput bool
    MaxInputTokens           int
    MaxOutputTokens          int
}
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

## `internal/tools/textreducer/adapter.go`

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

## `internal/tools/textreducer/textrank.go`

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

## `internal/documents/ocr/adapter.go`

### Interface

`OCR` — `Process(ctx, docId, path)`, `CanHandle`, `Name`

### Factory

`NewOCR`

---

## `internal/documents/ocr/tesseract_link.go`

### CGo preamble

Links Tesseract, Leptonica, and libpng statically. Includes the Leptonica include path in `#cgo CFLAGS` and exposes a Go function `suppressLeptonicaStderr()` that registers a no-op stderr handler via `leptSetStderrHandler()`. All Leptonica `lept_stderr()` calls in the process are silently dropped. Genuine errors still propagate through Tesseract's C API return codes, which Go handles via `GetBoundingBoxes()` and `SetImageFromBytes()`.

## `internal/documents/ocr/gosseract.go`

### Struct

`Gosseract` — Tesseract + MuPDF CGo adapter. The `Process()` method forks `kushim internal-ocr` as a subprocess to run the OCR pipeline (MuPDF render at 200 DPI → Tesseract recognition → fpdf searchable PDF assembly with text rendering mode 3). The parent waits on `exec.CommandContext` which yields the Go scheduler via `entersyscall`, preventing CGo heartbeat starvation. Optimization runs in the parent after the child exits.

### Error handling

When the subprocess exits with a non-zero code, the adapter checks three conditions before treating the failure as fatal:

1. The error is an `exec.ExitError` (process ran but exited abnormally — covers exit code 2 from worker goroutine panics)
2. The output file exists on disk (the PDF was fully written before the exit)
3. The captured stderr contains known non-fatal Leptonica patterns (`pixReadMemTiff: function not present`, `font pixa not made`, `bmfCreate`)

If all three hold, the error is logged as a warning and processing continues — the output PDF is passed to the optimizer. Otherwise, the output is removed and the error is returned as before. This recovers files that would otherwise be quarantined due to exit code 2 from harmless Leptonica TIFF warnings during bitmap font creation (Tesseract's `api->Init()` calls `pixReadMem` with embedded TIFF font data, which fails through the `--without-libtiff` stub).

## `internal/documents/ocr/standalone.go`

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

## `internal/documents/textextractor/adapter.go`

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

## `internal/documents/pdfoptimizer/adapter.go`

### Interface

`PdfOptimizer` — `Optimize(ctx, docId, path)`, `Name`

### Factory

`NewPdfOptimizer`

### Implementations

- `MuPDF` (default, CGo) — `pdf_clean_file`
- `Ghostscript` (external tool)

---

## `internal/cgo/mupdf_wrapper.go`

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

- [Tag Matcher Guide](../tags/matcher.md) — Matcher config, memory, and CPU tuning
- [Pipeline](pipeline.md) — Consumer and Enricher that use the Runner
- [Config & Utils](config-and-utils.md) — Tool configuration (engines, timeouts, models)
- `internal/llm/` — LLM capability registry (model catalog, adapter/provider/model discovery)
