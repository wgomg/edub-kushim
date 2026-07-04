package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wgomg/edub-kushim/internal/commands"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/ocr"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func main() {
	if len(os.Args) < 2 {
		commands.PrintUsage()
	}

	commandName := os.Args[1]
	args := os.Args[2:]

	if commandName == "setup" {
		startupLogger := utils.NewLogger("info")
		if err := commands.RunSetup(args, startupLogger); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if commandName == "internal-ocr" {
		var inputPath, outputPath, languagesStr, dataDir string
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
			}
		}
		if inputPath == "" || outputPath == "" || languagesStr == "" || dataDir == "" {
			fmt.Fprintf(os.Stderr, "usage: kushim internal-ocr --input <file> --output <file> --languages <lang,lang> --datadir <path>\n")
			os.Exit(1)
		}
		languages := strings.Split(languagesStr, ",")
		if err := ocr.RunStandalone(inputPath, outputPath, languages, dataDir); err != nil {
			fmt.Fprintf(os.Stderr, "ocr: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "usage: kushim internal-mupdf-clean --input <file> --output <file>\n")
			os.Exit(1)
		}
		ctx, err := adapters.NewMuContext()
		if err != nil {
			fmt.Fprintf(os.Stderr, "mupdf context: %v\n", err)
			os.Exit(1)
		}
		opts := adapters.NewCleanOptions()
		if err := ctx.PdfCleanFile(inputPath, outputPath, opts); err != nil {
			fmt.Fprintf(os.Stderr, "mupdf pdf_clean_file: %v\n", err)
			os.Exit(1)
		}
		ctx.Close()
		return
	}

	if commandName == "--help" || commandName == "-h" {
		commands.PrintUsage()
		return
	}

	startupLogger := utils.NewLogger("error")
	configDir, err := utils.ConfigDir()
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

	logger := utils.NewLogger(cfg.App.LogLevel)
	logFile := filepath.Join(*configDir, "logs", "kushim.log")
	os.MkdirAll(filepath.Dir(logFile), 0755)
	if err := logger.SetLogFile(logFile); err != nil {
		logger.Error(nil, "failed to open log file: %v", err)
	}
	container := commands.NewContainer(cfg, logger)
	defer container.Close()

	runner := commands.NewCommandRunner(container, "cli")

	if err := runner.ExecuteCommand(commandName, args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
