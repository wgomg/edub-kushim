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
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Ghostscript struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewGhostscript(logger *utils.Logger, cfg config.ToolConfig) (*Ghostscript, error) {
	if _, err := exec.LookPath(cfg.Command); err != nil {
		return nil, fmt.Errorf("%s not found in PATH: %w", cfg.Command, err)
	}

	return &Ghostscript{logger: logger, config: cfg}, nil
}

func (g *Ghostscript) Optimize(ctx context.Context, docId, path string) (*string, error) {
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
		os.Remove(outputPath)
		return nil, fmt.Errorf("%s failed: %w, stderr: %s", g.Name(), err, stderr.String())
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s did not create output file", g.Name())
	}

	g.logger.Debug(&docId, "%s processed %s -> %s", g.Name(), path, outputPath)
	return &outputPath, nil
}

func (g *Ghostscript) Name() string {
	return config.PdfOptimizer.GS
}
