package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/utils"
)

var schemaSQL = func() string {
	data, err := os.ReadFile("../../../sql/schema.sql")
	if err != nil {
		panic("cannot read schema.sql: " + err.Error())
	}
	return string(data)
}()

func setupTestDB(t *testing.T) *sql.DB {
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

func seedDocuments(t *testing.T, db *sql.DB) {
	t.Helper()
	queries := database.NewQueries(db)
	ctx := context.Background()

	docs := []struct {
		title, md5, sha512, mime, text string
		size                           int64
	}{
		{"doc1.pdf", "md5-1", "sha512-1", "application/pdf", "quantum mechanics basics", 100},
		{"doc2.pdf", "md5-2", "sha512-2", "application/pdf", "quantum physics advanced", 200},
		{"doc3.pdf", "md5-3", "sha512-3", "text/plain", "cell biology intro", 300},
	}

	for _, d := range docs {
		_, err := queries.CreateDocument(ctx, database.CreateDocumentParams{
			Title:          d.title,
			Md5Checksum:    d.md5,
			Sha512Checksum: d.sha512,
			MimeType:       d.mime,
			FileSize:       d.size,
			OriginalPath:   "/orig/" + d.title,
			StoragePath:    "/store/" + d.title,
			TextContent:    sql.NullString{String: d.text, Valid: true},
		})
		if err != nil {
			t.Fatalf("seed doc %s: %v", d.title, err)
		}
	}
}

func newTestRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, url, nil)
	req = req.WithContext(context.WithValue(req.Context(), "reqid", "test-reqid"))
	pb := utils.NewParamBag(req)
	req = utils.WithParamBag(req, pb)
	return req
}

func decodeResponse(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func TestListDocuments(t *testing.T) {
	db := setupTestDB(t)
	seedDocuments(t, db)
	handler := NewDocumentHandler(database.NewQueries(db), utils.NewDiscardLogger(), search.NewEngine(utils.NewDiscardLogger(), db))

	req := newTestRequest(t, "GET", "/api/v1/documents?limit=10&offset=0")
	w := httptest.NewRecorder()
	handler.ListDocuments(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var docs []types.DocumentResponse
	decodeResponse(t, w.Body.Bytes(), &docs)

	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(docs))
	}

	titles := map[string]bool{}
	for _, d := range docs {
		titles[d.Title] = true
	}
	if !titles["doc1.pdf"] || !titles["doc2.pdf"] || !titles["doc3.pdf"] {
		t.Errorf("expected all three docs, got %v", docs)
	}
}

func TestListDocumentsPagination(t *testing.T) {
	db := setupTestDB(t)
	seedDocuments(t, db)
	handler := NewDocumentHandler(database.NewQueries(db), utils.NewDiscardLogger(), search.NewEngine(utils.NewDiscardLogger(), db))

	req := newTestRequest(t, "GET", "/api/v1/documents?limit=1&offset=0")
	w := httptest.NewRecorder()
	handler.ListDocuments(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var docs []types.DocumentResponse
	decodeResponse(t, w.Body.Bytes(), &docs)

	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].Title == "" {
		t.Error("expected non-empty title")
	}
}

func TestListDocumentsDefaultLimit(t *testing.T) {
	db := setupTestDB(t)
	seedDocuments(t, db)
	handler := NewDocumentHandler(database.NewQueries(db), utils.NewDiscardLogger(), search.NewEngine(utils.NewDiscardLogger(), db))

	req := newTestRequest(t, "GET", "/api/v1/documents")
	w := httptest.NewRecorder()
	handler.ListDocuments(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var docs []types.DocumentResponse
	decodeResponse(t, w.Body.Bytes(), &docs)

	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(docs))
	}
}

func TestGetDocument(t *testing.T) {
	db := setupTestDB(t)
	seedDocuments(t, db)
	handler := NewDocumentHandler(database.NewQueries(db), utils.NewDiscardLogger(), search.NewEngine(utils.NewDiscardLogger(), db))

	req := newTestRequest(t, "GET", "/api/v1/documents/1")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler.GetDocument(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var doc types.DocumentResponse
	decodeResponse(t, w.Body.Bytes(), &doc)

	if doc.ID != 1 {
		t.Errorf("expected ID 1, got %d", doc.ID)
	}
	if doc.Title != "doc1.pdf" {
		t.Errorf("expected doc1.pdf, got %s", doc.Title)
	}
	if doc.MD5Checksum != "md5-1" {
		t.Errorf("expected md5-1, got %s", doc.MD5Checksum)
	}
}

func TestGetDocumentNotFound(t *testing.T) {
	db := setupTestDB(t)
	seedDocuments(t, db)
	handler := NewDocumentHandler(database.NewQueries(db), utils.NewDiscardLogger(), search.NewEngine(utils.NewDiscardLogger(), db))

	req := newTestRequest(t, "GET", "/api/v1/documents/999")
	req.SetPathValue("id", "999")
	w := httptest.NewRecorder()
	handler.GetDocument(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetDocumentInvalidID(t *testing.T) {
	db := setupTestDB(t)
	handler := NewDocumentHandler(database.NewQueries(db), utils.NewDiscardLogger(), search.NewEngine(utils.NewDiscardLogger(), db))

	req := newTestRequest(t, "GET", "/api/v1/documents/abc")
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()
	handler.GetDocument(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSearchDocuments(t *testing.T) {
	db := setupTestDB(t)
	seedDocuments(t, db)
	handler := NewDocumentHandler(database.NewQueries(db), utils.NewDiscardLogger(), search.NewEngine(utils.NewDiscardLogger(), db))

	req := newTestRequest(t, "GET", "/api/v1/documents/search?q=quantum&limit=10&offset=0")
	w := httptest.NewRecorder()
	handler.SearchDocuments(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []types.FTSDocumentResponse
	decodeResponse(t, w.Body.Bytes(), &results)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	ids := map[int64]bool{}
	for _, r := range results {
		ids[r.ID] = true
	}
	if !ids[1] || !ids[2] {
		t.Errorf("expected docs 1 and 2, got %v", ids)
	}
}

func TestSearchDocumentsNoQuery(t *testing.T) {
	db := setupTestDB(t)
	seedDocuments(t, db)
	handler := NewDocumentHandler(database.NewQueries(db), utils.NewDiscardLogger(), search.NewEngine(utils.NewDiscardLogger(), db))

	req := newTestRequest(t, "GET", "/api/v1/documents/search")
	w := httptest.NewRecorder()
	handler.SearchDocuments(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSearchDocumentsNoResults(t *testing.T) {
	db := setupTestDB(t)
	seedDocuments(t, db)
	handler := NewDocumentHandler(database.NewQueries(db), utils.NewDiscardLogger(), search.NewEngine(utils.NewDiscardLogger(), db))

	req := newTestRequest(t, "GET", "/api/v1/documents/search?q=nonexistent")
	w := httptest.NewRecorder()
	handler.SearchDocuments(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []types.FTSDocumentResponse
	decodeResponse(t, w.Body.Bytes(), &results)

	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchDocumentsSnippet(t *testing.T) {
	db := setupTestDB(t)
	seedDocuments(t, db)
	handler := NewDocumentHandler(database.NewQueries(db), utils.NewDiscardLogger(), search.NewEngine(utils.NewDiscardLogger(), db))

	req := newTestRequest(t, "GET", "/api/v1/documents/search?q=quantum&limit=1&offset=0")
	w := httptest.NewRecorder()
	handler.SearchDocuments(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []types.FTSDocumentResponse
	decodeResponse(t, w.Body.Bytes(), &results)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
	if results[0].Rank == 0 {
		t.Error("expected non-zero rank")
	}
}

type mockConsumerTextExtractor struct{}

func (m *mockConsumerTextExtractor) Extract(path string) (*string, error) {
	t := "mock extracted text"
	return &t, nil
}
func (m *mockConsumerTextExtractor) CanHandle(mimeType string) bool { return true }
func (m *mockConsumerTextExtractor) Name() string                   { return "mock" }

// mockConsumerOCR implements ocr.OCR interface
type mockConsumerOCR struct{}

func (m *mockConsumerOCR) Process(path string) (*string, error) { return nil, nil }
func (m *mockConsumerOCR) CanHandle(mimeType string) bool       { return true }
func (m *mockConsumerOCR) Name() string                         { return "mock" }

// mockConsumerPdfOptimizer implements pdfoptimizer.PdfOptimizer interface
type mockConsumerPdfOptimizer struct{}

func (m *mockConsumerPdfOptimizer) Optimize(path string) (*string, error) {
	dir := filepath.Dir(path)
	optPath := filepath.Join(dir, "optimized.pdf")
	if err := os.WriteFile(optPath, []byte("optimized"), 0644); err != nil {
		return nil, fmt.Errorf("write optimized: %w", err)
	}
	return &optPath, nil
}
func (m *mockConsumerPdfOptimizer) Name() string { return "mock" }

func setupTestConsumer(t *testing.T, db *sql.DB, inboxDir string) *consumption.Consumer {
	t.Helper()
	runner := tools.NewRunnerWithAdapters(
		utils.NewDiscardLogger(),
		&config.ConsumerConfig{},
		&mockConsumerTextExtractor{},
		&mockConsumerOCR{},
		&mockConsumerPdfOptimizer{},
	)
	cfg := &config.Config{
		Storage: config.StorageConfig{
			ConsumptionDir: inboxDir,
			StorageDir:     t.TempDir(),
		},
		Consumer: config.ConsumerConfig{
			SupportedFiles: []string{".pdf"},
		},
	}
	return consumption.NewConsumerWithRunner(cfg, utils.NewDiscardLogger(), db, runner)
}

func TestConsumeSuccess(t *testing.T) {
	db := setupTestDB(t)
	inbox := t.TempDir()

	pdfPath := filepath.Join(inbox, "test.pdf")
	if err := os.WriteFile(pdfPath, []byte("pdf content"), 0644); err != nil {
		t.Fatalf("write test pdf: %v", err)
	}

	consumer := setupTestConsumer(t, db, inbox)
	handler := NewConsumeHandler(consumer, utils.NewDiscardLogger())

	req := httptest.NewRequest("POST", "/api/v1/consume", nil)
	req = req.WithContext(context.WithValue(req.Context(), "reqid", "test-reqid"))
	w := httptest.NewRecorder()
	handler.Consume(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %s", resp["status"])
	}
}

func TestConsumeNoFiles(t *testing.T) {
	db := setupTestDB(t)
	inbox := t.TempDir()

	consumer := setupTestConsumer(t, db, inbox)
	handler := NewConsumeHandler(consumer, utils.NewDiscardLogger())

	req := httptest.NewRequest("POST", "/api/v1/consume", nil)
	req = req.WithContext(context.WithValue(req.Context(), "reqid", "test-reqid"))
	w := httptest.NewRecorder()
	handler.Consume(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %s", resp["status"])
	}
}

func TestConsumeWithInboxFile(t *testing.T) {
	db := setupTestDB(t)
	inbox := t.TempDir()

	pdfPath := filepath.Join(inbox, "doc.pdf")
	if err := os.WriteFile(pdfPath, []byte("some content"), 0644); err != nil {
		t.Fatalf("write test pdf: %v", err)
	}

	consumer := setupTestConsumer(t, db, inbox)
	handler := NewConsumeHandler(consumer, utils.NewDiscardLogger())

	req := httptest.NewRequest("POST", "/api/v1/consume", nil)
	req = req.WithContext(context.WithValue(req.Context(), "reqid", "test-reqid"))
	w := httptest.NewRecorder()
	handler.Consume(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	decodeResponse(t, w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %s", resp["status"])
	}
}
