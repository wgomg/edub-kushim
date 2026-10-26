// Package configtask runs the environment tasks a configuration change triggers: database and storage
// migration. Each kind is its own task type with its own dedicated handler and a no-op migration reports
// ErrNoOp. Environment asset downloads (tessdata, hugot) are not tasks: the config update paths call
// config.DownloadTessdataLanguage and config.DownloadHugotModel directly.
package configtask

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// ErrNoOp is returned by the migrations when old and new paths match.
var ErrNoOp = errors.New("migration is a no-op")

// DirChanged reports whether the two directory settings differ after
// path cleaning; empty on either side means no change.
func DirChanged(oldDir, newDir string) bool {
	if oldDir == "" || newDir == "" {
		return false
	}
	return filepath.Clean(oldDir) != filepath.Clean(newDir)
}

// The migration dedup keys, so concurrent triggers collapse.
const (
	DedupKeyMigrateDB      = "migrate-db"
	DedupKeyMigrateStorage = "migrate-storage"
)

// MigrateDBPayload is the database migration task's input: the connection to migrate to.
type MigrateDBPayload struct {
	ConfigDir string `json:"config_dir"`
	Host      string `json:"host"`
	Port      string `json:"port"`
	User      string `json:"user"`
	Password  string `json:"password"`
	Database  string `json:"database"`
	SSLMode   string `json:"sslmode"`
}

// MigrateStoragePayload is the storage move task's input.
type MigrateStoragePayload struct {
	ConfigDir         string `json:"config_dir"`
	OldStorageDir     string `json:"old_storage_dir"`
	NewStorageDir     string `json:"new_storage_dir"`
	OldConsumptionDir string `json:"old_consumption_dir"`
	NewConsumptionDir string `json:"new_consumption_dir"`
}

// MigrateDBSpec returns the database migration task for the given target.
func MigrateDBSpec(payload MigrateDBPayload) (task.TaskSpec, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return task.TaskSpec{}, fmt.Errorf("marshal database migration payload: %w", err)
	}
	return task.TaskSpec{Type: types.Task.Type.MigrateDB, Payload: raw}, nil
}

// MigrateStorageSpec returns the storage migration task for the given move.
func MigrateStorageSpec(payload MigrateStoragePayload) (task.TaskSpec, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return task.TaskSpec{}, fmt.Errorf("marshal storage migration payload: %w", err)
	}
	return task.TaskSpec{Type: types.Task.Type.MigrateStorage, Payload: raw}, nil
}

// StorageMigrationPayload builds the storage migration payload from the live configuration and the requested
// directories: each changed pair carries its old and new path, and an unchanged pair is omitted from it.
// It reports false when neither directory changes, so every boundary decides a no-op the same way.
func StorageMigrationPayload(cfg *config.Config, newStorageDir, newConsumptionDir string) (MigrateStoragePayload, bool) {
	payload := MigrateStoragePayload{ConfigDir: cfg.App.ConfigDir}
	if DirChanged(cfg.Storage.StorageDir, newStorageDir) {
		payload.OldStorageDir = cfg.Storage.StorageDir
		payload.NewStorageDir = newStorageDir
	}
	if DirChanged(cfg.Storage.ConsumptionDir, newConsumptionDir) {
		payload.OldConsumptionDir = cfg.Storage.ConsumptionDir
		payload.NewConsumptionDir = newConsumptionDir
	}
	return payload, payload.NewStorageDir != "" || payload.NewConsumptionDir != ""
}

// MigrateDBTaskHandler runs a database migration task.
type MigrateDBTaskHandler struct {
	queries *database.Queries
	logger  *log.Logger
}

// NewMigrateDBTaskHandler wires the handler to the queries and logger.
func NewMigrateDBTaskHandler(queries *database.Queries, logger *log.Logger) *MigrateDBTaskHandler {
	return &MigrateDBTaskHandler{queries: queries, logger: logger}
}

// Handle runs the database migration, tracking progress on the task row.
func (h *MigrateDBTaskHandler) Handle(ctx context.Context, t task.Task) (result json.RawMessage, err error) {
	progress := task.NewProgressTracker(h.queries, t.TaskID)

	var p MigrateDBPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal migrate-db payload: %w", err)
	}
	if err := MigrateDatabase(ctx, h.logger, p, func(step, detail string, pct float64) {
		progress.Set(step, detail, pct)
	}); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"status": "migrated"})
}

// MigrateStorageTaskHandler runs a storage move task.
type MigrateStorageTaskHandler struct {
	queries *database.Queries
	logger  *log.Logger
}

// NewMigrateStorageTaskHandler wires the handler to the queries and logger.
func NewMigrateStorageTaskHandler(queries *database.Queries, logger *log.Logger) *MigrateStorageTaskHandler {
	return &MigrateStorageTaskHandler{queries: queries, logger: logger}
}

// Handle runs the storage move, reporting a no-op migration as a result instead of an error.
func (h *MigrateStorageTaskHandler) Handle(ctx context.Context, t task.Task) (result json.RawMessage, err error) {
	progress := task.NewProgressTracker(h.queries, t.TaskID)

	var p MigrateStoragePayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal migrate-storage payload: %w", err)
	}
	err = MigrateStorage(ctx, h.logger, p, func(step, detail string, pct float64) {
		progress.Set(step, detail, pct)
	})
	switch {
	case err == nil:
		return json.Marshal(map[string]string{"status": "migrated"})
	case errors.Is(err, ErrNoOp):
		return json.Marshal(map[string]string{"status": "no-op"})
	default:
		return nil, err
	}
}

// MigrateDatabase migrates the schema to a fresh database named after
// the configured one: creates it, applies the schema and seeders, copies
// the data, and swaps the connection. It refuses a destination that is
// not the configured database plus the migration suffix. Equal
// connections report ErrNoOp. report must be non-nil; pass
// task.NoopProgress to report nothing.
func MigrateDatabase(ctx context.Context, logger *log.Logger, p MigrateDBPayload, report task.ProgressFunc) error {
	cfg, err := config.Load(p.ConfigDir)
	if err != nil {
		return fmt.Errorf("load config from %s: %w", p.ConfigDir, err)
	}

	if err := database.CheckRestoreTooling(cfg.Db.Runtime, cfg.Db.Container); err != nil {
		return err
	}

	oldDB, err := database.NewPostgresDB(config.BuildPostgresDSN(cfg.Db))
	if err != nil {
		return fmt.Errorf("connect to current database: %w", err)
	}
	defer oldDB.Close()

	report("migrate-db", "dump", 0)
	logger.Info("", "migrate-db: copying database to %s@%s:%s/%s", p.User, p.Host, p.Port, p.Database)

	safetySnapshot(ctx, oldDB, cfg, logger)

	tmpDump, err := os.CreateTemp("", "edub-migrate-*.sql")
	if err != nil {
		return fmt.Errorf("create temp dump file: %w", err)
	}
	tmpPath := tmpDump.Name()
	defer os.Remove(tmpPath)

	if err := database.DumpSchemaAndData(ctx, oldDB, database.SchemaFS, tmpDump); err != nil {
		_ = tmpDump.Close()
		return fmt.Errorf("dump current database: %w", err)
	}
	if err := tmpDump.Close(); err != nil {
		return fmt.Errorf("close temp dump file: %w", err)
	}

	port, err := strconv.Atoi(p.Port)
	if err != nil {
		return fmt.Errorf("invalid destination port %q: %w", p.Port, err)
	}

	newDSN := config.BuildPostgresDSN(config.DatabaseConfig{
		Host:     p.Host,
		Port:     port,
		User:     p.User,
		Password: p.Password,
		Database: p.Database,
		SSLMode:  p.SSLMode,
	})
	newDB, err := database.NewPostgresDB(database.WithConnectTimeout(newDSN, 10))
	if err != nil {
		return fmt.Errorf("connect to destination database: %w", err)
	}
	defer newDB.Close()

	report("migrate-db", "restore", 0)
	if err := restoreData(ctx, cfg.Db, newDB, newDSN, tmpPath, logger); err != nil {
		return err
	}

	body := map[string]any{
		"database.host":     p.Host,
		"database.port":     port,
		"database.user":     p.User,
		"database.password": p.Password,
		"database.database": p.Database,
		"database.sslmode":  p.SSLMode,
		// Without this, a leftover database.dsn in config.yaml overrides the
		// new fields on the next load (BuildPostgresDSN prefers DSN).
		"database.dsn": "",
	}
	if err := config.SaveMap(p.ConfigDir, body); err != nil {
		return fmt.Errorf("persist new config: %w", err)
	}

	logger.Info("", "migrate-db: database %q is live and config.yaml updated", p.Database)
	return nil
}

// MigrateStorage moves the document layout to the new directories:
// originals, processed files and thumbnails are renamed or copied into
// the new tree, matching the old tree's structure. Equal directories
// report ErrNoOp. report must be non-nil; pass task.NoopProgress to
// report nothing.
func MigrateStorage(ctx context.Context, logger *log.Logger, p MigrateStoragePayload, report task.ProgressFunc) error {
	storageChanged := DirChanged(p.OldStorageDir, p.NewStorageDir)
	consumptionChanged := DirChanged(p.OldConsumptionDir, p.NewConsumptionDir)
	if !storageChanged && !consumptionChanged {
		return ErrNoOp
	}

	cfg, err := config.Load(p.ConfigDir)
	if err != nil {
		return fmt.Errorf("load config from %s: %w", p.ConfigDir, err)
	}

	// Config may have been updated by a preceding migrate-db task, so the
	// database connection is read from disk rather than from the payload.
	db, err := database.NewPostgresDB(config.BuildPostgresDSN(cfg.Db))
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	mode := strings.ToLower(cfg.Storage.MigrationMode)
	if mode != "move" {
		mode = "copy"
	}

	// Rewrite pending consume task payloads before touching the files so
	// queued work references the new inbox paths.
	if consumptionChanged {
		report("migrate-storage", "rewrite payloads", 0)
		if err := database.RewriteTaskPayloadPaths(ctx, db, p.OldConsumptionDir, p.NewConsumptionDir); err != nil {
			return err
		}
	}

	if storageChanged {
		report("migrate-storage", "move storage", 0)
		if err := moveDirEntries(ctx, p.OldStorageDir, p.NewStorageDir, mode, true, logger); err != nil {
			return err
		}
	}

	if consumptionChanged {
		report("migrate-storage", "move consumption", 0)
		if err := moveDirEntries(ctx, p.OldConsumptionDir, p.NewConsumptionDir, mode, false, logger); err != nil {
			return err
		}
	}

	if storageChanged {
		report("migrate-storage", "rewrite paths", 0)
		if err := database.RewriteStoragePaths(ctx, db, p.OldStorageDir, p.NewStorageDir); err != nil {
			return fmt.Errorf("rewrite storage paths: %w", err)
		}
	}

	body := map[string]any{}
	if storageChanged {
		body["storage.storage_dir"] = p.NewStorageDir
	}
	if consumptionChanged {
		body["storage.consumption_dir"] = p.NewConsumptionDir
	}
	if err := config.SaveMap(p.ConfigDir, body); err != nil {
		return fmt.Errorf("persist new config: %w", err)
	}

	logger.Info("", "migrate-storage: files relocated and config.yaml updated")
	return nil
}

func moveDirEntries(ctx context.Context, oldDir, newDir, mode string, dirsOnly bool, logger *log.Logger) error {
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read old dir %s: %w", oldDir, err)
	}
	for _, e := range entries {
		if dirsOnly && !e.IsDir() {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		src := filepath.Join(oldDir, e.Name())
		dst := filepath.Join(newDir, e.Name())
		if err := movePath(src, dst, mode, logger); err != nil {
			return fmt.Errorf("relocate %s: %w", e.Name(), err)
		}
	}
	return nil
}

func movePath(src, dst, mode string, logger *log.Logger) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("create destination parent: %w", err)
	}
	if mode == "move" {
		if err := os.Rename(src, dst); err != nil {
			logger.Warn("", "os.Rename %s -> %s failed: %v (falling back to copy)", src, dst, err)
		} else {
			return nil
		}
	}
	if err := storage.CopyTree(src, dst); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("remove old path %s: %w", src, err)
	}
	return nil
}

func safetySnapshot(ctx context.Context, oldDB *sql.DB, cfg *config.Config, logger *log.Logger) {
	if cfg.Backup.Path == "" {
		logger.Warn("", "backup path not configured, skipping pre-migration safety snapshot")
		return
	}
	pruneSafetySnapshots(cfg.Backup.Path, logger)
	backupPath := filepath.Join(cfg.Backup.Path, fmt.Sprintf("pre-migration-%s.sql.gz", time.Now().UTC().Format("2006-01-02T15-04-05")))
	if err := database.SQLDumpToFile(ctx, oldDB, database.SchemaFS, backupPath); err != nil {
		logger.Error("", "pre-migration safety snapshot failed: %v (continuing)", err)
		return
	}
	logger.Info("", "pre-migration safety snapshot saved to %s", backupPath)
}

const maxSafetySnapshots = 5

func pruneSafetySnapshots(dir string, logger *log.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Warn("", "safety snapshot pruning skipped: read %s: %v", dir, err)
		return
	}
	var snaps []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "pre-migration-") && strings.HasSuffix(name, ".sql.gz") {
			snaps = append(snaps, filepath.Join(dir, name))
		}
	}
	if len(snaps) <= maxSafetySnapshots {
		return
	}
	slices.Sort(snaps)
	for _, p := range snaps[:len(snaps)-maxSafetySnapshots] {
		if err := os.Remove(p); err != nil {
			logger.Error("", "remove old safety snapshot %s: %v", p, err)
		}
	}
}

func restoreData(ctx context.Context, dbCfg config.DatabaseConfig, newDB *sql.DB, newDSN, dumpPath string, logger *log.Logger) error {
	if destinationHasData(ctx, newDB) {
		logger.Info("", "migrate-db: destination database already has data, skipping data migration")
		return nil
	}
	if err := database.ValidateMigrationDestination(ctx, newDB); err != nil {
		return err
	}
	return database.RestoreDumpViaPSQL(ctx, dbCfg.Runtime, dbCfg.Container, newDSN, dumpPath)
}

func destinationHasData(ctx context.Context, db *sql.DB) bool {
	var count int64
	err := db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM document) + (SELECT COUNT(*) FROM task)").Scan(&count)
	return err == nil && count > 0
}
