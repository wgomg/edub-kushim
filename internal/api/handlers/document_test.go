package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/utils"
	_ "modernc.org/sqlite"
)

func contains(ss []string, s string) bool {
	return slices.Contains(ss, s)
}

func docHandlerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE document (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			md5_checksum TEXT NOT NULL,
			sha512_checksum TEXT UNIQUE NOT NULL,
			mime_type TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			page_count INTEGER NOT NULL DEFAULT 0,
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
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func seedDoc(t *testing.T, q *database.Queries, title, md5, sha512 string) int64 {
	t.Helper()
	result, err := q.CreateDocument(context.Background(), database.CreateDocumentParams{
		Title:          title,
		Md5Checksum:    md5,
		Sha512Checksum: sha512,
		MimeType:       "application/pdf",
		FileSize:       100,
		PageCount:      0,
		OriginalPath:   "/" + title,
		StoragePath:    "/store/" + title,
	})
	if err != nil {
		t.Fatalf("seedDoc: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func docHandlerRequest(t *testing.T, method, target string, body string) *http.Request {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	ctx := context.WithValue(req.Context(), "reqid", "test-req")
	req = req.WithContext(ctx)
	pb := utils.NewParamBag(req)
	req = utils.WithParamBag(req, pb)
	return req
}

func TestNewDocumentHandler(t *testing.T) {
	db := docHandlerTestDB(t)
	q := database.New(db)
	logger := utils.NewDiscardLogger()
	e := search.NewEngine(logger, db)

	h := NewDocumentHandler(q, logger, e)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestListDocuments_Empty(t *testing.T) {
	db := docHandlerTestDB(t)
	q := database.New(db)
	logger := utils.NewDiscardLogger()
	e := search.NewEngine(logger, db)
	h := NewDocumentHandler(q, logger, e)

	w := httptest.NewRecorder()
	req := docHandlerRequest(t, "GET", "/documents", "")
	h.ListDocuments(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var results []types.DocumentResponse
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestListDocuments_WithResults(t *testing.T) {
	db := docHandlerTestDB(t)
	q := database.New(db)
	logger := utils.NewDiscardLogger()
	e := search.NewEngine(logger, db)
	h := NewDocumentHandler(q, logger, e)

	seedDoc(t, q, "doc1", "md5-1", "sha512-1")
	seedDoc(t, q, "doc2", "md5-2", "sha512-2")

	w := httptest.NewRecorder()
	req := docHandlerRequest(t, "GET", "/documents", "")
	h.ListDocuments(w, req)

	var results []types.DocumentResponse
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	var titles []string
	for _, d := range results {
		titles = append(titles, d.Title)
	}
	if !contains(titles, "doc1") || !contains(titles, "doc2") {
		t.Errorf("results contain %v, want both doc1 and doc2", titles)
	}
}

func TestGetDocument_Success(t *testing.T) {
	db := docHandlerTestDB(t)
	q := database.New(db)
	logger := utils.NewDiscardLogger()
	e := search.NewEngine(logger, db)
	h := NewDocumentHandler(q, logger, e)

	id := seedDoc(t, q, "found", "md5-f", "sha512-f")

	w := httptest.NewRecorder()
	req := docHandlerRequest(t, "GET", "/documents/"+strconv.FormatInt(id, 10), "")
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	h.GetDocument(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var doc types.DocumentResponse
	if err := json.NewDecoder(w.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Title != "found" {
		t.Errorf("Title = %q", doc.Title)
	}
}

func TestGetDocument_NotFound(t *testing.T) {
	db := docHandlerTestDB(t)
	q := database.New(db)
	logger := utils.NewDiscardLogger()
	e := search.NewEngine(logger, db)
	h := NewDocumentHandler(q, logger, e)

	w := httptest.NewRecorder()
	req := docHandlerRequest(t, "GET", "/documents/999", "")
	req.SetPathValue("id", "999")
	h.GetDocument(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetDocument_InvalidID(t *testing.T) {
	db := docHandlerTestDB(t)
	q := database.New(db)
	logger := utils.NewDiscardLogger()
	e := search.NewEngine(logger, db)
	h := NewDocumentHandler(q, logger, e)

	w := httptest.NewRecorder()
	req := docHandlerRequest(t, "GET", "/documents/abc", "")
	req.SetPathValue("id", "abc")
	h.GetDocument(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestSearchDocuments_NoQuery(t *testing.T) {
	db := docHandlerTestDB(t)
	q := database.New(db)
	logger := utils.NewDiscardLogger()
	e := search.NewEngine(logger, db)
	h := NewDocumentHandler(q, logger, e)

	w := httptest.NewRecorder()
	req := docHandlerRequest(t, "GET", "/documents/search", "")
	h.SearchDocuments(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestSearchDocuments_WithResults(t *testing.T) {
	db := docHandlerTestDB(t)
	q := database.New(db)
	logger := utils.NewDiscardLogger()
	e := search.NewEngine(logger, db)
	h := NewDocumentHandler(q, logger, e)

	id := seedDoc(t, q, "results", "md5-r", "sha512-r")
	if err := q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{id, "results", "lorem ipsum text"}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	req := docHandlerRequest(t, "GET", "/documents/search?q=lorem", "")
	h.SearchDocuments(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var results []types.FTSDocumentResponse
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Title != "results" {
		t.Errorf("Title = %q", results[0].Title)
	}
}
