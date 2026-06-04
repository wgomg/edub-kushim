package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/utils"
	_ "modernc.org/sqlite"
)

func serverTestDB(t *testing.T) *sql.DB {
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
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			modified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			document_type_id INTEGER,
			original_path TEXT NOT NULL,
			storage_path TEXT NOT NULL,
			text_content TEXT
		);
		CREATE VIRTUAL TABLE document_fts USING fts5(
			document_id UNINDEXED, title, content, tokenize = 'unicode61'
		);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		Srv: config.ServerConfig{
			Host: "127.0.0.1",
			Port: 0,
		},
		Consumer: config.ConsumerConfig{
			Workers:        1,
			SupportedFiles: []string{".pdf"},
			TextExtractor:  config.TextExtractorConfig{Timeout: 5},
			PdfOptimizer:   config.PdfOptimizerConfig{Timeout: 5},
			OCR:            config.OCRConfig{Timeout: 5},
		},
	}
}

func TestNewServer(t *testing.T) {
	db := serverTestDB(t)
	s := NewServer(testConfig(t), utils.NewDiscardLogger(), db)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestRequestMiddleware(t *testing.T) {
	var capturedReqID string
	handler := requestMiddleware(utils.NewDiscardLogger(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReqID = r.Context().Value("reqid").(string)
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	handler.ServeHTTP(w, req)

	if capturedReqID == "" {
		t.Fatal("expected reqid to be set")
	}
}

func TestParambagMiddleware(t *testing.T) {
	var capturedPB bool
	handler := parambagMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pb := utils.GetParamBag(r)
		capturedPB = pb != nil
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test?foo=bar", nil)
	handler.ServeHTTP(w, req)

	if !capturedPB {
		t.Fatal("expected param bag to be set")
	}
}

func TestChainMiddleware(t *testing.T) {
	var seenReqID bool
	var seenPB bool
	handler := chainMiddleware(utils.NewDiscardLogger(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenReqID = r.Context().Value("reqid") != nil
		seenPB = utils.GetParamBag(r) != nil
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test?q=hello", nil)
	handler.ServeHTTP(w, req)

	if !seenReqID {
		t.Error("expected reqid in context")
	}
	if !seenPB {
		t.Error("expected parambag in context")
	}
}

func TestRegisterRoutes_Health(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux, utils.NewDiscardLogger(), nil, nil, nil, nil)
	handler := chainMiddleware(utils.NewDiscardLogger(), mux)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "healthy" {
		t.Errorf("status = %v", body["status"])
	}
}

func TestRegisterRoutes_DocumentList(t *testing.T) {
	db := serverTestDB(t)
	q := database.New(db)
	engine := search.NewEngine(utils.NewDiscardLogger(), db)

	mux := http.NewServeMux()
	registerRoutes(mux, utils.NewDiscardLogger(), q, engine, nil, &config.Config{})
	handler := chainMiddleware(utils.NewDiscardLogger(), mux)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/v1/documents")
	if err != nil {
		t.Fatalf("GET /api/v1/documents: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestRegisterRoutes_TaskList(t *testing.T) {
	db := serverTestDB(t)
	q := database.New(db)

	mux := http.NewServeMux()
	registerRoutes(mux, utils.NewDiscardLogger(), q, nil, nil, &config.Config{})
	handler := chainMiddleware(utils.NewDiscardLogger(), mux)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestAddr(t *testing.T) {
	db := serverTestDB(t)
	s := NewServer(testConfig(t), utils.NewDiscardLogger(), db)
	addr := s.Addr()
	if addr != "127.0.0.1:0" {
		t.Errorf("addr = %q", addr)
	}
}
