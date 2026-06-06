package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
	_ "modernc.org/sqlite"
)

func consumeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE task (
			id INTEGER PRIMARY KEY,
			task_id TEXT NOT NULL UNIQUE,
			task_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			batch_id TEXT,
			payload JSON,
			result JSON,
			dedup_key TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			completed_at DATETIME,
			error TEXT
		);
		CREATE TABLE document (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			md5_checksum TEXT NOT NULL,
			sha512_checksum TEXT UNIQUE NOT NULL,
			mime_type TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			page_count INTEGER NOT NULL DEFAULT 0,
			word_count INTEGER NOT NULL DEFAULT 0,
			char_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			modified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			document_type_id INTEGER,
			original_path TEXT NOT NULL,
			storage_path TEXT NOT NULL,
			text_content TEXT
		);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}

	return db
}

func cfgWithConsumptionDir(t *testing.T, dir string) *config.Config {
	t.Helper()
	return &config.Config{
		Storage: config.StorageConfig{
			ConsumptionDir: dir,
		},
		Consumer: config.ConsumerConfig{
			SupportedFiles: []string{".pdf"},
			TextExtractor:  config.TextExtractorConfig{Timeout: 5},
			PdfOptimizer:   config.PdfOptimizerConfig{Timeout: 5},
			OCR:            config.OCRConfig{Timeout: 5},
		},
	}
}

func writePDF(t *testing.T, dir, name string) string {
	t.Helper()
	content := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\nxref\n0 3\n0000000000 65535 f \n0000000009 00000 n \n0000000058 00000 n \ntrailer\n<< /Size 3 /Root 1 0 R >>\nstartxref\n120\n%%EOF\n")
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewConsumeHandler(t *testing.T) {
	db := consumeTestDB(t)
	dir := t.TempDir()
	d, err := task.NewDispatcher(cfgWithConsumptionDir(t, dir), utils.NewDiscardLogger(), db, nil)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}

	h := NewConsumeHandler(cfgWithConsumptionDir(t, dir), utils.NewDiscardLogger(), d)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestConsumeHandler_NoFiles(t *testing.T) {
	db := consumeTestDB(t)
	dir := t.TempDir()
	d, err := task.NewDispatcher(cfgWithConsumptionDir(t, dir), utils.NewDiscardLogger(), db, nil)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}

	h := NewConsumeHandler(cfgWithConsumptionDir(t, dir), utils.NewDiscardLogger(), d)

	req := makeReq(t, "POST", "/api/v1/consume", "")
	w := httptest.NewRecorder()
	h.Consume(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["total_files"].(float64) != 0 {
		t.Errorf("total_files = %v, want 0", body["total_files"])
	}
	if body["message"] != "no files found" {
		t.Errorf("message = %v", body["message"])
	}
}

func TestConsumeHandler_WithFiles(t *testing.T) {
	db := consumeTestDB(t)
	dir := t.TempDir()
	d, err := task.NewDispatcher(cfgWithConsumptionDir(t, dir), utils.NewDiscardLogger(), db, nil)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}

	writePDF(t, dir, "doc1.pdf")
	writePDF(t, dir, "doc2.pdf")

	h := NewConsumeHandler(cfgWithConsumptionDir(t, dir), utils.NewDiscardLogger(), d)

	req := makeReq(t, "POST", "/api/v1/consume", "")
	w := httptest.NewRecorder()
	h.Consume(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d", w.Code, http.StatusAccepted)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["total_files"].(float64) != 2 {
		t.Errorf("total_files = %v, want 2", body["total_files"])
	}
	if body["enqueued"].(float64) != 2 {
		t.Errorf("enqueued = %v, want 2", body["enqueued"])
	}
	batchID, ok := body["batch_id"].(string)
	if !ok || batchID == "" {
		t.Errorf("batch_id missing or empty")
	}
	links, ok := body["_links"].(map[string]any)
	if !ok {
		t.Fatal("_links missing")
	}
	tasksLink, ok := links["tasks"].(string)
	if !ok || !strings.Contains(tasksLink, batchID) {
		t.Errorf("tasks link = %v, expected to contain %s", tasksLink, batchID)
	}
}
