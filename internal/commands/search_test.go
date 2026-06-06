package commands

import (
	"database/sql"
	"testing"

	"github.com/wgomg/edub-kushim/internal/utils"

	_ "modernc.org/sqlite"
)

func TestSearchHandler_Help(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := searchHandler(c, []string{"--help"})
	if err != nil {
		t.Fatalf("expected nil error for --help, got %v", err)
	}
}

func TestSearchHandler_MissingQuery(t *testing.T) {
	c := &Container{logger: utils.NewDiscardLogger()}
	err := searchHandler(c, []string{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestSearchHandler_NoResults(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

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
			document_id UNINDEXED, title, content, tokenize = 'unicode61'
		);
	`)
	if err != nil {
		t.Fatal(err)
	}

	c := &Container{
		logger: utils.NewDiscardLogger(),
		db:     db,
	}

	err = searchHandler(c, []string{"nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchHandler_WithResults(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

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
			document_id UNINDEXED, title, content, tokenize = 'unicode61'
		);
		INSERT INTO document (id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, text_content)
		VALUES (1, 'Test Report', 'abc', 'def', 'application/pdf', 2048, '/tmp/a.pdf', '/store/a.pdf', 'quarterly financial report');
		INSERT INTO document_fts (document_id, title, content) VALUES (1, 'Test Report', 'quarterly financial report');
	`)
	if err != nil {
		t.Fatal(err)
	}

	c := &Container{
		logger: utils.NewDiscardLogger(),
		db:     db,
	}

	err = searchHandler(c, []string{"financial"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchHandler_RebuildIndex(t *testing.T) {
	chdirToProjectRoot(t)

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

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
			document_id UNINDEXED, title, content, tokenize = 'unicode61'
		);
	`)
	if err != nil {
		t.Fatal(err)
	}

	c := &Container{
		logger: utils.NewDiscardLogger(),
		db:     db,
	}

	err = searchHandler(c, []string{"--rebuild-index"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHighlightSnippet(t *testing.T) {
	input := "the <b>quick</b> brown fox"
	got := highlightSnippet(input)
	if got != "the \033[1;33mquick\033[0m brown fox" {
		t.Errorf("highlightSnippet = %q", got)
	}
}

func TestHighlightSnippet_NoTags(t *testing.T) {
	input := "plain text"
	got := highlightSnippet(input)
	if got != "plain text" {
		t.Errorf("highlightSnippet = %q", got)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tc := range tests {
		got := formatSize(tc.bytes)
		if got != tc.want {
			t.Errorf("formatSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}
