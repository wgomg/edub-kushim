package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Environment string

const (
	Development Environment = "development"
	Production  Environment = "production"
)

type AppConfig struct {
	Env       Environment `mapstructure:"environment" yaml:"environment" json:"environment"`
	LogLevel  string      `mapstructure:"log_level" yaml:"log_level" json:"log_level"`
	ConfigDir string      `json:"config_dir"`
}

type ServerConfig struct {
	Host                 string        `mapstructure:"host" yaml:"host" json:"host"`
	Port                 int           `mapstructure:"port" yaml:"port" json:"port"`
	ReadTimeout          time.Duration `mapstructure:"read_timeout" yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout         time.Duration `mapstructure:"write_timeout" yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout          time.Duration `mapstructure:"idle_timeout" yaml:"idle_timeout" json:"idle_timeout"`
	MaxUploadSize        int64         `mapstructure:"max_upload_size" yaml:"max_upload_size" json:"max_upload_size"`
	MaxConcurrentBatches int           `mapstructure:"max_concurrent_batches" yaml:"max_concurrent_batches" json:"max_concurrent_batches"`
	MaxDownloadFiles     int           `mapstructure:"max_download_files" yaml:"max_download_files" json:"max_download_files"`
	MaxDownloadSizeMB    int64         `mapstructure:"max_download_size_mb" yaml:"max_download_size_mb" json:"max_download_size_mb"`
	MaxBatchDelete       int           `mapstructure:"max_batch_delete" yaml:"max_batch_delete" json:"max_batch_delete"`
	AuthEnabled          bool          `mapstructure:"auth_enabled" yaml:"auth_enabled" json:"auth_enabled"`
	SessionSecret        string        `mapstructure:"session_secret" yaml:"session_secret" json:"session_secret"`
}

type DatabaseConfig struct {
	Type    string   `mapstructure:"type" yaml:"type" json:"type"`
	Path    string   `mapstructure:"path" yaml:"path" json:"path"`
	Name    string   `yaml:"name" json:"name"`
	Seeders []string `yaml:"seeders" json:"seeders"`
}

type StorageConfig struct {
	ConsumptionDir string `mapstructure:"consumption_dir" yaml:"consumption_dir" json:"consumption_dir"`
	StorageDir     string `mapstructure:"storage_dir" yaml:"storage_dir" json:"storage_dir"`
}

type TextExtractorConfig struct {
	Engine  string `mapstructure:"engine" yaml:"engine" json:"engine"`
	Timeout int    `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
}

type PdfOptimizerConfig struct {
	Engine   string `mapstructure:"engine" yaml:"engine" json:"engine"`
	Fallback string `mapstructure:"fallback" yaml:"fallback" json:"fallback"`
	Timeout  int    `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
}

type OCRConfig struct {
	Engine    string   `mapstructure:"engine" yaml:"engine" json:"engine"`
	Languages []string `mapstructure:"languages" yaml:"languages" json:"languages"`
	DataDir   string   `mapstructure:"data_dir" yaml:"data_dir" json:"data_dir"`
	Timeout   int      `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
}

type PollingWindow struct {
	Start string `mapstructure:"start" yaml:"start" json:"start"`
	End   string `mapstructure:"end" yaml:"end" json:"end"`
}

type PollingConfig struct {
	Enabled  bool            `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Interval int             `mapstructure:"interval" yaml:"interval" json:"interval"` // minutes
	Windows  []PollingWindow `mapstructure:"windows" yaml:"windows" json:"windows"`
}

type ReclaimConfig struct {
	Enabled bool `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
}

type ConsumerConfig struct {
	SupportedFiles  []string            `json:"supported_files"`
	Workers         int                 `mapstructure:"workers" yaml:"workers" json:"workers"`
	MaxFilesPerBatch int                `mapstructure:"max_files_per_batch" yaml:"max_files_per_batch" json:"max_files_per_batch"`
	TextExtractor   TextExtractorConfig `mapstructure:"textextractor" yaml:"textextractor" json:"textextractor"`
	PdfOptimizer    PdfOptimizerConfig  `mapstructure:"pdfoptimizer" yaml:"pdfoptimizer" json:"pdfoptimizer"`
	OCR             OCRConfig           `mapstructure:"ocr" yaml:"ocr" json:"ocr"`
	Polling         PollingConfig       `mapstructure:"polling" yaml:"polling" json:"polling"`
	Reclaim         ReclaimConfig       `mapstructure:"reclaim" yaml:"reclaim" json:"reclaim"`
}

type TextReducerConfig struct {
	Engine      string `mapstructure:"engine" yaml:"engine" json:"engine"`
	Timeout     int    `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	TargetWords int    `mapstructure:"target_words" yaml:"target_words" json:"target_words"`
}

type ContentAnalyzerConfig struct {
	Engine  string         `mapstructure:"engine" yaml:"engine" json:"engine"`
	Timeout int            `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	Llm     LlmToolsConfig `mapstructure:"llm" yaml:"llm" json:"llm"`
}

type EnricherConfig struct {
	Workers         int                   `mapstructure:"workers" yaml:"workers" json:"workers"`
	TextReducer     TextReducerConfig     `mapstructure:"textreducer" yaml:"textreducer" json:"textreducer"`
	ContentAnalyzer ContentAnalyzerConfig `mapstructure:"contentanalyzer" yaml:"contentanalyzer" json:"contentanalyzer"`
	TagMatcher      TagMatcherConfig      `mapstructure:"tagmatcher" yaml:"tagmatcher" json:"tagmatcher"`
}

type LlmToolConfig struct {
	BaseURL string `mapstructure:"base_url" yaml:"base_url" json:"base_url"`
	Model   string `mapstructure:"model" yaml:"model" json:"model"`
	Token   string `mapstructure:"token" yaml:"token" json:"token"`
}

type LlmToolsConfig struct {
	OpenAI    LlmToolConfig `mapstructure:"openai" yaml:"openai" json:"openai"`
	Anthropic LlmToolConfig `mapstructure:"anthropic" yaml:"anthropic" json:"anthropic"`
	DeepSeek  LlmToolConfig `mapstructure:"deepseek" yaml:"deepseek" json:"deepseek"`
	Ollama    LlmToolConfig `mapstructure:"ollama" yaml:"ollama" json:"ollama"`
}

type HugotConfig struct {
	Model          string `mapstructure:"model" yaml:"model" json:"model"`
	Backend        string `mapstructure:"backend" yaml:"backend" json:"backend"`
	ModelPath      string `json:"model_path"`
	BackendLibPath string `json:"backend_lib_path"`
	CpuMemArena    bool
	MemPattern     bool
}

type TagMatcherConfig struct {
	Timeout                 int         `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	ReduceTargetWords       int         `mapstructure:"reduce_target_words" yaml:"reduce_target_words" json:"reduce_target_words"`
	ChunkSize               int         `mapstructure:"chunk_size" yaml:"chunk_size" json:"chunk_size"`
	Hugot                   HugotConfig `mapstructure:"hugot" yaml:"hugot" json:"hugot"`
	TopN                    int         `json:"top_n"`
	MinSimilarity           float64     `json:"min_similarity"`
	ConsolidationSimilarity float64     `json:"consolidation_similarity"`
}

type ToolConfig struct {
	Command string        `yaml:"command" json:"command"`
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
}

type BackupConfig struct {
	Enabled  bool    `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Interval float64 `mapstructure:"interval" yaml:"interval" json:"interval"`
	Time     string  `mapstructure:"time" yaml:"time" json:"time"`
	Path     string  `mapstructure:"path" yaml:"path" json:"path"`
	Keep     int     `mapstructure:"keep" yaml:"keep" json:"keep"`
}

type Config struct {
	App      AppConfig      `mapstructure:"app" yaml:"app" json:"app"`
	Srv      ServerConfig   `mapstructure:"server" yaml:"server" json:"server"`
	Db       DatabaseConfig `mapstructure:"database" yaml:"database" json:"database"`
	Storage  StorageConfig  `mapstructure:"storage" yaml:"storage" json:"storage"`
	Consumer ConsumerConfig `yaml:"consumer" json:"consumer"`
	Enricher EnricherConfig `mapstructure:"enricher" yaml:"enricher" json:"enricher"`
	Backup   BackupConfig   `mapstructure:"backup" yaml:"backup" json:"backup"`
}

// engine identifiers grouped by tool category.
var (
	ContentAnalyzer = struct {
		OpenAI    string
		Anthropic string
		DeepSeek  string
		Ollama    string
	}{
		OpenAI:    "llmopenai",
		Anthropic: "llmanthropic",
		DeepSeek:  "llmdeepseek",
		Ollama:    "llmollama",
	}

	OCR = struct {
		Gosseract string
		OcrMyPdf  string
	}{
		Gosseract: "gosseract",
		OcrMyPdf:  "ocrmypdf",
	}

	PdfOptimizer = struct {
		MuPDF string
		GS    string
	}{
		MuPDF: "mupdf",
		GS:    "gs",
	}

	TextExtractor = struct {
		MuPDF     string
		GoPdf     string
		PdfToText string
	}{
		MuPDF:     "mupdf",
		GoPdf:     "gopdf",
		PdfToText: "pdftotext",
	}

	TextReducer = struct {
		TextRank string
	}{
		TextRank: "textrank",
	}
)

type EngineEntry struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

var AvailableEngines = map[string][]EngineEntry{
	"content_analyzer": {
		{Value: ContentAnalyzer.OpenAI, Label: "OpenAI"},
		{Value: ContentAnalyzer.Anthropic, Label: "Anthropic"},
		{Value: ContentAnalyzer.DeepSeek, Label: "DeepSeek"},
		{Value: ContentAnalyzer.Ollama, Label: "Ollama"},
	},
	"ocr": {
		{Value: OCR.Gosseract, Label: "gosseract"},
		{Value: OCR.OcrMyPdf, Label: "ocrmypdf"},
	},
	"pdf_optimizer": {
		{Value: PdfOptimizer.MuPDF, Label: "mupdf"},
		{Value: PdfOptimizer.GS, Label: "gs"},
	},
	"text_extractor": {
		{Value: TextExtractor.MuPDF, Label: "mupdf"},
		{Value: TextExtractor.GoPdf, Label: "gopdf"},
		{Value: TextExtractor.PdfToText, Label: "pdftotext"},
	},
	"text_reducer": {
		{Value: TextReducer.TextRank, Label: "textrank"},
	},
}

func DefaultConfig(configDir string) *Config {
	return &Config{
		App: AppConfig{
			Env:       Development,
			LogLevel:  "info",
			ConfigDir: configDir,
		},
		Srv: ServerConfig{
			Host:                 "0.0.0.0",
			Port:                 3000,
			ReadTimeout:          60 * time.Second,
			WriteTimeout:         60 * time.Second,
			IdleTimeout:          60 * time.Second,
			MaxUploadSize:        100,
			MaxConcurrentBatches: 2,
			MaxDownloadFiles:     50,
			MaxDownloadSizeMB:    500,
			MaxBatchDelete:       50,
			AuthEnabled:          true,
		},
		Db: DatabaseConfig{
			Type: "sqlite",
			Path: filepath.Join(configDir, "data"),
			Name: "edub.db",
		},
		Storage: StorageConfig{
			ConsumptionDir: filepath.Join(configDir, "inbox"),
			StorageDir:     filepath.Join(configDir, "storage"),
		},
		Consumer: ConsumerConfig{
			SupportedFiles:   []string{".pdf"},
			Workers:          1,
			MaxFilesPerBatch: 10,
			TextExtractor: TextExtractorConfig{
				Engine:  TextExtractor.MuPDF,
				Timeout: 120,
			},
			PdfOptimizer: PdfOptimizerConfig{
				Engine:  PdfOptimizer.MuPDF,
				Timeout: 120,
			},
			OCR: OCRConfig{
				Engine:    OCR.Gosseract,
				Languages: []string{"eng"},
				DataDir:   filepath.Join(configDir, "ocr/tessdata"),
				Timeout:   120,
			},
			Polling: PollingConfig{
				Interval: 5,
			},
			Reclaim: ReclaimConfig{
				Enabled: true,
			},
		},
		Backup: BackupConfig{
			Enabled:  false,
			Interval: 1,
			Time:     "02:00",
			Keep:     7,
		},
		Enricher: EnricherConfig{
			Workers: 1,
			TextReducer: TextReducerConfig{
				Engine:      TextReducer.TextRank,
				Timeout:     120,
				TargetWords: 2000,
			},
			ContentAnalyzer: ContentAnalyzerConfig{
				Engine:  ContentAnalyzer.OpenAI,
				Timeout: 120,
				Llm: LlmToolsConfig{
					OpenAI: LlmToolConfig{
						BaseURL: "https://api.openai.com/v1",
						Model:   "gpt-4o",
					},
					Anthropic: LlmToolConfig{
						BaseURL: "https://api.anthropic.com/v1",
						Model:   "claude-sonnet-4-5",
					},
					DeepSeek: LlmToolConfig{
						BaseURL: "https://api.deepseek.com",
						Model:   "deepseek-v4-flash",
					},
					Ollama: LlmToolConfig{
						BaseURL: "http://localhost:11434",
						Model:   "llama3.2",
					},
				},
			},
			TagMatcher: TagMatcherConfig{
				Timeout:           120,
				ReduceTargetWords: 4000,
				ChunkSize:         0,
				Hugot: HugotConfig{
					Model:       "BAAI/bge-m3",
					Backend:     "ort",
					CpuMemArena: false,
					MemPattern:  false,
				},
			},
		},
	}
}

func Load(configDir string) (*Config, error) {
	cfg := DefaultConfig(configDir)

	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
	}

	if err := finalizeConfig(cfg, configDir); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Reload re-reads config.yaml from disk and updates cfg in place.
// It returns (true, nil) if the file was read successfully, (false, nil)
// if the file does not exist, or (false, err) on I/O/parse errors.
func Reload(configDir string, cfg *Config) (bool, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	// Reset to defaults first so removed keys revert to their defaults.
	*cfg = *DefaultConfig(configDir)

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return false, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := finalizeConfig(cfg, configDir); err != nil {
		return false, err
	}

	return true, nil
}

func finalizeConfig(cfg *Config, configDir string) error {
	if len(cfg.Consumer.OCR.Languages) == 0 {
		return fmt.Errorf("consumer.ocr.languages is required — run 'kushim setup --languages eng,spa,...' first")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("expand home dir: %w", err)
	}

	cfg.App.ConfigDir = configDir

	modelParts := strings.Split(cfg.Enricher.TagMatcher.Hugot.Model, "/")
	modelShortName := modelParts[len(modelParts)-1]
	cfg.Enricher.TagMatcher.Hugot.ModelPath = filepath.Join(configDir, "tagmatcher", "hugot", "models", modelShortName)
	cfg.Enricher.TagMatcher.TopN = 15
	cfg.Enricher.TagMatcher.MinSimilarity = defaultMinSimilarity(modelShortName)
	cfg.Enricher.TagMatcher.ConsolidationSimilarity = defaultConsolidationSimilarity(modelShortName)
	cfg.Enricher.TagMatcher.Hugot.BackendLibPath = filepath.Join(configDir, "tagmatcher", "hugot", "libs")

	cfg.Db.Path = expandPath(cfg.Db.Path, homeDir)
	cfg.Storage.ConsumptionDir = expandPath(cfg.Storage.ConsumptionDir, homeDir)
	cfg.Storage.StorageDir = expandPath(cfg.Storage.StorageDir, homeDir)
	cfg.Consumer.OCR.DataDir = expandPath(cfg.Consumer.OCR.DataDir, homeDir)

	if err := requireAbsPath(cfg.Db.Path, "database.path"); err != nil {
		return err
	}
	if err := requireAbsPath(cfg.Storage.ConsumptionDir, "storage.consumption_dir"); err != nil {
		return err
	}
	if err := requireAbsPath(cfg.Storage.StorageDir, "storage.storage_dir"); err != nil {
		return err
	}
	if err := requireAbsPath(cfg.Consumer.OCR.DataDir, "consumer.ocr.data_dir"); err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.Storage.ConsumptionDir, 0755); err != nil {
		return fmt.Errorf("failed to create consumption directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.StorageDir, 0755); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}

	if cfg.Backup.Enabled {
		if cfg.Backup.Interval <= 0 {
			return fmt.Errorf("backup.interval must be > 0")
		}
		if _, err := parseHHMM(cfg.Backup.Time, false); err != nil {
			return fmt.Errorf("backup.time: %w", err)
		}
		if cfg.Backup.Path == "" {
			cfg.Backup.Path = filepath.Join(configDir, "backups")
		}
		cfg.Backup.Path = expandPath(cfg.Backup.Path, homeDir)
		if err := os.MkdirAll(cfg.Backup.Path, 0755); err != nil {
			return fmt.Errorf("failed to create backup directory: %w", err)
		}
	}

	return nil
}

func expandPath(path, homeDir string) string {
	if len(path) > 0 && path[0] == '~' {
		return homeDir + path[1:]
	}
	return path
}

func requireAbsPath(path, name string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("%s must be an absolute path or start with '~', got: %s", name, path)
	}
	return nil
}

var (
	reHHMM    = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	reEndHHMM = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$|^24:00$`)
)

func parseHHMM(s string, allow2400 bool) (int, error) {
	if s == "24:00" && allow2400 {
		return 1440, nil
	}
	r := reHHMM
	if allow2400 {
		r = reEndHHMM
	}
	if !r.MatchString(s) {
		return 0, fmt.Errorf("invalid time %q — expected HH:MM (00:00–23:59)", s)
	}
	hour, _ := strconv.Atoi(s[:2])
	min, _ := strconv.Atoi(s[3:])
	return hour*60 + min, nil
}

func ValidatePollingWindows(body map[string]any) error {
	raw, ok := body["consumer.polling.windows"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("consumer.polling.windows must be an array")
	}
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("consumer.polling.windows[%d]: expected object with start/end", i)
		}
		start, _ := entry["start"].(string)
		end, _ := entry["end"].(string)
		if start == "" || end == "" {
			return fmt.Errorf("consumer.polling.windows[%d]: start and end are required", i)
		}
		startMins, err := parseHHMM(start, false)
		if err != nil {
			return fmt.Errorf("consumer.polling.windows[%d].start: %w", i, err)
		}
		endMins, err := parseHHMM(end, true)
		if err != nil {
			return fmt.Errorf("consumer.polling.windows[%d].end: %w", i, err)
		}
		if end == "00:00" {
			return fmt.Errorf("consumer.polling.windows[%d].end: 00:00 is not a valid end time (use 24:00 for end-of-day)", i)
		}
		if startMins >= endMins {
			return fmt.Errorf("consumer.polling.windows[%d]: start (%s) must be before end (%s)", i, start, end)
		}
	}
	return nil
}

func IsWithinActiveWindows(windows []PollingWindow) bool {
	if len(windows) == 0 {
		return true
	}
	now := time.Now()
	currentMins := now.Hour()*60 + now.Minute()
	for _, w := range windows {
		startMins, _ := parseHHMM(w.Start, false)
		endMins, _ := parseHHMM(w.End, true)
		if currentMins >= startMins && currentMins < endMins {
			return true
		}
	}
	return false
}

// defaultMinSimilarity returns a sensible default min_similarity threshold for
// the given model name. Larger models with higher-dimensional embeddings tend
// to produce tighter clusters, requiring a higher threshold to avoid false
// positives when comparing a full document against short tag names.
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

// defaultConsolidationSimilarity returns the threshold for tag-name-to-tag-name
// consolidation (post-LLM MatchEach). This must be higher than the document-to-tag
// threshold because single tag names have much sparser semantic signal than full
// document text. The values are tuned per model dimension: higher-dim models
// produce tighter clusters and need a higher threshold.
func defaultConsolidationSimilarity(modelShortName string) float64 {
	switch modelShortName {
	case "bge-m3":
		return 0.82 // 1024-dim, tight clusters
	case "all-mpnet-base-v2":
		return 0.75 // 768-dim
	case "all-MiniLM-L6-v2":
		return 0.70 // 384-dim, compressed distribution
	default:
		return 0.75
	}
}
