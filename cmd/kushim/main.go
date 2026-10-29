// The kushim binary: the CLI verb dispatcher, the matcher daemon host,
// and the per-batch worker that consume and enrich children fork. In
// CGo-less builds the internal OCR and thumbnail verbs report their
// unavailability.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wgomg/edub-kushim/internal/cgo"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/documents/ocr"
	"github.com/wgomg/edub-kushim/internal/documents/thumbnail"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/cli"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/dispatch"
	"github.com/wgomg/edub-kushim/internal/ops/setup"
	"github.com/wgomg/edub-kushim/internal/task"
)

func main() {
	if len(os.Args) < 2 {
		cli.PrintUsage()
	}

	commandName := os.Args[1]
	args := os.Args[2:]

	executor := false
	if commandName == task.ExecutorFlag {
		executor = true
		if len(args) == 0 {
			cli.PrintUsage()
		}
		commandName, args = args[0], args[1:]
		if !dispatch.IsExecutorVerb(commandName) {
			_, _ = fmt.Fprintf(os.Stderr,
				"Error: %s is internal to the batch fork contract and does not apply to %q\n",
				task.ExecutorFlag, commandName)
			os.Exit(1)
		}
	}

	if commandName == "setup" {
		startupLogger := log.NewLogger("info")
		if err := setup.RunSetup(args, startupLogger); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if commandName == "internal-ocr" {
		var inputPath, outputPath, languagesStr, dataDir, ocrWorkersStr string
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--input":
				if i+1 < len(args) {
					inputPath = args[i+1]
					i++
				}
			case "--output":
				if i+1 < len(args) {
					outputPath = args[i+1]
					i++
				}
			case "--languages":
				if i+1 < len(args) {
					languagesStr = args[i+1]
					i++
				}
			case "--datadir":
				if i+1 < len(args) {
					dataDir = args[i+1]
					i++
				}
			case "--ocr-workers":
				if i+1 < len(args) {
					ocrWorkersStr = args[i+1]
					i++
				}
			}
		}
		if inputPath == "" || outputPath == "" || languagesStr == "" || dataDir == "" {
			_, _ = fmt.Fprintf(os.Stderr, "usage: kushim internal-ocr --input <file> --output <file> --languages <lang,lang> --datadir <path>\n")
			os.Exit(1)
		}
		languages := strings.Split(languagesStr, ",")
		var ocrWorkers int
		if ocrWorkersStr != "" {
			var err error
			ocrWorkers, err = strconv.Atoi(ocrWorkersStr)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "invalid --ocr-workers value %q: %v\n", ocrWorkersStr, err)
				os.Exit(1)
			}
		}
		if err := ocr.RunStandalone(inputPath, outputPath, languages, dataDir, ocrWorkers); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "ocr: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if commandName == "internal-thumbnail" {
		var inputPath, outputPath string
		dpi, maxWidth, quality := 72, 400, 80
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--input":
				if i+1 < len(args) {
					inputPath = args[i+1]
					i++
				}
			case "--output":
				if i+1 < len(args) {
					outputPath = args[i+1]
					i++
				}
			case "--dpi":
				if i+1 < len(args) {
					dpi, _ = strconv.Atoi(args[i+1])
					i++
				}
			case "--max-width":
				if i+1 < len(args) {
					maxWidth, _ = strconv.Atoi(args[i+1])
					i++
				}
			case "--quality":
				if i+1 < len(args) {
					quality, _ = strconv.Atoi(args[i+1])
					i++
				}
			}
		}
		if inputPath == "" || outputPath == "" {
			_, _ = fmt.Fprintf(os.Stderr, "usage: kushim internal-thumbnail --input <file> --output <file> [--dpi <dpi>] [--max-width <px>] [--quality <1-100>]\n")
			os.Exit(1)
		}
		if err := thumbnail.RunStandalone(inputPath, outputPath, dpi, maxWidth, quality); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "thumbnail: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if commandName == "internal-mupdf-clean" {
		var inputPath, outputPath string
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--input":
				if i+1 < len(args) {
					inputPath = args[i+1]
					i++
				}
			case "--output":
				if i+1 < len(args) {
					outputPath = args[i+1]
					i++
				}
			}
		}
		if inputPath == "" || outputPath == "" {
			_, _ = fmt.Fprintf(os.Stderr, "usage: kushim internal-mupdf-clean --input <file> --output <file>\n")
			os.Exit(1)
		}
		ctx, err := cgo.NewMuContext()
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "mupdf context: %v\n", err)
			os.Exit(1)
		}
		opts := cgo.NewCleanOptions()
		if err := ctx.PdfCleanFile(inputPath, outputPath, opts); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "mupdf pdf_clean_file: %v\n", err)
			os.Exit(1)
		}
		ctx.Close()
		return
	}

	if commandName == "--help" || commandName == "-h" {
		cli.PrintUsage()
		return
	}

	startupLogger := log.NewLogger("error")
	configDir, err := config.ConfigDir()
	if err != nil {
		startupLogger.Fatal("Cannot determine home directory:", err)
	}

	cfg, err := config.Load(*configDir)
	if err != nil {
		if strings.Contains(err.Error(), "ocr.languages is required") {
			startupLogger.Fatal("Not initialized: run 'kushim setup --languages eng,spa,...' first")
		}
		startupLogger.Fatal("Failed to load configuration:", err)
	}

	logger := log.NewLogger(cfg.App.LogLevel)
	slog.SetDefault(logger.SlogLogger())
	logFile := filepath.Join(*configDir, "logs", "kushim.log")
	_ = os.MkdirAll(filepath.Dir(logFile), 0755)
	if err := logger.SetLogFile(log.LogFileConfig{
		Path:       logFile,
		MaxSize:    cfg.App.Logging.MaxSize,
		MaxBackups: cfg.App.Logging.MaxBackups,
		MaxAge:     cfg.App.Logging.MaxAge,
		Compress:   cfg.App.Logging.Compress,
	}); err != nil {
		logger.Error("", "failed to open log file: %v", err)
	}
	var c *container.Container
	if executor {
		c = container.NewBatchExecutor(cfg, logger)
	} else {
		c = container.NewContainer(cfg, logger)
	}
	defer c.Close()

	runner := cli.NewCommandRunner(c, "cli")

	if err := runner.ExecuteCommand(commandName, args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
