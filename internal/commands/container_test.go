package commands

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"

	_ "modernc.org/sqlite"
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

func TestNewContainer(t *testing.T) {
	cfg := &config.Config{}
	c := NewContainer(cfg, utils.NewDiscardLogger())
	if c == nil {
		t.Fatal("expected non-nil container")
	}
	if c.config != cfg {
		t.Error("config not stored")
	}
}

func TestNewContainerWithDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &config.Config{}
	c := NewContainerWithDB(cfg, utils.NewDiscardLogger(), db)
	if c == nil {
		t.Fatal("expected non-nil container")
	}
	if c.db != db {
		t.Error("db not stored")
	}
}

func TestGetDB_LazyInit(t *testing.T) {
	chdirToProjectRoot(t)
	cfg := &config.Config{
		Db: config.DatabaseConfig{
			Path: t.TempDir(),
			Name: "test.db",
		},
	}
	c := NewContainer(cfg, utils.NewDiscardLogger())

	db, err := c.GetDB()
	if err != nil {
		t.Fatalf("GetDB: %v", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}
	defer db.Close()

	db2, err := c.GetDB()
	if err != nil {
		t.Fatalf("second GetDB: %v", err)
	}
	if db2 != db {
		t.Error("GetDB returned different instances")
	}
}

func TestGetDB_AlreadySet(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &config.Config{}
	c := NewContainerWithDB(cfg, utils.NewDiscardLogger(), db)

	got, err := c.GetDB()
	if err != nil {
		t.Fatalf("GetDB: %v", err)
	}
	if got != db {
		t.Error("GetDB returned different instance than the one provided")
	}
}

func TestGetSearchEngine(t *testing.T) {
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

	cfg := &config.Config{}
	c := NewContainerWithDB(cfg, utils.NewDiscardLogger(), db)

	e1, err := c.GetSearchEngine()
	if err != nil {
		t.Fatalf("GetSearchEngine: %v", err)
	}
	if e1 == nil {
		t.Fatal("expected non-nil engine")
	}

	e2, err := c.GetSearchEngine()
	if err != nil {
		t.Fatalf("second GetSearchEngine: %v", err)
	}
	if e2 != e1 {
		t.Error("GetSearchEngine returned different instances")
	}
}

func TestClose_NoPanicWhenEmpty(t *testing.T) {
	cfg := &config.Config{}
	c := NewContainer(cfg, utils.NewDiscardLogger())
	c.Close()
}

func TestClose_WithDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	c := NewContainerWithDB(cfg, utils.NewDiscardLogger(), db)
	c.Close()
}
