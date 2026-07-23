# Tools Framework (`internal/tools/`)

## `runner.go`

### Struct

`Runner`

- **Fields**: `logger`, `config`, `textExtractor`, `ocr`, `pdfOptimizer`, `tagMatcher tagmatcher.Matcher`, `contentAnalyzer`, `textReducer`
- **Functions**:
  - `NewRunner(logger, cfg, tools []string) *Runner` — Initializes only the listed tool adapters (e.g., `["textextractor","ocr","pdfoptimizer"]` for consumer). Loads the LLM model catalog from `<configDir>/model_catalog.json` via `llm.NewRegistry`. No longer creates a tagmatcher internally. Content analyzer is created via the new adapter-based factory: `contentanalyzer.NewContentAnalyzer(logger, cfg.ToolConfig, &cfg.Enricher.ContentAnalyzer.Llm, cfg.Enricher.ContentAnalyzer.PromptTemplate, reg)`.
  - `NewRunnerWithMatcher(logger, cfg, tools, matcher tagmatcher.Matcher) *Runner` — Calls `NewRunner` then conditionally sets `r.tagMatcher` if `matcher != nil` and `"tagmatcher"` is in the tools list.
- **Methods**: `ExtractText`, `OCR(ctx, docId, path)`, `OptimizePdf(ctx, docId, path)`, `ReduceContent`, `MatchTags(ctx, docId, input)`, `AnalyzeContent`, `AnalyzeDocType`
- **Result types**: `TextExtractionResult`, `OCRResult`, `PdfOptimizationResult`, `TextReducerResult` (with Text, WordCount, CharCount, TargetWordCount), `TagMatchResult`, `ContentAnalysisResult` (with People)
- **Helper**: `runWithTimeout[T](ctx, fn) (T, error)` — Generic goroutine wrapper with context cancellation

---

## `adapters/contentanalyzer/adapter.go`

### Interface

```go
type ContentAnalyzer interface {
    Analyze(ctx, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error)
    AnalyzeDocType(ctx, prevResult, headTailText, docTypes) (string, error)
    Name() string
}
```

### Structs

- `AnalysisResult` — `Title`, `DocType`, `Tags`, `People []PeopleResult`, `Language`, `Stats *json.RawMessage`, `Prompt`
- `PeopleResult` — `Name`, `NameRomanized` (optional, for non-Latin names), `Type`, `NormalizedName` (pre-populated by enricher via `canonicalPersonName` + `NormalizeForDB`, used by `FilterTags`, excluded from JSON serialization via `json:"-"`)

### Factory

`NewContentAnalyzer(logger, cfg, llmCfg, promptTemplate, registry)` — Selects adapter by `llmCfg.Adapter` using a capability registry. The adapter is chosen from the `llmCfg.Adapter` field (`openai-compatible`, `anthropic`, `custom`). Capabilities are looked up from the `registry` via `registry.Lookup(llmCfg.Provider, llmCfg.Model)` and passed to each adapter for dynamic request building (stripping unsupported parameters). `promptTemplate` is a custom Go `text/template` string; empty/missing means use the built-in default.

---

## `adapters/contentanalyzer/shared.go`

### Constants

- `SystemMessage` (exported) — `"You are a helpful assistant specialized in document analysis and metadata extraction"`, used by both adapter implementations.
- `maxTags` (5) — Final cap enforced by `FilterTags`; future config field.
- `tagRequestBuffer` (3) — Extra tags requested to survive filtering.
- `requestedTagCount` (8) — `maxTags + tagRequestBuffer`, injected into the prompt so the LLM emits headroom.

### Types

- `ContentTooLargeError` — Returned by `checkContentTooLarge` when estimated tokens exceed the model's `MaxInputTokens`. Fields: `EstimatedTokens`, `MaxInputTokens`.
- `TokenLimitError` — Returned by `parseTokenLimitError` when the provider error body matches the context-length exceeded regex. Fields: `MaxTokens`, `RequestedTokens`, `RawBody`.

### Functions

- `checkContentTooLarge(caps *llm.ModelCapability, fullPrompt string) error` — Estimates tokens via `utils.EstimateTokens` and returns a `ContentTooLargeError` if the estimate exceeds `caps.MaxInputTokens`. Returns nil if `caps` is nil or `MaxInputTokens ≤ 0`.
- `parseTokenLimitError(body []byte) error` — Matches provider error bodies against the regex `maximum context length is (\d+) tokens.*?you requested (?:about )?(\d+) tokens` and returns a `TokenLimitError` with the parsed counts. Returns nil on no match or zero values. Covers OpenAI, DeepSeek, and compatible providers; for Anthropic (different error format) it returns nil and the error propagates as generic API failure.
- `BuildPrompt(text, docTypes, peopleTypes, tagSuggestions, customTemplate) string` — Builds system prompt with JSON output instructions including people types. Prompts the LLM to provide a `name_romanized` field for any name containing non-Latin characters (Korean, Arabic, Cyrillic, Hebrew, etc.). When `customTemplate` is non-empty (after trimming whitespace), it is used as a Go `text/template` with placeholders `{{.DocTypePrompt}}`, `{{.TagsPrompt}}`, `{{.PeoplePrompt}}`, `{{.Text}}`, and `{{.RequestedTags}}`. On parse or execution error, falls back silently to the hardcoded default template. The rendered prompt is captured in `AnalysisResult.Prompt` for debugging.
- `NormalizeTags(raw []string) []string` — Converts LLM-extracted tags to canonical space-separated form: folds accented characters to ASCII base letters (é→e, ü→u, ñ→n) via the shared `normalizeCore` pipeline, then deduplicates and rejects empty strings.
- `FilterTags(tags, people, knownPeopleNormalized, title, docTypeNames) []string` — Post-normalization deterministic tag cleaner. Drops tags with more than 3 tokens, tags sharing any token with a person's `NormalizedName` (pre-populated by the enricher via `canonicalPersonName` + `NormalizeForDB`), multi-word tags (≥2 tokens) whose full token set is a strict subset of a known person's normalized name (from the full `people` table, passed via `knownPeopleNormalized`), tags matching a doc-type name token, and tags whose token set is a subset of the document title's token set. Caps survivors at `maxTags` (5) in emit order. Operates after `NormalizeTags` and before `Consolidate`.
- `buildTokenUsageStats(prompt, completion, total int) *json.RawMessage` — Creates token usage stats JSON

### Adapter integration

Both `Analyze()` and `AnalyzeDocType()` call `checkContentTooLarge` with their constructed prompt before making any HTTP request. If it returns an error, the adapter returns the `ContentTooLargeError` directly — no API call is made, saving time and credits. Both adapter `doRequest()` methods (OpenAI's shared `doRequest`, Anthropic's inline HTTP in both `Analyze` and `AnalyzeDocType`) also call `parseTokenLimitError` on non-200 responses, catching provider-side context-length errors at response time.

---

## `adapters/contentanalyzer/llm_openai_compatible.go`

### Struct

`LlmOpenAiCompatible` — OpenAI-compatible `/chat/completions` API (absorbs former OpenAI, DeepSeek, Mistral, Groq, etc.)

- Messages: system + user with `json_object` response format when `caps.SupportsResponseSchema`
- Headers: `Authorization: Bearer {token}`
- Dynamic request building based on `*ModelCapability`:
  - Strips `reasoning_effort` if `!caps.SupportsReasoning`
  - Strips `temperature` if `!caps.SupportsSamplingParams`
  - Uses `response_format: {type: "json_object"}` only when `caps.SupportsResponseSchema`
- Default provider URL used unless `llmCfg.Endpoint` override is set
- Token usage from `usage` field

---

## `adapters/contentanalyzer/llm_anthropic.go`

### Struct

`LlmAnthropic` — Anthropic `/messages` API

- System message via `system` field, user message via `messages`
- Headers: `x-api-key`, `anthropic-version: 2023-06-01`
- Dynamic request building based on `*ModelCapability`:
  - `llmCfg.Reasoning == true` → sets `thinking: {type: "enabled"}` (or `adaptive` if `caps.AdaptiveThinking`)
  - `llmCfg.Reasoning == false` → sets `thinking: {type: "disabled"}`
  - Uses `output_config` for structured output when `caps.SupportsOutputConfig`
  - `max_tokens` set to `min(256, caps.MaxOutputTokens)` for no-generation calls, else `caps.MaxOutputTokens`
- Token usage from `usage.input_tokens` + `usage.output_tokens`

---

## `adapters/tagmatcher/adapter.go`

### Interfaces

```go
type Matcher interface {
    Match(ctx, docId, input string) ([]string, error)
    Close()
    Name() string
}

type Embedder interface {
    Encode(ctx, docId *string, texts []string) ([][]float32, error)
    Consolidate(ctx, docId string, queries []string) ([]string, error)
    AddToStore(ctx context.Context, names []string) error
    RemoveFromStore(ctx context.Context, names []string) error
    Close()
    Name() string
}

type EmbeddingStore interface {
    Add(key string, embedding []float32)
    Remove(key string)
    Entries() map[string][]float32
}
```

The `Matcher` interface is used by the Runner for document-to-tag matching. The `Embedder` interface is used by TagService for encoding and post-LLM consolidation; `AddToStore`/`RemoveFromStore` delegate store management to the adapter (encoding + adding to the embedding store, or removing from it). The `EmbeddingStore` interface provides read/write access to the shared tag embedding cache.

The composition root builds a single `*Hugot` (for the `kushim` CLI) or uses a `*tagmatch.MatcherClient` (for the `edub` API server) — both satisfy the `Matcher` and `Embedder` interfaces. The `MatcherClient` forwards all calls over a Unix socket to a standalone `kushim hugot` process.

---

## `adapters/tagmatcher/hugot.go`

### Struct

`Hugot` — Hugot session with `FeatureExtractionPipeline`

- **Backend**: `"GO"` (default, pure Go with `libtokenizers.a`) or `"ort"` (ONNX Runtime, auto-downloads `libonnxruntime.so`)
- **Chunked encoding**: Documents exceeding `max_position_embeddings` are split into overlapping token chunks, mean-pooled
- **Ranking**: Cosine similarity on L2-normalized embeddings (dot product); separate `minSimilarity` (doc→tag) and `consolidationSim` (tag→tag consolidation)
- **Config**: `TopN`, `MinSimilarity`, `ConsolidationSimilarity`, `ChunkSize` (auto-derived from model config), `ChunkOverlap` (10% of chunk size)
- **Fields**: `store EmbeddingStore` — shared reference to the tag embedding cache
- **Methods**:
  - `Match(ctx, docId, input)` — Reads all entries from the store, encodes the input, ranks by cosine similarity, returns top-N matches
  - `Consolidate(ctx, docId, queries []string)` — Reads all entries from the store internally, normalizes query tags via `normalizeForEmbedding`, encodes them, re-matches against canonical tag embeddings
  - `Encode(ctx, *docId, texts)` — Batch embedding with chunked encoding for long inputs. Does NOT normalize input — shared with document text matching where punctuation is meaningful.
  - `AddToStore(ctx, names)` — Normalizes names via `normalizeForEmbedding`, encodes them in batches of 32 (`embedBatchSize`), and adds them to the store
  - `RemoveFromStore(ctx, names)` — Removes names from the store (moved from TagService)
  - `SetStore(s EmbeddingStore)` — Injects the shared store reference after construction
  - `Close()` — Idempotent (nil-safe, sets session to nil after destroy)
- **Nil-receiver guards**: `Match`, `Consolidate`, `Encode` all return an error if `h == nil`, preventing typed-nil interface panics
- **Helpers**: `meanPool`, `rankMatches`, `cosineSimilarity`, `tokenize`, `encodeChunked`, `readMaxPositionEmbeddings`, `downloadLib`, `getBackendSession`

---

---

## `adapters/contentanalyzer/llm_custom.go`

### Struct

`LlmCustom` — Generic HTTP adapter using Go `text/template` for request body

- Uses `llmCfg.Custom.RequestBody` as a Go `text/template`
- Template variables: `{{.Model}}`, `{{.SystemPrompt}}`, `{{.UserPrompt}}`, `{{.Temperature}}`, `{{.ReasoningEffort}}`, `{{.MaxTokens}}`
- Executes HTTP POST to `llmCfg.Custom.URL` (or `llmCfg.Endpoint` if set)
- Extracts response using `llmCfg.Custom.ResponsePath` (dot-notation: `choices.0.message.content`)
- Falls back to raw response body if path is empty

---

## `internal/llm/` package (Capability Registry)

### Data source

A manually maintained `model_catalog.json` file committed to the repository at `internal/llm/model_catalog.json`. Downloaded at runtime on user request (no embedding).

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
| `NewRegistry(path)` | Loads JSON catalog from disk, builds index |
| `Lookup(provider, model)` | Returns `*ModelCapability` for a given provider+model |
| `Adapters()` | Returns `["openai-compatible", "anthropic", "custom"]` |
| `ProvidersForAdapter(adapter)` | Returns provider keys for a given adapter |
| `ModelsForProvider(provider)` | Returns model entries with capabilities |

### Provider→Adapter mapping

Built-in mapping routes providers to the correct adapter:
- `openai`, `deepseek`, `mistral`, `groq`, `together_ai`, `fireworks_ai`, `openrouter`, `azure` → `openai-compatible`
- `anthropic`, `bedrock` → `anthropic`
- Everything else → `custom`

### Runtime refresh

- Web UI settings page has "Refresh model catalog" button
- CLI: `kushim model-catalog refresh` command
- A stale local copy works offline

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

`RunStandalone(inputPath, outputPath, languages, dataDir, ocrWorkers)` — Self-contained OCR pipeline called by the `internal-ocr` subcommand. Renders pages at 200 DPI, downscales to 150 DPI via nearest-neighbor, OCRs with Tesseract, assembles a searchable PDF. When `numPages > 1`, parallelizes Tesseract calls across `ocrWorkers` goroutines (0 = auto, resolves to `runtime.NumCPU()`). No context or logger — parent handles cancellation and logging. Same package-level helpers (`downscaleRGB`, `samplesToRGBA`, `encodePNG`, `encodeJPEG`) used by the in-process code.

Calls `suppressLeptonicaStderr()` at the top of the function to suppress Leptonica diagnostic output (TIFF warnings from bitmap font creation) before any Tesseract or Leptonica operations run. The handler is set once per subprocess lifetime and is global to the process — harmless since the subprocess exits after a single OCR task.

<table>
<thead><tr><th>CLI flag</th><th>Config key</th><th>Description</th></tr></thead>
<tbody>
<tr><td><code>--ocr-workers</code></td><td><code>consumer.ocr.ocr_workers</code></td><td>Parallel OCR goroutine count; 0 = auto (CPU count), max is capped at <code>min(workers, pages, CPU*2)</code></td></tr>
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

---

## See Also

- [Pipeline](pipeline.md) — Consumer and Enricher that use the Runner
- [Config & Utils](config-and-utils.md) — Tool configuration (engines, timeouts, models)
- `internal/llm/` — LLM capability registry (model catalog, adapter/provider/model discovery)
