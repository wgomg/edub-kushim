package configtask

import (
	"context"
	"database/sql"
	"encoding/json"
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

const (
	TaskTypeConfig = "config"

	opTessdata  = "tessdata"
	opHugot     = "hugot"
	opMigrateDB = "migrate-db"

	DedupKeyMigrateDB = "config:migrate-db"
)

type MigrateDBPayload struct {
	Op                string `json:"op"`
	ConfigDir         string `json:"config_dir"`
	Host              string `json:"host"`
	Port              string `json:"port"`
	User              string `json:"user"`
	Password          string `json:"password"`
	Database          string `json:"database"`
	SSLMode           string `json:"sslmode"`
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

	default:
		return nil, fmt.Errorf("unsupported config task operation: %q", p.Op)
	}
}

func (h *ConfigTaskHandler) handleMigrateDB(ctx context.Context, t task.Task) (json.RawMessage, error) {
	var p MigrateDBPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal migrate-db payload: %w", err)
	}

	cfg, err := config.Load(p.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("load config from %s: %w", p.ConfigDir, err)
	}

	oldDB, err := database.NewPostgresDB(config.BuildPostgresDSN(cfg.Db))
	if err != nil {
		return nil, fmt.Errorf("connect to current database: %w", err)
	}
	defer oldDB.Close()

	oldClient := database.NewClient(oldDB)

	rows, err := oldClient.Queries.AcquireBackupLock(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire backup lock: %w", err)
	}
	if rows == 0 {
		return nil, fmt.Errorf("backup lock held by another process — migration cannot start")
	}
	defer func() {
		if _, relErr := oldClient.Queries.ReleaseBackupLock(context.Background()); relErr != nil {
			h.logger.Error(nil, "release backup lock after migration: %v", relErr)
		}
	}()

	if err := database.WaitForTaskDrain(ctx, oldClient.Queries, h.logger, "migrate-db"); err != nil {
		return nil, err
	}

	h.logger.Info(nil, "migrate-db: copying database to %s@%s:%s/%s", p.User, p.Host, p.Port, p.Database)

	h.safetySnapshot(ctx, oldDB, cfg)

	tmpDump, err := os.CreateTemp("", "edub-migrate-*.sql")
	if err != nil {
		return nil, fmt.Errorf("create temp dump file: %w", err)
	}
	tmpPath := tmpDump.Name()
	defer os.Remove(tmpPath)

	if err := database.DumpSchemaAndData(ctx, oldDB, database.SchemaFS, tmpDump); err != nil {
		tmpDump.Close()
		return nil, fmt.Errorf("dump current database: %w", err)
	}
	if err := tmpDump.Close(); err != nil {
		return nil, fmt.Errorf("close temp dump file: %w", err)
	}

	port, err := strconv.Atoi(p.Port)
	if err != nil {
		return nil, fmt.Errorf("invalid destination port %q: %w", p.Port, err)
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
		return nil, fmt.Errorf("connect to destination database: %w", err)
	}
	defer newDB.Close()

	if err := h.restoreData(ctx, newDB, tmpPath); err != nil {
		return nil, err
	}

	if p.OldStorageDir != "" && p.NewStorageDir != "" && filepath.Clean(p.OldStorageDir) != filepath.Clean(p.NewStorageDir) {
		if err := database.RewriteStoragePaths(ctx, newDB, p.OldStorageDir, p.NewStorageDir); err != nil {
			return nil, fmt.Errorf("rewrite storage paths: %w", err)
		}
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
	if p.NewStorageDir != "" {
		body["storage.storage_dir"] = p.NewStorageDir
	}
	if p.NewConsumptionDir != "" {
		body["storage.consumption_dir"] = p.NewConsumptionDir
	}
	if err := config.SaveMap(p.ConfigDir, body); err != nil {
		return nil, fmt.Errorf("persist new config: %w", err)
	}

	h.logger.Info(nil, "migrate-db: database %q is live and config.yaml updated", p.Database)
	return json.Marshal(map[string]string{"status": "migrated"})
}

func (h *ConfigTaskHandler) safetySnapshot(ctx context.Context, oldDB *sql.DB, cfg *config.Config) {
	if cfg.Backup.Path == "" {
		h.logger.Warn(nil, "backup path not configured, skipping pre-migration safety snapshot")
		return
	}
	h.pruneSafetySnapshots(cfg.Backup.Path)
	backupPath := filepath.Join(cfg.Backup.Path, fmt.Sprintf("pre-migration-%s.sql.gz", time.Now().UTC().Format("2006-01-02T15-04-05")))
	if err := database.SQLDumpToFile(ctx, oldDB, database.SchemaFS, backupPath); err != nil {
		h.logger.Error(nil, "pre-migration safety snapshot failed: %v (continuing)", err)
		return
	}
	h.logger.Info(nil, "pre-migration safety snapshot saved to %s", backupPath)
}

const maxSafetySnapshots = 5

func (h *ConfigTaskHandler) pruneSafetySnapshots(dir string) {
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
			h.logger.Error(nil, "remove old safety snapshot %s: %v", p, err)
		}
	}
}

func (h *ConfigTaskHandler) restoreData(ctx context.Context, newDB *sql.DB, dumpPath string) error {
	if h.destinationHasData(ctx, newDB) {
		h.logger.Info(nil, "migrate-db: destination database already has data, skipping data migration")
		return nil
	}
	if err := database.ValidateMigrationDestination(ctx, newDB); err != nil {
		return err
	}
	return database.ExecuteDumpFile(ctx, newDB, dumpPath)
}

func (h *ConfigTaskHandler) destinationHasData(ctx context.Context, db *sql.DB) bool {
	var count int64
	err := db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM document) + (SELECT COUNT(*) FROM task)").Scan(&count)
	return err == nil && count > 0
}
