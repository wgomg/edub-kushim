package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func TestValidateArchive_Valid(t *testing.T) {
	db := database.NewTestDB(t)
	t.Cleanup(func() { database.ResetTestDatabase(db) })

	dir := t.TempDir()

	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")

	storageDir := filepath.Join(dir, "storage")
	os.MkdirAll(storageDir, 0755)

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	result, err := Create(context.Background(), db, database.SchemaFS, backupDir, configPath, storageDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	manifest, err := ValidateArchive(result.Path)
	if err != nil {
		t.Fatalf("ValidateArchive: %v", err)
	}
	if manifest.Version != 1 {
		t.Errorf("Version = %d, want 1", manifest.Version)
	}
	if manifest.Format != "sql-dump" {
		t.Errorf("Format = %q, want \"sql-dump\"", manifest.Format)
	}
	if manifest.AppVersion == "" {
		t.Error("AppVersion is empty")
	}
}

func TestValidateArchive_InvalidGzip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.tar.gz")
	testutil.CreateTestFile(t, path, "not gzip")

	_, err := ValidateArchive(path)
	if err == nil {
		t.Fatal("ValidateArchive() expected error for invalid gzip")
	}
}

func TestValidateArchive_MissingManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-manifest.tar.gz")

	f, _ := os.Create(path)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: "dummy.txt", Size: 4, Mode: 0644})
	tw.Write([]byte("test"))
	tw.Close()
	gw.Close()
	f.Close()

	_, err := ValidateArchive(path)
	if err == nil {
		t.Fatal("ValidateArchive() expected error for missing manifest")
	}
}

func TestValidateArchive_MissingFile(t *testing.T) {
	_, err := ValidateArchive("/nonexistent/archive.tar.gz")
	if err == nil {
		t.Fatal("ValidateArchive() expected error for missing file")
	}
}

func TestExtractArchive_Valid(t *testing.T) {
	db := database.NewTestDB(t)
	t.Cleanup(func() { database.ResetTestDatabase(db) })

	dir := t.TempDir()

	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")

	storageDir := filepath.Join(dir, "storage")
	os.MkdirAll(filepath.Join(storageDir, "sub"), 0755)
	testutil.CreateTestFile(t, filepath.Join(storageDir, "file.txt"), "storage content")
	testutil.CreateTestFile(t, filepath.Join(storageDir, "sub", "nested.txt"), "nested")

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	result, err := Create(context.Background(), db, database.SchemaFS, backupDir, configPath, storageDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	destDir := filepath.Join(dir, "extract")
	if err := ExtractArchive(result.Path, destDir); err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}

	for _, want := range []string{"edub.sql", "config.yaml", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(destDir, want)); err != nil {
			t.Errorf("%s not extracted: %v", want, err)
		}
	}
	for _, want := range []string{"storage/file.txt", "storage/sub/nested.txt"} {
		if _, err := os.Stat(filepath.Join(destDir, want)); err != nil {
			t.Errorf("%s not extracted: %v", want, err)
		}
	}
}

func TestExtractArchive_PathTraversal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malicious.tar.gz")

	f, _ := os.Create(path)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: "../../etc/passwd", Size: 5, Mode: 0644})
	tw.Write([]byte("pwned"))
	tw.Close()
	gw.Close()
	f.Close()

	err := ExtractArchive(path, filepath.Join(t.TempDir(), "dest"))
	if err == nil {
		t.Fatal("ExtractArchive() expected error for path traversal")
	}
}

func TestExtractArchive_SymlinkSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "symlink.tar.gz")

	f, _ := os.Create(path)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Size: 0})
	tw.Close()
	gw.Close()
	f.Close()

	destDir := filepath.Join(t.TempDir(), "dest")
	err := ExtractArchive(path, destDir)
	if err != nil {
		t.Fatalf("ExtractArchive with symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "link")); err == nil {
		t.Error("symlink should have been skipped, but file exists")
	}
}

func TestReplaceFiles_SQLDump(t *testing.T) {
	db := database.NewTestDB(t)
	t.Cleanup(func() { database.ResetTestDatabase(db) })

	dir := t.TempDir()

	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")

	storageDir := filepath.Join(dir, "storage")
	os.MkdirAll(storageDir, 0755)
	testutil.CreateTestFile(t, filepath.Join(storageDir, "old.txt"), "old content")

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	result, err := Create(context.Background(), db, database.SchemaFS, backupDir, configPath, storageDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	extractDir := filepath.Join(dir, "extract")
	if err := ExtractArchive(result.Path, extractDir); err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}

	restoreDB := database.NewTestDB(t)
	t.Cleanup(func() {
		restoreDB.Exec("DROP SCHEMA IF EXISTS public CASCADE")
		restoreDB.Exec("CREATE SCHEMA public")
		database.InitializeSchema(restoreDB)
	})

	restoreDBPath := filepath.Join(dir, "restored.db")
	if err := ReplaceFiles(extractDir, restoreDB, restoreDBPath, configPath, filepath.Join(dir, "new_storage")); err != nil {
		t.Fatalf("ReplaceFiles: %v", err)
	}

	var count int
	if err := restoreDB.QueryRow("SELECT COUNT(*) FROM document_type").Scan(&count); err != nil {
		t.Fatalf("query restored db: %v", err)
	}
	if count == 0 {
		t.Error("no rows found in document_type after restore")
	}
}

func TestReplaceFiles_ConfigOnly(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, "target.yaml")
	testutil.CreateTestFile(t, configPath, "old: true\n")

	extractDir := filepath.Join(dir, "extract")
	os.MkdirAll(extractDir, 0755)
	testutil.CreateTestFile(t, filepath.Join(extractDir, "config.yaml"), "new: true\n")

	manifestData := `{"version":1,"format":"sqlite-file","timestamp":"","app_version":"","db_size_bytes":0,"storage_files_count":0,"storage_size_bytes":0,"config_hash":""}`
	testutil.CreateTestFile(t, filepath.Join(extractDir, "manifest.json"), manifestData)

	if err := ReplaceFiles(extractDir, nil, "/nonexistent/db", configPath, t.TempDir()); err != nil {
		t.Fatalf("ReplaceFiles (no db, sqlite format): %v", err)
	}

	data, _ := os.ReadFile(configPath)
	if string(data) != "new: true\n" {
		t.Errorf("config = %q, want %q", string(data), "new: true\n")
	}
}

func TestCopyDir(t *testing.T) {
	dir := t.TempDir()

	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	os.MkdirAll(filepath.Join(src, "sub"), 0755)
	testutil.CreateTestFile(t, filepath.Join(src, "a.txt"), "alpha")
	testutil.CreateTestFile(t, filepath.Join(src, "sub", "b.txt"), "beta")

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dst, "a.txt"))
	if string(data) != "alpha" {
		t.Errorf("a.txt = %q, want %q", string(data), "alpha")
	}
	data, _ = os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	if string(data) != "beta" {
		t.Errorf("sub/b.txt = %q, want %q", string(data), "beta")
	}
}
