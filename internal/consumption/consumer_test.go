package consumption

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/utils"
)

var schemaSQL = func() string {
	data, err := os.ReadFile("../../sql/schema.sql")
	if err != nil {
		panic("cannot read schema.sql: " + err.Error())
	}
	return string(data)
}()

func setupConsumerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pdf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		input    time.Duration
		expected string
	}{
		{0, "0.0s"},
		{500 * time.Millisecond, "0.5s"},
		{59 * time.Second, "59.0s"},
		{60 * time.Second, "1m 0s"},
		{90 * time.Second, "1m 30s"},
		{3600 * time.Second, "1h 0m 0s"},
	}
	for _, tt := range tests {
		got := humanDuration(tt.input)
		if got != tt.expected {
			t.Errorf("humanDuration(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsDuplicateTrue(t *testing.T) {
	db := setupConsumerDB(t)
	content := "duplicate test content"
	path := writeTempFile(t, content)

	md5sum, _ := calculateMD5(path)
	sha512sum, _ := calculateSHA512(path)

	queries := database.NewQueries(db)
	ctx := context.Background()
	queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title:          "existing.pdf",
		Md5Checksum:    md5sum,
		Sha512Checksum: sha512sum,
		MimeType:       "application/pdf",
		FileSize:       100,
		OriginalPath:   "/existing",
		StoragePath:    "/existing",
	})

	c := &Consumer{
		config: &config.Config{},
		logger: utils.NewLogger("info"),
		db:     db,
	}
	dup, err := c.isDuplicate(path)
	if err != nil {
		t.Fatalf("isDuplicate: %v", err)
	}
	if !dup {
		t.Error("expected duplicate, got false")
	}
}

func TestIsDuplicateFalse(t *testing.T) {
	db := setupConsumerDB(t)
	path := writeTempFile(t, "unique content")

	c := &Consumer{
		config: &config.Config{},
		logger: utils.NewLogger("info"),
		db:     db,
	}
	dup, err := c.isDuplicate(path)
	if err != nil {
		t.Fatalf("isDuplicate: %v", err)
	}
	if dup {
		t.Error("expected not duplicate, got true")
	}
}

func TestIsDuplicateMD5Only(t *testing.T) {
	db := setupConsumerDB(t)
	content := "md5 collision test"
	path := writeTempFile(t, content)

	md5sum, _ := calculateMD5(path)

	queries := database.NewQueries(db)
	ctx := context.Background()
	queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title:          "existing.pdf",
		Md5Checksum:    md5sum,
		Sha512Checksum: "different-sha512",
		MimeType:       "application/pdf",
		FileSize:       100,
		OriginalPath:   "/existing",
		StoragePath:    "/existing",
	})

	c := &Consumer{
		config: &config.Config{},
		logger: utils.NewLogger("info"),
		db:     db,
	}
	dup, err := c.isDuplicate(path)
	if err != nil {
		t.Fatalf("isDuplicate: %v", err)
	}
	if dup {
		t.Error("expected not duplicate (SHA512 mismatch), got true")
	}
}

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

func makeConsumerWithMocks(t *testing.T, textExt *mockTextExtractor, optPath *string, optErr error, ocrPath *string, ocrErr error) *Consumer {
	t.Helper()
	runner := tools.NewRunnerWithAdapters(
		utils.NewLogger("info"),
		&config.ConsumerConfig{},
		textExt,
		&mockOCR{path: ocrPath, err: ocrErr},
		&mockPdfOptimizer{path: optPath, err: optErr},
	)
	return NewConsumerWithRunner(&config.Config{}, utils.NewLogger("info"), nil, runner)
}

func TestExtractTextSuccess(t *testing.T) {
	text := "extracted text content"
	optPath := "/tmp/optimized.pdf"
	textExt := &mockTextExtractor{texts: []*string{&text}}
	c := makeConsumerWithMocks(t, textExt, &optPath, nil, nil, nil)

	path := writeTempFile(t, "pdf content")
	file := File{Name: "test.pdf", OriginalPath: path, FileSize: 100}
	result, err := c.extractText(file)
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if !result.Text.Valid || result.Text.String != text {
		t.Errorf("expected text %q, got %v", text, result.Text)
	}
	if result.OptimizedPdfTmpPath == nil || *result.OptimizedPdfTmpPath != optPath {
		t.Errorf("expected OptimizedPdfTmpPath %q, got %v", optPath, result.OptimizedPdfTmpPath)
	}
	if result.OCRTmpPath != nil {
		t.Error("expected nil OCRTmpPath")
	}
}

func TestExtractTextOptimizationFails(t *testing.T) {
	text := "extracted text content"
	textExt := &mockTextExtractor{texts: []*string{&text}}
	c := makeConsumerWithMocks(t, textExt, nil, fmt.Errorf("optimization failed"), nil, nil)

	path := writeTempFile(t, "pdf content")
	file := File{Name: "test.pdf", OriginalPath: path, FileSize: 100}
	result, err := c.extractText(file)
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if !result.Text.Valid || result.Text.String != text {
		t.Errorf("expected text %q, got %v", text, result.Text)
	}
	if result.OptimizedPdfTmpPath != nil {
		t.Error("expected nil OptimizedPdfTmpPath after optimization failure")
	}
}

func TestExtractTextEmptyFallsToOCR(t *testing.T) {
	empty := ""
	ocrText := "ocr extracted text"
	ocrPath := writeTempFile(t, "ocr-output")
	textExt := &mockTextExtractor{texts: []*string{&empty, &ocrText}}
	c := makeConsumerWithMocks(t, textExt, nil, nil, &ocrPath, nil)

	path := writeTempFile(t, "scanned pdf content")
	file := File{Name: "scan.pdf", OriginalPath: path, FileSize: 100}
	result, err := c.extractText(file)
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if !result.Text.Valid || result.Text.String != ocrText {
		t.Errorf("expected OCR text %q, got %v", ocrText, result.Text)
	}
	if result.OCRTmpPath == nil || *result.OCRTmpPath != ocrPath {
		t.Errorf("expected OCRTmpPath %q, got %v", ocrPath, result.OCRTmpPath)
	}
}

func TestExtractTextOCRFails(t *testing.T) {
	empty := ""
	textExt := &mockTextExtractor{texts: []*string{&empty}}
	c := makeConsumerWithMocks(t, textExt, nil, nil, nil, fmt.Errorf("OCR failed"))

	path := writeTempFile(t, "scanned pdf content")
	file := File{Name: "scan.pdf", OriginalPath: path, FileSize: 100}
	_, err := c.extractText(file)
	if err == nil {
		t.Fatal("expected error from OCR failure, got nil")
	}
}

func TestExtractTextOCRNoText(t *testing.T) {
	empty := ""
	ocrPath := writeTempFile(t, "ocr-output")
	textExt := &mockTextExtractor{texts: []*string{&empty, &empty}}
	c := makeConsumerWithMocks(t, textExt, nil, nil, &ocrPath, nil)

	path := writeTempFile(t, "scanned pdf content")
	file := File{Name: "scan.pdf", OriginalPath: path, FileSize: 100}
	result, err := c.extractText(file)
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if result.Text.Valid {
		t.Error("expected Text.Valid == false")
	}
	if result.OCRTmpPath == nil || *result.OCRTmpPath != ocrPath {
		t.Errorf("expected OCRTmpPath %q, got %v", ocrPath, result.OCRTmpPath)
	}
}

func makeConsumerForProcess(t *testing.T, db *sql.DB, textExt *mockTextExtractor, optPath *string, optErr error, ocrPath *string, ocrErr error) *Consumer {
	t.Helper()
	runner := tools.NewRunnerWithAdapters(
		utils.NewLogger("info"),
		&config.ConsumerConfig{},
		textExt,
		&mockOCR{path: ocrPath, err: ocrErr},
		&mockPdfOptimizer{path: optPath, err: optErr},
	)
	cfg := &config.Config{
		Storage: config.StorageConfig{
			StorageDir: t.TempDir(),
		},
	}
	return NewConsumerWithRunner(cfg, utils.NewLogger("info"), db, runner)
}

func TestProcessOCRPath(t *testing.T) {
	db := setupConsumerDB(t)
	empty := ""
	ocrText := "ocr extracted text"
	ocrOutput := writeTempFile(t, "ocr-output-content")
	textExt := &mockTextExtractor{texts: []*string{&empty, &ocrText}}
	c := makeConsumerForProcess(t, db, textExt, nil, nil, &ocrOutput, nil)

	original := writeTempFile(t, "scanned document")
	file := File{
		Name:         "scan.pdf",
		OriginalPath: original,
		MimeType:     "application/pdf",
		FileSize:     100,
		Date:         time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
	}

	result, err := c.Process(file)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if result.StorageProcessedPath == nil {
		t.Fatal("expected StorageProcessedPath")
	}
	if _, err := os.Stat(*result.StorageProcessedPath); os.IsNotExist(err) {
		t.Error("processed file does not exist")
	}

	if result.StorageOriginalPath == nil {
		t.Fatal("expected StorageOriginalPath")
	}
	if _, err := os.Stat(*result.StorageOriginalPath); os.IsNotExist(err) {
		t.Error("original file does not exist")
	}
}

func TestProcessOptimizedPath(t *testing.T) {
	db := setupConsumerDB(t)
	text := "extracted text"
	optOutput := writeTempFile(t, "optimized-content")
	textExt := &mockTextExtractor{texts: []*string{&text}}
	c := makeConsumerForProcess(t, db, textExt, &optOutput, nil, nil, nil)

	original := writeTempFile(t, "pdf document")
	file := File{
		Name:         "doc.pdf",
		OriginalPath: original,
		MimeType:     "application/pdf",
		FileSize:     100,
		Date:         time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
	}

	result, err := c.Process(file)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if result.StorageProcessedPath == nil {
		t.Fatal("expected StorageProcessedPath")
	}
	if _, err := os.Stat(*result.StorageProcessedPath); os.IsNotExist(err) {
		t.Error("processed file does not exist")
	}

	if result.StorageOriginalPath == nil {
		t.Fatal("expected StorageOriginalPath")
	}
	if _, err := os.Stat(*result.StorageOriginalPath); os.IsNotExist(err) {
		t.Error("original file does not exist")
	}
}

func TestProcessUnoptimizedPath(t *testing.T) {
	db := setupConsumerDB(t)
	text := "extracted text"
	textExt := &mockTextExtractor{texts: []*string{&text}}
	c := makeConsumerForProcess(t, db, textExt, nil, fmt.Errorf("optimization failed"), nil, nil)

	original := writeTempFile(t, "pdf document")
	file := File{
		Name:         "doc.pdf",
		OriginalPath: original,
		MimeType:     "application/pdf",
		FileSize:     100,
		Date:         time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
	}

	result, err := c.Process(file)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if result.StorageProcessedPath == nil {
		t.Fatal("expected StorageProcessedPath")
	}
	if _, err := os.Stat(*result.StorageProcessedPath); os.IsNotExist(err) {
		t.Error("processed file does not exist")
	}

	if result.StorageOriginalPath == nil {
		t.Fatal("expected StorageOriginalPath")
	}
	if _, err := os.Stat(*result.StorageOriginalPath); os.IsNotExist(err) {
		t.Error("original file does not exist")
	}
}
