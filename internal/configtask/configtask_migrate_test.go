package configtask

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func TestMigrateDBTask(t *testing.T) {
	baseDSN := os.Getenv("TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	queries, srcDB := database.NewTestQueries(t)
	database.CreateTestDocument(t, queries, "migration test doc")
	if _, err := srcDB.ExecContext(ctx, "INSERT INTO tag (name) VALUES ('migration-test-tag') ON CONFLICT DO NOTHING"); err != nil {
		t.Fatalf("insert tag: %v", err)
	}

	srcCount := countRows(t, srcDB, "document")

	configDir := t.TempDir()
	storageDir := filepath.Join(configDir, "storage")
	os.MkdirAll(storageDir, 0755)

	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	port, _ := strconv.Atoi(u.Port())
	password, _ := u.User.Password()
	var srcDBName string
	if err := srcDB.QueryRowContext(ctx, "SELECT current_database()").Scan(&srcDBName); err != nil {
		t.Fatalf("get source database name: %v", err)
	}

	if err := config.SaveMap(configDir, map[string]any{
		"database.host":           u.Hostname(),
		"database.port":           port,
		"database.user":           u.User.Username(),
		"database.password":       password,
		"database.database":       srcDBName,
		"database.sslmode":        u.Query().Get("sslmode"),
		"storage.storage_dir":     storageDir,
		"storage.consumption_dir": filepath.Join(configDir, "inbox"),
	}); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// A document whose paths point into the current storage dir, to exercise
	// the path rewrite when the storage dir moves.
	if _, err := srcDB.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path)
		VALUES ('migration-path-doc', 'path doc',
		        '9e107d9d372bb6826bd81d3542a419d6',
		        '00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000',
		        'application/pdf', 10, %s, %s)`,
		quoteLiteral(filepath.Join(storageDir, "orig", "1.pdf")),
		quoteLiteral(filepath.Join(storageDir, "files", "1.pdf")))); err != nil {
		t.Fatalf("insert path document: %v", err)
	}

	newStorageDir := filepath.Join(t.TempDir(), "moved-storage")
	targetDBName := srcDBName + "_migrate"

	payload, _ := json.Marshal(MigrateDBPayload{
		Op:                opMigrateDB,
		ConfigDir:         configDir,
		Host:              u.Hostname(),
		Port:              strconv.Itoa(port),
		User:              u.User.Username(),
		Password:          password,
		Database:          targetDBName,
		SSLMode:           u.Query().Get("sslmode"),
		OldStorageDir:     storageDir,
		NewStorageDir:     newStorageDir,
		OldConsumptionDir: filepath.Join(configDir, "inbox"),
		NewConsumptionDir: filepath.Join(configDir, "inbox"),
	})

	h := NewConfigTaskHandler(testutil.NewTestLogger())
	if _, err := h.Handle(ctx, task.Task{TaskID: "migrate-test", Payload: payload}); err != nil {
		t.Fatalf("Handle(migrate-db): %v", err)
	}

	// A retry after the migration completed must be idempotent: the handler
	// detects the destination schema and only re-persists the config.
	if _, err := h.Handle(ctx, task.Task{TaskID: "migrate-test-retry", Payload: payload}); err != nil {
		t.Fatalf("Handle(migrate-db) retry: %v", err)
	}

	// Backup lock must be released after the migration.
	locked, err := queries.IsBackupLocked(ctx)
	if err != nil {
		t.Fatalf("check backup lock: %v", err)
	}
	if locked != 0 {
		t.Fatal("backup lock still held after migration")
	}

	targetDSN := replaceDSNDatabase(baseDSN, targetDBName)
	targetDB, err := database.NewPostgresDB(targetDSN)
	if err != nil {
		t.Fatalf("open target database: %v", err)
	}
	defer targetDB.Close()
	t.Cleanup(func() { dropTestDatabase(t, baseDSN, targetDBName) })

	if got := countRows(t, targetDB, "document"); got != srcCount+1 {
		t.Errorf("target document count = %d, want %d", got, srcCount+1)
	}

	var tagCount int
	if err := targetDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM tag WHERE name = 'migration-test-tag'").Scan(&tagCount); err != nil {
		t.Fatalf("count tag: %v", err)
	}
	if tagCount != 1 {
		t.Errorf("target tag count = %d, want 1", tagCount)
	}

	// goose_db_version must survive the dump so InitializeSchema is a no-op.
	if err := database.InitializeSchema(targetDB); err != nil {
		t.Errorf("InitializeSchema on migrated database: %v", err)
	}
	var srcVersion, dstVersion int64
	if err := srcDB.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version").Scan(&srcVersion); err != nil {
		t.Fatalf("source goose version: %v", err)
	}
	if err := targetDB.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version").Scan(&dstVersion); err != nil {
		t.Fatalf("target goose version: %v", err)
	}
	if dstVersion != srcVersion {
		t.Errorf("goose version = %d, want %d", dstVersion, srcVersion)
	}

	var storagePath string
	if err := targetDB.QueryRowContext(ctx, "SELECT storage_path FROM document WHERE document_id = 'migration-path-doc'").Scan(&storagePath); err != nil {
		t.Fatalf("query migrated path: %v", err)
	}
	if !strings.HasPrefix(storagePath, newStorageDir) {
		t.Errorf("storage_path = %q, want prefix %q", storagePath, newStorageDir)
	}

	// The handler must have persisted the new connection settings.
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("load config after migration: %v", err)
	}
	if cfg.Db.Database != targetDBName {
		t.Errorf("config database = %q, want %q", cfg.Db.Database, targetDBName)
	}
	if cfg.Storage.StorageDir != newStorageDir {
		t.Errorf("config storage_dir = %q, want %q", cfg.Storage.StorageDir, newStorageDir)
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func quoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}

func replaceDSNDatabase(dsn, dbName string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + dbName
	u.RawPath = ""
	return u.String()
}

func dropTestDatabase(t *testing.T, baseDSN, dbName string) {
	t.Helper()
	admin := replaceDSNDatabase(baseDSN, "postgres")
	adminDB, err := database.NewPostgresDB(admin)
	if err != nil {
		return
	}
	defer adminDB.Close()
	adminDB.ExecContext(context.Background(),
		fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", quoteIdent(dbName)))
}

func TestMigrateDBTask_RefusesForeignDestination(t *testing.T) {
	baseDSN := os.Getenv("TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	_, srcDB := database.NewTestQueries(t)

	configDir := t.TempDir()
	storageDir := filepath.Join(configDir, "storage")
	os.MkdirAll(storageDir, 0755)

	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	port, _ := strconv.Atoi(u.Port())
	password, _ := u.User.Password()
	var srcDBName string
	if err := srcDB.QueryRowContext(ctx, "SELECT current_database()").Scan(&srcDBName); err != nil {
		t.Fatalf("get source database name: %v", err)
	}

	if err := config.SaveMap(configDir, map[string]any{
		"database.host":           u.Hostname(),
		"database.port":           port,
		"database.user":           u.User.Username(),
		"database.password":       password,
		"database.database":       srcDBName,
		"database.sslmode":        u.Query().Get("sslmode"),
		"storage.storage_dir":     storageDir,
		"storage.consumption_dir": filepath.Join(configDir, "inbox"),
	}); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// A database that is not an edub database: has tables, no goose history.
	foreignName := srcDBName + "_foreign"
	foreignDSN := replaceDSNDatabase(baseDSN, foreignName)
	foreignDB, err := database.NewPostgresDB(foreignDSN)
	if err != nil {
		t.Fatalf("open foreign database: %v", err)
	}
	if _, err := foreignDB.ExecContext(ctx, "CREATE TABLE myapp_data (id int)"); err != nil {
		t.Fatalf("create foreign table: %v", err)
	}
	foreignDB.Close()
	t.Cleanup(func() { dropTestDatabase(t, baseDSN, foreignName) })

	payload, _ := json.Marshal(MigrateDBPayload{
		Op:                opMigrateDB,
		ConfigDir:         configDir,
		Host:              u.Hostname(),
		Port:              strconv.Itoa(port),
		User:              u.User.Username(),
		Password:          password,
		Database:          foreignName,
		SSLMode:           u.Query().Get("sslmode"),
		OldStorageDir:     storageDir,
		NewStorageDir:     storageDir,
		OldConsumptionDir: filepath.Join(configDir, "inbox"),
		NewConsumptionDir: filepath.Join(configDir, "inbox"),
	})

	h := NewConfigTaskHandler(testutil.NewTestLogger())
	if _, err := h.Handle(ctx, task.Task{TaskID: "migrate-foreign", Payload: payload}); err == nil {
		t.Fatal("Handle(migrate-db) against a foreign database succeeded, want refusal")
	}

	// The foreign database must be untouched.
	still, err := database.NewPostgresDB(foreignDSN)
	if err != nil {
		t.Fatalf("reopen foreign database: %v", err)
	}
	defer still.Close()
	var tableCount int
	if err := still.QueryRowContext(ctx, "SELECT COUNT(*) FROM pg_tables WHERE schemaname = 'public' AND tablename = 'myapp_data'").Scan(&tableCount); err != nil {
		t.Fatalf("check foreign table: %v", err)
	}
	if tableCount != 1 {
		t.Errorf("foreign table count = %d, want 1 (database must not be replaced)", tableCount)
	}
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
