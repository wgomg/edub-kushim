package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/knights-analytics/hugot"
	"github.com/spf13/viper"
	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	hugotModelHFRepo  = "BAAI/bge-m3"
	hugotModelDirName = "bge-m3"
)

func ConfigExists(configDir string) bool {
	_, err := os.Stat(filepath.Join(configDir, "config.yaml"))
	return err == nil
}

func LoadOrBootstrap(configDir string) (*Config, error) {
	if ConfigExists(configDir) {
		return Load(configDir)
	}
	return Bootstrap(configDir)
}

func Bootstrap(configDir string) (*Config, error) {
	cfg := DefaultConfig(configDir)

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.ConsumptionDir, 0755); err != nil {
		return nil, fmt.Errorf("create inbox dir: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.StorageDir, 0755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	if err := os.MkdirAll(cfg.Consumer.OCR.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("create tessdata dir: %w", err)
	}

	if err := SaveMap(configDir, map[string]any{}); err != nil {
		return nil, fmt.Errorf("write skeleton config: %w", err)
	}

	secret, err := auth.GenerateSessionSecret()
	if err != nil {
		return nil, err
	}
	cfg.Srv.SessionSecret = secret
	if err := SaveMap(configDir, map[string]any{
		"server.session_secret": cfg.Srv.SessionSecret,
		"server.auth_enabled":   false,
	}); err != nil {
		return nil, fmt.Errorf("write bootstrap config: %w", err)
	}

	db, err := database.NewPostgresDB(BuildPostgresDSN(cfg.Db))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.Close()

	return cfg, nil
}

func SaveMap(configDir string, body map[string]any) error {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigPermissions(0600)
	configPath := filepath.Join(configDir, "config.yaml")

	if _, err := os.Stat(configPath); err == nil {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read existing config: %w", err)
		}
	}

	for key, val := range body {
		v.Set(key, val)
	}
	if err := v.WriteConfigAs(configPath); err != nil {
		return err
	}

	return os.Chmod(configPath, 0600)
}

func MissingTessdataLanguages(cfg *Config) []string {
	if cfg.Consumer.OCR.Engine != OCR.Gosseract {
		return nil
	}

	var missing []string
	for _, lang := range cfg.Consumer.OCR.Languages {
		dest := filepath.Join(cfg.Consumer.OCR.DataDir, lang+".traineddata")
		if _, err := os.Stat(dest); err != nil {
			missing = append(missing, lang)
		}
	}
	return missing
}

func MissingHugotModel(cfg *Config) bool {
	modelPath := filepath.Join(cfg.App.ConfigDir, "tagmatcher", "hugot", "models", hugotModelDirName)
	_, err := os.Stat(modelPath)
	return err != nil
}

func DownloadTessdataLanguage(ctx context.Context, cfg *Config, lang string) error {
	dest := filepath.Join(cfg.Consumer.OCR.DataDir, lang+".traineddata")
	if _, err := os.Stat(dest); err == nil {
		return nil
	}

	url := fmt.Sprintf("https://github.com/tesseract-ocr/tessdata_fast/raw/main/%s.traineddata", lang)
	if err := downloadFile(url, dest); err != nil {
		os.Remove(dest)
		return fmt.Errorf("download %s: %w", lang, err)
	}
	return nil
}

func DownloadHugotModel(ctx context.Context, cfg *Config, logger *utils.Logger) error {
	modelsParent := filepath.Join(cfg.App.ConfigDir, "tagmatcher", "hugot", "models")
	targetModelPath := filepath.Join(modelsParent, hugotModelDirName)

	if _, err := os.Stat(targetModelPath); err == nil {
		logger.Info(nil, "hugot model already downloaded at %s", targetModelPath)
		return nil
	}

	logger.Info(nil, "downloading %s model for hugot...", hugotModelHFRepo)

	opts := hugot.NewDownloadOptions()
	opts.Verbose = true
	opts.OnnxFilePath = "onnx/model.onnx"
	opts.ExternalDataPath = "onnx/model.onnx_data"

	_, err := hugot.DownloadModel(ctx, hugotModelHFRepo, modelsParent, opts)
	if err != nil {
		return fmt.Errorf("download %s: %w", hugotModelHFRepo, err)
	}

	hfDir := strings.ReplaceAll(hugotModelHFRepo, "/", "_")
	downloadedPath := filepath.Join(modelsParent, hfDir)
	if err := os.Rename(downloadedPath, targetModelPath); err != nil {
		return fmt.Errorf("rename model dir %s -> %s: %w", downloadedPath, targetModelPath, err)
	}

	logger.Info(nil, "hugot model downloaded to %s", targetModelPath)
	return nil
}

func downloadFile(url, dest string) error {
	cmd := exec.Command("curl", "-fsSL", "--retry", "3", "-o", dest, url)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
