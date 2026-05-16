package commands

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

var schemaSQL = func() string {
	data, err := os.ReadFile("../../sql/schema.sql")
	if err != nil {
		panic("cannot read schema.sql: " + err.Error())
	}
	return string(data)
}()

func setupCommandsDB(t *testing.T) *sql.DB {
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

func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func newTestContainer(t *testing.T, db *sql.DB) (*Container, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger := utils.NewLoggerWithWriter(buf)
	return &Container{
		config: &config.Config{},
		logger: logger,
		db:     db,
	}, buf
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tt := range tests {
		got := formatSize(tt.input)
		if got != tt.expected {
			t.Errorf("formatSize(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestHighlightSnippet(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"no tags", "no tags"},
		{"<b>bold</b> text", "\033[1;33mbold\033[0m text"},
		{"<b>a</b> and <b>b</b>", "\033[1;33ma\033[0m and \033[1;33mb\033[0m"},
	}
	for _, tt := range tests {
		got := highlightSnippet(tt.input)
		if got != tt.expected {
			t.Errorf("highlightSnippet(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSearchHandlerResults(t *testing.T) {
	db := setupCommandsDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title: "quantum.pdf", Md5Checksum: "a", Sha512Checksum: "a1",
		MimeType: "application/pdf", FileSize: 1024, OriginalPath: "/a", StoragePath: "/a",
		TextContent: sql.NullString{String: "quantum mechanics", Valid: true},
	})

	c, logBuf := newTestContainer(t, db)

	output := captureStdout(func() {
		err := searchHandler(c, []string{"quantum"})
		if err != nil {
			t.Fatalf("searchHandler: %v", err)
		}
	})

	if !strings.Contains(output, "quantum.pdf") {
		t.Error("output should contain document title")
	}
	if !strings.Contains(output, "1.0 KB") {
		t.Error("output should contain formatted size")
	}
	if !strings.Contains(output, "rank=") {
		t.Error("output should contain rank")
	}
	if !strings.Contains(logBuf.String(), "1 results for") {
		t.Errorf("log should contain result count, got: %s", logBuf.String())
	}
}

func TestSearchHandlerNoResults(t *testing.T) {
	db := setupCommandsDB(t)
	c, logBuf := newTestContainer(t, db)

	err := searchHandler(c, []string{"nonexistent"})
	if err != nil {
		t.Fatalf("searchHandler: %v", err)
	}

	if !strings.Contains(logBuf.String(), "no results for") {
		t.Errorf("log should indicate no results, got: %s", logBuf.String())
	}
}

func TestSearchHandlerMissingQuery(t *testing.T) {
	db := setupCommandsDB(t)
	c, _ := newTestContainer(t, db)

	err := searchHandler(c, []string{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestSearchHandlerRebuildIndex(t *testing.T) {
	db := setupCommandsDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	for i := range 5 {
		queries.CreateDocument(ctx, database.CreateDocumentParams{
			Title:          fmt.Sprintf("doc%d.pdf", i),
			Md5Checksum:    fmt.Sprintf("md5-%d", i),
			Sha512Checksum: fmt.Sprintf("sha-%d", i),
			MimeType:       "application/pdf",
			FileSize:       100,
			OriginalPath:   "/a",
			StoragePath:    "/a",
			TextContent:    sql.NullString{String: "rebuild content", Valid: true},
		})
	}

	c, logBuf := newTestContainer(t, db)

	db.Exec("DELETE FROM document_fts")

	err := searchHandler(c, []string{"--rebuild-index"})
	if err != nil {
		t.Fatalf("searchHandler --rebuild-index: %v", err)
	}

	if !strings.Contains(logBuf.String(), "FTS index rebuilt") {
		t.Errorf("log should indicate rebuild completed, got: %s", logBuf.String())
	}

	results, err := database.NewQueries(db).SearchDocumentsFTS(ctx, `"rebuild"`, 10, 0)
	if err != nil {
		t.Fatalf("search after rebuild: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("expected 5 FTS results after rebuild, got %d", len(results))
	}
}

func TestSearchHandlerPagination(t *testing.T) {
	db := setupCommandsDB(t)
	queries := database.NewQueries(db)
	ctx := context.Background()

	for i := range 5 {
		queries.CreateDocument(ctx, database.CreateDocumentParams{
			Title:          fmt.Sprintf("doc%d.pdf", i),
			Md5Checksum:    fmt.Sprintf("md5-%d", i),
			Sha512Checksum: fmt.Sprintf("sha-%d", i),
			MimeType:       "application/pdf",
			FileSize:       100,
			OriginalPath:   "/a",
			StoragePath:    "/a",
			TextContent:    sql.NullString{String: "common pagination text", Valid: true},
		})
	}

	c, logBuf := newTestContainer(t, db)

	captureStdout(func() {
		err := searchHandler(c, []string{"--limit", "2", "common"})
		if err != nil {
			t.Fatalf("searchHandler: %v", err)
		}
	})

	if !strings.Contains(logBuf.String(), "2 results for") {
		t.Errorf("expected 2 results in log, got: %s", logBuf.String())
	}

	logBuf.Reset()
	captureStdout(func() {
		err := searchHandler(c, []string{"--limit", "2", "--offset", "2", "common"})
		if err != nil {
			t.Fatalf("searchHandler: %v", err)
		}
	})

	if !strings.Contains(logBuf.String(), "2 results for") {
		t.Errorf("expected 2 results on page 2 in log, got: %s", logBuf.String())
	}
}
