package commands

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/knights-analytics/hugot"
	"github.com/spf13/viper"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
	_ "modernc.org/sqlite"
)

func RunSetup(args []string, logger *utils.Logger) error {
	var langs, inboxDir, storageDir, dbPath, optimizationFallback, ocrEngine, pdfEngine string

	p := NewFlagParser(args)
	if err := p.String("--langs", &langs); err != nil {
		return err
	}
	if err := p.String("--inbox-dir", &inboxDir); err != nil {
		return err
	}
	if err := p.String("--storage-dir", &storageDir); err != nil {
		return err
	}
	if err := p.String("--db-path", &dbPath); err != nil {
		return err
	}
	if err := p.String("--consumer-pdfoptimizer-fallback", &optimizationFallback); err != nil {
		return err
	}
	if err := p.String("--consumer-pdfoptimizer-engine", &pdfEngine); err != nil {
		return err
	}
	if err := p.String("--consumer-ocr-engine", &ocrEngine); err != nil {
		return err
	}
	if rest := p.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	if langs == "" {
		return fmt.Errorf(`usage: kushim setup --langs eng,spa,...

Flags:
  --langs                            ISO 639-3 codes (eng, spa, fra, ...)
  --inbox-dir                        inbox directory (default: ~/.config/edub-kushim/inbox)
  --storage-dir                      storage directory (default: ~/.config/edub-kushim/storage)
  --db-path                          database path (default: ~/.config/edub-kushim/data)
  --consumer-ocr-engine              gosseract | ocrmypdf (default: gosseract)
  --consumer-pdfoptimizer-engine     mupdf | gs (default: mupdf)
  --consumer-pdfoptimizer-fallback   external PDF optimizer binary (ignored when engine is gs)`)
	}

	langList := strings.Split(langs, ",")

	validOCREngines := []string{"gosseract", "ocrmypdf"}
	if ocrEngine != "" && !slices.Contains(validOCREngines, ocrEngine) {
		return fmt.Errorf("invalid --consumer-ocr-engine %q: must be one of %v", ocrEngine, validOCREngines)
	}
	validPdfEngines := []string{"mupdf", "gs"}
	if pdfEngine != "" && !slices.Contains(validPdfEngines, pdfEngine) {
		return fmt.Errorf("invalid --consumer-pdfoptimizer-engine %q: must be one of %v", pdfEngine, validPdfEngines)
	}

	configDir, err := utils.ConfigDir()
	if err != nil {
		logger.Fatal("Cannot determine home directory:", err)
	}

	cfg := config.DefaultConfig(*configDir)
	cfg.Consumer.OCR.Languages = langList

	if ocrEngine != "" {
		cfg.Consumer.OCR.Engine = ocrEngine
	}
	if pdfEngine != "" {
		cfg.Consumer.PdfOptimizer.Engine = pdfEngine
	}

	if inboxDir != "" {
		cfg.Storage.ConsumptionDir = inboxDir
	}
	if storageDir != "" {
		cfg.Storage.StorageDir = storageDir
	}
	if dbPath != "" {
		cfg.Db.Path = dbPath
	}

	if pdfEngine == "gs" {
		optimizationFallback = ""
	} else if optimizationFallback != "" {
		cfg.Consumer.PdfOptimizer.Fallback = optimizationFallback
	}

	// Write minimal config.yaml with only user-specified overrides
	v := viper.New()
	v.SetConfigType("yaml")
	v.Set("consumer.ocr.languages", langList)
	if ocrEngine != "" {
		v.Set("consumer.ocr.engine", ocrEngine)
	}
	if pdfEngine != "" {
		v.Set("consumer.pdfoptimizer.engine", pdfEngine)
	}
	if inboxDir != "" {
		v.Set("storage.consumption_dir", inboxDir)
	}
	if storageDir != "" {
		v.Set("storage.storage_dir", storageDir)
	}
	if dbPath != "" {
		v.Set("database.path", dbPath)
	}
	if optimizationFallback != "" {
		v.Set("consumer.pdfoptimizer.fallback", optimizationFallback)
	}

	configPath := filepath.Join(*configDir, "config.yaml")
	if err := v.WriteConfigAs(configPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	logger.Info(nil, "created config: %s", configPath)

	// Create required directories
	if err := os.MkdirAll(cfg.Db.Path, 0755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.ConsumptionDir, 0755); err != nil {
		return fmt.Errorf("create inbox directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.StorageDir, 0755); err != nil {
		return fmt.Errorf("create storage directory: %w", err)
	}

	tessdataDir := cfg.Consumer.OCR.DataDir
	if err := os.MkdirAll(tessdataDir, 0755); err != nil {
		return fmt.Errorf("create tessdata directory: %w", err)
	}

	// Initialize database
	dsn := filepath.Join(cfg.Db.Path, cfg.Db.Name)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	if err := database.InitializeSchema(db); err != nil {
		db.Close()
		return err
	}
	db.Close()
	logger.Info(nil, "created database: %s", dsn)

	// Download hugot model
	if err := setupHugotModel(context.Background(), *configDir, logger); err != nil {
		return fmt.Errorf("hugot model download: %w", err)
	}

	if cfg.Consumer.PdfOptimizer.Engine == "mupdf" && optimizationFallback != "" {
		if _, err := exec.LookPath(optimizationFallback); err != nil {
			logger.Info(nil, "WARNING: --consumer-pdfoptimizer-fallback %s set but %q not found on PATH. "+
				"Install it before running 'kushim consume'.", optimizationFallback, optimizationFallback)
		}
	}

	if cfg.Consumer.OCR.Engine == "gosseract" {
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
	}

	if cfg.Consumer.OCR.Engine == "gosseract" {
		logger.Info(nil, "setup complete — %d languages in %s", len(langList), tessdataDir)
	} else {
		logger.Info(nil, "setup complete — %d languages configured with %s engine",
			len(langList), cfg.Consumer.OCR.Engine)
	}
	fmt.Println("\nNext: run 'kushim consume' to process documents")

	return nil
}

func downloadFile(url, dest string) error {
	cmd := exec.Command("curl", "-fsSL", "--retry", "3", "-o", dest, url)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

const hugotModelHFRepo = "BAAI/bge-m3"
const hugotModelDirName = "bge-m3"

func setupHugotModel(ctx context.Context, configDir string, logger *utils.Logger) error {
	modelsParent := filepath.Join(configDir, "tagmatcher", "hugot", "models")
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
		return fmt.Errorf("rename model dir %s → %s: %w", downloadedPath, targetModelPath, err)
	}

	logger.Info(nil, "hugot model downloaded to %s", targetModelPath)
	return nil
}

func setupHandler(container *Container, args []string) error {
	return fmt.Errorf("setup must be run without a config file — use 'kushim setup --langs ...' directly")
}
