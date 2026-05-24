package search

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"

	_ "modernc.org/sqlite"
)

func setupSearchDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
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
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func seedDocument(t *testing.T, queries *database.Queries, title, path, text string) {
	t.Helper()
	ctx := context.Background()
	result, err := queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title:          title,
		Md5Checksum:    "md5-" + title,
		Sha512Checksum: "sha512-" + title,
		MimeType:       "application/pdf",
		FileSize:       100,
		OriginalPath:   path,
		StoragePath:    path,
		TextContent:    sql.NullString{String: text, Valid: text != ""},
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	id, _ := result.LastInsertId()
	if err := queries.UpdateDocumentFTS(ctx, struct {
		DocumentID int64
		Title      string
		Content    string
	}{DocumentID: id, Title: title, Content: text}); err != nil {
		t.Fatalf("UpdateDocumentFTS: %v", err)
	}
}

func TestSanitizeQuery_Empty(t *testing.T) {
	got := sanitizeQuery("")
	if got != "" {
		t.Errorf("sanitizeQuery('') = %q, want ''", got)
	}
}

func TestSanitizeQuery_PlainText(t *testing.T) {
	got := sanitizeQuery("hello world")
	want := `"hello world"`
	if got != want {
		t.Errorf("sanitizeQuery('hello world') = %q, want %q", got, want)
	}
}

func TestSanitizeQuery_EmbeddedQuotes(t *testing.T) {
	got := sanitizeQuery(`say "hello"`)
	want := `"say ""hello"""`
	if got != want {
		t.Errorf("sanitizeQuery('say \"hello\"') = %q, want %q", got, want)
	}
}

func TestSanitizeQuery_SpecialChars(t *testing.T) {
	got := sanitizeQuery(`NOT OR AND * ( )`)
	want := `"NOT OR AND * ( )"`
	if got != want {
		t.Errorf("sanitizeQuery() = %q, want %q", got, want)
	}
}

func TestNewEngine(t *testing.T) {
	db := setupSearchDB(t)
	logger := utils.NewDiscardLogger()

	e := NewEngine(logger, db)
	if e == nil {
		t.Fatal("NewEngine() returned nil")
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	db := setupSearchDB(t)
	logger := utils.NewDiscardLogger()
	e := NewEngine(logger, db)

	results, err := e.Search(context.Background(), "", 10, 0)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("Search() = %v, want nil", results)
	}
}

func TestSearch_NoResults(t *testing.T) {
	db := setupSearchDB(t)
	logger := utils.NewDiscardLogger()
	e := NewEngine(logger, db)

	results, err := e.Search(context.Background(), "nothing", 10, 0)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Search() = %d results, want 0", len(results))
	}
}

func TestSearch_FindsResults(t *testing.T) {
	db := setupSearchDB(t)
	logger := utils.NewDiscardLogger()
	queries := database.NewQueries(db)
	e := NewEngine(logger, db)

	seedDocument(t, queries, "invoice march", "/inv.pdf", "invoice for march services rendered")
	seedDocument(t, queries, "report", "/rep.pdf", "annual report with charts")

	results, err := e.Search(context.Background(), "invoice", 10, 0)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search() = %d results, want 1", len(results))
	}
	if results[0].Title != "invoice march" {
		t.Errorf("result title = %q, want %q", results[0].Title, "invoice march")
	}
}

func TestSearch_MultipleMatches(t *testing.T) {
	db := setupSearchDB(t)
	logger := utils.NewDiscardLogger()
	queries := database.NewQueries(db)
	e := NewEngine(logger, db)

	seedDocument(t, queries, "doc1", "/1.pdf", "the quick brown fox")
	seedDocument(t, queries, "doc2", "/2.pdf", "the slow brown fox")
	seedDocument(t, queries, "doc3", "/3.pdf", "the lazy dog")

	results, err := e.Search(context.Background(), "brown", 10, 0)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("Search() = %d results, want 2", len(results))
	}
}

func TestSearch_LimitAndOffset(t *testing.T) {
	db := setupSearchDB(t)
	logger := utils.NewDiscardLogger()
	queries := database.NewQueries(db)
	e := NewEngine(logger, db)

	for i := range 10 {
		title := fmt.Sprintf("doc-%d", i)
		seedDocument(t, queries, title, "/a.pdf", "common term in all documents")
		_ = i
	}

	results, err := e.Search(context.Background(), "common", 3, 0)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("Search() with limit=3 = %d results, want 3", len(results))
	}
}

func TestSearch_ResultFields(t *testing.T) {
	db := setupSearchDB(t)
	logger := utils.NewDiscardLogger()
	queries := database.NewQueries(db)
	e := NewEngine(logger, db)

	seedDocument(t, queries, "test title", "/path/test.pdf", "lorem ipsum dolor sit amet")

	results, err := e.Search(context.Background(), "lorem", 10, 0)
	if err != nil {
		t.Fatalf("Search() unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search() returned 0 results")
	}

	r := results[0]
	if r.Title != "test title" {
		t.Errorf("Title = %q, want %q", r.Title, "test title")
	}
	if r.MimeType != "application/pdf" {
		t.Errorf("MimeType = %q, want %q", r.MimeType, "application/pdf")
	}
	if r.FileSize != 100 {
		t.Errorf("FileSize = %d, want 100", r.FileSize)
	}
	if !strings.Contains(r.Snippet, "lorem") {
		t.Errorf("Snippet = %q, expected 'lorem'", r.Snippet)
	}
	if r.Rank == 0 {
		t.Errorf("Rank = 0, expected non-zero BM25 score")
	}
}
