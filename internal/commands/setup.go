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

	"github.com/spf13/viper"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
	"github.com/wgomg/edub-kushim/internal/wizard"
	_ "modernc.org/sqlite"
)

func RunSetup(args []string, logger *utils.Logger) error {
	configDir, err := utils.ConfigDir()
	if err != nil {
		return fmt.Errorf("cannot determine config directory: %w", err)
	}
	configPath := filepath.Join(*configDir, "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("setup has already been completed — remove %s to re-run", configPath)
	}

	var cli bool
	p := NewFlagParser(args)
	if err := p.Bool("--cli", &cli); err != nil {
		return err
	}

	if cli {
		return runSetupCLI(args, logger)
	}
	return runSetupWizard(args, logger)
}

func runSetupWizard(args []string, logger *utils.Logger) error {
	addr := "0.0.0.0:8420"

	srv := wizard.NewServer(addr, logger)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	logger.Info(nil, "Setup wizard running at http://%s", addr)

	select {
	case err := <-errCh:
		return err
	}
}

func runSetupCLI(args []string, logger *utils.Logger) error {
	var langs, inboxDir, storageDir, dbPath, optimizationFallback, ocrEngine, pdfEngine, textExtractorEngine string
	var resetDb, cli bool

	p := NewFlagParser(args)
	if err := p.Bool("--cli", &cli); err != nil {
		return err
	}
	if err := p.Bool("--reset-database", &resetDb); err != nil {
		return err
	}
	if err := p.String("--languages", &langs); err != nil {
		return err
	}
	if err := p.String("--inbox-path", &inboxDir); err != nil {
		return err
	}
	if err := p.String("--storage-path", &storageDir); err != nil {
		return err
	}
	if err := p.String("--database-path", &dbPath); err != nil {
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
	if err := p.String("--consumer-textextractor-engine", &textExtractorEngine); err != nil {
		return err
	}
	if rest := p.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	if langs == "" {
		return fmt.Errorf(`usage: kushim setup --languages eng,spa,...

Flags:
  --languagess                       ISO 639-3 codes (eng, spa, fra, ...)
  --inbox-path                       inbox directory (default: ~/.config/edub-kushim/inbox)
  --storage-path                     storage directory (default: ~/.config/edub-kushim/storage)
  --database-path                    database path (default: ~/.config/edub-kushim/data)
  --consumer-ocr-engine              gosseract | ocrmypdf (default: gosseract)
  --consumer-textextractor-engine    mupdf | gopdf | pdftotext (default: mupdf)
  --consumer-pdfoptimizer-engine     mupdf | gs (default: mupdf)
  --consumer-pdfoptimizer-fallback   external PDF optimizer binary (ignored when engine is gs)
  --reset-database                   drop all tables and re-run schema + seeders`)
	}

	langList := strings.Split(langs, ",")

	validOCREngines := []string{config.OCR.Gosseract, config.OCR.OcrMyPdf}
	if ocrEngine != "" && !slices.Contains(validOCREngines, ocrEngine) {
		return fmt.Errorf("invalid --consumer-ocr-engine %q: must be one of %v", ocrEngine, validOCREngines)
	}
	validPdfEngines := []string{config.PdfOptimizer.MuPDF, config.PdfOptimizer.GS}
	if pdfEngine != "" && !slices.Contains(validPdfEngines, pdfEngine) {
		return fmt.Errorf("invalid --consumer-pdfoptimizer-engine %q: must be one of %v", pdfEngine, validPdfEngines)
	}
	validTextExtractorEngines := []string{config.TextExtractor.MuPDF, config.TextExtractor.GoPdf, config.TextExtractor.PdfToText}
	if textExtractorEngine != "" && !slices.Contains(validTextExtractorEngines, textExtractorEngine) {
		return fmt.Errorf("invalid --consumer-textextractor-engine %q: must be one of %v", textExtractorEngine, validTextExtractorEngines)
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
	if textExtractorEngine != "" {
		cfg.Consumer.TextExtractor.Engine = textExtractorEngine
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

	if pdfEngine == config.PdfOptimizer.GS {
		optimizationFallback = ""
	} else if optimizationFallback != "" {
		cfg.Consumer.PdfOptimizer.Fallback = optimizationFallback
	}

	v := viper.New()
	v.SetConfigType("yaml")
	configPath := filepath.Join(*configDir, "config.yaml")

	if _, err := os.Stat(configPath); err == nil {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read existing config: %w", err)
		}
	}

	v.Set("consumer.ocr.languages", langList)
	if ocrEngine != "" {
		v.Set("consumer.ocr.engine", ocrEngine)
	}
	if pdfEngine != "" {
		v.Set("consumer.pdfoptimizer.engine", pdfEngine)
	}
	if textExtractorEngine != "" {
		v.Set("consumer.textextractor.engine", textExtractorEngine)
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

	if err := v.WriteConfigAs(configPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	logger.Info(nil, "created config: %s", configPath)

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

	dsn := filepath.Join(cfg.Db.Path, cfg.Db.Name)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return fmt.Errorf("enable foreign keys: %w", err)
	}

	if resetDb {
		logger.Info(nil, "resetting database...")
		if err := database.ResetDatabase(db); err != nil {
			db.Close()
			return err
		}
		logger.Info(nil, "database reset complete")
	} else {
		if err := database.InitializeSchema(db); err != nil {
			db.Close()
			return err
		}
	}
	db.Close()
	logger.Info(nil, "created database: %s", dsn)

	cfg, err = config.Load(*configDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := config.DownloadHugotModel(context.Background(), cfg, logger); err != nil {
		return fmt.Errorf("hugot model download: %w", err)
	}

	if cfg.Consumer.PdfOptimizer.Engine == config.PdfOptimizer.MuPDF && optimizationFallback != "" {
		if _, err := exec.LookPath(optimizationFallback); err != nil {
			logger.Info(nil, "WARNING: --consumer-pdfoptimizer-fallback %s set but %q not found on PATH. "+
				"Install it before running 'kushim consume'.", optimizationFallback, optimizationFallback)
		}
	}

	if cfg.Consumer.OCR.Engine == config.OCR.Gosseract {
		for _, lang := range langList {
			if err := config.DownloadTessdataLanguage(context.Background(), cfg, lang); err != nil {
				return err
			}
			logger.Info(nil, "downloaded: %s", lang)
		}
	}

	if cfg.Consumer.OCR.Engine == config.OCR.Gosseract {
		logger.Info(nil, "setup complete — %d languages in %s", len(langList), tessdataDir)
	} else {
		logger.Info(nil, "setup complete — %d languages configured with %s engine",
			len(langList), cfg.Consumer.OCR.Engine)
	}
	fmt.Println("\nNext: run 'kushim consume' to process documents")

	return nil
}

func setupHandler(container *Container, args []string) error {
	return fmt.Errorf("setup must be run without a config file — use 'kushim setup' for the wizard or 'kushim setup --cli --languages ...' for terminal setup")
}
