//go:build cgo

package ocr

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/pdfoptimizer"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// Gosseract implements OCR using Tesseract (gosseract) with MuPDF for page
// rendering via the custom CGo wrapper (mupdf_wrapper.go).
type Gosseract struct {
	logger     *utils.Logger
	config     config.ToolConfig
	optimizer  pdfoptimizer.PdfOptimizer
	ocrWorkers int

	languages []string
	dataDir   string
}

func NewGosseract(logger *utils.Logger, cfg config.ToolConfig, optimizerCmd string, languages []string, dataDir string, ocrWorkers int) (*Gosseract, error) {
	optCfg := config.ToolConfig{Command: optimizerCmd, Timeout: cfg.Timeout}
	optimizer, err := pdfoptimizer.NewPdfOptimizer(logger, optCfg)
	if err != nil {
		return nil, fmt.Errorf("create optimizer: %w", err)
	}

	if len(languages) == 0 {
		languages = []string{"eng"}
	}

	return &Gosseract{logger: logger, config: cfg, optimizer: optimizer, languages: languages, dataDir: dataDir, ocrWorkers: ocrWorkers}, nil
}

func (o *Gosseract) Process(ctx context.Context, docId, path string) (*string, error) {
	if err := EnsureLanguages(o.logger, o.dataDir, o.languages); err != nil {
		return nil, fmt.Errorf("tessdata setup: %w", err)
	}

	outPath := filepath.Join(os.TempDir(), "ocr_"+uuid.New().String()+".pdf")

	langStr := LangString(o.languages)
	cmd := exec.CommandContext(ctx, os.Args[0], "internal-ocr",
		"--input", path,
		"--output", outPath,
		"--languages", langStr,
		"--datadir", o.dataDir,
		"--ocr-workers", strconv.Itoa(o.ocrWorkers),
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		os.Remove(outPath)
		return nil, fmt.Errorf("start OCR subprocess: %w", err)
	}

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			o.logger.Info(&docId, "%s", scanner.Text())
		}
	}()

	if err := cmd.Wait(); err != nil {
		os.Remove(outPath)
		return nil, fmt.Errorf("gosseract OCR: %w (stderr: %s)", err, stderr.String())
	}

	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("OCR did not create output file")
	}

	o.logger.Info(&docId, "created searchable PDF: %s", outPath)

	optResult, err := o.optimizer.Optimize(ctx, docId, outPath)
	if err != nil {
		os.Remove(outPath)
		return nil, fmt.Errorf("optimize OCR output: %w", err)
	}
	os.Remove(outPath)
	o.logger.Debug(&docId, "optimized OCR output: %s -> %s", outPath, *optResult)
	return optResult, nil
}

func (o *Gosseract) CanHandle(mimeType string) bool {
	return mimeType == "application/pdf"
}

func (o *Gosseract) Name() string {
	return config.OCR.Gosseract
}

func init() {
	newGosseract = func(logger *utils.Logger, cfg config.ToolConfig, optimizerCmd string, languages []string, dataDir string, ocrWorkers int) (OCR, error) {
		return NewGosseract(logger, cfg, optimizerCmd, languages, dataDir, ocrWorkers)
	}
}

