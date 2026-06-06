package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func ftsTestDB(t *testing.T) *sql.DB {
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
			word_count INTEGER NOT NULL DEFAULT 0,
			char_count INTEGER NOT NULL DEFAULT 0,
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

		CREATE TRIGGER document_ad AFTER DELETE ON document
		BEGIN
			DELETE FROM document_fts WHERE document_id = old.id;
		END;

		CREATE TRIGGER document_au AFTER UPDATE ON document
		BEGIN
			DELETE FROM document_fts WHERE document_id = old.id;
			INSERT INTO document_fts(document_id, title, content)
			VALUES (new.id, new.title, COALESCE(new.text_content, ''));
		END;
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func insertFTSDoc(t *testing.T, q *Queries, title, content, mime string) int64 {
	t.Helper()
	result, err := q.CreateDocument(context.Background(), CreateDocumentParams{
		Title:          title,
		Md5Checksum:    "md5-" + title,
		Sha512Checksum: "sha512-" + title,
		MimeType:       mime,
		FileSize:       int64(len(content)),
		PageCount:      0,
		OriginalPath:   "/" + title,
		StoragePath:    "/store/" + title,
		TextContent:    sql.NullString{String: content, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestSearchDocumentsFTS(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	d1 := insertFTSDoc(t, q, "invoice march", "invoice for march services", "application/pdf")
	d2 := insertFTSDoc(t, q, "report", "annual report with charts", "application/pdf")

	if err := q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{d1, "invoice march", "invoice for march services"}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{d2, "report", "annual report with charts"}); err != nil {
		t.Fatal(err)
	}

	rows, err := q.SearchDocumentsFTS(context.Background(), "invoice", 10, 0)
	if err != nil {
		t.Fatalf("SearchDocumentsFTS: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d results, want 1", len(rows))
	}
	if rows[0].Title != "invoice march" {
		t.Errorf("Title = %q", rows[0].Title)
	}
	if rows[0].Rank >= 0 {
		t.Errorf("expected negative BM25 rank, got %f", rows[0].Rank)
	}
}

func TestSearchDocumentsFTS_NoMatch(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	rows, err := q.SearchDocumentsFTS(context.Background(), "nonexistent", 10, 0)
	if err != nil {
		t.Fatalf("SearchDocumentsFTS: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d results, want 0", len(rows))
	}
}

func TestSearchDocumentsFTS_Snippet(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	id := insertFTSDoc(t, q, "doc", "the quick brown fox jumps over the lazy dog", "text/plain")
	if err := q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{id, "doc", "the quick brown fox jumps over the lazy dog"}); err != nil {
		t.Fatal(err)
	}

	rows, err := q.SearchDocumentsFTS(context.Background(), "fox", 10, 0)
	if err != nil {
		t.Fatalf("SearchDocumentsFTS: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d results, want 1", len(rows))
	}
	if !strings.Contains(rows[0].Snippet, "fox") {
		t.Errorf("Snippet = %q, expected 'fox'", rows[0].Snippet)
	}
}

func TestSearchDocumentsFTSWithFilters(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	d1 := insertFTSDoc(t, q, "doc1", "lorem ipsum", "application/pdf")
	d2 := insertFTSDoc(t, q, "doc2", "lorem ipsum", "text/plain")

	if err := q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{d1, "doc1", "lorem ipsum"}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{d2, "doc2", "lorem ipsum"}); err != nil {
		t.Fatal(err)
	}

	rows, err := q.SearchDocumentsFTSWithFilters(context.Background(), struct {
		SearchQuery string
		MimeType    string
		Limit       int32
		Offset      int32
	}{SearchQuery: "lorem", MimeType: "text/plain", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("SearchDocumentsFTSWithFilters: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d results, want 1", len(rows))
	}
	if rows[0].MimeType != "text/plain" {
		t.Errorf("MimeType = %q", rows[0].MimeType)
	}
}

func TestSearchDocumentsFTSWithFilters_NoMatch(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	rows, err := q.SearchDocumentsFTSWithFilters(context.Background(), struct {
		SearchQuery string
		MimeType    string
		Limit       int32
		Offset      int32
	}{SearchQuery: "nothing", MimeType: "application/pdf", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("SearchDocumentsFTSWithFilters: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d results, want 0", len(rows))
	}
}

func TestSearchDocumentsFTS_Pagination(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	titles := []string{"a-one", "a-two", "a-three", "a-four", "a-five"}
	for _, title := range titles {
		id := insertFTSDoc(t, q, title, "common term", "text/plain")
		if err := q.UpdateDocumentFTS(context.Background(), struct {
			DocumentID int64
			Title      string
			Content    string
		}{id, title, "common term"}); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := q.SearchDocumentsFTS(context.Background(), "common", 3, 0)
	if err != nil {
		t.Fatalf("SearchDocumentsFTS: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("got %d results with limit=3, want 3", len(rows))
	}
}

func TestGetDocumentFTSContent(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	id := insertFTSDoc(t, q, "doc", "hello world", "text/plain")
	if err := q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{id, "doc", "hello world"}); err != nil {
		t.Fatal(err)
	}

	content, err := q.GetDocumentFTSContent(context.Background(), id)
	if err != nil {
		t.Fatalf("GetDocumentFTSContent: %v", err)
	}
	if content != "hello world" {
		t.Errorf("content = %q, want %q", content, "hello world")
	}
}

func TestGetDocumentFTSContent_NotFound(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	_, err := q.GetDocumentFTSContent(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error for missing FTS entry, got nil")
	}
}

func TestUpdateDocumentFTS(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	id := insertFTSDoc(t, q, "update-test", "initial content", "text/plain")
	if err := q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{id, "update-test", "initial content"}); err != nil {
		t.Fatal(err)
	}

	content, _ := q.GetDocumentFTSContent(context.Background(), id)
	if content != "initial content" {
		t.Fatalf("expected 'initial content', got %q", content)
	}

	if err := q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{id, "update-test", "updated content"}); err != nil {
		t.Fatal(err)
	}

	content, _ = q.GetDocumentFTSContent(context.Background(), id)
	if content != "updated content" {
		t.Errorf("after update: got %q, want 'updated content'", content)
	}
}

func TestDeleteDocumentFTS(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	id := insertFTSDoc(t, q, "del-test", "delete me", "text/plain")
	if err := q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{id, "del-test", "delete me"}); err != nil {
		t.Fatal(err)
	}

	if err := q.DeleteDocumentFTS(context.Background(), id); err != nil {
		t.Fatalf("DeleteDocumentFTS: %v", err)
	}

	_, err := q.GetDocumentFTSContent(context.Background(), id)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestRebuildDocumentFTS(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	insertFTSDoc(t, q, "doc1", "content one", "text/plain")
	insertFTSDoc(t, q, "doc2", "content two", "text/plain")

	if err := q.RebuildDocumentFTS(context.Background()); err != nil {
		t.Fatalf("RebuildDocumentFTS: %v", err)
	}

	rows, err := q.SearchDocumentsFTS(context.Background(), "content", 10, 0)
	if err != nil {
		t.Fatalf("SearchDocumentsFTS after rebuild: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d results after rebuild, want 2", len(rows))
	}
}

func TestFTS_InsertTrigger(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	insertFTSDoc(t, q, "trigger-test", "hello world", "text/plain")

	rows, err := q.SearchDocumentsFTS(context.Background(), "hello", 10, 0)
	if err != nil {
		t.Fatalf("SearchDocumentsFTS: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d results, want 1", len(rows))
	}
	if rows[0].Title != "trigger-test" {
		t.Errorf("Title = %q, want 'trigger-test'", rows[0].Title)
	}
}

func TestFTS_UpdateTrigger(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	id := insertFTSDoc(t, q, "update-trigger", "old content", "text/plain")

	_, err := db.Exec("UPDATE document SET text_content = 'new content' WHERE id = ?", id)
	if err != nil {
		t.Fatalf("UPDATE: %v", err)
	}

	rows, err := q.SearchDocumentsFTS(context.Background(), "old", 10, 0)
	if err != nil {
		t.Fatalf("SearchDocumentsFTS: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d results for 'old', want 0", len(rows))
	}

	rows, err = q.SearchDocumentsFTS(context.Background(), "new", 10, 0)
	if err != nil {
		t.Fatalf("SearchDocumentsFTS: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d results for 'new', want 1", len(rows))
	}
}

func TestFTS_DeleteTrigger(t *testing.T) {
	db := ftsTestDB(t)
	q := New(db)

	id := insertFTSDoc(t, q, "delete-trigger", "delete me", "text/plain")

	_, err := db.Exec("DELETE FROM document WHERE id = ?", id)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}

	rows, err := q.SearchDocumentsFTS(context.Background(), "delete", 10, 0)
	if err != nil {
		t.Fatalf("SearchDocumentsFTS: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d results, want 0", len(rows))
	}
}
