package config

import (
	"fmt"
	"os"
	"path/filepath"
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
	Env       Environment `mapstructure:"environment" yaml:"environment"`
	LogLevel  string      `mapstructure:"log_level" yaml:"log_level"`
	LogFile   string      `mapstructure:"log_file" yaml:"log_file"`
	ConfigDir string
}

type ServerConfig struct {
	Host         string        `mapstructure:"host" yaml:"host"`
	Port         int           `mapstructure:"port" yaml:"port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout" yaml:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout" yaml:"write_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout" yaml:"idle_timeout"`
}

type DatabaseConfig struct {
	Type    string   `mapstructure:"type" yaml:"type"`
	Path    string   `mapstructure:"path" yaml:"path"`
	Name    string   `yaml:"name"`
	Seeders []string `yaml:"seeders"`
}

type StorageConfig struct {
	ConsumptionDir string `mapstructure:"consumption_dir" yaml:"consumption_dir"`
	StorageDir     string `mapstructure:"storage_dir" yaml:"storage_dir"`
}

type TextExtractorConfig struct {
	Engine  string `mapstructure:"engine" yaml:"engine"`
	Timeout int    `mapstructure:"timeout" yaml:"timeout"`
}

type PdfOptimizerConfig struct {
	Engine   string `mapstructure:"engine" yaml:"engine"`
	Fallback string `mapstructure:"fallback" yaml:"fallback"`
	Timeout  int    `mapstructure:"timeout" yaml:"timeout"`
}

type OCRConfig struct {
	Engine    string   `mapstructure:"engine" yaml:"engine"`
	Languages []string `mapstructure:"languages" yaml:"languages"`
	DataDir   string   `mapstructure:"data_dir"`
	Timeout   int      `mapstructure:"timeout" yaml:"timeout"`
}

type ConsumerConfig struct {
	SupportedFiles []string
	DeleteOriginal bool                `mapstructure:"delete_original" yaml:"delete_original"`
	Workers        int                 `mapstructure:"workers" yaml:"workers"`
	TextExtractor  TextExtractorConfig `mapstructure:"textextractor" yaml:"textextractor"`
	PdfOptimizer   PdfOptimizerConfig  `mapstructure:"pdfoptimizer" yaml:"pdfoptimizer"`
	OCR            OCRConfig           `mapstructure:"ocr" yaml:"ocr"`
}

type TextReducerConfig struct {
	Engine      string `mapstructure:"engine" yaml:"engine"`
	Timeout     int    `mapstructure:"timeout" yaml:"timeout"`
	TargetWords int    `mapstructure:"target_words" yaml:"target_words"`
}

type ContentAnalyzerConfig struct {
	Engine  string         `mapstructure:"engine" yaml:"engine"`
	Timeout int            `mapstructure:"timeout" yaml:"timeout"`
	Llm     LlmToolsConfig `mapstructure:"llm" yaml:"llm"`
}

type EnricherConfig struct {
	Workers         int                   `mapstructure:"workers" yaml:"workers"`
	TextReducer     TextReducerConfig     `mapstructure:"textreducer" yaml:"textreducer"`
	ContentAnalyzer ContentAnalyzerConfig `mapstructure:"contentanalyzer" yaml:"contentanalyzer"`
	TagMatcher      TagMatcherConfig      `mapstructure:"tagmatcher" yaml:"tagmatcher"`
}

type LlmToolConfig struct {
	BaseURL string `mapstructure:"base_url" yaml:"base_url"`
	Model   string `mapstructure:"model" yaml:"model"`
	Token   string `mapstructure:"token" yaml:"token"`
}

type LlmToolsConfig struct {
	OpenAI    LlmToolConfig `mapstructure:"openai" yaml:"openai"`
	Anthropic LlmToolConfig `mapstructure:"anthropic" yaml:"anthropic"`
	DeepSeek  LlmToolConfig `mapstructure:"deepseek" yaml:"deepseek"`
	Ollama    LlmToolConfig `mapstructure:"ollama" yaml:"ollama"`
}

type HugotConfig struct {
	Model          string `mapstructure:"model" yaml:"model"`
	Backend        string `mapstructure:"backend" yaml:"backend"`
	ModelPath      string
	BackendLibPath string
}

type TagMatcherConfig struct {
	Engine                  string      `mapstructure:"engine" yaml:"engine"`
	Timeout                 int         `mapstructure:"timeout" yaml:"timeout"`
	ReduceTargetWords       int         `mapstructure:"reduce_target_words" yaml:"reduce_target_words"`
	ChunkSize               int         `mapstructure:"chunk_size" yaml:"chunk_size"`
	Hugot                   HugotConfig `mapstructure:"hugot" yaml:"hugot"`
	TopN                    int
	MinSimilarity           float64
	ConsolidationSimilarity float64
}

type ToolConfig struct {
	Command string        `yaml:"command"`
	Timeout time.Duration `yaml:"timeout"`
}

type Config struct {
	App      AppConfig      `mapstructure:"app" yaml:"app"`
	Srv      ServerConfig   `mapstructure:"server" yaml:"server"`
	Db       DatabaseConfig `mapstructure:"database" yaml:"database"`
	Storage  StorageConfig  `mapstructure:"storage" yaml:"storage"`
	Consumer ConsumerConfig `yaml:"consumer"`
	Enricher EnricherConfig `mapstructure:"enricher" yaml:"enricher"`
}

func DefaultConfig(configDir string) *Config {
	return &Config{
		App: AppConfig{
			Env:       Development,
			LogLevel:  "info",
			LogFile:   filepath.Join(configDir, "kushim.log"),
			ConfigDir: configDir,
		},
		Srv: ServerConfig{
			Host:         "localhost",
			Port:         3000,
			ReadTimeout:  60 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  60 * time.Second,
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
			SupportedFiles: []string{".pdf"},
			DeleteOriginal: false,
			Workers:        1,
			TextExtractor: TextExtractorConfig{
				Engine:  "mupdf",
				Timeout: 120,
			},
			PdfOptimizer: PdfOptimizerConfig{
				Engine:  "mupdf",
				Timeout: 120,
			},
			OCR: OCRConfig{
				Engine:  "gosseract",
				DataDir: filepath.Join(configDir, "ocr/tessdata"),
				Timeout: 120,
			},
		},
		Enricher: EnricherConfig{
			Workers: 1,
			TextReducer: TextReducerConfig{
				Engine:      "textrank",
				Timeout:     120,
				TargetWords: 2000,
			},
			ContentAnalyzer: ContentAnalyzerConfig{
				Engine:  "llmopenai",
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
				Engine:            "hugot",
				Timeout:           120,
				ReduceTargetWords: 4000,
				ChunkSize:         0,
				Hugot: HugotConfig{
					Model:   "BAAI/bge-m3",
					Backend: "ort",
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
	if len(cfg.Consumer.OCR.Languages) == 0 {
		return fmt.Errorf("consumer.ocr.languages is required — run 'kushim setup --langs eng,spa,...' first")
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
