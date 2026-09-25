package pdfoptimizer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// Ghostscript rewrites PDFs by shelling out to gs.
type Ghostscript struct {
	logger *log.Logger
	config config.ToolConfig
}

// NewGhostscript verifies the gs binary is on PATH, erroring with
// install hints when it is not.
func NewGhostscript(logger *log.Logger, cfg config.ToolConfig) (*Ghostscript, error) {
	if _, err := exec.LookPath(cfg.Command); err != nil {
		return nil, fmt.Errorf("%s not found in PATH: %w\nInstall it (e.g. 'sudo apt install ghostscript' or 'brew install ghostscript') or switch to the built-in 'mupdf' engine", cfg.Command, err)
	}

	return &Ghostscript{logger: logger, config: cfg}, nil
}

// Optimize runs gs over a temp copy and returns its path. The path must
// be absolute.
func (g *Ghostscript) Optimize(ctx context.Context, docId, path string) (*string, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%s: input path must be absolute, got %q", g.Name(), path)
	}

	tmpDir := os.TempDir()
	ogName := filepath.Base(path)
	outputName := fmt.Sprintf(
		"gs_%s_%d.pdf",
		strings.TrimSuffix(ogName, filepath.Ext(ogName)),
		time.Now().Unix(),
	)
	outputPath := filepath.Join(tmpDir, outputName)

	args := []string{
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		"-dPDFSETTINGS=/ebook",
		"-dDetectDuplicateImages=true",
		"-dCompressFonts=true",
		"-dSubsetFonts=true",
		"-dColorImageDownsampleType=/Bicubic",
		"-dColorImageResolution=150",
		"-dGrayImageDownsampleType=/Bicubic",
		"-dGrayImageResolution=150",
		"-dMonoImageDownsampleType=/Bicubic",
		"-dMonoImageResolution=300",
		"-dConvertCMYKImagesToRGB=true",
		"-dEmbedAllFonts=true",
		"-dNOPAUSE",
		"-dQUIET",
		"-dBATCH",
		"-sOutputFile=" + outputPath,
		path,
	}

	cmd := exec.CommandContext(ctx, g.config.Command, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		_ = os.Remove(outputPath)
		return nil, fmt.Errorf("%s failed: %w, stderr: %s", g.Name(), err, stderr.String())
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s did not create output file", g.Name())
	}

	g.logger.Debug(docId, "%s processed %s -> %s", g.Name(), path, outputPath)
	return &outputPath, nil
}

// Name identifies the engine.
func (g *Ghostscript) Name() string {
	return config.PdfOptimizer.GS
}
