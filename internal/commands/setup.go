package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wgomg/edub-kushim/internal/utils"
	"gopkg.in/yaml.v3"
)

func RunSetup(args []string, logger *utils.Logger) error {
	var langs string
	configDir := ""
	inboxDir := ""
	storageDir := ""
	dbPath := ""
	optimizationFallback := ""

	p := NewFlagParser(args)
	p.String("--langs", &langs)
	p.String("--config-dir", &configDir)
	p.String("--inbox-dir", &inboxDir)
	p.String("--storage-dir", &storageDir)
	p.String("--db-path", &dbPath)
	p.String("--optimization-fallback", &optimizationFallback)

	if langs == "" {
		return fmt.Errorf("usage: kushim setup --langs eng,spa,... [--config-dir ~/.config/kushim] [--inbox-dir ./inbox] [--storage-dir ./storage] [--db-path ./data/] [--optimization-fallback gs]\n  Languages are ISO 639-3 codes (eng, spa, fra, deu, rus, chi_sim, jpn, etc.)")
	}

	langList := strings.Split(langs, ",")

	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home dir: %w", err)
		}
		configDir = filepath.Join(home, ".config", "kushim")
	}

	tessdataDir := filepath.Join(configDir, "ocr", "tessdata")

	if err := os.MkdirAll(tessdataDir, 0755); err != nil {
		return fmt.Errorf("create tessdata dir: %w", err)
	}

	cfg := map[string]any{
		"consumer": map[string]any{
			"ocr_languages": langList,
		},
	}
	if inboxDir != "" || storageDir != "" {
		storageCfg := map[string]any{}
		if inboxDir != "" {
			storageCfg["consumption_dir"] = inboxDir
		}
		if storageDir != "" {
			storageCfg["storage_dir"] = storageDir
		}
		cfg["storage"] = storageCfg
	}
	if dbPath != "" {
		cfg["database"] = map[string]any{
			"path": dbPath,
		}
	}
	if optimizationFallback != "" {
		consumerCfg := cfg["consumer"].(map[string]any)
		consumerCfg["optimization_fallback"] = optimizationFallback
	}

	configPath := filepath.Join(configDir, "config.yaml")

	f, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	defer f.Close()

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	enc.Close()
	logger.Info(nil, "created config: %s", configPath)

	if optimizationFallback != "" {
		if _, err := exec.LookPath(optimizationFallback); err != nil {
			logger.Info(nil, "WARNING: --optimization-fallback %s set but %q not found on PATH. "+
				"Install it before running 'kushim consume'.", optimizationFallback, optimizationFallback)
		}
	}

	for _, lang := range langList {
		dest := filepath.Join(tessdataDir, lang+".traineddata")
		if _, err := os.Stat(dest); err == nil {
			logger.Info(nil, "already downloaded: %s", lang)
			continue
		}

		url := fmt.Sprintf("https://github.com/tesseract-ocr/tessdata_fast/raw/main/%s.traineddata", lang)
		logger.Info(nil, "downloading %s...", lang)

		if err := downloadFile(url, dest); err != nil {
			os.Remove(dest)
			return fmt.Errorf("download %s: %w", lang, err)
		}
	}

	logger.Info(nil, "setup complete — %d languages in %s", len(langList), tessdataDir)
	fmt.Println("\nNext: run 'kushim consume' to process documents")

	return nil
}

func downloadFile(url, dest string) error {
	cmd := exec.Command("curl", "-fsSL", "--retry", "3", "-o", dest, url)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func setupHandler(container *Container, args []string) error {
	return fmt.Errorf("setup must be run without a config file — use 'kushim setup --langs ...' directly")
}
