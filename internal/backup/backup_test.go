package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func TestCreate_FullBackup(t *testing.T) {
	db := database.NewTestDB(t)
	t.Cleanup(func() { database.ResetTestDatabase(db) })

	dir := t.TempDir()

	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")

	storageDir := filepath.Join(dir, "storage", "originals")
	os.MkdirAll(storageDir, 0755)
	testutil.CreateTestFile(t, filepath.Join(storageDir, "doc.pdf"), "fake pdf")

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	result, err := Create(context.Background(), db, database.SchemaFS, BackupModeFull, backupDir, configPath, filepath.Join(dir, "storage"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if result.Path == "" {
		t.Error("result.Path is empty")
	}
	if result.SizeBytes <= 0 {
		t.Errorf("result.SizeBytes = %d, want > 0", result.SizeBytes)
	}
	if result.DbSizeBytes <= 0 {
		t.Errorf("result.DbSizeBytes = %d, want > 0", result.DbSizeBytes)
	}
	if result.FilesCount != 1 {
		t.Errorf("result.FilesCount = %d, want 1", result.FilesCount)
	}
	if result.Manifest == nil {
		t.Fatal("result.Manifest is nil")
	}
	if result.Manifest.Version != 1 {
		t.Errorf("manifest.Version = %d, want 1", result.Manifest.Version)
	}
	if result.Manifest.Format != "sql-dump" {
		t.Errorf("manifest.Format = %q, want \"sql-dump\"", result.Manifest.Format)
	}
	if !strings.HasPrefix(result.Manifest.ConfigHash, "sha256:") {
		t.Errorf("manifest.ConfigHash = %q, want sha256: prefix", result.Manifest.ConfigHash)
	}
	if result.Manifest.StorageDir != filepath.Join(dir, "storage") {
		t.Errorf("manifest.StorageDir = %q, want %q", result.Manifest.StorageDir, filepath.Join(dir, "storage"))
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Errorf("archive file missing: %v", err)
	}
}

func TestCreate_MissingDB(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	_, err := Create(context.Background(), nil, database.SchemaFS, BackupModeFull, backupDir, configPath, filepath.Join(dir, "storage"))
	if err == nil {
		t.Fatal("Create() expected error for nil db")
	}
}

func TestCreate_MissingStorageDir(t *testing.T) {
	db := database.NewTestDB(t)
	t.Cleanup(func() { database.ResetTestDatabase(db) })

	dir := t.TempDir()

	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	result, err := Create(context.Background(), db, database.SchemaFS, BackupModeFull, backupDir, configPath, "/nonexistent/storage")
	if err != nil {
		t.Fatalf("Create() with missing storage: %v", err)
	}
	if result.FilesCount != 0 {
		t.Errorf("FilesCount = %d, want 0", result.FilesCount)
	}
}

func TestCreate_NoStorageFiles(t *testing.T) {
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
	if result.FilesCount != 0 {
		t.Errorf("FilesCount = %d, want 0 for empty storage", result.FilesCount)
	}
}

func TestCreate_SQLDumpContent(t *testing.T) {
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

	extractDir := filepath.Join(dir, "extract")
	if err := ExtractArchive(result.Path, extractDir); err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}

	sqlData, err := os.ReadFile(filepath.Join(extractDir, "edub.sql"))
	if err != nil {
		t.Fatalf("read edub.sql: %v", err)
	}
	sqlContent := string(sqlData)

	if !strings.Contains(sqlContent, "BEGIN;") {
		t.Error("SQL dump missing BEGIN")
	}
	if !strings.Contains(sqlContent, "SET session_replication_role = 'replica'") {
		t.Error("SQL dump missing session_replication_role")
	}
	if !strings.Contains(sqlContent, "SET session_replication_role = 'origin'") {
		t.Error("SQL dump missing session_replication_role restore")
	}
	if !strings.Contains(sqlContent, "COMMIT;") {
		t.Error("SQL dump missing COMMIT")
	}
	if !strings.Contains(sqlContent, "CREATE TABLE document_type") {
		t.Error("SQL dump missing schema preamble")
	}
	if !strings.Contains(sqlContent, `INSERT INTO "document_type"`) {
		t.Error("SQL dump missing INSERT for document_type")
	}
	for line := range strings.SplitSeq(sqlContent, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), `INSERT INTO "document_type"`) {
			if strings.Contains(line, "text_search_vector") {
				t.Error("INSERT INTO document_type includes generated column text_search_vector")
			}
			break
		}
	}
}

func TestApplyRetention_DeleteOldest(t *testing.T) {
	dir := t.TempDir()

	for i := range 5 {
		testutil.CreateTestFile(t, filepath.Join(dir, "edub-backup-full-2026-06-30T02-00-0"+string(rune('0'+i))+":00.tar.gz"), "data")
	}

	if err := ApplyRetention(dir, BackupModeFull, 3); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("remaining = %d, want 3", len(entries))
	}
}

func TestApplyRetention_KeepAll(t *testing.T) {
	dir := t.TempDir()

	for i := range 3 {
		testutil.CreateTestFile(t, filepath.Join(dir, "edub-backup-full-2026-06-30T02-00-0"+string(rune('0'+i))+":00.tar.gz"), "data")
	}

	if err := ApplyRetention(dir, BackupModeFull, 7); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("remaining = %d, want 3 (all kept)", len(entries))
	}
}

func TestApplyRetention_KeepZeroNoOp(t *testing.T) {
	dir := t.TempDir()
	testutil.CreateTestFile(t, filepath.Join(dir, "edub-backup-full-2026-06-30T02-00-00.tar.gz"), "data")

	if err := ApplyRetention(dir, BackupModeFull, 0); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("remaining = %d, want 1 (keep=0 = no deletion)", len(entries))
	}
}

func TestCreate_DatabaseMode_ExcludesStorage(t *testing.T) {
	db := database.NewTestDB(t)
	t.Cleanup(func() { database.ResetTestDatabase(db) })

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")

	storageDir := filepath.Join(dir, "storage", "originals")
	os.MkdirAll(storageDir, 0755)
	testutil.CreateTestFile(t, filepath.Join(storageDir, "doc.pdf"), "fake pdf")

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	result, err := Create(context.Background(), db, database.SchemaFS, BackupModeDatabase, backupDir, configPath, storageDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Manifest.StorageFilesCount != 0 {
		t.Errorf("StorageFilesCount = %d, want 0 (database mode must skip storage walk)", result.Manifest.StorageFilesCount)
	}
	if result.Manifest.StorageSizeBytes != 0 {
		t.Errorf("StorageSizeBytes = %d, want 0", result.Manifest.StorageSizeBytes)
	}
	if result.FilesCount != 0 {
		t.Errorf("FilesCount = %d, want 0", result.FilesCount)
	}
	if result.Manifest.DbSizeBytes <= 0 {
		t.Errorf("DbSizeBytes = %d, want > 0 (database mode must include edub.sql)", result.Manifest.DbSizeBytes)
	}
	if result.Manifest.Mode != BackupModeDatabase {
		t.Errorf("manifest.Mode = %q, want %q", result.Manifest.Mode, BackupModeDatabase)
	}

	extractDir := filepath.Join(dir, "extract")
	if err := ExtractArchive(result.Path, extractDir); err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(extractDir, "edub.sql")); err != nil {
		t.Errorf("edub.sql missing in database archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(extractDir, "storage")); err == nil {
		t.Error("storage/ present in database archive (must be excluded)")
	}
}

func TestCreate_DocumentsMode_ExcludesDatabase(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")

	storageDir := filepath.Join(dir, "storage")
	os.MkdirAll(storageDir, 0755)
	testutil.CreateTestFile(t, filepath.Join(storageDir, "doc.pdf"), "fake pdf")

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	result, err := Create(context.Background(), nil, database.SchemaFS, BackupModeDocuments, backupDir, configPath, storageDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Manifest.DbSizeBytes != 0 {
		t.Errorf("DbSizeBytes = %d, want 0 (documents mode must skip SQL dump)", result.Manifest.DbSizeBytes)
	}
	if result.FilesCount != 1 {
		t.Errorf("FilesCount = %d, want 1 (documents mode must include storage)", result.FilesCount)
	}
	if result.Manifest.Mode != BackupModeDocuments {
		t.Errorf("manifest.Mode = %q, want %q", result.Manifest.Mode, BackupModeDocuments)
	}

	extractDir := filepath.Join(dir, "extract")
	if err := ExtractArchive(result.Path, extractDir); err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(extractDir, "edub.sql")); err == nil {
		t.Error("edub.sql present in documents archive (must be excluded)")
	}
	if _, err := os.Stat(filepath.Join(extractDir, "storage", "doc.pdf")); err != nil {
		t.Errorf("storage/doc.pdf missing in documents archive: %v", err)
	}
}

func TestCreate_InvalidMode(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	testutil.CreateTestFile(t, configPath, "test: true\n")
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)

	_, err := Create(context.Background(), nil, database.SchemaFS, "bogus", backupDir, configPath, filepath.Join(dir, "storage"))
	if err == nil {
		t.Fatal("Create() expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "invalid backup mode") {
		t.Errorf("error %q does not mention 'invalid backup mode'", err)
	}
}

func TestApplyRetention_LegacyArchivesMatchedUnderFull(t *testing.T) {
	dir := t.TempDir()

	// Archives from the pre-mode binary have no mode segment and are always
	// full backups — full-mode retention must include them or they accumulate.
	for i := range 4 {
		testutil.CreateTestFile(t, filepath.Join(dir, fmt.Sprintf("edub-backup-2026-06-30T02-00-0%d:00.tar.gz", i)), "legacy")
	}
	for i := range 2 {
		testutil.CreateTestFile(t, filepath.Join(dir, fmt.Sprintf("edub-backup-full-2026-06-30T02-00-0%d:00.tar.gz", i)), "new")
	}
	// And a non-matching archive that must NOT be pruned.
	testutil.CreateTestFile(t, filepath.Join(dir, "edub-backup-database-2026-06-30T02-00-00.tar.gz"), "other mode")

	if err := ApplyRetention(dir, BackupModeFull, 3); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// 4 legacy + 2 new = 6 matched, keep 3, so 3 of those are pruned;
	// the 1 database archive is never matched and stays on disk.
	if len(names) != 4 {
		t.Errorf("remaining count = %d, want 4 (3 pruned from matched set, 1 database untouched); names = %v", len(names), names)
	}
	var foundDatabase bool
	for _, n := range names {
		if n == "edub-backup-database-2026-06-30T02-00-00.tar.gz" {
			foundDatabase = true
		}
	}
	if !foundDatabase {
		t.Error("database archive missing from remaining list — full-mode retention pruned it")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	testutil.CreateTestFile(t, src, "hello world")

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	data, _ := os.ReadFile(dst)
	if string(data) != "hello world" {
		t.Errorf("content = %q, want %q", string(data), "hello world")
	}
}

func TestFileHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	testutil.CreateTestFile(t, path, "hello")

	hash, err := fileHash(path)
	if err != nil {
		t.Fatalf("fileHash: %v", err)
	}

	h := sha256.Sum256([]byte("hello"))
	want := hex.EncodeToString(h[:])
	got := hex.EncodeToString(hash)
	if got != want {
		t.Errorf("hash = %s, want %s", got, want)
	}
}
