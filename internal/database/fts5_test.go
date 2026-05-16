package database

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

var schemaSQL = func() string {
	data, err := os.ReadFile("../../sql/schema.sql")
	if err != nil {
		panic("cannot read schema.sql: " + err.Error())
	}
	return string(data)
}()

func setupFTS5DB(t *testing.T) *sql.DB {
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

func TestRebuildFTS(t *testing.T) {
	db := setupFTS5DB(t)
	queries := NewQueries(db)
	ctx := context.Background()

	for i := range 5 {
		queries.CreateDocument(ctx, CreateDocumentParams{
			Title:          "doc.pdf",
			Md5Checksum:    string(rune('a' + i)),
			Sha512Checksum: string(rune('a' + i)),
			MimeType:       "application/pdf",
			FileSize:       100,
			OriginalPath:   "/a",
			StoragePath:    "/a",
			TextContent:    sql.NullString{String: "common searchable text", Valid: true},
		})
	}

	results, err := queries.SearchDocumentsFTS(ctx, `"common"`, 10, 0)
	if err != nil {
		t.Fatalf("search after insert: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results from triggers, got %d", len(results))
	}

	db.Exec("DELETE FROM document_fts")

	results, err = queries.SearchDocumentsFTS(ctx, `"common"`, 10, 0)
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results after manual delete, got %d", len(results))
	}

	if err := queries.RebuildDocumentFTS(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	results, err = queries.SearchDocumentsFTS(ctx, `"common"`, 10, 0)
	if err != nil {
		t.Fatalf("search after rebuild: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("expected 5 results after rebuild, got %d", len(results))
	}
}

func TestUpdateFTS(t *testing.T) {
	db := setupFTS5DB(t)
	queries := NewQueries(db)
	ctx := context.Background()

	result, err := queries.CreateDocument(ctx, CreateDocumentParams{
		Title:          "doc.pdf",
		Md5Checksum:    "a",
		Sha512Checksum: "a1",
		MimeType:       "application/pdf",
		FileSize:       100,
		OriginalPath:   "/a",
		StoragePath:    "/a",
		TextContent:    sql.NullString{String: "original content", Valid: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id, _ := result.LastInsertId()

	content, err := queries.GetDocumentFTSContent(ctx, id)
	if err != nil {
		t.Fatalf("get fts content after insert: %v", err)
	}
	if content != "original content" {
		t.Fatalf("expected trigger to populate FTS, got %q", content)
	}

	err = queries.UpdateDocumentFTS(ctx, struct {
		DocumentID int64
		Title      string
		Content    string
	}{DocumentID: id, Title: "doc.pdf", Content: "updated content"})
	if err != nil {
		t.Fatalf("update fts: %v", err)
	}

	content, err = queries.GetDocumentFTSContent(ctx, id)
	if err != nil {
		t.Fatalf("get fts content after update: %v", err)
	}
	if content != "updated content" {
		t.Errorf("expected 'updated content', got %q", content)
	}
}

func TestDeleteFTS(t *testing.T) {
	db := setupFTS5DB(t)
	queries := NewQueries(db)
	ctx := context.Background()

	result, err := queries.CreateDocument(ctx, CreateDocumentParams{
		Title:          "doc.pdf",
		Md5Checksum:    "a",
		Sha512Checksum: "a1",
		MimeType:       "application/pdf",
		FileSize:       100,
		OriginalPath:   "/a",
		StoragePath:    "/a",
		TextContent:    sql.NullString{String: "deletable content", Valid: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id, _ := result.LastInsertId()

	if err := queries.DeleteDocumentFTS(ctx, id); err != nil {
		t.Fatalf("delete fts: %v", err)
	}

	results, err := queries.SearchDocumentsFTS(ctx, `"deletable"`, 10, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}

func TestSearchWithFilters(t *testing.T) {
	db := setupFTS5DB(t)
	queries := NewQueries(db)
	ctx := context.Background()

	queries.CreateDocument(ctx, CreateDocumentParams{
		Title: "a.pdf", Md5Checksum: "a", Sha512Checksum: "a1",
		MimeType: "application/pdf", FileSize: 100, OriginalPath: "/a", StoragePath: "/a",
		TextContent: sql.NullString{String: "searchable content", Valid: true},
	})
	queries.CreateDocument(ctx, CreateDocumentParams{
		Title: "b.txt", Md5Checksum: "b", Sha512Checksum: "b1",
		MimeType: "text/plain", FileSize: 50, OriginalPath: "/b", StoragePath: "/b",
		TextContent: sql.NullString{String: "searchable content", Valid: true},
	})

	results, err := queries.SearchDocumentsFTSWithFilters(ctx, struct {
		SearchQuery string
		MimeType    string
		Limit       int32
		Offset      int32
	}{SearchQuery: `"searchable"`, MimeType: "application/pdf", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("search with filters: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 PDF result, got %d", len(results))
	}
	if results[0].MimeType != "application/pdf" {
		t.Errorf("expected PDF, got %s", results[0].MimeType)
	}
}
