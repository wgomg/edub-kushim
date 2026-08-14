package config

import (
	"fmt"
	"net/url"
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

type LoggingConfig struct {
	MaxSize    int  `mapstructure:"max_size" yaml:"max_size" json:"max_size"`
	MaxBackups int  `mapstructure:"max_backups" yaml:"max_backups" json:"max_backups"`
	MaxAge     int  `mapstructure:"max_age" yaml:"max_age" json:"max_age"`
	Compress   bool `mapstructure:"compress" yaml:"compress" json:"compress"`
}

type AppConfig struct {
	Env       Environment   `mapstructure:"environment" yaml:"environment" json:"environment"`
	LogLevel  string        `mapstructure:"log_level" yaml:"log_level" json:"log_level"`
	Logging   LoggingConfig `mapstructure:"logging" yaml:"logging" json:"logging"`
	ConfigDir string        `yaml:"-" json:"config_dir"`
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
	Type     string   `mapstructure:"type" yaml:"type" json:"type"`
	Host     string   `yaml:"host,omitempty" json:"host"`
	Port     int      `yaml:"port,omitempty" json:"port"`
	User     string   `yaml:"user,omitempty" json:"user"`
	Password string   `yaml:"password,omitempty" json:"password"`
	Database string   `yaml:"database,omitempty" json:"database"`
	SSLMode  string   `yaml:"sslmode,omitempty" json:"sslmode"`
	DSN      string   `yaml:"dsn,omitempty" json:"dsn"`
	Seeders  []string `yaml:"seeders,omitempty" json:"seeders"`
}

func DatabaseConnectionChanged(old, new DatabaseConfig) bool {
	return effectiveConnection(old) != effectiveConnection(new)
}

func effectiveConnection(cfg DatabaseConfig) string {
	host, port, user, password, database, sslmode := cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode
	if cfg.DSN != "" {
		u, err := url.Parse(cfg.DSN)
		if err != nil {
			return "dsn:" + cfg.DSN
		}
		host = u.Hostname()
		if p := u.Port(); p != "" {
			if n, err := strconv.Atoi(p); err == nil {
				port = n
			}
		}
		if u.User != nil {
			user = u.User.Username()
			if pw, ok := u.User.Password(); ok {
				password = pw
			}
		}
		database = strings.TrimPrefix(u.Path, "/")
		sslmode = u.Query().Get("sslmode")
	}
	return fmt.Sprintf("%s:%d:%s:%s:%s:%s", host, port, user, password, database, sslmode)
}

func BuildPostgresDSN(cfg DatabaseConfig) string {
	if cfg.DSN != "" {
		return cfg.DSN
	}
	u := &url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:   cfg.Database,
	}
	if cfg.User != "" {
		if cfg.Password != "" {
			u.User = url.UserPassword(cfg.User, cfg.Password)
		} else {
			u.User = url.User(cfg.User)
		}
	}
	q := u.Query()
	if cfg.SSLMode != "" {
		q.Set("sslmode", cfg.SSLMode)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type TrashConfig struct {
	RetentionDays int `mapstructure:"retention_days" yaml:"retention_days" json:"retention_days"`
}

type StorageConfig struct {
	ConsumptionDir string      `mapstructure:"consumption_dir" yaml:"consumption_dir" json:"consumption_dir"`
	StorageDir     string      `mapstructure:"storage_dir" yaml:"storage_dir" json:"storage_dir"`
	MigrationMode  string      `mapstructure:"migration_mode" yaml:"migration_mode" json:"migration_mode"`
	Trash          TrashConfig `mapstructure:"trash" yaml:"trash" json:"trash"`
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
	Engine     string   `mapstructure:"engine" yaml:"engine" json:"engine"`
	Languages  []string `mapstructure:"languages" yaml:"languages" json:"languages"`
	DataDir    string   `mapstructure:"data_dir" yaml:"data_dir" json:"data_dir"`
	Timeout    int      `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	OcrWorkers int      `mapstructure:"ocr_workers" yaml:"ocr_workers" json:"ocr_workers"`
}

type ThumbnailConfig struct {
	Enabled  bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Engine   string `mapstructure:"engine" yaml:"engine" json:"engine"`
	DPI      int    `mapstructure:"dpi" yaml:"dpi" json:"dpi"`
	MaxWidth int    `mapstructure:"max_width" yaml:"max_width" json:"max_width"`
	Quality  int    `mapstructure:"quality" yaml:"quality" json:"quality"`
	Timeout  int    `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	Workers  int    `mapstructure:"workers" yaml:"workers" json:"workers"`
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
	Enabled        bool  `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	MaxRetries     int   `mapstructure:"max_retries" yaml:"max_retries" json:"max_retries"`
	StaleTaskAfter int64 `mapstructure:"stale_task_after" yaml:"stale_task_after" json:"stale_task_after"`
}

type DocxOdtConverterConfig struct {
	Enabled bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Binary  string `mapstructure:"binary" yaml:"binary" json:"binary"`
	Timeout int    `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
}

type ConsumerConfig struct {
	SupportedFiles   []string               `mapstructure:"supported_files" yaml:"supported_files" json:"supported_files"`
	Workers          int                    `mapstructure:"workers" yaml:"workers" json:"workers"`
	MaxFilesPerBatch int                    `mapstructure:"max_files_per_batch" yaml:"max_files_per_batch" json:"max_files_per_batch"`
	Converter        DocxOdtConverterConfig `mapstructure:"converter" yaml:"converter" json:"converter"`
	TextExtractor    TextExtractorConfig    `mapstructure:"textextractor" yaml:"textextractor" json:"textextractor"`
	PdfOptimizer     PdfOptimizerConfig     `mapstructure:"pdfoptimizer" yaml:"pdfoptimizer" json:"pdfoptimizer"`
	OCR              OCRConfig              `mapstructure:"ocr" yaml:"ocr" json:"ocr"`
	Thumbnail        ThumbnailConfig        `mapstructure:"thumbnail" yaml:"thumbnail" json:"thumbnail"`
	Polling          PollingConfig          `mapstructure:"polling" yaml:"polling" json:"polling"`
	Reclaim          ReclaimConfig          `mapstructure:"reclaim" yaml:"reclaim" json:"reclaim"`
}

type TextReducerConfig struct {
	Engine      string `mapstructure:"engine" yaml:"engine" json:"engine"`
	Timeout     int    `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	TargetWords int    `mapstructure:"target_words" yaml:"target_words" json:"target_words"`
}

type DocTypeRefinementConfig struct {
	Enabled   bool `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	HeadWords int  `mapstructure:"head_words" yaml:"head_words" json:"head_words"`
	TailWords int  `mapstructure:"tail_words" yaml:"tail_words" json:"tail_words"`
}

type ContentAnalyzerConfig struct {
	Enabled            bool                    `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Timeout            int                     `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	Llm                LlmConfig               `mapstructure:"llm" yaml:"llm" json:"llm"`
	Fallback           *FallbackConfig         `mapstructure:"fallback" yaml:"fallback" json:"fallback,omitempty"`
	PromptTemplate     string                  `mapstructure:"prompt_template" yaml:"prompt_template" json:"prompt_template,omitempty"`
	DocTypeRefinement  DocTypeRefinementConfig `mapstructure:"doc_type_refinement" yaml:"doc_type_refinement" json:"doc_type_refinement"`
	PauseOnCreditError bool                    `mapstructure:"pause_on_credit_error" yaml:"pause_on_credit_error" json:"pause_on_credit_error"`
}

type FallbackConfig struct {
	Enabled bool      `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Llm     LlmConfig `mapstructure:"llm" yaml:"llm" json:"llm"`
}

type EnricherConfig struct {
	Workers         int                   `mapstructure:"workers" yaml:"workers" json:"workers"`
	TextReducer     TextReducerConfig     `mapstructure:"textreducer" yaml:"textreducer" json:"textreducer"`
	ContentAnalyzer ContentAnalyzerConfig `mapstructure:"contentanalyzer" yaml:"contentanalyzer" json:"contentanalyzer"`
	TagMatcher      TagMatcherConfig      `mapstructure:"tagmatcher" yaml:"tagmatcher" json:"tagmatcher"`
}

type LlmConfig struct {
	Adapter         string  `mapstructure:"adapter" yaml:"adapter" json:"adapter"`
	Provider        string  `mapstructure:"provider" yaml:"provider" json:"provider"`
	Model           string  `mapstructure:"model" yaml:"model" json:"model"`
	Token           string  `mapstructure:"token" yaml:"token" json:"token,omitempty"`
	Reasoning       bool    `yaml:"-"`
	ReasoningEffort string  `yaml:"-"`
	Temperature     float64 `mapstructure:"temperature" yaml:"temperature" json:"temperature"`
	RequestDelay    float64 `mapstructure:"request_delay" yaml:"request_delay" json:"request_delay"`
}

// maxRequestDelaySeconds caps llm.request_delay so a misconfiguration can't
// stall an enricher worker well beyond the content-analyzer timeout budget.
const maxRequestDelaySeconds = 60

type HugotConfig struct {
	Model          string `mapstructure:"model" yaml:"model" json:"model"`
	Backend        string `mapstructure:"backend" yaml:"backend" json:"backend"`
	ModelPath      string `yaml:"-" json:"model_path"`
	BackendLibPath string `yaml:"-" json:"backend_lib_path"`
	CpuMemArena    bool   `yaml:"-"`
	MemPattern     bool   `yaml:"-"`
}

type TagMatcherConfig struct {
	Timeout                 int         `mapstructure:"timeout" yaml:"timeout" json:"timeout"`
	ReduceTargetWords       int         `mapstructure:"reduce_target_words" yaml:"reduce_target_words" json:"reduce_target_words"`
	ChunkSize               int         `mapstructure:"chunk_size" yaml:"chunk_size" json:"chunk_size"`
	Hugot                   HugotConfig `mapstructure:"hugot" yaml:"hugot" json:"hugot"`
	TopN                    int         `yaml:"-" json:"top_n"`
	MinSimilarity           float64     `yaml:"-" json:"min_similarity"`
	ConsolidationSimilarity float64     `yaml:"-" json:"consolidation_similarity"`
}

type ToolConfig struct {
	Command string        `yaml:"command" json:"command"`
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
}

type BackupSchedule struct {
	Mode     string  `mapstructure:"mode" yaml:"mode" json:"mode"`
	Interval float64 `mapstructure:"interval" yaml:"interval" json:"interval"`
	Time     string  `mapstructure:"time" yaml:"time" json:"time"`
	Path     string  `mapstructure:"path" yaml:"path" json:"path"`
	Keep     int     `mapstructure:"keep" yaml:"keep" json:"keep"`
}

var ValidBackupModes = map[string]struct{}{
	"full":      {},
	"database":  {},
	"documents": {},
}

type BackupConfig struct {
	Enabled   bool             `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Path      string           `mapstructure:"path" yaml:"path" json:"path"`
	Schedules []BackupSchedule `mapstructure:"schedules" yaml:"schedules" json:"schedules"`

	// Deprecated flat fields, kept so finalizeConfig can auto-convert
	// legacy configs into a single full schedule.
	Interval float64 `mapstructure:"interval" yaml:"interval" json:"interval"`
	Time     string  `mapstructure:"time" yaml:"time" json:"time"`
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
		Docx      string
		Odt       string
	}{
		MuPDF:     "mupdf",
		GoPdf:     "gopdf",
		PdfToText: "pdftotext",
		Docx:      "docx",
		Odt:       "odt",
	}

	TextReducer = struct {
		TextRank string
	}{
		TextRank: "textrank",
	}

	Converter = struct {
		LibreOffice string
	}{
		LibreOffice: "libreoffice",
	}

	Thumbnail = struct {
		MuPDF       string
		Imagemagick string
	}{
		MuPDF:       "mupdf",
		Imagemagick: "imagemagick",
	}
)

type EngineEntry struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

var AvailableEngines = map[string][]EngineEntry{
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
	"thumbnail": {
		{Value: Thumbnail.MuPDF, Label: "mupdf"},
	},
}

func DefaultConfig(configDir string) *Config {
	return &Config{
		App: AppConfig{
			Env:      Development,
			LogLevel: "info",
			Logging: LoggingConfig{
				MaxSize:    100,
				MaxBackups: 7,
				MaxAge:     30,
				Compress:   true,
			},
			ConfigDir: configDir,
		},
		Srv: ServerConfig{
			Host:                 "0.0.0.0",
			Port:                 3000,
			ReadTimeout:          60 * time.Second,
			WriteTimeout:         60 * time.Second,
			IdleTimeout:          60 * time.Second,
			MaxUploadSize:        100,
			MaxConcurrentBatches: 4,
			MaxDownloadFiles:     50,
			MaxDownloadSizeMB:    500,
			MaxBatchDelete:       50,
			AuthEnabled:          true,
		},
		Db: DatabaseConfig{
			Type:     "postgres",
			Host:     "localhost",
			Port:     5432,
			User:     "edub",
			Password: "edub",
			Database: "edub",
			SSLMode:  "disable",
		},
		Storage: StorageConfig{
			ConsumptionDir: filepath.Join(configDir, "inbox"),
			StorageDir:     filepath.Join(configDir, "storage"),
			MigrationMode:  "copy",
			Trash: TrashConfig{
				RetentionDays: 30,
			},
		},
		Consumer: ConsumerConfig{
			SupportedFiles:   []string{".pdf"},
			Workers:          1,
			MaxFilesPerBatch: 10,
			Converter: DocxOdtConverterConfig{
				Enabled: false,
				Binary:  "libreoffice",
				Timeout: 300,
			},
			TextExtractor: TextExtractorConfig{
				Engine:  TextExtractor.MuPDF,
				Timeout: 120,
			},
			PdfOptimizer: PdfOptimizerConfig{
				Engine:  PdfOptimizer.MuPDF,
				Timeout: 0,
			},
			OCR: OCRConfig{
				Engine:     OCR.Gosseract,
				Languages:  []string{"eng"},
				DataDir:    filepath.Join(configDir, "ocr/tessdata"),
				Timeout:    120,
				OcrWorkers: 0,
			},
			Thumbnail: ThumbnailConfig{
				Enabled:  true,
				Engine:   Thumbnail.MuPDF,
				DPI:      72,
				MaxWidth: 400,
				Quality:  80,
				Timeout:  30,
				Workers:  1,
			},
			Polling: PollingConfig{
				Interval: 5,
			},
			Reclaim: ReclaimConfig{
				Enabled:        true,
				MaxRetries:     3,
				StaleTaskAfter: 600,
			},
		},
		Backup: BackupConfig{
			Enabled: false,
		},
		Enricher: EnricherConfig{
			Workers: 1,
			TextReducer: TextReducerConfig{
				Engine:      TextReducer.TextRank,
				Timeout:     120,
				TargetWords: 2000,
			},
			ContentAnalyzer: ContentAnalyzerConfig{
				Enabled:            false,
				Timeout:            120,
				PromptTemplate:     "",
				PauseOnCreditError: true,
				Llm: LlmConfig{
					RequestDelay: 1,
				},
				DocTypeRefinement: DocTypeRefinementConfig{
					Enabled:   true,
					HeadWords: 600,
					TailWords: 400,
				},
			},
			TagMatcher: TagMatcherConfig{
				Timeout:           120,
				ReduceTargetWords: 4000,
				// 4096 tokens keeps per-request inference memory bounded
				// (~2.2–2.5 GB idle, ~4–6 GB peak with BGE-M3). The model's
				// full context (0 = max_position_embeddings, 8180 tokens for
				// BGE-M3) scales attention memory roughly quadratically and
				// can OOM small hosts. See docs/tag-matcher.md.
				ChunkSize: 4096,
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

func finalizeConfig(cfg *Config, configDir string) error {
	if cfg.Enricher.ContentAnalyzer.Enabled {
		llmCfg := &cfg.Enricher.ContentAnalyzer.Llm
		if llmCfg.Adapter == "" {
			return fmt.Errorf("enricher.contentanalyzer.llm.adapter is required when content analyzer is enabled")
		}
		if llmCfg.Provider == "" {
			return fmt.Errorf("enricher.contentanalyzer.llm.provider is required when content analyzer is enabled")
		}
		if llmCfg.Model == "" {
			return fmt.Errorf("enricher.contentanalyzer.llm.model is required when content analyzer is enabled")
		}
	}
	if len(cfg.Consumer.OCR.Languages) == 0 {
		return fmt.Errorf("consumer.ocr.languages is required — run 'kushim setup --languages eng,spa,...' first")
	}

	if cfg.Consumer.Reclaim.Enabled {
		if cfg.Consumer.Reclaim.MaxRetries <= 0 {
			return fmt.Errorf("consumer.reclaim.max_retries must be > 0 when reclaim is enabled")
		}
		if cfg.Consumer.Reclaim.StaleTaskAfter < 60 {
			return fmt.Errorf("consumer.reclaim.stale_task_after must be >= 60 when reclaim is enabled")
		}
	}

	if cfg.Srv.MaxBatchDelete < 1 {
		return fmt.Errorf("server.max_batch_delete must be >= 1")
	}

	if cfg.Storage.Trash.RetentionDays < 1 {
		return fmt.Errorf("storage.trash.retention_days must be >= 1")
	}

	if cfg.Consumer.TextExtractor.Timeout < 0 {
		return fmt.Errorf("consumer.textextractor.timeout must be >= 0")
	}
	if cfg.Consumer.OCR.Timeout < 0 {
		return fmt.Errorf("consumer.ocr.timeout must be >= 0")
	}
	if cfg.Consumer.Thumbnail.Timeout < 0 {
		return fmt.Errorf("consumer.thumbnail.timeout must be >= 0")
	}
	if cfg.Consumer.Thumbnail.MaxWidth < 1 {
		return fmt.Errorf("consumer.thumbnail.max_width must be >= 1")
	}
	if cfg.Consumer.Thumbnail.DPI < 1 {
		return fmt.Errorf("consumer.thumbnail.dpi must be >= 1")
	}
	if cfg.Consumer.Thumbnail.Quality < 1 || cfg.Consumer.Thumbnail.Quality > 100 {
		return fmt.Errorf("consumer.thumbnail.quality must be between 1 and 100")
	}
	if cfg.Enricher.TextReducer.Timeout < 0 {
		return fmt.Errorf("enricher.textreducer.timeout must be >= 0")
	}
	if cfg.Enricher.TagMatcher.Timeout < 0 {
		return fmt.Errorf("enricher.tagmatcher.timeout must be >= 0")
	}

	if cfg.Enricher.ContentAnalyzer.DocTypeRefinement.HeadWords < 0 {
		return fmt.Errorf("enricher.contentanalyzer.doc_type_refinement.head_words must be >= 0")
	}
	if cfg.Enricher.ContentAnalyzer.DocTypeRefinement.TailWords < 0 {
		return fmt.Errorf("enricher.contentanalyzer.doc_type_refinement.tail_words must be >= 0")
	}
	if cfg.Enricher.ContentAnalyzer.Llm.RequestDelay < 0 || cfg.Enricher.ContentAnalyzer.Llm.RequestDelay > maxRequestDelaySeconds {
		return fmt.Errorf("enricher.contentanalyzer.llm.request_delay must be between 0 and %d", maxRequestDelaySeconds)
	}

	if fb := cfg.Enricher.ContentAnalyzer.Fallback; fb != nil && fb.Enabled {
		llmCfg := &fb.Llm
		if llmCfg.Adapter == "" {
			return fmt.Errorf("enricher.contentanalyzer.fallback.llm.adapter is required when the fallback is enabled")
		}
		if llmCfg.Provider == "" {
			return fmt.Errorf("enricher.contentanalyzer.fallback.llm.provider is required when the fallback is enabled")
		}
		if llmCfg.Model == "" {
			return fmt.Errorf("enricher.contentanalyzer.fallback.llm.model is required when the fallback is enabled")
		}
		if llmCfg.RequestDelay < 0 || llmCfg.RequestDelay > maxRequestDelaySeconds {
			return fmt.Errorf("enricher.contentanalyzer.fallback.llm.request_delay must be between 0 and %d", maxRequestDelaySeconds)
		}
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

	cfg.Storage.ConsumptionDir = expandPath(cfg.Storage.ConsumptionDir, homeDir)
	cfg.Storage.StorageDir = expandPath(cfg.Storage.StorageDir, homeDir)
	cfg.Consumer.OCR.DataDir = expandPath(cfg.Consumer.OCR.DataDir, homeDir)

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
		switch {
		case len(cfg.Backup.Schedules) > 0 && (cfg.Backup.Interval > 0 || cfg.Backup.Time != "" || cfg.Backup.Keep > 0):
			return fmt.Errorf("remove deprecated flat backup fields (interval, time, keep) when using backup.schedules")
		case len(cfg.Backup.Schedules) > 0:
		case cfg.Backup.Interval > 0:
			cfg.Backup.Schedules = []BackupSchedule{{
				Mode:     "full",
				Interval: cfg.Backup.Interval,
				Time:     cfg.Backup.Time,
				Path:     cfg.Backup.Path,
				Keep:     cfg.Backup.Keep,
			}}
		default:
			return fmt.Errorf("at least one backup schedule is required when backup.enabled is true")
		}
		if cfg.Backup.Path == "" {
			cfg.Backup.Path = filepath.Join(configDir, "backups")
		}
		cfg.Backup.Path = expandPath(cfg.Backup.Path, homeDir)
		if err := os.MkdirAll(cfg.Backup.Path, 0755); err != nil {
			return fmt.Errorf("failed to create backup directory: %w", err)
		}
		seen := make(map[string]bool, len(cfg.Backup.Schedules))
		for i := range cfg.Backup.Schedules {
			s := &cfg.Backup.Schedules[i]
			if _, ok := ValidBackupModes[s.Mode]; !ok {
				return fmt.Errorf("backup.schedules[%d].mode must be one of full, database, documents, got %q", i, s.Mode)
			}
			if seen[s.Mode] {
				return fmt.Errorf("backup.schedules: duplicate mode %q — each mode can appear at most once", s.Mode)
			}
			seen[s.Mode] = true
			if s.Interval <= 0 {
				return fmt.Errorf("backup.schedules[%d].interval must be > 0", i)
			}
			if s.Time == "" {
				s.Time = "02:00"
			}
			if _, err := parseHHMM(s.Time, false); err != nil {
				return fmt.Errorf("backup.schedules[%d].time: %w", i, err)
			}
			if s.Path == "" {
				s.Path = cfg.Backup.Path
			}
			s.Path = expandPath(s.Path, homeDir)
			if err := os.MkdirAll(s.Path, 0755); err != nil {
				return fmt.Errorf("failed to create backup directory for schedule %q: %w", s.Mode, err)
			}
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
		return 0.80 // 1024-dim; reduced from 0.82 due to SentencePiece tokenization variance
	case "all-mpnet-base-v2":
		return 0.75 // 768-dim
	case "all-MiniLM-L6-v2":
		return 0.70 // 384-dim, compressed distribution
	default:
		return 0.75
	}
}
