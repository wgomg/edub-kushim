package pdfoptimizer

import (
	"bytes"
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

func (g *Ghostscript) Optimize(path string) (*string, error) {
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

	cmd := exec.Command(g.config.Command, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case err := <-done:
		if err != nil {
			os.Remove(outputPath)
			return nil, fmt.Errorf("%s failed: %w, stderr: %s", g.Name(), err, stderr.String())
		}
	case <-time.After(g.config.Timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		os.Remove(outputPath)
		return nil, fmt.Errorf("%s timed out after %v", g.Name(), g.config.Timeout)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s did not create output file", g.Name())
	}

	g.logger.Debug(nil, "%s processed %s -> %s", g.Name(), path, outputPath)
	return &outputPath, nil
}

func (g *Ghostscript) Name() string {
	return "ghostscript"
}
