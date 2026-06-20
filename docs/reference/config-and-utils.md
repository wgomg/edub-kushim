# Configuration (`internal/config/config.go`)

## Structs

- `Config` — `App AppConfig`, `Srv ServerConfig`, `Db DatabaseConfig`, `Storage StorageConfig`, `Consumer ConsumerConfig`, `Enricher EnricherConfig`
- `AppConfig`: `Env Environment`, `LogLevel string`, `LogFile string`, `ConfigDir string`
- `ServerConfig`: `Host`, `Port`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`
- `DatabaseConfig`: `Type`, `Path`, `Name`, `Seeders []string`
- `StorageConfig`: `ConsumptionDir`, `StorageDir`
- `ConsumerConfig`: `SupportedFiles []string`, `DeleteOriginal bool`, `Workers int`, `TextExtractor TextExtractorConfig`, `PdfOptimizer PdfOptimizerConfig`, `OCR OCRConfig`
  - `TextExtractorConfig`: `Engine string`, `Timeout int`
  - `PdfOptimizerConfig`: `Engine string`, `Fallback string`, `Timeout int`
  - `OCRConfig`: `Engine string`, `Languages []string`, `DataDir string`, `Timeout int`
- `EnricherConfig`: `Workers int`, `TextReducer TextReducerConfig`, `ContentAnalyzer ContentAnalyzerConfig`, `TagMatcher TagMatcherConfig`
  - `TextReducerConfig`: `Engine string`, `Timeout int`, `TargetWords int`
  - `ContentAnalyzerConfig`: `Engine string`, `Timeout int`, `Llm LlmToolsConfig`
    - `LlmToolsConfig`: `OpenAI LlmToolConfig`, `Anthropic LlmToolConfig`, `DeepSeek LlmToolConfig`, `Ollama LlmToolConfig`
    - `LlmToolConfig`: `BaseURL string`, `Model string`, `Token string`
  - `TagMatcherConfig`: `Engine`, `Timeout`, `ReduceTargetWords`, `ChunkSize`, `Hugot HugotConfig`, `TopN`, `MinSimilarity`, `ConsolidationSimilarity`
    - `HugotConfig`: `Model`, `Backend` (`"GO"` or `"ort"`), `ModelPath`, `BackendLibPath`
- `ToolConfig`: `Command string`, `Timeout time.Duration`

## Constants

`Environment` (`Development`, `Production`)

## Functions

- `DefaultConfig(configDir string) *Config` — Full defaults (BAAI/bge-m3, ort backend, gosseract OCR, textrank reducer, llmopenai analyzer, etc.)
- `Load(configDir string) (*Config, error)` — Loads YAML over defaults, validates OCR languages required, expands paths, creates dirs
- `defaultMinSimilarity(modelShortName string) float64` — Per-model thresholds (bge-m3: 0.40)
- `defaultConsolidationSimilarity(modelShortName string) float64` — Tag-to-tag thresholds (bge-m3: 0.82)
- `finalizeConfig(cfg, configDir) error`

## Engine Identifier Constants

Named constants for all adapter engine strings, used throughout the codebase
instead of string literals:

| Group              | Constants                                                                                     |
| ------------------ | --------------------------------------------------------------------------------------------- |
| `ContentAnalyzer`  | `OpenAI` (`"llmopenai"`), `Anthropic` (`"llmanthropic"`), `DeepSeek` (`"llmdeepseek"`), `Ollama` (`"llmollama"`) |
| `OCR`              | `Gosseract` (`"gosseract"`), `OcrMyPdf` (`"ocrmypdf"`)                                       |
| `PdfOptimizer`     | `MuPDF` (`"mupdf"`), `GS` (`"gs"`)                                                           |
| `TextExtractor`    | `MuPDF` (`"mupdf"`), `GoPdf` (`"gopdf"`), `PdfToText` (`"pdftotext"`)                        |
| `TextReducer`      | `TextRank` (`"textrank"`)                                                                     |
| `TagMatcher`       | `Hugot` (`"hugot"`)                                                                           |

## AvailableEngines

`AvailableEngines` is a `map[string][]EngineEntry` that lists all available engines
per tool category, used by the frontend settings UI to populate select dropdowns:

| Key                  | Entries                                           |
| -------------------- | ------------------------------------------------- |
| `content_analyzer`   | openai, anthropic, deepseek, ollama               |
| `ocr`                | gosseract, ocrmypdf                               |
| `pdf_optimizer`      | mupdf, gs                                         |
| `text_extractor`     | mupdf, gopdf, pdftotext                           |
| `text_reducer`       | textrank                                          |
| `tag_matcher`        | hugot                                             |

`EngineEntry` has `Value string` and `Label string` fields for UI display.

---

# Config Setup (`internal/config/setup.go`)

## Functions

- `ConfigExists(configDir string) bool` — Returns true if `config.yaml` exists in the given directory.
- `LoadOrBootstrap(configDir string) (*Config, error)` — Loads existing config if `ConfigExists`, otherwise calls `Bootstrap`. Prevents the wizard from overwriting saved settings on re-start.
- `Bootstrap(configDir string) (*Config, error)` — Creates config directory, subdirectories (data, inbox, storage, tessdata), writes skeleton `config.yaml`, initializes SQLite schema. Returns the default config.
- `SaveMap(configDir string, body map[string]any) error` — Writes arbitrary key-value map to `config.yaml` using viper. Keys use dot notation (e.g., `"consumer.ocr.languages"`).
- `MissingTessdataLanguages(cfg *Config) []string` — Returns languages whose `.traineddata` files are missing from the tessdata directory. Only checks when OCR engine is `"gosseract"`.
- `MissingHugotModel(cfg *Config) bool` — Returns true if the Hugot model directory does not exist.
- `DownloadTessdataLanguage(ctx context.Context, cfg *Config, lang string) error` — Downloads a single tessdata language file from GitHub. Idempotent (skips if already present).
- `DownloadHugotModel(ctx context.Context, cfg *Config, logger *utils.Logger) error` — Downloads the Hugot ONNX model from HuggingFace. Idempotent.

---

# Cache (`internal/cache/`)

## `cache.go`

### Interface

`Store` — `Attr(key) (any, bool)`, `Attrs() map[string]any`, `Keys() []string`, `Len() int`, `Remove(key string)`

### Structs

- `storeBase` — `myu sync.RWMutex`, `attrs map[string]any` — Thread-safe key-value store, `Attrs()` returns a copy
- `Cache` — `mu sync.RWMutex`, `stores map[string]Store` — Named store registry with `Set(name, store)` and `Get(name) (Store, bool)`

---

## `bootstrap.go`

### Constants

`defaultDim = 384`, `batchSize = 32`

### Function

- `BuildTagCache(ctx, db, logger, tmCfg) (*Cache, error)` — Fetches all tag names from DB, creates an initial empty `EmbeddingStore` at key `"tags"` with attrs `{dim:384, model:..., normalized:true}`, embeds tags in batches of 32 via `tagmatcher.NewHugot` directly, replaces the store. Graceful fallback to empty cache on Hugot init failure (returns the empty store).

---

## `embedding_store.go`

### Struct

`EmbeddingStore` — Embeds `storeBase`, fields: `entries map[string][]float32`

- **Methods**: `Keys()`, `Len()`, `Remove(key)`, `Get(key) ([]float32, bool)`, `Add(key, embedding)`, `Entries() map[string][]float32` (returns deep copy)

---

# Version (`internal/version/`)

## `version.go`

`const Version = "0.1.0"`

---

# Utilities (`internal/utils/`)

## `config.go`

### Function

`ConfigDir() (*string, error)` — Returns `~/.config/edub-kushim`

---

## `logger.go`

### Struct

`Logger` — `NewLogger(level string)`, `NewDiscardLogger()`, `NewLoggerWithWriter(w)`, `SetLevel(LogLevel)`, `Level()`, `Info(reqID *string, format, v...)`, `Error`, `Debug`, `Fatal`, `SetLogFile(path string) error`

- Numeric log levels: `LevelSilent` (1), `LevelFatal` (2), `LevelError` (3), `LevelInfo` (6), `LevelDebug` (7)
- File logging writes to file regardless of console level
- `reqID` parameter for request-scoped logging

---

## `metrics.go`

### Struct

`MemSnapshot` — `HeapInUse uint64`, `HeapAlloc uint64`, `NumGC uint64`, `RSS uint64`

### Functions

`ReadMemSnapshot()`, `FormatMemDelta(before, after)`, `HumanDuration(d)`, `readVmRSS()` (from `/proc/self/status`)

---

## `text.go`

### Functions

`CountWords`, `EstimateTokensFromWords`, `CleanUp` (removes special chars), `Truncate` (returns "Unknown" for whitespace-only), `CleanCodeBlock`, `ContainsNonLatin` (checks for CJK, Cyrillic, Arabic, Hebrew, Greek, Thai, Devanagari, Bengali, Hangul), `NormalizeName` (NFKC → lowercase → remove dots/commas/apostrophes/quotes → dash variants to space → whitespace collapse)

---

## `parambag.go`

### Struct

`ParamBag` — `NewParamBag(r)`, `Get`, `GetInt`, `GetInt64`, `GetBool`, `GetStrings`, `SetPathValue`

### Context helpers

`WithParamBag`, `GetParamBag`

---

## See Also

- [API](api.md) — Uses ParamBag middleware, ConfigHandler for `/wizard/config` endpoints
- [CLI](cli.md) — Uses ConfigDir, Logger, FlagParser, config setup functions
- [Pipeline](pipeline.md) — Uses Config structs for Consumer/Enricher settings
- [Tools](tools.md) — Uses Config for tool adapter selection
- [Task System](task-system.md) — ConfigTaskHandler processes config-related async tasks
