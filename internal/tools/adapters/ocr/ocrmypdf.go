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
	logger    *utils.Logger
	config    config.ToolConfig
	languages []string
}

func NewOcrMyPdf(logger *utils.Logger, cfg config.ToolConfig, languages []string) (*OcrMyPdf, error) {
	if _, err := exec.LookPath(cfg.Command); err != nil {
		return nil, fmt.Errorf("%s not found in PATH: %w\nInstall it (e.g. 'sudo apt install ocrmypdf' or 'brew install ocrmypdf') or switch to the built-in 'gosseract' engine", cfg.Command, err)
	}

	if len(languages) == 0 {
		languages = []string{"eng"}
	}

	return &OcrMyPdf{logger: logger, config: cfg, languages: languages}, nil
}

func (o *OcrMyPdf) Process(ctx context.Context, docId, path string) (*string, error) {
	tmpDir := os.TempDir()
	ogName := filepath.Base(path)
	outputName := fmt.Sprintf(
		"ocr_%s_%d.pdf",
		strings.TrimSuffix(ogName, filepath.Ext(ogName)),
		time.Now().Unix(),
	)
	outputPath := filepath.Join(tmpDir, outputName)

	args := []string{
		"--language", strings.Join(o.languages, "+"),
		"--output-type", "pdfa-2",
		"--optimize", "2",
		"--rotate-pages",
		"--deskew",
		"--clean",
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
		stderrStr := stderr.String()
		hint := ""
		if strings.Contains(stderrStr, "read_params_file") || strings.Contains(stderrStr, "couldn't load languages") || strings.Contains(stderrStr, "Can't open") {
			hint = fmt.Sprintf("ensure the tesseract language packs are installed for %v (e.g. 'sudo apt install tesseract-ocr-eng')", o.languages)
		} else if strings.Contains(stderrStr, "unpaper") || strings.Contains(stderrStr, "--clean") {
			hint = "install unpaper (e.g. 'sudo apt install unpaper') or disable the --clean option"
		} else if strings.Contains(stderrStr, "pngquant") || strings.Contains(stderrStr, "optipng") || strings.Contains(stderrStr, "jbig2") || strings.Contains(stderrStr, "--optimize") {
			hint = "install pngquant (e.g. 'sudo apt install pngquant') for image optimization, or ocrmypdf will skip it"
		} else if strings.Contains(stderrStr, "tesseract") && strings.Contains(stderrStr, "not found") {
			hint = "install tesseract (e.g. 'sudo apt install tesseract-ocr')"
		}
		if hint != "" {
			return nil, fmt.Errorf("%s failed: %w, stderr: %s\n... %s", o.Name(), err, stderrStr, hint)
		}
		return nil, fmt.Errorf("%s failed: %w, stderr: %s", o.Name(), err, stderrStr)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s did not create output file", o.Name())
	}

	o.logger.Debug(&docId, "%s processed %s -> %s", o.Name(), path, outputPath)
	return &outputPath, nil
}

func (o *OcrMyPdf) CanHandle(mimeType string) bool {
	return mimeType == "application/pdf"
}

func (o *OcrMyPdf) Name() string {
	return config.OCR.OcrMyPdf
}
