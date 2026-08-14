//go:build cgo

package thumbnail

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/mime"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type MuPDF struct {
	logger   *utils.Logger
	config   config.ToolConfig
	dpi      int
	maxWidth int
	quality  int
}

func init() {
	newMuPDF = func(logger *utils.Logger, cfg config.ToolConfig, dpi, maxWidth, quality int) (Thumbnailer, error) {
		return &MuPDF{logger: logger, config: cfg, dpi: dpi, maxWidth: maxWidth, quality: quality}, nil
	}
}

func (m *MuPDF) Name() string {
	return config.Thumbnail.MuPDF
}

func (m *MuPDF) CanHandle(mimeType string) bool {
	return mime.IsPDF(mimeType) || mime.IsImage(mimeType)
}

func (m *MuPDF) Generate(ctx context.Context, docId, path, outputPath string) (width, height int, err error) {
	tmpDir := os.TempDir()
	ogName := filepath.Base(path)
	outputName := fmt.Sprintf(
		"thumb_%s_%d.jpg",
		strings.TrimSuffix(ogName, filepath.Ext(ogName)),
		time.Now().UnixNano(),
	)
	tmpOutputPath := filepath.Join(tmpDir, outputName)

	m.logger.Debug(&docId, "mupdf: thumbnail %s -> %s (PID=%d)", path, tmpOutputPath, os.Getpid())

	cmd := exec.CommandContext(ctx, os.Args[0], "internal-thumbnail",
		"--input", path, "--output", tmpOutputPath,
		"--dpi", strconv.Itoa(m.dpi),
		"--max-width", strconv.Itoa(m.maxWidth),
		"--quality", strconv.Itoa(m.quality))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmpOutputPath)
		return 0, 0, fmt.Errorf("mupdf thumbnail: %w (stderr: %s)", err, stderr.String())
	}

	if _, err := os.Stat(tmpOutputPath); os.IsNotExist(err) {
		return 0, 0, fmt.Errorf("mupdf did not create thumbnail output file")
	}

	// The standalone prints "<width>x<height>" to stdout; missing dimensions
	// are non-fatal, the handler can still move the file into place.
	dims := strings.TrimSpace(stdout.String())
	if w, h, ok := parseDimensions(dims); ok {
		width, height = w, h
	}

	if err := os.Rename(tmpOutputPath, outputPath); err != nil {
		os.Remove(tmpOutputPath)
		return 0, 0, fmt.Errorf("move thumbnail into place: %w", err)
	}

	m.logger.Debug(&docId, "mupdf thumbnail %s -> %s (%dx%d)", path, outputPath, width, height)
	return width, height, nil
}

func parseDimensions(s string) (w, h int, ok bool) {
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, errW := strconv.Atoi(parts[0])
	h, errH := strconv.Atoi(parts[1])
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}
