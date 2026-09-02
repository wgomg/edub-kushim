package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func restoreDBConfig(t *testing.T) (config.DatabaseConfig, string) {
	t.Helper()
	return config.DatabaseConfig{Runtime: "host"}, database.TestDSN(t)
}

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

	result, err := Create(context.Background(), db, database.SchemaFS, BackupModeFull, backupDir, configPath, storageDir)
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

	result, err := Create(context.Background(), db, database.SchemaFS, BackupModeFull, backupDir, configPath, storageDir)
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

	result, err := Create(context.Background(), db, database.SchemaFS, BackupModeFull, backupDir, configPath, storageDir)
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

	dbCfg, dsn := restoreDBConfig(t)
	if err := ReplaceFiles(extractDir, restoreDB, dbCfg, dsn, configPath, filepath.Join(dir, "new_storage")); err != nil {
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

func TestReplaceFiles_PathRewrite(t *testing.T) {
	dir := t.TempDir()
	oldStorage := filepath.Join(dir, "old_storage")
	newStorage := filepath.Join(dir, "new_storage")

	extractDir, configPath := createRewriteTestBackup(t, fmt.Sprintf("storage:\n  storage_dir: %q\n", oldStorage), oldStorage)

	restoreDB := newRestoreDB(t)
	dbCfg, dsn := restoreDBConfig(t)
	if err := ReplaceFiles(extractDir, restoreDB, dbCfg, dsn, configPath, newStorage); err != nil {
		t.Fatalf("ReplaceFiles: %v", err)
	}

	var storagePath, originalPath string
	if err := restoreDB.QueryRow(`SELECT storage_path, original_path FROM document WHERE document_id = 'rewrite-test-doc'`).Scan(&storagePath, &originalPath); err != nil {
		t.Fatalf("query document: %v", err)
	}
	if !strings.HasPrefix(storagePath, newStorage) {
		t.Errorf("document storage_path = %q, want prefix %q", storagePath, newStorage)
	}
	if !strings.HasPrefix(originalPath, newStorage) {
		t.Errorf("document original_path = %q, want prefix %q", originalPath, newStorage)
	}

	var filePath, orphanOriginalPath string
	if err := restoreDB.QueryRow(`SELECT file_path, original_path FROM orphaned_file WHERE document_key = 'rewrite-test-uuid'`).Scan(&filePath, &orphanOriginalPath); err != nil {
		t.Fatalf("query orphaned_file: %v", err)
	}
	if !strings.HasPrefix(filePath, newStorage) {
		t.Errorf("orphaned file_path = %q, want prefix %q", filePath, newStorage)
	}
	if orphanOriginalPath != "processed/2026/07/15/14/uuid.pdf" {
		t.Errorf("orphaned original_path = %q, want relative path untouched", orphanOriginalPath)
	}
}

func TestReplaceFiles_PathRewrite_SameDirNoOp(t *testing.T) {
	dir := t.TempDir()
	oldStorage := filepath.Join(dir, "old_storage")

	extractDir, configPath := createRewriteTestBackup(t, fmt.Sprintf("storage:\n  storage_dir: %q\n", oldStorage), oldStorage)

	restoreDB := newRestoreDB(t)
	dbCfg, dsn := restoreDBConfig(t)
	if err := ReplaceFiles(extractDir, restoreDB, dbCfg, dsn, configPath, oldStorage); err != nil {
		t.Fatalf("ReplaceFiles: %v", err)
	}

	var storagePath string
	if err := restoreDB.QueryRow(`SELECT storage_path FROM document WHERE document_id = 'rewrite-test-doc'`).Scan(&storagePath); err != nil {
		t.Fatalf("query document: %v", err)
	}
	if !strings.HasPrefix(storagePath, oldStorage) {
		t.Errorf("storage_path = %q, want unchanged prefix %q", storagePath, oldStorage)
	}
}

func TestReplaceFiles_PathRewrite_ManifestFallback(t *testing.T) {
	dir := t.TempDir()
	oldStorage := filepath.Join(dir, "storage")
	newStorage := filepath.Join(dir, "new_storage")

	extractDir, configPath := createRewriteTestBackup(t, fmt.Sprintf("storage:\n  storage_dir: %s\n", oldStorage), oldStorage)

	manifestPath := filepath.Join(extractDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	delete(m, "storage_dir")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, out, 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	restoreDB := newRestoreDB(t)
	dbCfg, dsn := restoreDBConfig(t)
	if err := ReplaceFiles(extractDir, restoreDB, dbCfg, dsn, configPath, newStorage); err != nil {
		t.Fatalf("ReplaceFiles: %v", err)
	}

	var storagePath string
	if err := restoreDB.QueryRow(`SELECT storage_path FROM document WHERE document_id = 'rewrite-test-doc'`).Scan(&storagePath); err != nil {
		t.Fatalf("query document: %v", err)
	}
	if !strings.HasPrefix(storagePath, newStorage) {
		t.Errorf("storage_path = %q, want prefix %q", storagePath, newStorage)
	}
}

func TestReplaceFiles_PathRewrite_TildeRejected(t *testing.T) {
	dir := t.TempDir()
	oldStorage := filepath.Join(dir, "storage")
	newStorage := filepath.Join(dir, "new_storage")

	extractDir, configPath := createRewriteTestBackup(t, fmt.Sprintf("storage:\n  storage_dir: %s\n", oldStorage), oldStorage)

	manifestPath := filepath.Join(extractDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	delete(m, "storage_dir")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, out, 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Rewrite config.yaml in the archive to use ~
	testutil.CreateTestFile(t, filepath.Join(extractDir, "config.yaml"), "storage:\n  storage_dir: ~/storage\n")

	restoreDB := newRestoreDB(t)
	dbCfg, dsn := restoreDBConfig(t)
	if err := ReplaceFiles(extractDir, restoreDB, dbCfg, dsn, configPath, newStorage); err != nil {
		t.Fatalf("ReplaceFiles: %v", err)
	}

	// Paths should be unchanged — ~ expansion is rejected for host-dependent safety
	var storagePath string
	if err := restoreDB.QueryRow(`SELECT storage_path FROM document WHERE document_id = 'rewrite-test-doc'`).Scan(&storagePath); err != nil {
		t.Fatalf("query document: %v", err)
	}
	if !strings.HasPrefix(storagePath, oldStorage) {
		t.Errorf("storage_path = %q, want unchanged prefix %q (rewrite should have been skipped)", storagePath, oldStorage)
	}
}

func TestReplaceFiles_ConfigSavedAsRestored(t *testing.T) {
	dir := t.TempDir()
	oldStorage := filepath.Join(dir, "old_storage")
	newStorage := filepath.Join(dir, "new_storage")

	extractDir, configPath := createRewriteTestBackup(t, fmt.Sprintf("storage:\n  storage_dir: %q\n", oldStorage), oldStorage)

	editedConfig := "storage:\n  storage_dir: /edited/storage\n"
	testutil.CreateTestFile(t, configPath, editedConfig)

	restoreDB := newRestoreDB(t)
	dbCfg, dsn := restoreDBConfig(t)
	if err := ReplaceFiles(extractDir, restoreDB, dbCfg, dsn, configPath, newStorage); err != nil {
		t.Fatalf("ReplaceFiles: %v", err)
	}

	current, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read current config: %v", err)
	}
	if string(current) != editedConfig {
		t.Errorf("current config was modified: %q, want %q", string(current), editedConfig)
	}

	restored, err := os.ReadFile(configPath + ".restored")
	if err != nil {
		t.Fatalf("read restored config: %v", err)
	}
	if !strings.Contains(string(restored), oldStorage) {
		t.Errorf("restored config missing archived storage dir: %q", string(restored))
	}
}

func createRewriteTestBackup(t *testing.T, configContent, oldStorage string) (extractDir, configPath string) {
	t.Helper()
	db := database.NewTestDB(t)
	t.Cleanup(func() { database.ResetTestDatabase(db) })

	dir := t.TempDir()
	configPath = filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, configContent)

	os.MkdirAll(oldStorage, 0755)
	insertRewriteTestRows(t, db, oldStorage)

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	result, err := Create(context.Background(), db, database.SchemaFS, BackupModeFull, backupDir, configPath, oldStorage)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	extractDir = filepath.Join(dir, "extract")
	if err := ExtractArchive(result.Path, extractDir); err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	return extractDir, configPath
}

func insertRewriteTestRows(t *testing.T, db *sql.DB, base string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path)
		VALUES ('rewrite-test-doc', 'rewrite test', 'd41d8cd98f00b204e9800998ecf8427e',
		        'cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e',
		        'application/pdf', 10, $1, $2)`,
		filepath.Join(base, "originals", "doc.pdf"), filepath.Join(base, "processed", "doc.pdf")); err != nil {
		t.Fatalf("insert document: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO orphaned_file (document_key, document_key_type, file_path, original_path, source_dir, file_size)
		VALUES ('rewrite-test-uuid', 'uuid', $1, 'processed/2026/07/15/14/uuid.pdf', 'processed', 10)`,
		filepath.Join(base, "quarantine", "orphan.pdf")); err != nil {
		t.Fatalf("insert orphaned_file: %v", err)
	}
}

func newRestoreDB(t *testing.T) *sql.DB {
	t.Helper()
	db := database.NewTestDB(t)
	t.Cleanup(func() {
		db.Exec("DROP SCHEMA IF EXISTS public CASCADE")
		db.Exec("CREATE SCHEMA public")
		database.InitializeSchema(db)
	})
	return db
}

func TestReplaceFiles_UnknownFormat(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, "target.yaml")
	testutil.CreateTestFile(t, configPath, "old: true\n")

	extractDir := filepath.Join(dir, "extract")
	os.MkdirAll(extractDir, 0755)
	testutil.CreateTestFile(t, filepath.Join(extractDir, "config.yaml"), "new: true\n")

	manifestData := `{"version":1,"format":"sqlite-file","timestamp":"","app_version":"","db_size_bytes":0,"storage_files_count":0,"storage_size_bytes":0,"config_hash":""}`
	testutil.CreateTestFile(t, filepath.Join(extractDir, "manifest.json"), manifestData)

	dbCfg, dsn := restoreDBConfig(t)
	err := ReplaceFiles(extractDir, nil, dbCfg, dsn, configPath, t.TempDir())
	if err == nil {
		t.Fatal("ReplaceFiles: expected error for unknown format, got nil")
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

func TestReplaceFiles_ThumbnailsRestored(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")

	storageDir := filepath.Join(dir, "storage")
	thumbRel := filepath.Join("thumbnails", "2026", "07", "15", "14", "doc1.jpg")
	os.MkdirAll(filepath.Join(storageDir, "thumbnails", "2026", "07", "15", "14"), 0755)
	testutil.CreateTestFile(t, filepath.Join(storageDir, thumbRel), "fake jpg")

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	result, err := Create(context.Background(), nil, database.SchemaFS, BackupModeDocuments, backupDir, configPath, storageDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	extractDir := filepath.Join(dir, "extract")
	if err := ExtractArchive(result.Path, extractDir); err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}

	newStorage := filepath.Join(dir, "new_storage")
	dbCfg, dsn := restoreDBConfig(t)
	if err := ReplaceFiles(extractDir, nil, dbCfg, dsn, configPath, newStorage); err != nil {
		t.Fatalf("ReplaceFiles: %v", err)
	}

	if _, err := os.Stat(filepath.Join(newStorage, thumbRel)); err != nil {
		t.Errorf("thumbnail %s missing after restore: %v", thumbRel, err)
	}
}

// createModeBackup makes a full backup with Create, then patches manifest.Mode
// to the requested value and re-writes it to the extract directory.
func createModeBackup(t *testing.T, mode BackupMode) (string, string) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")
	storageDir := filepath.Join(dir, "storage")
	os.MkdirAll(storageDir, 0755)
	testutil.CreateTestFile(t, filepath.Join(storageDir, "doc.pdf"), "fake pdf")
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	db := database.NewTestDB(t)
	t.Cleanup(func() { database.ResetTestDatabase(db) })

	result, err := Create(context.Background(), db, database.SchemaFS, BackupModeFull, backupDir, configPath, storageDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	extract := filepath.Join(dir, "extract")
	if err := ExtractArchive(result.Path, extract); err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}

	manifestPath := filepath.Join(extract, "manifest.json")
	data, _ := os.ReadFile(manifestPath)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	m["mode"] = string(mode)
	out, _ := json.Marshal(m)
	if err := os.WriteFile(manifestPath, out, 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return extract, configPath
}

func TestReplaceFiles_DocumentsMode_SkipsSQL(t *testing.T) {
	dir := t.TempDir()
	db := database.NewTestDB(t)
	t.Cleanup(func() { database.ResetTestDatabase(db) })

	if _, err := db.Exec(`
		INSERT INTO document_type (name) VALUES ('documents-mode-marker')`); err != nil {
		t.Fatalf("seed document_type: %v", err)
	}
	originalID := readDocumentTypeID(t, db, "documents-mode-marker")

	extract, _ := createModeBackup(t, BackupModeDocuments)

	// documents-mode restore must not execute the SQL dump. If it does,
	// the document_type row gets dropped and recreated with a new id.
	dbCfg, dsn := restoreDBConfig(t)
	if err := ReplaceFiles(extract, db, dbCfg, dsn, filepath.Join(dir, "config.yaml"), filepath.Join(dir, "new_storage")); err != nil {
		t.Fatalf("ReplaceFiles: %v", err)
	}

	persistedID := readDocumentTypeID(t, db, "documents-mode-marker")
	if persistedID != originalID {
		t.Errorf("documents-mode restore mutated the DB: id changed from %d to %d", originalID, persistedID)
	}
}

func TestReplaceFiles_DatabaseMode_StorageUntouched(t *testing.T) {
	dir := t.TempDir()
	storageDir := filepath.Join(dir, "storage")
	os.MkdirAll(storageDir, 0755)
	testutil.CreateTestFile(t, filepath.Join(storageDir, "untouched.txt"), "do not touch")

	extract, configPath := createModeBackup(t, BackupModeDatabase)
	newStorage := filepath.Join(dir, "new_storage")

	db := newRestoreDB(t)
	dbCfg, dsn := restoreDBConfig(t)
	if err := ReplaceFiles(extract, db, dbCfg, dsn, configPath, newStorage); err != nil {
		t.Fatalf("ReplaceFiles: %v", err)
	}

	if _, err := os.Stat(newStorage); err == nil {
		t.Error("database-mode restore created new_storage (must skip storage swap)")
	}
	original, err := os.ReadFile(filepath.Join(storageDir, "untouched.txt"))
	if err != nil {
		t.Fatalf("read original storage: %v", err)
	}
	if string(original) != "do not touch" {
		t.Errorf("original storage modified: %q", string(original))
	}
}

func TestReplaceFiles_LegacyManifestNoMode_TreatedAsFull(t *testing.T) {
	dir := t.TempDir()
	db := database.NewTestDB(t)
	t.Cleanup(func() { database.ResetTestDatabase(db) })

	extract, _ := createModeBackup(t, BackupModeFull)
	manifestPath := filepath.Join(extract, "manifest.json")
	data, _ := os.ReadFile(manifestPath)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	delete(m, "mode")
	out, _ := json.Marshal(m)
	if err := os.WriteFile(manifestPath, out, 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM document_type").Scan(&count); err != nil {
		t.Fatalf("count document_type: %v", err)
	}
	if count == 0 {
		t.Fatal("preseed no rows in document_type")
	}

	dbCfg, dsn := restoreDBConfig(t)
	if err := ReplaceFiles(extract, db, dbCfg, dsn, filepath.Join(dir, "config.yaml"), filepath.Join(dir, "storage")); err != nil {
		t.Fatalf("ReplaceFiles: %v", err)
	}

	var afterCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM document_type").Scan(&afterCount); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if afterCount != count {
		t.Errorf("document_type count = %d, want %d (legacy restore as full must re-execute SQL)", afterCount, count)
	}
}

func readDocumentTypeID(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM document_type WHERE name = $1`, name).Scan(&id); err != nil {
		t.Fatalf("query document_type id: %v", err)
	}
	return id
}
