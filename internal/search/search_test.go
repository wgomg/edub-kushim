package search

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
)

var schemaSQL = func() string {
	data, err := os.ReadFile("../../sql/schema.sql")
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

func TestSanitizeQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"hello", `"hello"`},
		{"hello world", `"hello world"`},
		{"hello AND world", `"hello AND world"`},
		{"term*", `"term*"`},
		{`"exact phrase"`, `"""exact phrase"""`},
		{`mix "of" things`, `"mix ""of"" things"`},
		{"(a OR b)", `"(a OR b)"`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeQuery(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeQuery(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSearchResults(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title: "quantum.pdf", Md5Checksum: "a", Sha512Checksum: "a1",
		MimeType: "application/pdf", FileSize: 100, OriginalPath: "/a", StoragePath: "/a",
		TextContent: sql.NullString{String: "quantum mechanics basics", Valid: true},
	})
	queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title: "physics.pdf", Md5Checksum: "b", Sha512Checksum: "b1",
		MimeType: "application/pdf", FileSize: 200, OriginalPath: "/b", StoragePath: "/b",
		TextContent: sql.NullString{String: "quantum physics advanced", Valid: true},
	})
	queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title: "biology.pdf", Md5Checksum: "c", Sha512Checksum: "c1",
		MimeType: "application/pdf", FileSize: 300, OriginalPath: "/c", StoragePath: "/c",
		TextContent: sql.NullString{String: "cell biology intro", Valid: true},
	})

	engine := NewEngine(nil, db)
	results, err := engine.Search(ctx, "quantum", 10, 0)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Rank == 0 || results[1].Rank == 0 {
		t.Error("expected non-zero bm25 ranks")
	}
}

func TestSearchNoResults(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title: "doc.pdf", Md5Checksum: "a", Sha512Checksum: "a1",
		MimeType: "application/pdf", FileSize: 100, OriginalPath: "/a", StoragePath: "/a",
		TextContent: sql.NullString{String: "some content", Valid: true},
	})

	engine := NewEngine(nil, db)
	results, err := engine.Search(ctx, "nonexistent", 10, 0)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title: "doc.pdf", Md5Checksum: "a", Sha512Checksum: "a1",
		MimeType: "application/pdf", FileSize: 100, OriginalPath: "/a", StoragePath: "/a",
		TextContent: sql.NullString{String: "some content", Valid: true},
	})

	engine := NewEngine(nil, db)
	results, err := engine.Search(ctx, "", 10, 0)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchPagination(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	for i := range 10 {
		queries.CreateDocument(ctx, database.CreateDocumentParams{
			Title:          "doc.pdf",
			Md5Checksum:    string(rune('a' + i)),
			Sha512Checksum: string(rune('a' + i)),
			MimeType:       "application/pdf",
			FileSize:       100,
			OriginalPath:   "/a",
			StoragePath:    "/a",
			TextContent:    sql.NullString{String: "common text in all docs", Valid: true},
		})
	}

	engine := NewEngine(nil, db)

	page1, err := engine.Search(ctx, "common", 3, 0)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("page1: expected 3 results, got %d", len(page1))
	}

	page2, err := engine.Search(ctx, "common", 3, 3)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 3 {
		t.Fatalf("page2: expected 3 results, got %d", len(page2))
	}

	ids := map[int64]bool{}
	for _, r := range page1 {
		ids[r.DocumentID] = true
	}
	for _, r := range page2 {
		if ids[r.DocumentID] {
			t.Error("overlap between page1 and page2")
		}
	}
}

func TestSearchLimit(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	for i := range 10 {
		queries.CreateDocument(ctx, database.CreateDocumentParams{
			Title:          "doc.pdf",
			Md5Checksum:    string(rune('a' + i)),
			Sha512Checksum: string(rune('a' + i)),
			MimeType:       "application/pdf",
			FileSize:       100,
			OriginalPath:   "/a",
			StoragePath:    "/a",
			TextContent:    sql.NullString{String: "common text", Valid: true},
		})
	}

	engine := NewEngine(nil, db)
	results, err := engine.Search(ctx, "common", 5, 0)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}
}

func TestSearchOffsetBeyond(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	for i := range 3 {
		queries.CreateDocument(ctx, database.CreateDocumentParams{
			Title:          "doc.pdf",
			Md5Checksum:    string(rune('a' + i)),
			Sha512Checksum: string(rune('a' + i)),
			MimeType:       "application/pdf",
			FileSize:       100,
			OriginalPath:   "/a",
			StoragePath:    "/a",
			TextContent:    sql.NullString{String: "common text", Valid: true},
		})
	}

	engine := NewEngine(nil, db)
	results, err := engine.Search(ctx, "common", 10, 20)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchSnippet(t *testing.T) {
	db := setupTestDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title: "physics.pdf", Md5Checksum: "a", Sha512Checksum: "a1",
		MimeType: "application/pdf", FileSize: 100, OriginalPath: "/a", StoragePath: "/a",
		TextContent: sql.NullString{String: "the theory of quantum mechanics describes nature at the smallest scales", Valid: true},
	})

	engine := NewEngine(nil, db)
	results, err := engine.Search(ctx, "quantum", 1, 0)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
	if !strings.Contains(results[0].Snippet, "<b>quantum</b>") {
		t.Errorf("snippet should contain highlighted term, got: %s", results[0].Snippet)
	}
}
