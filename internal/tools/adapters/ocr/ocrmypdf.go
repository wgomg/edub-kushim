package ocr

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

type OcrMyPdf struct {
	logger *utils.Logger
	config config.ToolConfig
}

func NewOcrMyPdf(logger *utils.Logger, cfg config.ToolConfig) (*OcrMyPdf, error) {
	if _, err := exec.LookPath(cfg.Command); err != nil {
		return nil, fmt.Errorf("%s not found in PATH: %w", cfg.Command, err)
	}

	return &OcrMyPdf{logger: logger, config: cfg}, nil
}

func (o *OcrMyPdf) Process(ctx context.Context, path string) (*string, error) {
	tmpDir := os.TempDir()
	ogName := filepath.Base(path)
	outputName := fmt.Sprintf(
		"ocr_%s_%d.pdf",
		strings.TrimSuffix(ogName, filepath.Ext(ogName)),
		time.Now().Unix(),
	)
	outputPath := filepath.Join(tmpDir, outputName)

	args := []string{
		"--output-type", "pdfa-2",
		"--optimize", "2",
		"--rotate-pages",
		"--deskew",
		"--clean",
		"--remove-background",
		"--pdfa-image-compression", "jpeg",
		"--jpeg-quality", "85",
		"--png-quality", "85",
		"--oversample", "150",
		path,
		outputPath,
	}

	cmd := exec.CommandContext(ctx, o.config.Command, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if _, statErr := os.Stat(outputPath); statErr == nil {
			os.Remove(outputPath)
		}
		return nil, fmt.Errorf("%s failed: %w, stderr: %s", o.Name(), err, stderr.String())
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s did not create output file", o.Name())
	}

	o.logger.Debug(nil, "%s processed %s -> %s", o.Name(), path, outputPath)
	return &outputPath, nil
}

func (o *OcrMyPdf) CanHandle(mimeType string) bool {
	return mimeType == "application/pdf"
}

func (o *OcrMyPdf) Name() string {
	return "ocrmypdf"
}
