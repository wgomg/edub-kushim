package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// TB is a minimal subset of testing.TB for use in test helpers.
type TB interface {
	Fatalf(format string, args ...any)
	Helper()
}

// NewTestDB creates an in-memory SQLite database with the schema initialized.
// The returned *sql.DB must be closed by the caller.
func NewTestDB(t TB) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory database: %v", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("failed to enable foreign keys: %v", err)
	}

	if err := InitializeSchema(db); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	return db
}

// NewTestQueries creates database.Queries from an in-memory test DB.
func NewTestQueries(t TB) (*Queries, *sql.DB) {
	t.Helper()
	db := NewTestDB(t)
	return New(db), db
}

// CreateTestDocument inserts a basic document into the database and returns
// its auto-increment ID and UUID. Uses the first seeded document type.
func CreateTestDocument(t TB, queries *Queries, title string) (int64, string) {
	t.Helper()
	ctx := context.Background()

	docTypes, err := queries.ListAllDocumentTypes(ctx)
	if err != nil {
		t.Fatalf("list document types: %v", err)
	}
	if len(docTypes) == 0 {
		t.Fatalf("no document types found (seeds not loaded?)")
	}

	docID := newTestUUID()
	result, err := queries.CreateDocument(ctx, CreateDocumentParams{
		DocumentID:     docID,
		Title:          title,
		Md5Checksum:    "d41d8cd98f00b204e9800998ecf8427e",
		Sha512Checksum: "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
		MimeType:       "application/pdf",
		FileSize:       1024,
		OriginalPath:   "/tmp/orig.pdf",
		StoragePath:    "/tmp/storage.pdf",
		TextContent:    sql.NullString{String: "test content for full-text search", Valid: true},
		PageCount:      1,
		WordCount:      5,
		CharCount:      35,
		Language:       "eng",
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id, docID
}

// SeedTagByName returns a tag by name or the first tag if name is empty.
func SeedTagByName(t TB, queries *Queries, name string) Tag {
	t.Helper()
	ctx := context.Background()
	if name == "" {
		tags, err := queries.ListTags(ctx, ListTagsParams{Limit: 1, Offset: 0})
		if err != nil {
			t.Fatalf("list tags: %v", err)
		}
		if len(tags) == 0 {
			t.Fatalf("no seeded tags found")
		}
		return tags[0]
	}
	tag, err := queries.GetTagByName(ctx, name)
	if err != nil {
		t.Fatalf("get tag by name %q: %v", name, err)
	}
	return tag
}

// newTestUUID generates a deterministic v4-like UUID for tests.
func newTestUUID() string {
	now := time.Now().UnixNano()
	return fmt.Sprintf("%08x-%04x-4%03x-%04x-%012x",
		uint32(now>>32), uint16(now>>16), uint16(now), uint16(now>>48), uint64(now))
}
