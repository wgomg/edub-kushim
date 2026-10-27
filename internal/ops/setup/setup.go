// Package setup is the CLI setup flow: it walks the operator through
// environment and account configuration in the terminal or by serving the
// wizard, then bootstraps the environment and downloads the assets the
// configuration requires.
package setup

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/viper"
	"github.com/wgomg/edub-kushim/internal/api/wizard"
	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/service"
	"golang.org/x/term"
)

// RunSetup is the kushim setup verb: it refuses to run over an existing configuration and drives either the
// terminal wizard or the served wizard, based on the flags. Interrupted setups keep that refusal and are
// re-run by deleting the config file the message names.
func RunSetup(args []string, logger *log.Logger) error {
	configDir, err := config.ConfigDir()
	if err != nil {
		return fmt.Errorf("cannot determine config directory: %w", err)
	}
	configPath := filepath.Join(*configDir, "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("a configuration already exists at %s — remove it to re-run setup", configPath)
	}

	var cli bool
	p := flags.NewFlagParser(args)
	if err := p.Bool("--cli", &cli); err != nil {
		return err
	}

	if cli {
		return runSetupCLI(args, logger)
	}
	return runSetupWizard(args, logger)
}

func runSetupWizard(args []string, logger *log.Logger) error {
	addr := "0.0.0.0:8420"

	srv := wizard.NewServer(addr, logger)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	logger.Info("", "Setup wizard running at http://%s", addr)

	select {
	case err := <-errCh:
		return err
	}
}

func runSetupCLI(args []string, logger *log.Logger) error {
	var langs, inboxDir, storageDir, optimizationFallback, ocrEngine, pdfEngine, textExtractorEngine, dbDsn string
	var resetDb, cli bool
	var adminUser, adminPassword string

	p := flags.NewFlagParser(args)
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
	if err := p.String("--admin-user", &adminUser); err != nil {
		return err
	}
	if err := p.String("--admin-password", &adminPassword); err != nil {
		return err
	}
	if err := p.String("--db-dsn", &dbDsn); err != nil {
		return err
	}
	if rest := p.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	if langs == "" {
		return fmt.Errorf(`usage: kushim setup --languages eng,spa,...

Flags:
  --languages                        ISO 639-3 codes (eng, spa, fra, ...)
  --inbox-path                       inbox directory (default: ~/.config/edub-kushim/inbox)
  --storage-path                     storage directory (default: ~/.config/edub-kushim/storage)
  --consumer-ocr-engine              gosseract | ocrmypdf (default: gosseract)
  --consumer-textextractor-engine    mupdf | gopdf | pdftotext (default: mupdf)
  --consumer-pdfoptimizer-engine     mupdf | gs (default: mupdf)
  --consumer-pdfoptimizer-fallback   external PDF optimizer binary (ignored when engine is gs)
  --admin-user                       admin username (prompted if omitted)
  --admin-password                   admin password (prompted if omitted)
  --db-dsn                           PostgreSQL DSN (default: localhost:5432)
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

	if pdfEngine == config.PdfOptimizer.GS {
		optimizationFallback = ""
	}

	configDir, err := config.ConfigDir()
	if err != nil {
		logger.Fatal("Cannot determine home directory:", err)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigPermissions(0600)
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
	if optimizationFallback != "" {
		v.Set("consumer.pdfoptimizer.fallback", optimizationFallback)
	}

	secret, err := auth.GenerateSessionSecret()
	if err != nil {
		return fmt.Errorf("generate session secret: %w", err)
	}
	v.Set("server.session_secret", secret)

	if dbDsn != "" {
		v.Set("database.dsn", dbDsn)
	}

	// the database is claimed and its schema created before the configuration is written, so a rejected
	// target leaves no configuration behind; the target is the default connection or the --db-dsn override
	effectiveDB := config.DefaultConfig(*configDir).Db
	if dbDsn != "" {
		effectiveDB.DSN = dbDsn
	}
	db, err := prepareSetupDatabase(effectiveDB, resetDb, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := v.WriteConfigAs(configPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Chmod(configPath, 0600); err != nil {
		return fmt.Errorf("chmod config: %w", err)
	}
	logger.Info("", "created config: %s", configPath)

	cfg, err := config.Load(*configDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
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

	if err := config.DownloadHugotModel(context.Background(), cfg, logger); err != nil {
		return fmt.Errorf("hugot model download: %w", err)
	}

	if cfg.Consumer.PdfOptimizer.Engine == config.PdfOptimizer.MuPDF && optimizationFallback != "" {
		if _, err := exec.LookPath(optimizationFallback); err != nil {
			logger.Info("", "WARNING: --consumer-pdfoptimizer-fallback %s set but %q not found on PATH. "+
				"Install it before running 'kushim consume'.", optimizationFallback, optimizationFallback)
		}
	}

	if cfg.Consumer.OCR.Engine == config.OCR.Gosseract {
		for _, lang := range langList {
			if err := config.DownloadTessdataLanguage(context.Background(), cfg, lang); err != nil {
				return err
			}
			logger.Info("", "downloaded: %s", lang)
		}
	}

	if adminUser == "" {
		scanner := bufio.NewScanner(os.Stdin)
		fmt.Print("Enter admin username: ")
		scanner.Scan()
		adminUser = strings.TrimSpace(scanner.Text())
		if adminUser == "" {
			return fmt.Errorf("admin username cannot be empty")
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read username: %w", err)
		}
	}

	if adminPassword == "" {
		fmt.Print("Enter admin password: ")
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		adminPassword = string(password)

		fmt.Print("Confirm admin password: ")
		confirm, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("confirm password: %w", err)
		}
		if adminPassword != string(confirm) {
			return fmt.Errorf("passwords do not match")
		}
	}

	client := database.NewClient(db)
	userSvc := service.NewUser(client.Queries)
	if _, err := userSvc.Create(context.Background(), adminUser, adminPassword, "admin"); err != nil {
		if errs.KindOf(err) == errs.KindConflict {
			return fmt.Errorf("admin user '%s' already exists", adminUser)
		}
		return fmt.Errorf("create admin user: %w", err)
	}

	logger.Info("", "admin user '%s' created", adminUser)

	svcPath, err := config.GenerateServiceFiles(cfg.App.ConfigDir)
	if err != nil {
		logger.Warn("", "Failed to generate systemd service files: %v", err)
	} else {
		fmt.Printf("\nService files written to: %s\n\n", svcPath)
		fmt.Println("To install and start as system services:")
		fmt.Printf("  sudo cp %s/* /etc/systemd/system/\n", svcPath)
		fmt.Println("  sudo systemctl daemon-reload")
		fmt.Println("  sudo systemctl enable --now edub-kushim.target")
	}

	if cfg.Consumer.OCR.Engine == config.OCR.Gosseract {
		logger.Info("", "setup complete — %d languages in %s", len(langList), tessdataDir)
	} else {
		logger.Info("", "setup complete — %d languages configured with %s engine",
			len(langList), cfg.Consumer.OCR.Engine)
	}
	fmt.Println("\nNext: run 'kushim consume' to process documents")

	return nil
}

// prepareSetupDatabase creates the schema on the connection the written config will carry. Without reset it
// enforces the empty-only policy first, so setup never adopts a database that already holds tables; with
// reset it drops and re-creates the existing one as asked. It returns the open handle the admin user
// creation and later setup steps use.
func prepareSetupDatabase(dbCfg config.DatabaseConfig, reset bool, logger *log.Logger) (*sql.DB, error) {
	dsn := config.BuildPostgresDSN(dbCfg)

	if reset {
		db, err := database.NewPostgresDB(dsn)
		if err != nil {
			return nil, fmt.Errorf("open database: %w", err)
		}
		logger.Info("", "resetting database...")
		if err := database.ResetDatabase(db); err != nil {
			_ = db.Close()
			return nil, err
		}
		logger.Info("", "database reset complete")
		return db, nil
	}

	if err := database.PrepareFreshDatabase(dsn); err != nil {
		return nil, err
	}
	db, err := database.NewPostgresDB(dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := database.InitializeSchema(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	logger.Info("", "created database")
	return db, nil
}
