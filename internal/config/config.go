package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Environment string

const (
	Development Environment = "development"
	Production  Environment = "production"
)

type AppConfig struct {
	Env       Environment `mapstructure:"environment"`
	LogLevel  string      `mapstructure:"log_level"`
	LogFile   string      `mapstructure:"log_file"`
	ConfigDir string
}

type ServerConfig struct {
	Host         string        `mapstructure:"host"`
	Port         int           `mapstructure:"port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`
}

type DatabaseConfig struct {
	Type    string `mapstructure:"type"`
	Path    string `mapstructure:"path"`
	Name    string
	Seeders []string
}

type StorageConfig struct {
	ConsumptionDir string `mapstructure:"consumption_dir"`
	StorageDir     string `mapstructure:"storage_dir"`
}

type TextExtractorConfig struct {
	Engine  string `mapstructure:"engine"`
	Timeout int    `mapstructure:"timeout"`
}

type PdfOptimizerConfig struct {
	Engine   string `mapstructure:"engine"`
	Fallback string `mapstructure:"fallback"`
	Timeout  int    `mapstructure:"timeout"`
}

type OCRConfig struct {
	Engine    string   `mapstructure:"engine"`
	Languages []string `mapstructure:"languages"`
	DataDir   string   `mapstructure:"data_dir"`
	Timeout   int      `mapstructure:"timeout"`
}

type ConsumerConfig struct {
	SupportedFiles []string
	DeleteOriginal bool                `mapstructure:"delete_original"`
	Workers        int                 `mapstructure:"workers"`
	TextExtractor  TextExtractorConfig `mapstructure:"textextractor"`
	PdfOptimizer   PdfOptimizerConfig  `mapstructure:"pdfoptimizer"`
	OCR            OCRConfig           `mapstructure:"ocr"`
}

type TextReducerConfig struct {
	Engine      string `mapstructure:"engine"`
	Timeout     int    `mapstructure:"timeout"`
	TargetWords int    `mapstructure:"target_words"`
}

type ContentAnalyzerConfig struct {
	Engine  string         `mapstructure:"engine"`
	Timeout int            `mapstructure:"timeout"`
	Llm     LlmToolsConfig `mapstructure:"llm"`
}

type EnricherConfig struct {
	Workers         int                   `mapstructure:"workers"`
	TextReducer     TextReducerConfig     `mapstructure:"textreducer"`
	ContentAnalyzer ContentAnalyzerConfig `mapstructure:"contentanalyzer"`
	TagMatcher      TagMatcherConfig      `mapstructure:"tagmatcher"`
}

type LlmToolConfig struct {
	BaseURL string `mapstructure:"base_url"`
	Model   string `mapstructure:"model"`
	Token   string `mapstructure:"token"`
}

type LlmToolsConfig struct {
	OpenAI    LlmToolConfig `mapstructure:"openai"`
	Anthropic LlmToolConfig `mapstructure:"anthropic"`
	DeepSeek  LlmToolConfig `mapstructure:"deepseek"`
	Ollama    LlmToolConfig `mapstructure:"ollama"`
}

type HugotConfig struct {
	Model          string `mapstructure:"model"`
	Backend        string `mapstructure:"backend"`
	ModelPath      string
	BackendLibPath string
}

type TagMatcherConfig struct {
	Engine            string      `mapstructure:"engine"`
	Timeout           int         `mapstructure:"timeout"`
	ReduceTargetWords int         `mapstructure:"reduce_target_words"`
	ChunkSize         int         `mapstructure:"chunk_size"`
	Hugot             HugotConfig `mapstructure:"hugot"`
	TopN              int
	MinSimilarity     float64
}

type ToolConfig struct {
	Command string
	Timeout time.Duration
}

type Config struct {
	App      AppConfig      `mapstructure:"app"`
	Srv      ServerConfig   `mapstructure:"server"`
	Db       DatabaseConfig `mapstructure:"database"`
	Storage  StorageConfig  `mapstructure:"storage"`
	Consumer ConsumerConfig
	Enricher EnricherConfig `mapstructure:"enricher"`
}

func Load(configDir string) (*Config, error) {
	viper.SetDefault("app.environment", "development")
	viper.SetDefault("app.log_level", "info")
	viper.SetDefault("app.log_file", filepath.Join(configDir, "kushim.log"))

	viper.SetDefault("server.host", "localhost")
	viper.SetDefault("server.port", 3000)
	viper.SetDefault("server.read_timeout", 60*time.Second)
	viper.SetDefault("server.write_timeout", 60*time.Second)
	viper.SetDefault("server.idle_timeout", 60*time.Second)

	viper.SetDefault("database.type", "sqlite")
	viper.SetDefault("database.path", filepath.Join(configDir, "data"))

	viper.SetDefault("storage.consumption_dir", filepath.Join(configDir, "inbox"))
	viper.SetDefault("storage.storage_dir", filepath.Join(configDir, "storage"))

	viper.SetDefault("consumer.supported_files", []string{".pdf"})
	viper.SetDefault("consumer.delete_original", false)
	viper.SetDefault("consumer.workers", 1)
	viper.SetDefault("consumer.textextractor.engine", "mupdf")
	viper.SetDefault("consumer.textextractor.timeout", 120)
	viper.SetDefault("consumer.pdfoptimizer.engine", "mupdf")
	viper.SetDefault("consumer.pdfoptimizer.fallback", "")
	viper.SetDefault("consumer.pdfoptimizer.timeout", 120)
	viper.SetDefault("consumer.ocr.engine", "gosseract")
	viper.SetDefault("consumer.ocr.data_dir", filepath.Join(configDir, "ocr/tessdata"))
	viper.SetDefault("consumer.ocr.timeout", 120)

	viper.SetDefault("enricher.workers", 1)
	viper.SetDefault("enricher.textreducer.engine", "textrank")
	viper.SetDefault("enricher.textreducer.timeout", 120)
	viper.SetDefault("enricher.textreducer.target_words", 2000)
	viper.SetDefault("enricher.contentanalyzer.engine", "llmopenai")
	viper.SetDefault("enricher.contentanalyzer.timeout", 120)

	viper.SetDefault("enricher.tagmatcher.engine", "hugot")
	viper.SetDefault("enricher.tagmatcher.timeout", 120)
	viper.SetDefault("enricher.tagmatcher.reduce_target_words", 4000)
	viper.SetDefault("enricher.tagmatcher.chunk_size", 0)
	viper.SetDefault("enricher.tagmatcher.hugot.model", "BAAI/bge-m3")
	viper.SetDefault("enricher.tagmatcher.hugot.backend", "GO")

	viper.SetDefault("enricher.contentanalyzer.llm.openai.base_url", "https://api.openai.com/v1")
	viper.SetDefault("enricher.contentanalyzer.llm.openai.model", "gpt-4o")
	viper.SetDefault("enricher.contentanalyzer.llm.anthropic.base_url", "https://api.anthropic.com/v1")
	viper.SetDefault("enricher.contentanalyzer.llm.anthropic.model", "claude-sonnet-4-5")
	viper.SetDefault("enricher.contentanalyzer.llm.deepseek.base_url", "https://api.deepseek.com")
	viper.SetDefault("enricher.contentanalyzer.llm.deepseek.model", "deepseek-v4-flash")
	viper.SetDefault("enricher.contentanalyzer.llm.ollama.base_url", "http://localhost:11434")
	viper.SetDefault("enricher.contentanalyzer.llm.ollama.model", "llama3.2")

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(configDir)

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
		fmt.Println("Config file not found, using defaults")
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	cfg.Db.Name = "edub.db"
	cfg.Db.Seeders = []string{"tags"}
	cfg.Consumer.SupportedFiles = []string{".pdf"}

	if len(cfg.Consumer.OCR.Languages) == 0 {
		return nil, fmt.Errorf("consumer.ocr.languages is required — run 'kushim setup --langs eng,spa,...' first")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("expand home dir: %w", err)
	}

	cfg.App.ConfigDir = configDir

	modelParts := strings.Split(cfg.Enricher.TagMatcher.Hugot.Model, "/")
	modelShortName := modelParts[len(modelParts)-1]
	cfg.Enricher.TagMatcher.Hugot.ModelPath = filepath.Join(configDir, "tagmatcher", "hugot", "models", modelShortName)
	cfg.Enricher.TagMatcher.TopN = 15
	cfg.Enricher.TagMatcher.MinSimilarity = defaultMinSimilarity(modelShortName)
	cfg.Enricher.TagMatcher.Hugot.BackendLibPath = filepath.Join(configDir, "tagmatcher", "hugot", "libs")

	cfg.Db.Path = expandPath(cfg.Db.Path, homeDir)
	cfg.Storage.ConsumptionDir = expandPath(cfg.Storage.ConsumptionDir, homeDir)
	cfg.Storage.StorageDir = expandPath(cfg.Storage.StorageDir, homeDir)
	cfg.Consumer.OCR.DataDir = expandPath(cfg.Consumer.OCR.DataDir, homeDir)

	if err := requireAbsPath(cfg.Db.Path, "database.path"); err != nil {
		return nil, err
	}
	if err := requireAbsPath(cfg.Storage.ConsumptionDir, "storage.consumption_dir"); err != nil {
		return nil, err
	}
	if err := requireAbsPath(cfg.Storage.StorageDir, "storage.storage_dir"); err != nil {
		return nil, err
	}
	if err := requireAbsPath(cfg.Consumer.OCR.DataDir, "consumer.ocr.data_dir"); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(cfg.Storage.ConsumptionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create consumption directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.StorageDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	return &cfg, nil
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
