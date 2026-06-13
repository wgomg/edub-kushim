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

# PID File (`internal/pidfile/`)

## `pidfile.go`

### Struct

`Lock` — `once sync.Once`, `path string`, `done chan struct{}`

- **Methods**:
  - `Acquire(batchID, force, onSignal) (*Lock, error)` — Writes PID to temp file, sets up SIGTERM/SIGINT handler
  - `Release()` — Removes PID file
  - `Path(batchID) string` — Returns `/tmp/kushim_<batchID>.pid`
  - `Read(batchID) (int, error)` — Reads PID from file
  - `IsAlive(batchID) bool` — Checks if PID is running
  - `isAlive(path) (bool, error)` — Sends signal 0 to PID

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

- [API](api.md) — Uses ParamBag middleware
- [CLI](cli.md) — Uses ConfigDir, Logger, FlagParser
- [Pipeline](pipeline.md) — Uses Config structs for Consumer/Enricher settings
- [Tools](tools.md) — Uses Config for tool adapter selection
