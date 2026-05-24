package consumption

import (
	"context"
	"crypto/md5"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/utils"

	_ "modernc.org/sqlite"
)

func setupConsumerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		PRAGMA foreign_keys = OFF;

		CREATE TABLE document_type (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE document (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			md5_checksum TEXT NOT NULL,
			sha512_checksum TEXT UNIQUE NOT NULL,
			mime_type TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			modified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			document_type_id INTEGER,
			original_path TEXT NOT NULL,
			storage_path TEXT NOT NULL,
			text_content TEXT
		);

		CREATE VIRTUAL TABLE document_fts USING fts5(
			document_id UNINDEXED,
			title,
			content,
			tokenize = 'unicode61'
		);

		CREATE TRIGGER document_ai AFTER INSERT ON document
		BEGIN
			INSERT INTO document_fts(document_id, title, content)
			VALUES (new.id, new.title, COALESCE(new.text_content, ''));
		END;
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func createTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func knownChecksums(content string) (md5Hex, sha512Hex string) {
	h := md5.Sum([]byte(content))
	md5Hex = hex.EncodeToString(h[:])
	h2 := sha512.Sum512([]byte(content))
	sha512Hex = hex.EncodeToString(h2[:])
	return
}

type mockTextExtractor struct {
	extractFunc func(ctx context.Context, path string) (*string, error)
}

func (m *mockTextExtractor) Extract(ctx context.Context, path string) (*string, error) {
	if m.extractFunc != nil {
		return m.extractFunc(ctx, path)
	}
	text := "mock extracted text"
	return &text, nil
}
func (m *mockTextExtractor) CanHandle(string) bool { return true }
func (m *mockTextExtractor) Name() string          { return "mock-textextractor" }

type mockOCR struct {
	processFunc func(ctx context.Context, path string) (*string, error)
}

func (m *mockOCR) Process(ctx context.Context, path string) (*string, error) {
	if m.processFunc != nil {
		return m.processFunc(ctx, path)
	}
	return nil, fmt.Errorf("mock OCR not configured")
}
func (m *mockOCR) CanHandle(string) bool { return true }
func (m *mockOCR) Name() string          { return "mock-ocr" }

type mockPdfOptimizer struct {
	optimizeFunc func(ctx context.Context, path string) (*string, error)
}

func (m *mockPdfOptimizer) Optimize(ctx context.Context, path string) (*string, error) {
	if m.optimizeFunc != nil {
		return m.optimizeFunc(ctx, path)
	}
	return nil, fmt.Errorf("mock optimizer not configured")
}
func (m *mockPdfOptimizer) Name() string { return "mock-optimizer" }

func newMockRunner(t *testing.T, logger *utils.Logger, cfg *config.ConsumerConfig,
	textExtractor *mockTextExtractor, ocr *mockOCR, optimizer *mockPdfOptimizer) *tools.Runner {
	t.Helper()
	return tools.NewRunnerWithAdapters(logger, cfg, textExtractor, ocr, optimizer)
}

func TestHumanDuration_Seconds(t *testing.T) {
	got := humanDuration(3 * time.Second)
	if !strings.HasSuffix(got, "s") {
		t.Errorf("humanDuration(%v) = %q, want seconds", 3*time.Second, got)
	}
}

func TestHumanDuration_Minutes(t *testing.T) {
	got := humanDuration(2*time.Minute + 30*time.Second)
	if !strings.Contains(got, "m") || !strings.Contains(got, "s") {
		t.Errorf("humanDuration(%v) = %q, want minutes and seconds", 2*time.Minute+30*time.Second, got)
	}
}

func TestHumanDuration_Hours(t *testing.T) {
	got := humanDuration(1*time.Hour + 5*time.Minute + 10*time.Second)
	if !strings.Contains(got, "h") {
		t.Errorf("humanDuration(%v) = %q, want hours", 1*time.Hour+5*time.Minute+10*time.Second, got)
	}
}

func TestHumanDuration_Zero(t *testing.T) {
	got := humanDuration(0)
	if got != "0.0s" {
		t.Errorf("humanDuration(0) = %q, want %q", got, "0.0s")
	}
}

func TestHumanDuration_EdgeMinute(t *testing.T) {
	got := humanDuration(59*time.Second + 999*time.Millisecond)
	if !strings.HasSuffix(got, "s") {
		t.Errorf("humanDuration near 1m = %q, want seconds", got)
	}
}

func TestCalculateMD5(t *testing.T) {
	content := "hello world\n"
	path := createTempFile(t, t.TempDir(), "test.txt", content)

	got, err := calculateMD5(path)
	if err != nil {
		t.Fatalf("calculateMD5() unexpected error: %v", err)
	}

	want, _ := knownChecksums(content)
	if got != want {
		t.Errorf("calculateMD5() = %q, want %q", got, want)
	}
}

func TestCalculateMD5_NonExistentFile(t *testing.T) {
	_, err := calculateMD5("/tmp/nonexistent-md5-test-file")
	if err == nil {
		t.Fatal("calculateMD5() expected error for non-existent file, got nil")
	}
}

func TestCalculateSHA512(t *testing.T) {
	content := "hello world\n"
	path := createTempFile(t, t.TempDir(), "test.txt", content)

	got, err := calculateSHA512(path)
	if err != nil {
		t.Fatalf("calculateSHA512() unexpected error: %v", err)
	}

	_, want := knownChecksums(content)
	if got != want {
		t.Errorf("calculateSHA512() = %q, want %q", got, want)
	}
}

func TestCalculateChecksums_Consistency(t *testing.T) {
	content := "the quick brown fox jumps over the lazy dog"
	path := createTempFile(t, t.TempDir(), "test.txt", content)

	md5sum, err := calculateMD5(path)
	if err != nil {
		t.Fatal(err)
	}
	sha512sum, err := calculateSHA512(path)
	if err != nil {
		t.Fatal(err)
	}

	wantMD5, wantSHA512 := knownChecksums(content)
	if md5sum != wantMD5 {
		t.Errorf("MD5 mismatch: got %q, want %q", md5sum, wantMD5)
	}
	if sha512sum != wantSHA512 {
		t.Errorf("SHA512 mismatch: got %q, want %q", sha512sum, wantSHA512)
	}
}

func TestIsDuplicate_NoMatch(t *testing.T) {
	db := setupConsumerDB(t)
	logger := utils.NewDiscardLogger()
	cfg := &config.Config{}
	consumer, err := NewConsumerWithRunner(cfg, logger, db, nil)
	if err != nil {
		t.Fatalf("NewConsumerWithRunner: %v", err)
	}

	dir := t.TempDir()
	path := createTempFile(t, dir, "unique.pdf", "unique content")

	dup, err := consumer.isDuplicate(context.Background(), path)
	if err != nil {
		t.Fatalf("isDuplicate() unexpected error: %v", err)
	}
	if dup {
		t.Fatal("isDuplicate() = true, want false")
	}
}

func TestIsDuplicate_ExactMatch(t *testing.T) {
	db := setupConsumerDB(t)
	logger := utils.NewDiscardLogger()
	cfg := &config.Config{}
	consumer, err := NewConsumerWithRunner(cfg, logger, db, nil)
	if err != nil {
		t.Fatalf("NewConsumerWithRunner: %v", err)
	}

	dir := t.TempDir()
	content := "duplicate content"
	path := createTempFile(t, dir, "dup.pdf", content)
	md5sum, sha512sum := knownChecksums(content)

	queries := database.NewQueries(db)
	_, err = queries.CreateDocument(context.Background(), database.CreateDocumentParams{
		Title:          "existing",
		Md5Checksum:    md5sum,
		Sha512Checksum: sha512sum,
		MimeType:       "application/pdf",
		FileSize:       int64(len(content)),
		OriginalPath:   "/dev/null",
		StoragePath:    "/dev/null",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	dup, err := consumer.isDuplicate(context.Background(), path)
	if err != nil {
		t.Fatalf("isDuplicate() unexpected error: %v", err)
	}
	if !dup {
		t.Fatal("isDuplicate() = false, want true")
	}
}

func TestProcess_Duplicate(t *testing.T) {
	db := setupConsumerDB(t)
	logger := utils.NewDiscardLogger()
	dir := t.TempDir()
	content := "process duplicate test"
	path := createTempFile(t, dir, "doc.pdf", content)
	md5sum, sha512sum := knownChecksums(content)

	queries := database.NewQueries(db)
	_, err := queries.CreateDocument(context.Background(), database.CreateDocumentParams{
		Title:          "existing",
		Md5Checksum:    md5sum,
		Sha512Checksum: sha512sum,
		MimeType:       "application/pdf",
		FileSize:       int64(len(content)),
		OriginalPath:   "/dev/null",
		StoragePath:    "/dev/null",
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	cfg := &config.Config{
		Storage: config.StorageConfig{StorageDir: filepath.Join(dir, "storage")},
		Consumer: config.ConsumerConfig{
			OCRLanguages:         []string{"eng"},
			OCRDataDir:           filepath.Join(dir, "tessdata"),
			TextExtractorTimeout: 5,
			OptimizationTimeout:  5,
			OCRTimeout:           5,
		},
	}
	consumer, err := NewConsumerWithRunner(cfg, logger, db, nil)
	if err != nil {
		t.Fatalf("NewConsumerWithRunner: %v", err)
	}

	file, err := FileFromPath(path)
	if err != nil {
		t.Fatalf("FileFromPath: %v", err)
	}

	_, err = consumer.Process(context.Background(), file)
	if err == nil {
		t.Fatal("Process() expected duplicate error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("Process() error = %q, want 'duplicate'", err)
	}
}

func TestProcess_TextExtractAndOptimize(t *testing.T) {
	db := setupConsumerDB(t)
	logger := utils.NewDiscardLogger()
	dir := t.TempDir()
	content := "some document content with enough text density"
	path := createTempFile(t, dir, "doc.pdf", content)

	textExtractor := &mockTextExtractor{
		extractFunc: func(ctx context.Context, p string) (*string, error) {
			text := "extracted text content"
			return &text, nil
		},
	}
	optimizer := &mockPdfOptimizer{
		optimizeFunc: func(ctx context.Context, p string) (*string, error) {
			tmp := filepath.Join(dir, "optimized-output.pdf")
			if err := os.WriteFile(tmp, []byte("optimized"), 0644); err != nil {
				return nil, err
			}
			return &tmp, nil
		},
	}
	ocrMock := &mockOCR{}

	cfg := &config.Config{
		Storage: config.StorageConfig{StorageDir: filepath.Join(dir, "storage")},
		Consumer: config.ConsumerConfig{
			OCRLanguages:         []string{"eng"},
			OCRDataDir:           filepath.Join(dir, "tessdata"),
			TextExtractorTimeout: 5,
			OptimizationTimeout:  5,
			OCRTimeout:           5,
		},
	}

	runner := newMockRunner(t, logger, &cfg.Consumer, textExtractor, ocrMock, optimizer)
	consumer, err := NewConsumerWithRunner(cfg, logger, db, runner)
	if err != nil {
		t.Fatalf("NewConsumerWithRunner: %v", err)
	}

	file, err := FileFromPath(path)
	if err != nil {
		t.Fatalf("FileFromPath: %v", err)
	}

	result, err := consumer.Process(context.Background(), file)
	if err != nil {
		t.Fatalf("Process() unexpected error: %v", err)
	}

	if !result.DocumentID.Valid {
		t.Fatal("Process() returned invalid DocumentID")
	}
	if result.StorageProcessedPath == nil {
		t.Fatal("Process() returned nil StorageProcessedPath")
	}
	if !result.Text.Valid || result.Text.String == "" {
		t.Fatal("Process() expected non-empty Text")
	}

	queries := database.NewQueries(db)
	doc, err := queries.GetDocument(context.Background(), result.DocumentID.Int64)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if doc.Title != file.Name {
		t.Errorf("doc.Title = %q, want %q", doc.Title, file.Name)
	}
	if doc.Md5Checksum != file.MD5Checksum {
		t.Errorf("doc.Md5Checksum mismatch")
	}
}

func TestProcess_OCRFallback(t *testing.T) {
	db := setupConsumerDB(t)
	logger := utils.NewDiscardLogger()
	dir := t.TempDir()
	content := "some content"
	path := createTempFile(t, dir, "scanned.pdf", content)

	ocrOutputPath := filepath.Join(dir, "ocr-output.pdf")
	if err := os.WriteFile(ocrOutputPath, []byte("ocr result"), 0644); err != nil {
		t.Fatal(err)
	}
	postOCRTextPath := filepath.Join(dir, "post-ocr-text.pdf")
	if err := os.WriteFile(postOCRTextPath, []byte("post ocr"), 0644); err != nil {
		t.Fatal(err)
	}

	callCount := 0
	textExtractor := &mockTextExtractor{
		extractFunc: func(ctx context.Context, p string) (*string, error) {
			callCount++
			if callCount == 1 {
				text := ""
				return &text, nil
			}

			text := "ocr extracted text"
			return &text, nil
		},
	}
	ocrMock := &mockOCR{
		processFunc: func(ctx context.Context, p string) (*string, error) {
			return &ocrOutputPath, nil
		},
	}
	optimizer := &mockPdfOptimizer{}

	cfg := &config.Config{
		Storage: config.StorageConfig{StorageDir: filepath.Join(dir, "storage")},
		Consumer: config.ConsumerConfig{
			OCRLanguages:         []string{"eng"},
			OCRDataDir:           filepath.Join(dir, "tessdata"),
			TextExtractorTimeout: 5,
			OptimizationTimeout:  5,
			OCRTimeout:           5,
		},
	}

	runner := newMockRunner(t, logger, &cfg.Consumer, textExtractor, ocrMock, optimizer)
	consumer, err := NewConsumerWithRunner(cfg, logger, db, runner)
	if err != nil {
		t.Fatalf("NewConsumerWithRunner: %v", err)
	}

	file, err := FileFromPath(path)
	if err != nil {
		t.Fatalf("FileFromPath: %v", err)
	}

	result, err := consumer.Process(context.Background(), file)
	if err != nil {
		t.Fatalf("Process() unexpected error: %v", err)
	}

	if !result.DocumentID.Valid {
		t.Fatal("Process() returned invalid DocumentID")
	}
	if result.OCRTmpPath == nil {
		t.Fatal("Process() expected OCR tmp path to be set")
	}
}

func TestNewConsumerWithRunner(t *testing.T) {
	db := setupConsumerDB(t)
	logger := utils.NewDiscardLogger()
	cfg := &config.Config{}

	c, err := NewConsumerWithRunner(cfg, logger, db, nil)
	if err != nil {
		t.Fatalf("NewConsumerWithRunner() unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("NewConsumerWithRunner() returned nil")
	}
}
