# Tools Framework (`internal/tools/`)

## `runner.go`

### Struct

`Runner`

- **Fields**: `logger`, `config`, `textExtractor`, `ocr`, `pdfOptimizer`, `tagMatcher`, `contentAnalyzer`, `textReducer`
- **Functions**:
  - `NewRunner(logger, cfg, tools []string) *Runner` — Initializes only the listed tool adapters (e.g., `["textextractor","ocr","pdfoptimizer"]` for consumer, `["textreducer","contentanalyzer","tagmatcher"]` for enricher). Tag matcher init errors are silently ignored (runner continues without it).
  - `NewRunnerWithAdapters(logger, cfg, adapters...) *Runner` — Dependency injection variant
- **Methods**: `ExtractText`, `OCR(ctx, docId, path)`, `OptimizePdf(ctx, docId, path)`, `ReduceContent`, `MatchTags(ctx, docId, input, tagsToMatch)`, `MatchEach(ctx, docId, queries, tagsToMatch)`, `EncodeTags(ctx, *docId, tags)`, `AnalyzeContent`
- **Result types**: `TextExtractionResult`, `OCRResult`, `PdfOptimizationResult`, `TextReducerResult` (with Text, WordCount, CharCount, TargetWordCount), `TagMatchResult`, `ContentAnalysisResult` (with People)
- **Helper**: `runWithTimeout[T](ctx, fn) (T, error)` — Generic goroutine wrapper with context cancellation

---

## `adapters/contentanalyzer/adapter.go`

### Interface

```go
type ContentAnalyzer interface {
    Analyze(ctx, text string, docTypes []database.DocumentType, peopleTypes []database.PeopleType, tagSuggestions []string) (*AnalysisResult, error)
    Name() string
}
```

### Structs

- `AnalysisResult` — `Title`, `DocType`, `Tags`, `People []PeopleResult`, `Language`, `Stats *json.RawMessage`, `Prompt`
- `PeopleResult` — `Name`, `Type`

### Factory

`NewContentAnalyzer(logger, cfg, llmCfg)` — Selects provider by `cfg.Command`: `"llmopenai"`, `"llmanthropic"`, `"llmdeepseek"`, `"llmollama"`. Default: OpenAI.

---

## `adapters/contentanalyzer/shared.go`

### Functions

- `BuildPrompt(text, docTypes, peopleTypes, tagSuggestions) string` — Builds system prompt with JSON output instructions including people types
- `buildTokenUsageStats(prompt, completion, total int) *json.RawMessage` — Creates token usage stats JSON

---

## `adapters/contentanalyzer/llm_openai.go`

### Struct

`LlmOpenAi` — OpenAI `/chat/completions` API

- Messages: system + user with `json_object` response format
- Headers: `Authorization: Bearer {token}`
- Token usage from `usage` field

---

## `adapters/contentanalyzer/llm_anthropic.go`

### Struct

`LlmAnthropic` — Anthropic `/messages` API

- System message via `system` field, user message via `messages`
- Headers: `x-api-key`, `anthropic-version: 2023-06-01`
- Token usage from `usage.input_tokens` + `usage.output_tokens`

---

## `adapters/contentanalyzer/llm_deepseek.go`

### Struct

`LlmDeepSeek` — DeepSeek `/chat/completions` API (OpenAI-compatible)

- Messages: system + user with `json_object` response format, thinking disabled
- Headers: `Authorization: Bearer {token}`
- Token usage from `usage` field

---

## `adapters/contentanalyzer/llm_ollama.go`

### Struct

`LlmOllama` — Local Ollama `/api/chat` API

- Messages: system + user with `json` format field
- No auth headers needed; `KeepAlive: 5m`
- Token usage from `prompt_eval_count` + `eval_count`

---

## `adapters/tagmatcher/adapter.go`

### Interface

```go
type TagMatcher interface {
    Match(ctx, docId, input string, tagsToMatch map[string][]float32) ([]string, error)
    MatchEach(ctx, docId string, queries []string, tagsToMatch map[string][]float32) ([]string, error)
    Encode(ctx, docId *string, texts []string) ([][]float32, error)
    Close()
    Name() string
}
```

### Factory

`NewTagMatcher(logger, tmConfig)` — Returns `Hugot` adapter

---

## `adapters/tagmatcher/hugot.go`

### Struct

`Hugot` — Hugot session with `FeatureExtractionPipeline`

- **Backend**: `"GO"` (default, pure Go with `libtokenizers.a`) or `"ort"` (ONNX Runtime, auto-downloads `libonnxruntime.so`)
- **Chunked encoding**: Documents exceeding `max_position_embeddings` are split into overlapping token chunks, mean-pooled
- **Ranking**: Cosine similarity on L2-normalized embeddings (dot product); separate `minSimilarity` (doc→tag) and `consolidationSim` (tag→tag consolidation)
- **Config**: `TopN`, `MinSimilarity`, `ConsolidationSimilarity`, `ChunkSize` (auto-derived from model config), `ChunkOverlap` (10% of chunk size)
- **Methods**: `Encode(ctx, *docId, texts)`, `Match(ctx, docId, input, tagsToMatch)`, `MatchEach(ctx, docId, queries, tagsToMatch)`, `Close()`
- **Helpers**: `meanPool`, `rankMatches`, `cosineSimilarity`, `tokenize`, `encodeChunked`, `readMaxPositionEmbeddings`, `downloadLib`, `getBackendSession`

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

## `adapters/ocr/gosseract.go`

### Struct

`Gosseract` — Tesseract + MuPDF CGo. Renders pages at 200 DPI via MuPDF, OCRs with gosseract (PNG input), builds searchable PDF with fpdf (text rendering mode 3 for invisible selectable text). Embedded LiberationSans TTF for Unicode text layers.

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

MuPDF 1.27.2 CGo wrapper. 6 C helpers: document open/close, page rendering, text extraction, `pdf_clean_file`.

---

## See Also

- [Pipeline](pipeline.md) — Consumer and Enricher that use the Runner
- [Config & Utils](config-and-utils.md) — Tool configuration (engines, timeouts, models)
