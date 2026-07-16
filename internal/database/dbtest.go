package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type TB interface {
	Fatalf(format string, args ...any)
	Helper()
	Cleanup(func())
}

var (
	testDBRefCounts   = make(map[string]int)
	testDBRefCountsMu sync.Mutex
)

func NewTestDB(t TB) *sql.DB {
	t.Helper()
	baseDSN := os.Getenv("TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Fatalf("TEST_DATABASE_URL not set — set it to a Postgres connection string")
	}
	dir := testPackageDir()
	dbName := "edub_test_" + dir
	release := acquireTestDB(baseDSN, dbName)

	testDSN := replaceDBName(baseDSN, dbName)
	db, err := NewPostgresDB(testDSN)
	if err != nil {
		release()
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := InitializeSchema(db); err != nil {
		db.Close()
		release()
		t.Fatalf("failed to initialize schema: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		release()
	})
	return db
}

// acquireTestDB registers a reference to the named test database and returns
// a release function. When the last reference is released, the database is
// dropped using DROP DATABASE ... WITH (FORCE) to terminate any lingering
// connections.
func acquireTestDB(baseDSN, dbName string) (release func()) {
	testDBRefCountsMu.Lock()
	testDBRefCounts[dbName]++
	testDBRefCountsMu.Unlock()

	return func() {
		testDBRefCountsMu.Lock()
		testDBRefCounts[dbName]--
		if testDBRefCounts[dbName] <= 0 {
			delete(testDBRefCounts, dbName)
			dropTestDatabase(baseDSN, dbName)
		}
		testDBRefCountsMu.Unlock()
	}
}

func dropTestDatabase(baseDSN, dbName string) {
	adminDSN := replaceDBName(baseDSN, "postgres")
	adminDB, err := sql.Open("pgx", adminDSN)
	if err != nil {
		return
	}
	defer adminDB.Close()
	adminDB.ExecContext(context.Background(),
		fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", quoteIdent(dbName)))
}

func testPackageDir() string {
	for i := 1; i < 32; i++ {
		_, file, _, ok := runtime.Caller(i)
		if !ok {
			break
		}
		if filepath.Base(file) != "dbtest.go" {
			return filepath.Base(filepath.Dir(file))
		}
	}
	return "unknown"
}

func NewTestQueries(t TB) (*Queries, *sql.DB) {
	t.Helper()
	db := NewTestDB(t)
	return New(db), db
}

func NewTestClient(t TB) *Client {
	t.Helper()
	q, db := NewTestQueries(t)
	return &Client{Queries: q, db: db}
}

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
	id, err := queries.CreateDocument(ctx, CreateDocumentParams{
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
	return id, docID
}

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

func ResetTestDatabase(db *sql.DB) {
	ctx := context.Background()
	tables := []string{
		"orphaned_file", "batch_owner", "batch",
		"document_tag", "document_people", "document",
		"task", "saved_search", `"user"`,
		"tag", "people", "people_type", "document_type",
	}
	for _, tbl := range tables {
		db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", tbl))
	}
	for _, tbl := range []string{"document_type", "people_type", "tag"} {
		db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN id RESTART WITH 1", tbl))
	}
	seeds := []string{"document-types", "people-types", "tags"}
	for _, seed := range seeds {
		data, err := SchemaFS.ReadFile(fmt.Sprintf("sql/schema/seed-%s.sql", seed))
		if err != nil {
			panic(fmt.Sprintf("read seed %s: %v", seed, err))
		}
		if _, err := db.ExecContext(ctx, string(data)); err != nil {
			panic(fmt.Sprintf("seed %s: %v", seed, err))
		}
	}
}

func newTestUUID() string {
	now := time.Now().UnixNano()
	return fmt.Sprintf("%08x-%04x-4%03x-%04x-%012x",
		uint32(now>>32), uint16(now>>16), uint16(now), uint16(now>>48), uint64(now))
}
