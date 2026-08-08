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

	// A document with path columns, to exercise the dump/restore of those
	// fields across the migration.
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

	targetDBName := srcDBName + "_migrate"

	payload, _ := json.Marshal(MigrateDBPayload{
		Op:        opMigrateDB,
		ConfigDir: configDir,
		Host:      u.Hostname(),
		Port:      strconv.Itoa(port),
		User:      u.User.Username(),
		Password:  password,
		Database:  targetDBName,
		SSLMode:   u.Query().Get("sslmode"),
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

	// The handler must have persisted the new connection settings.
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("load config after migration: %v", err)
	}
	if cfg.Db.Database != targetDBName {
		t.Errorf("config database = %q, want %q", cfg.Db.Database, targetDBName)
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
		Op:        opMigrateDB,
		ConfigDir: configDir,
		Host:      u.Hostname(),
		Port:      strconv.Itoa(port),
		User:      u.User.Username(),
		Password:  password,
		Database:  foreignName,
		SSLMode:   u.Query().Get("sslmode"),
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

func TestMigrateStorageTask(t *testing.T) {
	baseDSN := os.Getenv("TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	for _, mode := range []string{"copy", "move"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()

			queries, db := database.NewTestQueries(t)

			u, err := url.Parse(baseDSN)
			if err != nil {
				t.Fatalf("parse TEST_DATABASE_URL: %v", err)
			}
			port, _ := strconv.Atoi(u.Port())
			password, _ := u.User.Password()
			var dbName string
			if err := db.QueryRowContext(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
				t.Fatalf("get database name: %v", err)
			}

			configDir := t.TempDir()
			oldStorage := filepath.Join(configDir, "storage")
			oldInbox := filepath.Join(configDir, "inbox")
			newStorage := filepath.Join(configDir, "storage-new")
			newInbox := filepath.Join(configDir, "inbox-new")

			if err := config.SaveMap(configDir, map[string]any{
				"database.host":           u.Hostname(),
				"database.port":           port,
				"database.user":           u.User.Username(),
				"database.password":       password,
				"database.database":       dbName,
				"database.sslmode":        u.Query().Get("sslmode"),
				"storage.storage_dir":     oldStorage,
				"storage.consumption_dir": oldInbox,
				"storage.migration_mode":  mode,
			}); err != nil {
				t.Fatalf("write config: %v", err)
			}

			storageFiles := map[string]string{
				filepath.Join("processed", "2026", "07", "15", "14"): "doc1.pdf",
				filepath.Join("originals", "2026", "07", "15", "14"): "doc1.pdf",
				"errors":                               "fail1.pdf",
				filepath.Join("errors", "duplicated"):  "dup1.pdf",
				filepath.Join("orphaned", "processed"): "orphan1.pdf",
				"trash":                                "trashed.pdf",
			}
			for dir, name := range storageFiles {
				fullDir := filepath.Join(oldStorage, dir)
				if err := os.MkdirAll(fullDir, 0755); err != nil {
					t.Fatalf("create %s: %v", fullDir, err)
				}
				if err := os.WriteFile(filepath.Join(fullDir, name), []byte("data"), 0644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			os.MkdirAll(oldInbox, 0755)
			if err := os.WriteFile(filepath.Join(oldInbox, "inbox1.pdf"), []byte("inbox"), 0644); err != nil {
				t.Fatalf("write inbox file: %v", err)
			}

			// A document whose paths point into the old storage dir.
			docStorage := filepath.Join(oldStorage, "processed", "2026", "07", "15", "14", "doc1.pdf")
			docOriginal := filepath.Join(oldStorage, "originals", "2026", "07", "15", "14", "doc1.pdf")
			if _, err := db.ExecContext(ctx, fmt.Sprintf(`
				INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, original_type, file_size, original_path, storage_path)
				VALUES ('storage-migrate-doc', 'migrate doc',
				        '9e107d9d372bb6826bd81d3542a419d6',
				        '00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000',
				        'application/pdf', 10, %s, %s)`,
				quoteLiteral(docOriginal),
				quoteLiteral(docStorage))); err != nil {
				t.Fatalf("insert document: %v", err)
			}

			orphanPath := filepath.Join(oldStorage, "orphaned", "processed", "orphan1.pdf")
			if _, err := queries.CreateOrphanedFile(ctx, database.CreateOrphanedFileParams{
				DocumentKey:     "orphan1",
				DocumentKeyType: "uuid",
				FilePath:        orphanPath,
				OriginalPath:    filepath.Join("processed", "orphan1.pdf"),
				SourceDir:       "processed",
				FileSize:        4,
				OriginalType:    "application/pdf",
			}); err != nil {
				t.Fatalf("insert orphaned file: %v", err)
			}

			// A pending consume task whose payload references the old inbox.
			rewritten := json.RawMessage(`{"file_path":` + strconv.Quote(filepath.Join(oldInbox, "inbox1.pdf")) + `,"file_index":1}`)
			if _, err := queries.CreateTask(ctx, database.CreateTaskParams{
				TaskID: "stor-migrate-consume", TaskType: "consume", Status: "pending",
				Payload: &rewritten,
			}); err != nil {
				t.Fatalf("insert consume task: %v", err)
			}
			// A consume task pointing elsewhere must stay untouched.
			unrelated := json.RawMessage(`{"file_path":"/elsewhere/other.pdf"}`)
			if _, err := queries.CreateTask(ctx, database.CreateTaskParams{
				TaskID: "stor-migrate-other", TaskType: "consume", Status: "pending",
				Payload: &unrelated,
			}); err != nil {
				t.Fatalf("insert unrelated consume task: %v", err)
			}

			payload, _ := json.Marshal(MigrateStoragePayload{
				Op:                opMigrateStorage,
				ConfigDir:         configDir,
				OldStorageDir:     oldStorage,
				NewStorageDir:     newStorage,
				OldConsumptionDir: oldInbox,
				NewConsumptionDir: newInbox,
			})

			h := NewConfigTaskHandler(testutil.NewTestLogger())
			if _, err := h.Handle(ctx, task.Task{TaskID: "migrate-storage-test", Payload: payload}); err != nil {
				t.Fatalf("Handle(migrate-storage): %v", err)
			}

			// Files must have moved, old location left empty.
			for dir, name := range storageFiles {
				if _, err := os.Stat(filepath.Join(newStorage, dir, name)); err != nil {
					t.Errorf("new storage file %s/%s missing: %v", dir, name, err)
				}
				if _, err := os.Stat(filepath.Join(oldStorage, dir)); !os.IsNotExist(err) {
					t.Errorf("old storage dir %s still exists", dir)
				}
			}
			if _, err := os.Stat(filepath.Join(newInbox, "inbox1.pdf")); err != nil {
				t.Errorf("new inbox file missing: %v", err)
			}
			if entries, err := os.ReadDir(oldInbox); err != nil {
				t.Errorf("read old inbox: %v", err)
			} else if len(entries) != 0 {
				t.Errorf("old inbox not empty: %v", entries)
			}

			// Database paths must be rewritten.
			var gotStorage string
			if err := db.QueryRowContext(ctx, "SELECT storage_path FROM document WHERE document_id = 'storage-migrate-doc'").Scan(&gotStorage); err != nil {
				t.Fatalf("query document storage_path: %v", err)
			}
			if !strings.HasPrefix(gotStorage, newStorage) {
				t.Errorf("document storage_path = %q, want prefix %q", gotStorage, newStorage)
			}
			var gotOrphan string
			if err := db.QueryRowContext(ctx, "SELECT file_path FROM orphaned_file WHERE document_key = 'orphan1'").Scan(&gotOrphan); err != nil {
				t.Fatalf("query orphaned file_path: %v", err)
			}
			if !strings.HasPrefix(gotOrphan, filepath.Join(newStorage, "orphaned")) {
				t.Errorf("orphaned file_path = %q, want prefix %q", gotOrphan, filepath.Join(newStorage, "orphaned"))
			}

			// Task payloads must reference the new inbox.
			var gotRaw json.RawMessage
			var gotPayload struct {
				FilePath string `json:"file_path"`
			}
			if err := db.QueryRowContext(ctx, "SELECT payload FROM task WHERE task_id = 'stor-migrate-consume'").Scan(&gotRaw); err != nil {
				t.Fatalf("query consume task payload: %v", err)
			}
			if err := json.Unmarshal(gotRaw, &gotPayload); err != nil {
				t.Fatalf("unmarshal consume task payload: %v", err)
			}
			if gotPayload.FilePath != filepath.Join(newInbox, "inbox1.pdf") {
				t.Errorf("consume task file_path = %q, want %q", gotPayload.FilePath, filepath.Join(newInbox, "inbox1.pdf"))
			}
			if err := db.QueryRowContext(ctx, "SELECT payload FROM task WHERE task_id = 'stor-migrate-other'").Scan(&gotRaw); err != nil {
				t.Fatalf("query unrelated task payload: %v", err)
			}
			if err := json.Unmarshal(gotRaw, &gotPayload); err != nil {
				t.Fatalf("unmarshal unrelated task payload: %v", err)
			}
			if gotPayload.FilePath != "/elsewhere/other.pdf" {
				t.Errorf("unrelated consume task file_path was rewritten: %q", gotPayload.FilePath)
			}

			// Backup lock must be released and config persisted.
			locked, err := queries.IsBackupLocked(ctx)
			if err != nil {
				t.Fatalf("check backup lock: %v", err)
			}
			if locked != 0 {
				t.Error("backup lock still held after storage migration")
			}
			cfg, err := config.Load(configDir)
			if err != nil {
				t.Fatalf("load config after migration: %v", err)
			}
			if cfg.Storage.StorageDir != newStorage {
				t.Errorf("config storage_dir = %q, want %q", cfg.Storage.StorageDir, newStorage)
			}
			if cfg.Storage.ConsumptionDir != newInbox {
				t.Errorf("config consumption_dir = %q, want %q", cfg.Storage.ConsumptionDir, newInbox)
			}
			if cfg.Storage.MigrationMode != mode {
				t.Errorf("config migration_mode = %q, want %q", cfg.Storage.MigrationMode, mode)
			}
		})
	}
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
