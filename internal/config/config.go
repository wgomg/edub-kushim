package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

type Environment string

const (
	Development Environment = "development"
	Production  Environment = "production"
)

type AppConfig struct {
	Env      Environment `mapstructure:"environment"`
	LogLevel string      `mapstructure:"log_level"`
}

type ServerConfig struct {
	Host         string        `mapstructure:"host"`
	Port         int           `mapstructure:"port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`
}

type DatabaseConfig struct {
	Type string `mapstructure:"type"`
	Path string `mapstructure:"path"`
	Name string
}

type StorageConfig struct {
	ConsumptionDir string `mapstructure:"consumption_dir"`
	StorageDir     string `mapstructure:"storage_dir"`
}

type ConsumerConfig struct {
	SupportedFiles       []string
	TextExtractor        string
	PdfOptimizer         string
	OCR                  string
	DeleteOriginal       bool     `mapstructure:"delete_original"`
	OCRLanguages         []string `mapstructure:"ocr_languages"`
	OCRDataDir           string   `mapstructure:"ocr_data_dir"`
	OptimizationFallback string   `mapstructure:"optimization_fallback"`
	OptimizationTimeout  int      `mapstructure:"optimization_timeout"`
	TextExtractorTimeout int      `mapstructure:"textextractor_timeout"`
	OCRTimeout           int      `mapstructure:"ocr_timeout"`
	Workers              int      `mapstructure:"workers"`
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
}

func Load(configDir string) (*Config, error) {
	viper.SetDefault("app.environment", "development")
	viper.SetDefault("app.log_level", "info")

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
	viper.SetDefault("consumer.textextractor", "mupdf")
	viper.SetDefault("consumer.pdfoptimizer", "mupdf")
	viper.SetDefault("consumer.ocr", "gosseract")
	viper.SetDefault("consumer.delete_original", false)
	viper.SetDefault("consumer.ocr_data_dir", filepath.Join(configDir, "ocr/tessdata"))
	viper.SetDefault("consumer.optimization_fallback", "")
	viper.SetDefault("consumer.optimization_timeout", 120)
	viper.SetDefault("consumer.textextractor_timeout", 120)
	viper.SetDefault("consumer.ocr_timeout", 120)
	viper.SetDefault("consumer.workers", 1)

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
	cfg.Consumer.SupportedFiles = []string{".pdf"}

	if len(cfg.Consumer.OCRLanguages) == 0 {
		return nil, fmt.Errorf("consumer.ocr_languages is required — run 'kushim setup --langs eng,spa,...' first")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("expand home dir: %w", err)
	}

	cfg.Db.Path = expandPath(cfg.Db.Path, homeDir)
	cfg.Storage.ConsumptionDir = expandPath(cfg.Storage.ConsumptionDir, homeDir)
	cfg.Storage.StorageDir = expandPath(cfg.Storage.StorageDir, homeDir)
	cfg.Consumer.OCRDataDir = expandPath(cfg.Consumer.OCRDataDir, homeDir)

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
