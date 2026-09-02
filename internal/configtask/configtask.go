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
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

var ErrNoOp = errors.New("migration is a no-op")

func DirChanged(oldDir, newDir string) bool {
	if oldDir == "" || newDir == "" {
		return false
	}
	return filepath.Clean(oldDir) != filepath.Clean(newDir)
}

const (
	TaskTypeConfig = "config"

	opTessdata       = "tessdata"
	opHugot          = "hugot"
	opMigrateDB      = "migrate-db"
	opMigrateStorage = "migrate-storage"

	DedupKeyMigrateDB      = "config:migrate-db"
	DedupKeyMigrateStorage = "config:migrate-storage"
)

type MigrateDBPayload struct {
	Op        string `json:"op"`
	ConfigDir string `json:"config_dir"`
	Host      string `json:"host"`
	Port      string `json:"port"`
	User      string `json:"user"`
	Password  string `json:"password"`
	Database  string `json:"database"`
	SSLMode   string `json:"sslmode"`
}

type MigrateStoragePayload struct {
	Op                string `json:"op"`
	ConfigDir         string `json:"config_dir"`
	OldStorageDir     string `json:"old_storage_dir"`
	NewStorageDir     string `json:"new_storage_dir"`
	OldConsumptionDir string `json:"old_consumption_dir"`
	NewConsumptionDir string `json:"new_consumption_dir"`
}

type ConfigTaskHandler struct {
	logger *utils.Logger
}

func NewConfigTaskHandler(logger *utils.Logger) *ConfigTaskHandler {
	return &ConfigTaskHandler{logger: logger}
}

func (h *ConfigTaskHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		Op   string `json:"op"`
		Lang string `json:"lang"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	if p.Op == opTessdata {
		return "config:tessdata:" + p.Lang
	}
	if p.Op == opHugot {
		return "config:hugot"
	}
	if p.Op == opMigrateDB {
		return DedupKeyMigrateDB
	}
	if p.Op == opMigrateStorage {
		return DedupKeyMigrateStorage
	}
	return ""
}

func (h *ConfigTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	var p struct {
		ConfigDir string `json:"config_dir"`
		Op        string `json:"op"`
		Lang      string `json:"lang"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal config task payload: %w", err)
	}

	cfg, err := config.Load(p.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("load config from %s: %w", p.ConfigDir, err)
	}

	switch p.Op {
	case opTessdata:
		if err := config.DownloadTessdataLanguage(ctx, cfg, p.Lang); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"lang": p.Lang})

	case opHugot:
		if err := config.DownloadHugotModel(ctx, cfg, h.logger); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"model": "hugot"})

	case opMigrateDB:
		return h.handleMigrateDB(ctx, t)

	case opMigrateStorage:
		return h.handleMigrateStorage(ctx, t)

	default:
		return nil, fmt.Errorf("unsupported config task operation: %q", p.Op)
	}
}

func (h *ConfigTaskHandler) handleMigrateDB(ctx context.Context, t task.Task) (json.RawMessage, error) {
	var p MigrateDBPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal migrate-db payload: %w", err)
	}
	if err := MigrateDatabase(ctx, h.logger, p); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"status": "migrated"})
}

func MigrateDatabase(ctx context.Context, logger *utils.Logger, p MigrateDBPayload) error {
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

	oldClient := database.NewClient(oldDB)

	rows, err := oldClient.Queries.AcquireBackupLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire backup lock: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("backup lock held by another process — migration cannot start")
	}
	defer func() {
		if _, relErr := oldClient.Queries.ReleaseBackupLock(context.Background()); relErr != nil {
			logger.Error(nil, "release backup lock after migration: %v", relErr)
		}
	}()

	if err := database.WaitForTaskDrain(ctx, oldClient.Queries, logger, "migrate-db"); err != nil {
		return err
	}

	logger.Info(nil, "migrate-db: copying database to %s@%s:%s/%s", p.User, p.Host, p.Port, p.Database)

	safetySnapshot(ctx, oldDB, cfg, logger)

	tmpDump, err := os.CreateTemp("", "edub-migrate-*.sql")
	if err != nil {
		return fmt.Errorf("create temp dump file: %w", err)
	}
	tmpPath := tmpDump.Name()
	defer os.Remove(tmpPath)

	if err := database.DumpSchemaAndData(ctx, oldDB, database.SchemaFS, tmpDump); err != nil {
		tmpDump.Close()
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
		Type:     "postgres",
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

	logger.Info(nil, "migrate-db: database %q is live and config.yaml updated", p.Database)
	return nil
}

func (h *ConfigTaskHandler) handleMigrateStorage(ctx context.Context, t task.Task) (json.RawMessage, error) {
	var p MigrateStoragePayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal migrate-storage payload: %w", err)
	}
	err := MigrateStorage(ctx, h.logger, p)
	switch {
	case err == nil:
		return json.Marshal(map[string]string{"status": "migrated"})
	case errors.Is(err, ErrNoOp):
		return json.Marshal(map[string]string{"status": "no-op"})
	default:
		return nil, err
	}
}

func MigrateStorage(ctx context.Context, logger *utils.Logger, p MigrateStoragePayload) error {
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

	client := database.NewClient(db)

	rows, err := client.Queries.AcquireBackupLock(ctx)
	if err != nil {
		return fmt.Errorf("acquire backup lock: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("backup lock held by another process — migration cannot start")
	}
	defer func() {
		if _, relErr := client.Queries.ReleaseBackupLock(context.Background()); relErr != nil {
			logger.Error(nil, "release backup lock after storage migration: %v", relErr)
		}
	}()

	if err := database.WaitForTaskDrain(ctx, client.Queries, logger, "migrate-storage"); err != nil {
		return err
	}

	mode := strings.ToLower(cfg.Storage.MigrationMode)
	if mode != "move" {
		mode = "copy"
	}

	// Rewrite pending consume task payloads before touching the files so
	// queued work references the new inbox paths.
	if consumptionChanged {
		if err := database.RewriteTaskPayloadPaths(ctx, db, p.OldConsumptionDir, p.NewConsumptionDir); err != nil {
			return err
		}
	}

	if storageChanged {
		if err := moveDirEntries(ctx, p.OldStorageDir, p.NewStorageDir, mode, true, logger); err != nil {
			return err
		}
	}

	if consumptionChanged {
		if err := moveDirEntries(ctx, p.OldConsumptionDir, p.NewConsumptionDir, mode, false, logger); err != nil {
			return err
		}
	}

	if storageChanged {
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

	logger.Info(nil, "migrate-storage: files relocated and config.yaml updated")
	return nil
}

func moveDirEntries(ctx context.Context, oldDir, newDir, mode string, dirsOnly bool, logger *utils.Logger) error {
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

func movePath(src, dst, mode string, logger *utils.Logger) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("create destination parent: %w", err)
	}
	if mode == "move" {
		if err := os.Rename(src, dst); err != nil {
			logger.Warn(nil, "os.Rename %s -> %s failed: %v (falling back to copy)", src, dst, err)
		} else {
			return nil
		}
	}
	if err := utils.CopyTree(src, dst); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("remove old path %s: %w", src, err)
	}
	return nil
}

func safetySnapshot(ctx context.Context, oldDB *sql.DB, cfg *config.Config, logger *utils.Logger) {
	if cfg.Backup.Path == "" {
		logger.Warn(nil, "backup path not configured, skipping pre-migration safety snapshot")
		return
	}
	pruneSafetySnapshots(cfg.Backup.Path, logger)
	backupPath := filepath.Join(cfg.Backup.Path, fmt.Sprintf("pre-migration-%s.sql.gz", time.Now().UTC().Format("2006-01-02T15-04-05")))
	if err := database.SQLDumpToFile(ctx, oldDB, database.SchemaFS, backupPath); err != nil {
		logger.Error(nil, "pre-migration safety snapshot failed: %v (continuing)", err)
		return
	}
	logger.Info(nil, "pre-migration safety snapshot saved to %s", backupPath)
}

const maxSafetySnapshots = 5

func pruneSafetySnapshots(dir string, logger *utils.Logger) {
	entries, err := os.ReadDir(dir)
	if err != nil {
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
			logger.Error(nil, "remove old safety snapshot %s: %v", p, err)
		}
	}
}

func restoreData(ctx context.Context, dbCfg config.DatabaseConfig, newDB *sql.DB, newDSN, dumpPath string, logger *utils.Logger) error {
	if destinationHasData(ctx, newDB) {
		logger.Info(nil, "migrate-db: destination database already has data, skipping data migration")
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
