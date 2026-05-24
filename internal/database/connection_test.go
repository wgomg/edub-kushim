package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
)

func chdirToProjectRoot(t *testing.T) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chdir(wd) })
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func TestNewSQLiteDB(t *testing.T) {
	chdirToProjectRoot(t)
	cfg := config.DatabaseConfig{
		Path: t.TempDir(),
		Name: "test.db",
	}

	db, err := NewSQLiteDB(cfg)
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer db.Close()

	if db == nil {
		t.Fatal("expected non-nil db")
	}
}

func TestNewSQLiteDB_CreatesDirectory(t *testing.T) {
	chdirToProjectRoot(t)
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	cfg := config.DatabaseConfig{
		Path: dir,
		Name: "test.db",
	}

	db, err := NewSQLiteDB(cfg)
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("expected directory to be created")
	}
}

func TestNewSQLiteDB_DBFileExists(t *testing.T) {
	chdirToProjectRoot(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.db")

	first, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	cfg := config.DatabaseConfig{Path: dir, Name: "existing.db"}
	db, err := NewSQLiteDB(cfg)
	if err != nil {
		t.Fatalf("NewSQLiteDB on existing file: %v", err)
	}
	db.Close()
}

func TestNewSQLiteDB_SchemaCreated(t *testing.T) {
	chdirToProjectRoot(t)
	cfg := config.DatabaseConfig{
		Path: t.TempDir(),
		Name: "schema-test.db",
	}

	db, err := NewSQLiteDB(cfg)
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer db.Close()

	var tableExists bool
	err = db.QueryRow("SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name='document'").
		Scan(&tableExists)
	if err != nil {
		t.Fatal(err)
	}
	if !tableExists {
		t.Fatal("expected 'document' table to exist after NewSQLiteDB")
	}
}

func TestNewQueries(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	q := NewQueries(db)
	if q == nil {
		t.Fatal("expected non-nil Queries")
	}
}

func TestCreateSchema_AlreadyExists(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec("CREATE TABLE document (id INTEGER PRIMARY KEY)")
	if err != nil {
		t.Fatal(err)
	}

	err = createSchema(db)
	if err != nil {
		t.Fatalf("createSchema when table exists: %v", err)
	}
}

func TestNewSQLiteDB_ForeignKeysEnabled(t *testing.T) {
	chdirToProjectRoot(t)
	cfg := config.DatabaseConfig{
		Path: t.TempDir(),
		Name: "fk-test.db",
	}

	db, err := NewSQLiteDB(cfg)
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer db.Close()

	var fkEnabled int
	err = db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if fkEnabled != 1 {
		t.Errorf("foreign_keys = %d, want 1", fkEnabled)
	}
}

func TestNewSQLiteDB_MaxOpenConns(t *testing.T) {
	chdirToProjectRoot(t)
	cfg := config.DatabaseConfig{
		Path: t.TempDir(),
		Name: "conns-test.db",
	}

	db, err := NewSQLiteDB(cfg)
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer db.Close()

	stats := db.Stats()
	if stats.MaxOpenConnections != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", stats.MaxOpenConnections)
	}
}
