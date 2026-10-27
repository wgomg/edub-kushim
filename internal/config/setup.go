package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/knights-analytics/hugot"
	"github.com/spf13/viper"
	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/log"
)

const (
	hugotModelHFRepo  = "BAAI/bge-m3"
	hugotModelDirName = "bge-m3"
)

// ConfigExists reports whether a config.yaml is present.
func ConfigExists(configDir string) bool {
	_, err := os.Stat(filepath.Join(configDir, "config.yaml"))
	return err == nil
}

// databaseSettingsKeys are the config keys a setup claim persists, the database connection fields only. The
// deferred-migration flow persisted database.runtime/container without them, so a config holding just those
// does not mark a claimed target.
var databaseSettingsKeys = []string{
	"database.host", "database.port", "database.user", "database.password",
	"database.database", "database.sslmode", "database.dsn",
}

// DatabaseSettingsPresent reports whether a previous setup claim persisted the database connection settings.
// It reads the raw file because Load fills the defaults in, which makes a skeleton config and a claimed one
// indistinguishable afterwards; the setup handler uses it to tell a retry of an already-claimed target from
// a first submission. It says nothing about whether setup completed. A missing file reports false.
func DatabaseSettingsPresent(configDir string) bool {
	configPath := filepath.Join(configDir, "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		return false
	}

	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigFile(configPath)
	if err := v.ReadInConfig(); err != nil {
		return false
	}

	return slices.ContainsFunc(databaseSettingsKeys, v.IsSet)
}

// LoadOrBootstrap loads the configuration, bootstrapping the environment
// first when no config exists yet.
func LoadOrBootstrap(configDir string) (*Config, error) {
	if ConfigExists(configDir) {
		return Load(configDir)
	}
	return Bootstrap(configDir)
}

// Bootstrap prepares a fresh environment: it creates the config, storage, consumption, and tessdata
// directories and writes the skeleton config with a generated session secret and authentication off. It never
// touches the database; the connection is established only once the operator submits the connection settings
// and the schema is created. It does not download the embedding model or the OCR languages.
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

	return cfg, nil
}

// SaveMap merges the keys into the on-disk config.yaml through viper
// (reading the existing file when present), clears the deprecated backup
// flat fields when schedules are set, and writes the result with 0600
// permissions, healing a world-readable leftover.
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
	clearDeprecatedBackupFlatFields(v, body)
	if err := v.WriteConfigAs(configPath); err != nil {
		return err
	}

	return os.Chmod(configPath, 0600)
}

func clearDeprecatedBackupFlatFields(v *viper.Viper, body map[string]any) {
	if _, hasSchedules := body["backup.schedules"]; hasSchedules {
		v.Set("backup.interval", float64(0))
		v.Set("backup.time", "")
		v.Set("backup.keep", 0)
	}
}

// ValidateSave runs the merged config (on-disk config + body) through
// finalizeConfig without persisting anything, so the settings handler can
// reject invalid values before they reach disk. Mirrors SaveMap's viper merge
// and Load's validation.
func ValidateSave(configDir string, body map[string]any) error {
	v := viper.New()
	v.SetConfigType("yaml")
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
	clearDeprecatedBackupFlatFields(v, body)

	tmpDir, err := os.MkdirTemp("", "edub-config-validate-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := v.WriteConfigAs(filepath.Join(tmpDir, "config.yaml")); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	_, err = Load(tmpDir)
	return err
}

// MissingTessdataLanguages lists the OCR languages whose traineddata
// files are absent; only the gosseract engine needs them.
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

// MissingHugotModel reports whether the embedding model has not been
// downloaded into the config directory's tagmatcher layout.
func MissingHugotModel(cfg *Config) bool {
	modelPath := filepath.Join(cfg.App.ConfigDir, "tagmatcher", "hugot", "models", hugotModelDirName)
	_, err := os.Stat(modelPath)
	return err != nil
}

// DownloadTessdataLanguage fetches one OCR language pack from the tessdata_fast repository, skipping when
// present. It downloads into a temp file in the data directory and renames it into place, so the final
// path only ever holds a complete file, and a failed or concurrent download cannot disturb an existing
// one.
func DownloadTessdataLanguage(ctx context.Context, cfg *Config, lang string) error {
	dest := filepath.Join(cfg.Consumer.OCR.DataDir, lang+".traineddata")
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := os.MkdirAll(cfg.Consumer.OCR.DataDir, 0o755); err != nil {
		return fmt.Errorf("create tessdata dir: %w", err)
	}

	tmp, err := os.CreateTemp(cfg.Consumer.OCR.DataDir, "."+lang+".traineddata-*")
	if err != nil {
		return fmt.Errorf("create %s download temp file: %w", lang, err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close %s download temp file: %w", lang, err)
	}
	defer os.Remove(tmpPath)

	url := fmt.Sprintf("https://github.com/tesseract-ocr/tessdata_fast/raw/main/%s.traineddata", lang)
	if err := downloadFile(url, tmpPath); err != nil {
		return fmt.Errorf("download %s: %w", lang, err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("set %s.traineddata mode: %w", lang, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("install %s.traineddata: %w", lang, err)
	}
	return nil
}

// DownloadHugotModel fetches the bge-m3 embedding model into the config
// directory's tagmatcher layout, skipping when present.
func DownloadHugotModel(ctx context.Context, cfg *Config, logger *log.Logger) error {
	modelsParent := filepath.Join(cfg.App.ConfigDir, "tagmatcher", "hugot", "models")
	targetModelPath := filepath.Join(modelsParent, hugotModelDirName)

	if _, err := os.Stat(targetModelPath); err == nil {
		logger.Info("", "hugot model already downloaded at %s", targetModelPath)
		return nil
	}

	logger.Info("", "downloading %s model for hugot...", hugotModelHFRepo)

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

	logger.Info("", "hugot model downloaded to %s", targetModelPath)
	return nil
}

func downloadFile(url, dest string) error {
	cmd := exec.Command("curl", "-fsSL", "--retry", "3", "-o", dest, url)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
