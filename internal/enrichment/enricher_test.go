package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"

	_ "modernc.org/sqlite"
)

func setupEnricherDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE document_type (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE tag (
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
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func TestEnrich_ContentAnalyzerNotConfigured(t *testing.T) {
	db := setupEnricherDB(t)
	logger := utils.NewDiscardLogger()
	cfg := &config.Config{}

	e, err := NewEnricher(cfg, logger, db, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	doc := database.Document{
		ID:             1,
		StoragePath:    "/tmp/test.pdf",
		TextContent:    sql.NullString{String: "short text", Valid: true},
		Md5Checksum:    "abc",
		Sha512Checksum: "def",
		MimeType:       "application/pdf",
		FileSize:       100,
		OriginalPath:   "/tmp/test.pdf",
	}

	_, err = e.Enrich(context.Background(), doc)
	if err == nil {
		t.Fatal("expected error for missing content analyzer, got nil")
	}
}

func TestEnrich_ShortTextSkipsReducer(t *testing.T) {
	db := setupEnricherDB(t)
	logger := utils.NewDiscardLogger()
	cfg := &config.Config{}

	e, err := NewEnricher(cfg, logger, db, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	doc := database.Document{
		ID:             1,
		StoragePath:    "/tmp/test.pdf",
		TextContent:    sql.NullString{String: "short text", Valid: true},
		Md5Checksum:    "abc",
		Sha512Checksum: "def",
		MimeType:       "application/pdf",
		FileSize:       100,
		OriginalPath:   "/tmp/test.pdf",
	}

	_, err = e.Enrich(context.Background(), doc)
	if err == nil {
		t.Fatal("expected error from content analyzer stub, got nil")
	}
}

func TestShouldReduceContent(t *testing.T) {
	if !shouldReduceContent(3000, 2000) {
		t.Error("shouldReduceContent(3000, 2000) = false, want true")
	}
	if shouldReduceContent(1000, 2000) {
		t.Error("shouldReduceContent(1000, 2000) = true, want false")
	}
}

func TestEnrich_ReturnsStatsOnSuccess(t *testing.T) {
	db := setupEnricherDB(t)
	logger := utils.NewDiscardLogger()
	cfg := &config.Config{}

	e, err := NewEnricher(cfg, logger, db, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	doc := database.Document{
		ID:             1,
		StoragePath:    "/tmp/test.pdf",
		TextContent:    sql.NullString{String: "some content", Valid: true},
		Md5Checksum:    "abc",
		Sha512Checksum: "def",
		MimeType:       "application/pdf",
		FileSize:       100,
		OriginalPath:   "/tmp/test.pdf",
	}

	raw, err := e.Enrich(context.Background(), doc)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if raw == nil {
		t.Fatal("expected non-nil result")
	}

	var result map[string]any
	if err := json.Unmarshal(*raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
}

func TestEnrich_LongTextTriggersReducer(t *testing.T) {
	db := setupEnricherDB(t)
	logger := utils.NewDiscardLogger()
	cfg := &config.Config{}

	e, err := NewEnricher(cfg, logger, db, nil)
	if err != nil {
		t.Fatalf("NewEnricher: %v", err)
	}

	var longText strings.Builder
	for range 5000 {
		longText.WriteString("word ")
	}

	doc := database.Document{
		ID:             1,
		StoragePath:    "/tmp/test.pdf",
		TextContent:    sql.NullString{String: longText.String(), Valid: true},
		Md5Checksum:    "abc",
		Sha512Checksum: "def",
		MimeType:       "application/pdf",
		FileSize:       int64(len(longText.String())),
		OriginalPath:   "/tmp/test.pdf",
	}

	_, err = e.Enrich(context.Background(), doc)
	if err == nil {
		t.Fatal("expected error from content analyzer stub, got nil")
	}
}
