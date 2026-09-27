# Configuration (`internal/config/config.go`)

## Structs

- `Config` — `App AppConfig`, `Srv ServerConfig`, `Db DatabaseConfig`, `Storage StorageConfig`, `Consumer ConsumerConfig`, `Enricher EnricherConfig`, `Backup BackupConfig`, `Mirror MirrorConfig`
- `AppConfig`: `Env Environment`, `LogLevel string`, `Logging LoggingConfig`, `ConfigDir string`
  - `LoggingConfig`: `MaxSize int`, `MaxBackups int`, `MaxAge int`, `Compress bool`
- `ServerConfig`: `Host`, `Port`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `MaxUploadSize` (MB), `MaxConcurrentBatches` (default 4), `AuthEnabled` (when false, auth middleware passes all requests through; default `true` for existing installs, `false` for fresh bootstrap), `SessionSecret` (64-char hex JWT signing key; auto-generated during setup, fallback in-memory generation at server start), `MaxDownloadFiles`, `MaxDownloadSizeMB`, `MaxBatchDelete`
- `DatabaseConfig`: `Type`, `Host`, `Port`, `User`, `Password`, `Database`, `SSLMode`, `DSN`, `Runtime` (host|docker|podman|remote, default host — where restores run psql), `Container` (required when runtime is docker/podman), `Seeders []string`
- `StorageConfig`: `ConsumptionDir`, `StorageDir`, `Trash TrashConfig`
- `TrashConfig`: `RetentionDays` (default 30)
- `ConsumerConfig`: `SupportedFiles []string`, `Workers int`, `MaxFilesPerBatch int`, `TextExtractor TextExtractorConfig`, `PdfOptimizer PdfOptimizerConfig`, `OCR OCRConfig`, `Polling PollingConfig`, `Reclaim ReclaimConfig`
  - `PollingConfig`: `Enabled bool`, `Interval int` (minutes), `Windows []PollingWindow` (optional time-range restrictions with `Start`/`End` in HH:MM format)
  - `ReclaimConfig`: `Enabled bool`, `MaxRetries int` (default 3), `StaleTaskAfter int64` (default 600s)
   - `TextExtractorConfig`: `Engine string`, `Timeout int` (0 = disabled, no artificial deadline)
   - `PdfOptimizerConfig`: `Engine string`, `Fallback string`, `Timeout int` (0 = disabled)
   - `OCRConfig`: `Engine string`, `Languages []string`, `DataDir string`, `Timeout int` (0 = disabled), `OcrWorkers int` (0 = auto, resolved by the parent runner to `max(1, NumCPU / (max_concurrent_batches × consumer.workers))`)
- `EnricherConfig`: `Workers int`, `TextReducer TextReducerConfig`, `ContentAnalyzer ContentAnalyzerConfig`, `TagMatcher TagMatcherConfig`, `TagAdjudicator TagAdjudicatorConfig`, `TagQuality TagQualityConfig`
   - `TagAdjudicatorConfig`: `Enabled bool` (default `false` — opt-in), `Timeout int` (defaults to `ContentAnalyzer.Timeout` when zero), `Llm LlmConfig` (same shape as the analyzer's; `ResolvedAdjudicatorLlm` falls back to the content analyzer's `llm` at use time when `provider` or `model` is empty, so the inherit state survives saves)
   - `TagQualityConfig`: `Enabled bool` (default `false` — opt-in), `Timeout int` (defaults to `ContentAnalyzer.Timeout` when zero), `Llm LlmConfig` (same fallback shape as the adjudicator's). The weak-band thresholds are internal v1 constants in the enricher (`t_low = -1`, `no_anchor_similarity = 0.60`), not config keys.
   - `TextReducerConfig`: `Engine string`, `Timeout int` (0 = disabled), `TargetWords int`
  - `ContentAnalyzerConfig`: `Enabled bool` (default `false`), `Timeout int`, `MinTags int` (default `3`), `MaxTags int` (default `5`, max `20`; validated `1 ≤ min ≤ max ≤ 20`), `Llm LlmConfig`, `Fallbacks []FallbackConfig` (empty = absent/disabled), `PromptTemplate string`, `DocTypeRefinement DocTypeRefinementConfig`
      - `LlmConfig`: `Adapter string`, `Provider string`, `Model string`, `Token string`, `Reasoning bool` (yaml:"-", set via model catalog), `ReasoningEffort string` (yaml:"-", set via model catalog), `Temperature float64`
      - `FallbackConfig`: `Enabled bool`, `Llm LlmConfig` — same shape as the primary `llm` block; validated only when `Enabled` (adapter/provider/model required, `RequestDelay` 0–60)
      - `DocTypeRefinementConfig`: `Enabled bool`, `HeadWords int`, `TailWords int`
   - `TagMatcherConfig`: `Timeout` (0 = disabled), `ReduceTargetWords`, `ChunkSize` (default 4096 tokens to bound matcher memory; 0 = model's `max_position_embeddings` minus 12), `Hugot HugotConfig`, `TopN`, `MinSimilarity`, `ConsolidationSimilarity`
    - `HugotConfig`: `Model`, `Backend` (`"GO"` or `"ort"`), `ModelPath`, `BackendLibPath`; internal-only (no yaml/json tags): `CpuMemArena bool` (default `false`), `MemPattern bool` (default `false`)
- `BackupConfig`: `Enabled bool`, `Path string` (fallback output dir), `Schedules []BackupSchedule` (mode/interval/time/path/keep). Deprecated flat `Interval`/`Time`/`Keep` fields are kept for auto-conversion into a single `full` schedule by `finalizeConfig`.
- `MirrorConfig`: `Enabled bool`, `Path string` (local path or `[user@]host:path` remote target), `Interval float64` (days), `Time string` (HH:MM, default `02:00`). Validated by `finalizeConfig` (path required when enabled, interval > 0, HH:MM parse, `ValidateMirrorDestination` before `MkdirAll`).
- `IsRemoteMirrorTarget(path) bool` — Detects rsync remote syntax: `rsync://host/module` or `[user@]host:path` (host part without `/` or whitespace). Remote targets skip `~` expansion/`MkdirAll`.
- `ValidateMirrorDestination(dest, storageDir, backupPath) error` — Symlink-resolved containment guard (refuses dest inside/containing/equal to storage, inside backup, dash-prefixed). Used by `finalizeConfig` (load time) and `backup.ValidateDestination` (execution time).
- `ToolConfig`: `Command string`, `Timeout time.Duration`

## Constants

`Environment` (`Development`, `Production`)

## Functions

- `DefaultConfig(configDir string) *Config` — Full defaults (BAAI/bge-m3, ort backend, tagmatcher `chunk_size` 4096, gosseract OCR, textrank reducer, openai-compatible analyzer with gpt-4o, 100 MB max upload, 4 max concurrent batches, etc.)
- `Load(configDir string) (*Config, error)` — Loads YAML over defaults, validates OCR languages required, expands paths, creates dirs
- `defaultMinSimilarity(modelShortName string) float64` — Per-model thresholds (bge-m3: 0.40)
- `defaultConsolidationSimilarity(modelShortName string) float64` — Tag-to-tag thresholds (bge-m3: 0.82)
- `finalizeConfig(cfg, configDir) error`

## Engine Identifier Constants

Named constants for all adapter engine strings, used throughout the codebase
instead of string literals:

| Group              | Constants                                                                                     |
| ------------------ | --------------------------------------------------------------------------------------------- |
| `OCR`              | `Gosseract` (`"gosseract"`), `OcrMyPdf` (`"ocrmypdf"`)                                                           |
| `PdfOptimizer`    | `MuPDF` (`"mupdf"`), `GS` (`"gs"`)                                                                               |
| `TextExtractor`    | `MuPDF` (`"mupdf"`), `GoPdf` (`"gopdf"`), `PdfToText` (`"pdftotext"`)                                            |
| `TextReducer`      | `TextRank` (`"textrank"`)                                                                     |

## AvailableEngines

`AvailableEngines` is a `map[string][]EngineEntry` that lists all available engines
per tool category, used by the frontend settings UI to populate select dropdowns:

| Key                  | Entries                                           |
| -------------------- | ------------------------------------------------- |
| `ocr`                | gosseract, ocrmypdf                               |
| `pdf_optimizer`      | mupdf, gs                                         |
| `text_extractor`     | mupdf, gopdf, pdftotext                           |
| `text_reducer`       | textrank                                          |

`EngineEntry` has `Value string` and `Label string` fields for UI display.

---

# Config Setup (`internal/config/setup.go`)

## Functions

- `ConfigExists(configDir string) bool` — Returns true if `config.yaml` exists in the given directory.
- `LoadOrBootstrap(configDir string) (*Config, error)` — Loads existing config if `ConfigExists`, otherwise calls `Bootstrap`. Prevents the wizard from overwriting saved settings on re-start.
- `Bootstrap(configDir string) (*Config, error)` — Creates config directory, subdirectories (data, inbox, storage, tessdata), writes skeleton `config.yaml`, initializes PostgreSQL schema (auto-creates database if missing), generates a random 32-byte `SessionSecret` and persists it alongside `auth_enabled: false` to `config.yaml` via `SaveMap`. Returns the default config (which has `AuthEnabled: true`). The config file is created with `0600` permissions (owner read/write only).
- `SaveMap(configDir string, body map[string]any) error` — Writes arbitrary key-value map to `config.yaml` using viper with `0600` permissions (owner read/write only). If the file already exists with weaker permissions, it is automatically self-healed to `0600` via explicit `os.Chmod`. Keys use dot notation (e.g., `"consumer.ocr.languages"`).
- `MissingTessdataLanguages(cfg *Config) []string` — Returns languages whose `.traineddata` files are missing from the tessdata directory. Only checks when OCR engine is `"gosseract"`.
- `MissingHugotModel(cfg *Config) bool` — Returns true if the Hugot model directory does not exist.
- `DownloadTessdataLanguage(ctx context.Context, cfg *Config, lang string) error` — Downloads a single tessdata language file from GitHub. Idempotent (skips if already present).
- `DownloadHugotModel(ctx context.Context, cfg *Config, logger *utils.Logger) error` — Downloads the Hugot ONNX model from HuggingFace. Idempotent.

---

# Tag Matcher Store (`internal/matcher/`)

## `bootstrap.go`

### Constants

`embedBatchSize = 32`: batch cap for tag-name encoding, declared in this untagged file so `CGO_ENABLED=0` builds compile it (the cgo-tagged `hugot.go` shares the value)

### Function

- `BuildTagCache(ctx, queries, enc, store) error`: Fetches the promoted tag names from DB (`ListPromotedTagNames`; provisional tags stay out of the store until promoted), embeds them in batches of `embedBatchSize` via the provided `Embedder`, and swaps the store's complete entry map under its lock. An empty vocabulary leaves an empty store; the daemon call site logs an error and continues with an empty cache.

---

## `embedding_store.go`

### Struct

`TagEmbeddingStore`: `mu sync.RWMutex`, `entries map[string][]float32`

- **Constructor**: `NewTagEmbeddingStore(entries map[string][]float32)`, where a nil map becomes an empty one
- **Methods**: `Remove(key)`, `Add(key, embedding)`, `Entries() map[string][]float32` (returns a deep copy)

---

# Version (`internal/version/`)

## `version.go`

`var Version = "2.9.0"`

---

# Utilities

The helpers formerly grouped in `internal/utils/` live next to their owners.

## `internal/config/configdir.go`

### Function

`ConfigDir() (*string, error)` — Returns `~/.config/edub-kushim`

---

## `internal/documents/storage/`

### `files.go`

`CalculateMD5(path string) (string, error)` — Opens a file, computes its MD5 hash via `crypto/md5`, and returns the hex-encoded digest. Used by the consume handler for enqueue-time dedup and by `Consumer.isDuplicate` for processing-time dedup.
`ListFilePaths(src string, exts []string, maxFiles int) ([]string, error)` — Scans `src` for files matching the given MIME-type extensions, sorted by creation time (oldest first). Skips directories and unsupported types. When `maxFiles > 0`, stops after collecting that many matching files (early break, avoiding unnecessary I/O).
`CountFilePaths(src string, exts []string) (int, error)` — Counts matching files without collecting their paths.
`SameDevice(a, b string) (bool, error)` — Reports whether two paths resolve to the same filesystem device; a missing path falls back to its parent directory.
`isSupportedEntry` (unexported) — directory-walk predicate shared by `ListFilePaths` and `CountFilePaths`.

### `hash.go`

`SHA256Hex(s string) string` — Hex-encoded SHA-256 of a string; the digest written to `document.text_hash`.

### `copy.go`

`CopyTree(src, dst string) error` — Recursively copies a directory tree (regular files only); `copyFile` (unexported) is the single-file primitive.

---

## `internal/log/logger.go`

### Struct

`Logger` — `NewLogger(level string)`, `NewDiscardLogger()`, `NewLoggerWithWriter(w)`, `SetLevel(LogLevel)`, `Level()`, `Info(reqID string, format, v...)`, `Error`, `Debug`, `Fatal`, `SetLogFile(LogFileConfig) error`, `SlogLogger() *slog.Logger`

- Numeric log levels: `LevelSilent` (1), `LevelFatal` (2), `LevelError` (3), `LevelWarn` (4), `LevelInfo` (6), `LevelDebug` (7)
- File logging writes to file regardless of console level
- `reqID` parameter for request-scoped logging; a non-empty value is prepended as `REQID=<id>`, an empty string logs without the prefix
- Implementation uses `log/slog` internally with two custom `slog.Handler` types: `consoleHandler` (routes info/debug to stdout, error/fatal to stderr with `<N>LEVEL : date file:line: msg` format) and `fileHandler` (always enabled, writes `date LEVEL : msg` format). `Fatal` calls `os.Exit(1)` directly.
- `SlogLogger()` returns a `*slog.Logger` backed by the console handler, intended for `slog.SetDefault` wiring in entry points.

---

## `internal/log/metrics.go`

### Structs

`MemSnapshot` — `HeapInUse uint64`, `HeapAlloc uint64`, `NumGC uint64`, `RSS uint64`
`MemFull` — embeds `MemSnapshot`; adds `HeapSys uint64`, `HeapIdle uint64`, `HeapReleased uint64`

### Functions

`ReadMemSnapshot()`, `ReadMemFull()`, `FormatMemFull(m)`, `FormatMemDelta(before, after)`, `FormatBytes(b)`, `HumanDuration(d)`, `readVmRSS()` (from `/proc/self/status`)

---

## `internal/llm/analyzer/text.go`

### Functions

`estimateTokens` (character-based token estimator for LLM budget checks — single pass counting CJK vs Latin runes, uses blended formula `cjk*1.5 + non_cjk/4` when CJK ratio > 10%, otherwise `total/4`), `cleanCodeBlock` (strips ` ```json ` / ` ``` ` fences and trims LLM responses), `ContainsNonLatin` (checks for CJK, Cyrillic, Arabic, Hebrew, Greek, Thai, Devanagari, Bengali, Hangul)

---

## `internal/enrichment/text.go`

### Function

`truncate(s string, maxLength int) string` — Rune-aware truncation; returns `"Unknown"` for whitespace-only input and trims trailing whitespace after truncation.

---

## `internal/tools/textreducer/textrank.go`

### Function

`cleanUp(text string) string` — Removes symbol characters (`$ € £ ¥ ¢ % & * + = < > ^ | ~ @ # \ _ [ ] { }`) before tokenization.

---

## `internal/api/handlers/text.go`

### Functions

`stripTags`, `stripTagsPtr` (unexported) — Byte-level HTML tag stripping for request fields.

---

## `internal/textnorm/`

Tag and DB-key canonicalization, shared by the tag service, the enricher, the matcher, and the reconciler.

### `normalize.go`

`canonicalHead` (the shared head both canonical forms start from: NFKC → lowercase/trim → dash and underscore variants to space → accent fold), `FoldAccents` (stateless NFD → drop Mn → NFC accent fold; the canonical fold shared by `NormalizeForDB` and `NormalizeTag`, safe for concurrent use), `NormalizeForDB` (canonical head → strip non-`[a-z ]` → collapse whitespace)

### `tagname.go`

`NormalizeTag` (canonical tag name — NFKC → lowercase → dash family to space → accent fold → keep letters of any Unicode script, digits, and `+ # .` → collapse whitespace; returns `""` when the result has no letter or digit), `containsLetterOrDigit` (rune scan backing the empty-result rule).

### `tagjoint.go`

`SplitJointTag(name string) []string` — Splits a canonical joint tag on its conjunction (`and`, `or`, `vs`, `vs.`, `versus`) and returns the component segments.

---

## `internal/api/handlers/parambag.go`

### Struct

`ParamBag` — `NewParamBag(r)`, `Get`, `GetInt`, `GetInt64`, `GetBool`, `GetStrings` (query parameters only; path values are read from the request with `r.PathValue`)

### Context helpers

`WithParamBag`, `GetParamBag`

---

## See Also

- [Tag Matcher Guide](../tags/matcher.md) — Matcher config, memory, and CPU tuning (`chunk_size`)
- [API](api.md) — Uses ParamBag middleware, ConfigHandler for `/wizard/bootstrap` (public) and `/wizard/config` (admin-protected) endpoints
- [CLI](cli.md) — Uses ConfigDir, Logger, FlagParser, config setup functions
- [Pipeline](pipeline.md) — Uses Config structs for Consumer/Enricher settings
- [Tools](tools.md) — Uses Config for tool adapter selection, capability registry (`internal/llm/`)
- [Task System](task-system.md) — ConfigTaskHandler processes config-related async tasks
