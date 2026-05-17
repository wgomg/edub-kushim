package commands

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type mockTextExtractor struct {
	texts []*string
	errs  []error
	call  int
}

func (m *mockTextExtractor) Extract(path string) (*string, error) {
	var t *string
	var e error
	if m.call < len(m.texts) {
		t = m.texts[m.call]
	}
	if m.call < len(m.errs) {
		e = m.errs[m.call]
	}
	m.call++
	return t, e
}
func (m *mockTextExtractor) CanHandle(mimeType string) bool { return true }
func (m *mockTextExtractor) Name() string                   { return "mock" }

type mockOCR struct {
	path *string
	err  error
}

func (m *mockOCR) Process(path string) (*string, error) { return m.path, m.err }
func (m *mockOCR) CanHandle(mimeType string) bool       { return true }
func (m *mockOCR) Name() string                         { return "mock" }

type mockPdfOptimizer struct {
	path *string
	err  error
}

func (m *mockPdfOptimizer) Optimize(path string) (*string, error) { return m.path, m.err }
func (m *mockPdfOptimizer) Name() string                          { return "mock" }

func newTestContainerWithConsumer(t *testing.T, db *sql.DB, consumptionDir, storageDir string, textExt *mockTextExtractor, optPath *string, optErr error, ocrPath *string, ocrErr error) (*Container, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger := utils.NewLoggerWithWriter(buf)
	runner := tools.NewRunnerWithAdapters(
		logger,
		&config.ConsumerConfig{},
		textExt,
		&mockOCR{path: ocrPath, err: ocrErr},
		&mockPdfOptimizer{path: optPath, err: optErr},
	)
	consumer := consumption.NewConsumerWithRunner(
		&config.Config{
			Storage: config.StorageConfig{
				ConsumptionDir: consumptionDir,
				StorageDir:     storageDir,
			},
			Consumer: config.ConsumerConfig{
				SupportedFiles:       []string{".pdf"},
				OptimizationFallback: "",
			},
		},
		logger,
		db,
		runner,
	)
	return &Container{
		config: &config.Config{
			Storage: config.StorageConfig{
				ConsumptionDir: consumptionDir,
				StorageDir:     storageDir,
			},
			Consumer: config.ConsumerConfig{
				SupportedFiles: []string{".pdf"},
			},
		},
		logger:   logger,
		db:       db,
		consumer: consumer,
	}, buf
}

func writePDFFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("%PDF-1.4 fake pdf content"), 0644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	return path
}

func TestConsumeHandlerNoFiles(t *testing.T) {
	db := setupCommandsDB(t)
	consumptionDir := t.TempDir()
	storageDir := t.TempDir()
	text := "extracted"
	textExt := &mockTextExtractor{texts: []*string{&text}}
	c, _ := newTestContainerWithConsumer(t, db, consumptionDir, storageDir, textExt, nil, nil, nil, nil)

	// Capture stdout
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	err := consumeHandler(c, []string{})

	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("consumeHandler: %v", err)
	}

	if !strings.Contains(output, "No files found") {
		t.Errorf("expected 'No files found' in output, got: %s", output)
	}
}

func TestConsumeHandlerFilesProcessed(t *testing.T) {
	db := setupCommandsDB(t)
	consumptionDir := t.TempDir()
	storageDir := t.TempDir()
	writePDFFile(t, consumptionDir, "doc.pdf")

	text := "extracted text content"
	optOutput := writePDFFile(t, t.TempDir(), "optimized.pdf")
	textExt := &mockTextExtractor{texts: []*string{&text}}
	c, _ := newTestContainerWithConsumer(t, db, consumptionDir, storageDir, textExt, &optOutput, nil, nil, nil)

	// Capture stdout
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	err := consumeHandler(c, []string{})

	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("consumeHandler: %v", err)
	}

	if !strings.Contains(output, "Batch:") {
		t.Errorf("expected 'Batch:' in output, got: %s", output)
	}
	if !strings.Contains(output, "track progress") {
		t.Errorf("expected progress hint in output, got: %s", output)
	}
}

func TestConsumeHandlerDirNotExist(t *testing.T) {
	db := setupCommandsDB(t)
	storageDir := t.TempDir()
	text := "extracted"
	textExt := &mockTextExtractor{texts: []*string{&text}}
	c, _ := newTestContainerWithConsumer(t, db, "/nonexistent/path", storageDir, textExt, nil, nil, nil, nil)

	err := consumeHandler(c, []string{})
	if err == nil {
		t.Fatal("expected error for missing consumption dir")
	}
}
