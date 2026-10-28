package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/mime"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/configtask"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	itypes "github.com/wgomg/edub-kushim/internal/types"
)

// ConfigHandler is the configuration API: bootstrap, read and update of the whole tree, the status of the
// environment migration and of the direct asset downloads, and the admin user creation of first setup. It
// is the public path of the setup wizard, then protected once authentication exists.
type ConfigHandler struct {
	getConfig   func() *config.Config
	onConfigSet func(*config.Config)
	client      *database.Client
	queries     *database.Queries
	logger      *log.Logger
	enqueuer    *task.Enqueuer
	services    *service.CrudServices
	// OnBootstrap prepares the configuration environment of the setup wizard without opening any database.
	// OnDatabaseInit opens the configured database, applies the schema and returns the services bound to it;
	// the setup wizard wires it, the API server leaves it nil because it always starts with services attached.
	// The context bounds the schema work, so the save can report its outcome before the server write timeout.
	OnBootstrap    func(configDir string) (*config.Config, error)
	OnDatabaseInit func(ctx context.Context) (*database.Client, *task.Enqueuer, error)

	downloadMu   sync.Mutex
	downloadErrs map[string]string
	downloads    map[string]struct{}

	// the direct-download helpers are dependencies so tests can keep a handler offline; production leaves
	// the real helpers wired below
	downloadTessdata func(ctx context.Context, cfg *config.Config, lang string) error
	downloadHugot    func(ctx context.Context, cfg *config.Config) error
}

// NewConfigHandler wires the config handler; the services attach after
// bootstrap via SetServices. A nil client leaves the queries unset, so
// the wizard can serve its pre-bootstrap routes.
func NewConfigHandler(
	getConfig func() *config.Config,
	onConfigSet func(*config.Config),
	client *database.Client,
	logger *log.Logger,
	enqueuer *task.Enqueuer,
	services *service.CrudServices,
) *ConfigHandler {
	h := &ConfigHandler{
		getConfig:   getConfig,
		onConfigSet: onConfigSet,
		client:      client,
		logger:      logger,
		enqueuer:    enqueuer,
		services:    services,
	}
	if client != nil {
		h.queries = client.Queries
	}
	h.downloadTessdata = config.DownloadTessdataLanguage
	h.downloadHugot = func(ctx context.Context, cfg *config.Config) error {
		return config.DownloadHugotModel(ctx, cfg, h.logger)
	}
	return h
}

// SetServices attaches the lazily constructed services after bootstrap;
// only set once.
func (h *ConfigHandler) SetServices(client *database.Client, enqueuer *task.Enqueuer) {
	h.enqueuer = enqueuer
	if client != nil {
		h.client = client
		h.queries = client.Queries
		h.services = &service.CrudServices{
			Batch: service.NewBatch(client, h.getConfig().Consumer.Reclaim.MaxRetries),
			User:  service.NewUser(client.Queries),
		}
	}
}

// Bootstrap is the only /wizard/* route left reachable without authentication.
// It exists because the SPA needs to know whether auth is enabled (and which
// tools are missing) before it can decide whether to show the login screen —
// so it must expose only non-sensitive fields. Anything else (LLM tokens, DB
// connection details, full status) belongs behind GetConfig/ConfigStatus.
// Bootstrap runs the environment bootstrap and switches the wizard into
// its post-bootstrap state.
func (h *ConfigHandler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	cfg := h.getConfig()
	if cfg == nil {
		cfg = config.DefaultConfig("")
	}

	resp := BootstrapResponse{
		AuthEnabled:  cfg.Srv.AuthEnabled,
		MissingTools: config.MissingExternalToolErrors(cfg),
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetConfig serves the sanitized configuration view.
func (h *ConfigHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.getConfig()
	if cfg == nil {
		cfg = config.DefaultConfig("")
	}

	resp := ConfigResponseFrom(cfg)

	writeJSON(w, http.StatusOK, resp)
}

var dbConnectionKeys = []string{
	"database.host", "database.port", "database.user", "database.password",
	"database.database", "database.sslmode", "database.dsn",
}

// PutConfig validates and saves the submitted configuration.
func (h *ConfigHandler) PutConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if configDir, ok := body["config_dir"].(string); ok && h.getConfig() == nil {
		var cfg *config.Config
		var err error

		if h.OnBootstrap != nil {
			cfg, err = h.OnBootstrap(configDir)
		} else {
			cfg, err = config.Bootstrap(configDir)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		h.onConfigSet(cfg)

		svcPath, svcErr := config.GenerateServiceFiles(configDir)
		if svcErr != nil {
			h.logger.Warn("", "failed to generate service files: %v", svcErr)
		}
		resp := map[string]any{
			"config_dir": configDir,
		}
		if svcPath != "" {
			resp["service_files_path"] = svcPath
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if h.getConfig() == nil {
		http.Error(w, "config not initialized — send config_dir first", http.StatusBadRequest)
		return
	}

	configDir := h.getConfig().App.ConfigDir
	sanitizeConfigStrings(body)
	if err := config.ValidatePollingWindows(body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := config.ValidateSave(configDir, body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	newDB, err := dbParamsFromBody(body, h.getConfig().Db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if h.client == nil {
		h.freshSetup(r.Context(), w, configDir, body, newDB)
		return
	}

	cfg := h.getConfig()
	newStorageDir := storageDirFromBody(body, "storage.storage_dir", cfg.Storage.StorageDir)
	newConsumptionDir := storageDirFromBody(body, "storage.consumption_dir", cfg.Storage.ConsumptionDir)
	dbChanged := config.DatabaseConnectionChanged(cfg.Db, newDB)
	storageChanged := dirChanged(newStorageDir, cfg.Storage.StorageDir) ||
		dirChanged(newConsumptionDir, cfg.Storage.ConsumptionDir)

	if !dbChanged && !storageChanged {
		if err := config.SaveMap(configDir, body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		cfg, err := config.Load(configDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.onConfigSet(cfg)
		svcPath, svcErr := config.GenerateServiceFiles(configDir)
		if svcErr != nil {
			h.logger.Warn("", "failed to generate service files: %v", svcErr)
		}
		missing := config.MissingExternalToolErrors(cfg)

		downloading := h.startAssetDownloads(cfg)

		writeConfigSaveResponse(w, downloading, missing, svcPath)
		return
	}

	if h.enqueuer == nil {
		http.Error(w, "database not ready — retry after setup completes", http.StatusServiceUnavailable)
		return
	}

	if dirChanged(newStorageDir, cfg.Storage.StorageDir) {
		if err := probeWritableDir(newStorageDir, "storage.storage_dir"); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if dirChanged(newConsumptionDir, cfg.Storage.ConsumptionDir) {
		if err := probeWritableDir(newConsumptionDir, "storage.consumption_dir"); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	immediate := make(map[string]any, len(body))
	for k, v := range body {
		if slices.Contains(dbConnectionKeys, k) {
			continue
		}
		if k == "storage.storage_dir" || k == "storage.consumption_dir" {
			continue
		}
		immediate[k] = v
	}
	if err := config.SaveMap(configDir, immediate); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pending := 0
	if dbChanged {
		if !h.enqueueDBMigration(ctx, w, newDB) {
			return
		}
		pending++
	}
	if storageChanged {
		if !h.enqueueStorageMigration(ctx, w, body) {
			return
		}
		pending++
	}

	// the immediate keys are already saved; the downloads depend only on those, so read the new set back
	// from disk instead of waiting for the migration to apply the deferred keys
	if immediateCfg, loadErr := config.Load(configDir); loadErr != nil {
		h.logger.Error("", "reload config for asset downloads: %v", loadErr)
	} else {
		pending += h.startAssetDownloads(immediateCfg)
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"pending_tasks": pending,
		"message":       "migration(s) queued — settings apply once the migration completes",
	})
}

// writeConfigSaveResponse is the settings-save answer both the immediate-save branch and the fresh-setup
// branch produce: a download count keeps the wizard's step gate polling, an empty count advances it. The
// service-files path rides along when generation succeeded.
func writeConfigSaveResponse(w http.ResponseWriter, downloading int, missing []config.ExternalTool, svcPath string) {
	if downloading > 0 {
		resp := map[string]any{
			"pending_tasks": downloading,
			"missing_tools": missing,
		}
		if svcPath != "" {
			resp["service_files_path"] = svcPath
		}
		writeJSON(w, http.StatusCreated, resp)
		return
	}

	resp := map[string]any{
		"configured":    true,
		"missing_tools": missing,
	}
	if svcPath != "" {
		resp["service_files_path"] = svcPath
	}
	writeJSON(w, http.StatusOK, resp)
}

// schemaInitTimeout bounds the fresh-setup schema initialization: it must finish inside the wizard server's
// 60s write deadline, so the save reports success or failure instead of having its response cut. A slow
// database aborts here, and the next attempt resumes the idempotent migrations where they stopped.
const schemaInitTimeout = 55 * time.Second

// freshSetup applies the first settings submission of a setup wizard, before any service is bound: it claims
// the target database under the empty-only policy, persists the whole configuration including the connection
// fields, then has the wizard open that database, create the schema and bind its services to it. Nothing is
// persisted when the database cannot be claimed, so a rejected target leaves the wizard on the settings step.
// It writes the response itself.
func (h *ConfigHandler) freshSetup(ctx context.Context, w http.ResponseWriter, configDir string, body map[string]any, newDB config.DatabaseConfig) {
	cfg := h.getConfig()

	newStorageDir := storageDirFromBody(body, "storage.storage_dir", cfg.Storage.StorageDir)
	newConsumptionDir := storageDirFromBody(body, "storage.consumption_dir", cfg.Storage.ConsumptionDir)
	for _, dir := range []string{newStorageDir, newConsumptionDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			http.Error(w, fmt.Sprintf("create directory %s: %v", dir, err), http.StatusInternalServerError)
			return
		}
	}
	if err := probeWritableDir(newStorageDir, "storage.storage_dir"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := probeWritableDir(newConsumptionDir, "storage.consumption_dir"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// connection keys are persisted only once the claim passes, so a later adopt of this target can only be
	// the setup's own database; a retry of an unchanged persisted target already passed the claim
	if !config.DatabaseSettingsPresent(configDir) || config.DatabaseConnectionChanged(cfg.Db, newDB) {
		if err := database.PrepareFreshDatabase(config.BuildPostgresDSN(newDB)); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	saved := make(map[string]any, len(body)+7)
	maps.Copy(saved, body)
	saved["database.host"] = newDB.Host
	saved["database.port"] = newDB.Port
	saved["database.user"] = newDB.User
	saved["database.password"] = newDB.Password
	saved["database.database"] = newDB.Database
	saved["database.sslmode"] = newDB.SSLMode
	saved["database.dsn"] = ""

	if err := config.SaveMap(configDir, saved); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	reloaded, err := config.Load(configDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.onConfigSet(reloaded)

	if h.OnDatabaseInit == nil {
		http.Error(w, "database not ready — retry after setup completes", http.StatusServiceUnavailable)
		return
	}
	initCtx, cancel := context.WithTimeout(ctx, schemaInitTimeout)
	defer cancel()
	client, enqueuer, initErr := h.OnDatabaseInit(initCtx)
	if initErr != nil {
		http.Error(w, initErr.Error(), http.StatusInternalServerError)
		return
	}
	h.SetServices(client, enqueuer)

	svcPath, svcErr := config.GenerateServiceFiles(configDir)
	if svcErr != nil {
		h.logger.Warn("", "failed to generate service files: %v", svcErr)
	}

	missing := config.MissingExternalToolErrors(reloaded)

	downloading := h.startAssetDownloads(reloaded)
	writeConfigSaveResponse(w, downloading, missing, svcPath)
}

// hugotAssetKey names the hugot model in the direct-download bookkeeping; tessdata entries use the language
// code as their key.
const hugotAssetKey = "hugot"

// missingAssets lists the required assets absent from disk: one tessdata key per configured language plus
// the hugot model. It is the single source of the required-asset set for the download trigger, the status
// pending count and the failure listing.
func missingAssets(cfg *config.Config) []string {
	missing := config.MissingTessdataLanguages(cfg)
	if config.MissingHugotModel(cfg) {
		missing = append(missing, hugotAssetKey)
	}
	return missing
}

// startAssetDownloads launches one goroutine per missing asset and returns how many downloads it started.
// An asset already downloading is skipped, so overlapping saves and retries cannot write the same file
// concurrently. Downloads are config updates, not tasks: they create no batch, task row, dedup key, lease
// or queue progress. The request context is detached because a model download outlives the HTTP request; a
// process exit aborts one, and the live missing-asset check keeps the status honest afterwards. Both
// helpers skip when the asset is present, so re-triggering is idempotent.
func (h *ConfigHandler) startAssetDownloads(cfg *config.Config) int {
	count := 0
	for _, key := range missingAssets(cfg) {
		if !h.beginDownload(key) {
			continue
		}
		h.clearAssetFailure(key)
		assetKey := key
		go h.downloadAsset(assetKey, func() error {
			if assetKey == hugotAssetKey {
				return h.downloadHugot(context.Background(), cfg)
			}
			return h.downloadTessdata(context.Background(), cfg, assetKey)
		})
		count++
	}
	return count
}

// beginDownload marks an asset as downloading; it reports false when a download for that asset is already
// in flight.
func (h *ConfigHandler) beginDownload(key string) bool {
	h.downloadMu.Lock()
	defer h.downloadMu.Unlock()
	if h.downloads == nil {
		h.downloads = make(map[string]struct{})
	}
	if _, running := h.downloads[key]; running {
		return false
	}
	h.downloads[key] = struct{}{}
	return true
}

// downloadAsset runs one asset download and records or clears its failure entry, releasing its in-flight
// mark first so the entry is never left set.
func (h *ConfigHandler) downloadAsset(key string, run func() error) {
	defer h.endDownload(key)
	if err := run(); err != nil {
		h.logger.Error("", "config asset download %s failed: %v", key, err)
		h.downloadMu.Lock()
		if h.downloadErrs == nil {
			h.downloadErrs = make(map[string]string)
		}
		h.downloadErrs[key] = err.Error()
		h.downloadMu.Unlock()
		return
	}
	h.clearAssetFailure(key)
}

func (h *ConfigHandler) endDownload(key string) {
	h.downloadMu.Lock()
	delete(h.downloads, key)
	h.downloadMu.Unlock()
}

// clearAssetFailure drops an asset's recorded failure, so a retry in flight is not reported as failed.
func (h *ConfigHandler) clearAssetFailure(key string) {
	h.downloadMu.Lock()
	delete(h.downloadErrs, key)
	h.downloadMu.Unlock()
}

// failedAssetTasks lists the recorded download failures for assets still missing, in the same summary shape
// failed migration tasks use so the wizard renders both without a special case.
func (h *ConfigHandler) failedAssetTasks(missingKeys []string) []FailedTaskSummary {
	missing := make(map[string]bool, len(missingKeys))
	for _, key := range missingKeys {
		missing[key] = true
	}

	h.downloadMu.Lock()
	failures := make(map[string]string, len(h.downloadErrs))
	maps.Copy(failures, h.downloadErrs)
	h.downloadMu.Unlock()

	failed := make([]FailedTaskSummary, 0, len(failures))
	for key, message := range failures {
		if !missing[key] {
			continue
		}
		summary := FailedTaskSummary{Op: "tessdata", Error: message}
		if key == hugotAssetKey {
			summary.Op = hugotAssetKey
		} else {
			summary.Lang = key
		}
		failed = append(failed, summary)
	}
	sort.Slice(failed, func(i, j int) bool {
		if failed[i].Op != failed[j].Op {
			return failed[i].Op < failed[j].Op
		}
		return failed[i].Lang < failed[j].Lang
	})
	return failed
}

func dbParamsFromBody(body map[string]any, current config.DatabaseConfig) (config.DatabaseConfig, error) {
	db := config.DatabaseConfig{
		Host:      asString(body["database.host"]),
		User:      asString(body["database.user"]),
		Password:  asString(body["database.password"]),
		Database:  asString(body["database.database"]),
		SSLMode:   asString(body["database.sslmode"]),
		Runtime:   asString(body["database.runtime"]),
		Container: asString(body["database.container"]),
	}
	if db.Host == "" {
		db.Host = current.Host
	}
	if db.User == "" {
		db.User = current.User
	}
	if db.Password == "" {
		db.Password = current.Password
	}
	if db.Database == "" {
		db.Database = current.Database
	}
	if db.SSLMode == "" {
		db.SSLMode = current.SSLMode
	}
	if db.Runtime == "" {
		db.Runtime = current.Runtime
	}
	if db.Container == "" {
		db.Container = current.Container
	}

	switch v := body["database.port"].(type) {
	case nil:
		db.Port = current.Port
	case string:
		port, err := strconv.Atoi(v)
		if err != nil {
			return db, fmt.Errorf("database.port must be an integer, got %q", v)
		}
		db.Port = port
	case float64:
		db.Port = int(v)
	default:
		return db, fmt.Errorf("database.port must be an integer")
	}
	if db.Port == 0 {
		db.Port = current.Port
	}

	if current.DSN != "" &&
		db.Host == current.Host && db.Port == current.Port && db.User == current.User &&
		db.Password == current.Password && db.Database == current.Database && db.SSLMode == current.SSLMode {
		db.DSN = current.DSN
	}

	if db.Host == "" {
		return db, fmt.Errorf("database.host is required")
	}
	if db.Database == "" {
		return db, fmt.Errorf("database.database is required")
	}
	if db.Port < 1 || db.Port > 65535 {
		return db, fmt.Errorf("database.port must be between 1 and 65535")
	}
	switch db.SSLMode {
	case "", "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return db, fmt.Errorf("database.sslmode must be one of: disable, allow, prefer, require, verify-ca, verify-full")
	}
	if err := config.ValidateDatabaseRuntime(db.Runtime, db.Container); err != nil {
		return db, err
	}
	return db, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// storageDirFromBody returns the body value for a storage dir key, expanded
// like config.Load would, falling back to the current value when absent.
func storageDirFromBody(body map[string]any, key, current string) string {
	v, ok := body[key].(string)
	if !ok || v == "" {
		return current
	}
	if len(v) > 0 && v[0] == '~' {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, v[1:])
		}
	}
	return v
}

func dirChanged(newDir, oldDir string) bool {
	return filepath.Clean(newDir) != filepath.Clean(oldDir)
}

func probeWritableDir(dir, key string) error {
	probe, err := os.CreateTemp(dir, ".edub-write-test-*")
	if err != nil {
		return fmt.Errorf("%s (%s) is not writable: %w", key, dir, err)
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return nil
}

func (h *ConfigHandler) tryAcquireConfigTaskSlot(ctx context.Context, w http.ResponseWriter, taskType itypes.TaskType, dedupKey, label string) bool {
	existing, err := h.queries.GetConfigTaskByDedupKey(ctx, database.GetConfigTaskByDedupKeyParams{
		TaskType: taskType,
		DedupKey: sql.NullString{String: dedupKey, Valid: true},
	})
	switch {
	case err == nil && existing.Status == itypes.Task.Status.Processing:
		writeJSON(w, http.StatusConflict, map[string]any{"error": label + " already in progress"})
		return false
	case err == nil:
		deleted, delErr := h.queries.DeleteConfigTaskByDedupKey(ctx, database.DeleteConfigTaskByDedupKeyParams{
			TaskType: taskType,
			DedupKey: sql.NullString{String: dedupKey, Valid: true},
		})
		if delErr != nil {
			h.logger.Error("", "delete stale %s task: %v", label, delErr)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to queue " + label})
			return false
		}
		if deleted == 0 && existing.Status == itypes.Task.Status.Pending {
			writeJSON(w, http.StatusConflict, map[string]any{"error": label + " already in progress"})
			return false
		}
	case !errors.Is(err, sql.ErrNoRows):
		h.logger.Error("", "lookup %s task: %v", label, err)
	}
	return true
}

func (h *ConfigHandler) enqueueDBMigration(ctx context.Context, w http.ResponseWriter, newDB config.DatabaseConfig) bool {
	cfg := h.getConfig()
	taskSpec, err := configtask.MigrateDBSpec(configtask.MigrateDBPayload{
		ConfigDir: cfg.App.ConfigDir,
		Host:      newDB.Host,
		Port:      strconv.Itoa(newDB.Port),
		User:      newDB.User,
		Password:  newDB.Password,
		Database:  newDB.Database,
		SSLMode:   newDB.SSLMode,
	})
	if err != nil {
		h.logger.Error("", "build database migration task: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to enqueue database migration"})
		return false
	}

	dedupKey, err := h.enqueuer.DedupKey(taskSpec.Type, taskSpec.Payload)
	if err != nil {
		h.logger.Error("", "derive database migration dedup key: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to enqueue database migration"})
		return false
	}
	if !h.tryAcquireConfigTaskSlot(ctx, w, taskSpec.Type, dedupKey, "database migration") {
		return false
	}

	if !h.enqueueMigration(ctx, w, taskSpec, "failed to enqueue database migration") {
		return false
	}

	h.logger.Info("", "database migration queued: %s -> %s@%s:%d/%s", cfg.Db.Database, newDB.User, newDB.Host, newDB.Port, newDB.Database)
	return true
}

func (h *ConfigHandler) enqueueStorageMigration(ctx context.Context, w http.ResponseWriter, body map[string]any) bool {
	cfg := h.getConfig()
	newStorageDir := storageDirFromBody(body, "storage.storage_dir", cfg.Storage.StorageDir)
	newConsumptionDir := storageDirFromBody(body, "storage.consumption_dir", cfg.Storage.ConsumptionDir)
	payload, changed := configtask.StorageMigrationPayload(cfg, newStorageDir, newConsumptionDir)
	if !changed {
		return true
	}
	taskSpec, err := configtask.MigrateStorageSpec(payload)
	if err != nil {
		h.logger.Error("", "build storage migration task: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to enqueue storage migration"})
		return false
	}

	dedupKey, err := h.enqueuer.DedupKey(taskSpec.Type, taskSpec.Payload)
	if err != nil {
		h.logger.Error("", "derive storage migration dedup key: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to enqueue storage migration"})
		return false
	}
	if !h.tryAcquireConfigTaskSlot(ctx, w, taskSpec.Type, dedupKey, "storage migration") {
		return false
	}

	if !h.enqueueMigration(ctx, w, taskSpec, "failed to enqueue storage migration") {
		return false
	}

	h.logger.Info("", "storage migration queued: %s -> %s", cfg.Storage.StorageDir, newStorageDir)
	return true
}

// enqueueMigration queues one migration task through the transactional enqueue; a failure is reported
// through the response writer and leaves no batch behind. The caller has already claimed the task's
// dedup slot.
func (h *ConfigHandler) enqueueMigration(ctx context.Context, w http.ResponseWriter, taskSpec task.TaskSpec, failure string) bool {
	spec, err := task.NewBatchSpec(itypes.Batch.Origin.Wizard, "")
	if err != nil {
		h.logger.Error("", "build migration batch spec: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": failure})
		return false
	}

	if _, err := h.enqueuer.EnqueueBatch(ctx, spec, []task.TaskSpec{taskSpec}); err != nil {
		h.logger.Error("", "enqueue %s task: %v", taskSpec.Type, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": failure})
		return false
	}

	return true
}

// configHostedTaskTypes are the batch task types a configuration change creates: the two migration types.
// Asset downloads are direct config updates with no task row, so status and retry cover them separately.
var configHostedTaskTypes = []itypes.TaskType{
	itypes.Task.Type.MigrateDB,
	itypes.Task.Type.MigrateStorage,
}

// ConfigStatus serves the environment status the setup wizard polls: readiness, tool failures, pending or
// failed migration tasks and the direct asset downloads. Until the database is bound—setup has not reached
// the schema step—nothing counts as configured or pending: asset downloads only start with the settings
// save, so the wizard keeps the operator on the settings step instead of polling work that was never
// started.
func (h *ConfigHandler) ConfigStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cfg := h.getConfig()
	ready := cfg != nil && h.client != nil
	configured := ready && len(cfg.Consumer.OCR.Languages) > 0

	pendingTasks := 0
	var errors []string

	resp := ConfigStatusResponse{
		Configured:   configured,
		PendingTasks: pendingTasks,
		Errors:       errors,
	}

	if h.queries != nil {
		// the setup wizard's completion gate polls this count, so scope it to the migration task types
		for _, taskType := range configHostedTaskTypes {
			for _, status := range []itypes.TaskStatus{itypes.Task.Status.Pending, itypes.Task.Status.Processing} {
				count, err := h.queries.CountTasksByStatusAndType(ctx, database.CountTasksByStatusAndTypeParams{
					Status:   status,
					TaskType: taskType,
				})
				if err != nil {
					resp.Errors = append(resp.Errors, err.Error())
					continue
				}
				pendingTasks += int(count)
			}
		}
		resp.PendingTasks = pendingTasks

		respFailed := make([]FailedTaskSummary, 0)
		for _, taskType := range configHostedTaskTypes {
			failedTasks, err := h.queries.ListAllTasksByStatusAndType(ctx, database.ListAllTasksByStatusAndTypeParams{
				Status:   itypes.Task.Status.Failed,
				TaskType: taskType,
			})
			if err != nil {
				h.logger.Error("", "list failed migration tasks: %v", err)
				continue
			}
			for _, t := range failedTasks {
				summary := FailedTaskSummary{TaskID: t.TaskID, Op: string(t.TaskType)}
				if t.Error.Valid {
					summary.Error = t.Error.String
				}
				respFailed = append(respFailed, summary)
			}
		}
		resp.FailedTasks = respFailed
	}

	if cfg != nil {
		// asset downloads have no task rows: pending counts the live missing assets, and the failures come from
		// the update path's in-memory record
		if ready {
			missing := missingAssets(cfg)
			resp.PendingTasks += len(missing)
			resp.FailedTasks = append(resp.FailedTasks, h.failedAssetTasks(missing)...)
		}

		allTools := config.MissingExternalTools(cfg)
		resp.Tools = allTools
		resp.MissingTools = config.FilterToolErrors(allTools)
	}

	writeJSON(w, http.StatusOK, resp)
}

// RetryFailedConfig retries the environment's failed work: it resumes the batches of failed migration tasks
// and re-triggers asset downloads for missing or failed assets.
func (h *ConfigHandler) RetryFailedConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.queries == nil {
		http.Error(w, "not initialized", http.StatusBadRequest)
		return
	}
	cfg := h.getConfig()
	if cfg == nil {
		http.Error(w, "not initialized", http.StatusServiceUnavailable)
		return
	}

	batchIDs := make(map[string]struct{})
	for _, taskType := range configHostedTaskTypes {
		failedTasks, err := h.queries.ListAllTasksByStatusAndType(ctx, database.ListAllTasksByStatusAndTypeParams{
			Status:   itypes.Task.Status.Failed,
			TaskType: taskType,
		})
		if err != nil {
			h.logger.Error("", "list failed migration tasks: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		for _, t := range failedTasks {
			if t.BatchID.Valid {
				batchIDs[t.BatchID.String] = struct{}{}
			}
		}
	}

	retried := int64(0)
	for batchID := range batchIDs {
		result, err := task.ResumeBatch(ctx, h.client, h.logger, batchID, int32(cfg.Consumer.Reclaim.MaxRetries), true)
		if err != nil {
			h.logger.Error("", "retry config batch %s: %v", batchID, err)
			continue
		}
		retried += result.FailedRetried
	}

	downloads := h.startAssetDownloads(cfg)

	writeJSON(w, http.StatusOK, map[string]int64{"retried": retried, "downloads_started": int64(downloads)})
}

// CreateAdminUser creates the first admin account during setup.
func (h *ConfigHandler) CreateAdminUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.services == nil || h.services.User == nil {
		http.Error(w, "service not initialized", http.StatusBadRequest)
		return
	}

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Username = stripTags(strings.TrimSpace(req.Username))
	if req.Username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	user, err := h.services.User.Create(ctx, req.Username, req.Password, "admin")
	if err != nil {
		if errs.KindOf(err) == errs.KindConflict {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists"})
			return
		}
		writeServiceError(w, h.logger, "", "create admin user", err)
		return
	}

	created := ""
	if user.CreatedAt.Valid {
		created = user.CreatedAt.Time.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusCreated, UserResponse{
		ID:        user.ID,
		Username:  user.Username,
		Role:      "admin",
		CreatedAt: created,
	})
}

func sanitizeConfigStrings(v any) any {
	switch m := v.(type) {
	case string:
		return stripTags(m)
	case map[string]any:
		for k, val := range m {
			if k == "prompt_template" {
				continue
			}
			m[k] = sanitizeConfigStrings(val)
		}
	case []any:
		for i, val := range m {
			m[i] = sanitizeConfigStrings(val)
		}
	}
	return v
}

// FailedTaskSummary is one failed environment unit: a migration task, or a direct asset download that
// carries no task id.
type FailedTaskSummary struct {
	TaskID string `json:"task_id"`
	Op     string `json:"op"`
	Lang   string `json:"lang,omitempty"`
	Error  string `json:"error"`
}

// BootstrapResponse reports the bootstrap outcome: whether the server
// now authenticates and which tools are still missing.
type BootstrapResponse struct {
	AuthEnabled  bool                  `json:"auth_enabled"`
	MissingTools []config.ExternalTool `json:"missing_tools"`
}

// ConfigStatusResponse is the environment status the wizard polls.
type ConfigStatusResponse struct {
	Configured   bool                  `json:"configured"`
	PendingTasks int                   `json:"pending_tasks"`
	FailedTasks  []FailedTaskSummary   `json:"failed_tasks,omitempty"`
	Errors       []string              `json:"errors"`
	Tools        []config.ExternalTool `json:"tools"`
	MissingTools []config.ExternalTool `json:"missing_tools"`
}

// LoggingConfigResponse is the API view of the log rotation.
type LoggingConfigResponse struct {
	MaxSize    int  `json:"max_size"`
	MaxBackups int  `json:"max_backups"`
	MaxAge     int  `json:"max_age"`
	Compress   bool `json:"compress"`
}

// AppConfigResponse is the app section's API view.
type AppConfigResponse struct {
	Initialized bool                  `json:"initialized"`
	LogLevel    string                `json:"log_level"`
	Logging     LoggingConfigResponse `json:"logging"`
}

// StorageConfigResponse is the storage section's API view.
type StorageConfigResponse struct {
	ConsumptionDir string              `json:"consumption_dir"`
	StorageDir     string              `json:"storage_dir"`
	MigrationMode  string              `json:"migration_mode"`
	Trash          TrashConfigResponse `json:"trash"`
}

// TrashConfigResponse is the trash section's API view.
type TrashConfigResponse struct {
	RetentionDays int `json:"retention_days"`
}

// DatabaseConfigResponse is the database section's API view, without the
// password.
type DatabaseConfigResponse struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Database  string `json:"database"`
	SSLMode   string `json:"sslmode"`
	Runtime   string `json:"runtime"`
	Container string `json:"container"`
}

// ConfigResponse is the full configuration as the API shows it: every
// section view plus the engines and file types the UI offers.
type ConfigResponse struct {
	App                AppConfigResponse               `json:"app"`
	Server             ServerConfigResponse            `json:"server"`
	Storage            StorageConfigResponse           `json:"storage"`
	Database           DatabaseConfigResponse          `json:"database"`
	Consumer           ConsumerConfigResponse          `json:"consumer"`
	Enricher           EnricherConfigResponse          `json:"enricher"`
	Backup             BackupConfigResponse            `json:"backup"`
	Mirror             MirrorConfigResponse            `json:"mirror"`
	AvailableEngines   map[string][]config.EngineEntry `json:"available_engines"`
	AvailableFileTypes []AvailableFileType             `json:"available_file_types"`
}

// AvailableFileType is one ingest format the UI can offer.
type AvailableFileType struct {
	MimeType   string   `json:"mime_type"`
	Label      string   `json:"label"`
	Extensions []string `json:"extensions"`
	Required   bool     `json:"required"`
}

// ServerConfigResponse is the server section's API view.
type ServerConfigResponse struct {
	Host                 string `json:"host"`
	Port                 int    `json:"port"`
	MaxUploadSize        int64  `json:"max_upload_size"`
	MaxDownloadFiles     int    `json:"max_download_files"`
	MaxDownloadSizeMB    int64  `json:"max_download_size_mb"`
	MaxConcurrentBatches int    `json:"max_concurrent_batches"`
	MaxBatchDelete       int    `json:"max_batch_delete"`
	AuthEnabled          bool   `json:"auth_enabled"`
}

// PollingWindowResponse is one polling window.
type PollingWindowResponse struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// PollingConfigResponse is the polling section's API view.
type PollingConfigResponse struct {
	Enabled  bool                    `json:"enabled"`
	Interval int                     `json:"interval"`
	Windows  []PollingWindowResponse `json:"windows"`
}

// ReclaimConfigResponse is the reclaim section's API view.
type ReclaimConfigResponse struct {
	Enabled        bool  `json:"enabled"`
	MaxRetries     int   `json:"max_retries"`
	StaleTaskAfter int64 `json:"stale_task_after"`
}

// ConsumerConfigResponse is the consumer section's API view.
type ConsumerConfigResponse struct {
	SupportedFiles   []string                 `json:"supported_files"`
	Workers          int                      `json:"workers"`
	MaxFilesPerBatch int                      `json:"max_files_per_batch"`
	Converter        DocxOdtConverterResponse `json:"converter"`
	TextExtractor    TextExtractorResponse    `json:"textextractor"`
	PdfOptimizer     PdfOptimizerResponse     `json:"pdfoptimizer"`
	OCR              OCRResponse              `json:"ocr"`
	Thumbnail        ThumbnailConfigResponse  `json:"thumbnail"`
	Polling          PollingConfigResponse    `json:"polling"`
	Reclaim          ReclaimConfigResponse    `json:"reclaim"`
}

// DocxOdtConverterResponse is the converter section's API view.
type DocxOdtConverterResponse struct {
	Enabled bool   `json:"enabled"`
	Binary  string `json:"binary"`
	Timeout int    `json:"timeout"`
}

// TextExtractorResponse is the extractor section's API view.
type TextExtractorResponse struct {
	Engine  string `json:"engine"`
	Timeout int    `json:"timeout"`
}

// PdfOptimizerResponse is the optimizer section's API view.
type PdfOptimizerResponse struct {
	Engine   string `json:"engine"`
	Fallback string `json:"fallback"`
	Timeout  int    `json:"timeout"`
}

// OCRResponse is the OCR section's API view.
type OCRResponse struct {
	Engine     string   `json:"engine"`
	Languages  []string `json:"languages"`
	DataDir    string   `json:"data_dir"`
	Timeout    int      `json:"timeout"`
	OcrWorkers int      `json:"ocr_workers"`
}

// ThumbnailConfigResponse is the thumbnail section's API view.
type ThumbnailConfigResponse struct {
	Enabled          bool    `json:"enabled"`
	Engine           string  `json:"engine"`
	DPI              int     `json:"dpi"`
	MaxWidth         int     `json:"max_width"`
	Quality          int     `json:"quality"`
	Timeout          int     `json:"timeout"`
	Workers          int     `json:"workers"`
	BackfillInterval float64 `json:"backfill_interval"`
	BackfillTime     string  `json:"backfill_time"`
}

// EnricherConfigResponse is the enricher section's API view.
type EnricherConfigResponse struct {
	Workers         int                          `json:"workers"`
	TextReducer     TextReducerResponse          `json:"textreducer"`
	ContentAnalyzer ContentAnalyzerResponse      `json:"contentanalyzer"`
	TagMatcher      TagMatcherResponse           `json:"tagmatcher"`
	TagAdjudicator  TagAdjudicatorConfigResponse `json:"tag_adjudicator"`
	TagQuality      TagQualityConfigResponse     `json:"tag_quality"`
	TagReconcile    TagReconcileConfigResponse   `json:"tag_reconcile"`
	TagMeasure      TagMeasureConfigResponse     `json:"tag_measure"`
	TagAudit        TagAuditConfigResponse       `json:"tag_audit"`
	TagMaintenance  TagMaintenanceConfigResponse `json:"tag_maintenance"`
}

// TagQualityConfigResponse is the quality judge's API view.
type TagQualityConfigResponse struct {
	Enabled bool                      `json:"enabled"`
	Timeout int                       `json:"timeout"`
	Llm     TagAdjudicatorLlmResponse `json:"llm"`
}

// TagAdjudicatorConfigResponse is the adjudicator section's API view.
type TagAdjudicatorConfigResponse struct {
	Enabled bool                      `json:"enabled"`
	Timeout int                       `json:"timeout"`
	Llm     TagAdjudicatorLlmResponse `json:"llm"`
}

// TagAdjudicatorLlmResponse is one LLM endpoint of the judged steps.
type TagAdjudicatorLlmResponse struct {
	Adapter      string  `json:"adapter"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	Token        string  `json:"token,omitempty"`
	RequestDelay float64 `json:"request_delay"`
}

// TagReconcileConfigResponse is the reconcile section's API view.
type TagReconcileConfigResponse struct {
	Enabled              bool    `json:"enabled"`
	ReportOnly           bool    `json:"report_only"`
	Interval             float64 `json:"interval"`
	Time                 string  `json:"time"`
	MaxRejudge           int     `json:"max_rejudge"`
	MaxRetag             int     `json:"max_retag"`
	DeadTagGraceDays     int     `json:"dead_tag_grace_days"`
	ProvisionalGraceDays int     `json:"provisional_grace_days"`
}

// TagMeasureConfigResponse is the measure section's API view.
type TagMeasureConfigResponse struct {
	Enabled              bool    `json:"enabled"`
	Interval             float64 `json:"interval"`
	Time                 string  `json:"time"`
	AfterEnrichmentBatch bool    `json:"after_enrichment_batch"`
}

// TagAuditConfigResponse is the coverage audit section's API view.
type TagAuditConfigResponse struct {
	Enabled    bool    `json:"enabled"`
	Interval   float64 `json:"interval"`
	Time       string  `json:"time"`
	SampleSize int     `json:"sample_size"`
}

// TagMaintenanceConfigResponse is the maintenance routine section's API view.
type TagMaintenanceConfigResponse struct {
	Enabled  bool    `json:"enabled"`
	Interval float64 `json:"interval"`
	Time     string  `json:"time"`
}

// TextReducerResponse is the reducer section's API view.
type TextReducerResponse struct {
	Engine      string `json:"engine"`
	Timeout     int    `json:"timeout"`
	TargetWords int    `json:"target_words"`
}

// DocTypeRefinementResponse is the type-refinement section's API view.
type DocTypeRefinementResponse struct {
	Enabled   bool `json:"enabled"`
	HeadWords int  `json:"head_words"`
	TailWords int  `json:"tail_words"`
}

// ContentAnalyzerResponse is the analyzer section's API view.
type ContentAnalyzerResponse struct {
	Enabled            bool                      `json:"enabled"`
	Timeout            int                       `json:"timeout"`
	MinTags            int                       `json:"min_tags"`
	Llm                LlmConfigResponse         `json:"llm"`
	Fallbacks          []FallbackConfigResponse  `json:"fallbacks,omitempty"`
	PromptTemplate     string                    `json:"prompt_template,omitempty"`
	DocTypeRefinement  DocTypeRefinementResponse `json:"doc_type_refinement"`
	PauseOnCreditError bool                      `json:"pause_on_credit_error"`
}

// FallbackConfigResponse is one fallback's API view.
type FallbackConfigResponse struct {
	Enabled bool              `json:"enabled"`
	Llm     LlmConfigResponse `json:"llm"`
}

// LlmConfigResponse is one LLM endpoint's API view, without the token.
type LlmConfigResponse struct {
	Adapter      string  `json:"adapter"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	Token        string  `json:"token,omitempty"`
	Reasoning    bool    `json:"reasoning"`
	Temperature  float64 `json:"temperature"`
	RequestDelay float64 `json:"request_delay"`
}

// TagMatcherResponse is the matcher section's API view.
type TagMatcherResponse struct {
	Timeout           int           `json:"timeout"`
	ReduceTargetWords int           `json:"reduce_target_words"`
	ChunkSize         int           `json:"chunk_size"`
	TopN              int           `json:"top_n"`
	Hugot             HugotResponse `json:"hugot"`
}

// HugotResponse is the embedding-model section's API view.
type HugotResponse struct {
	Model   string `json:"model"`
	Backend string `json:"backend"`
}

// BackupScheduleResponse is one backup schedule's API view.
type BackupScheduleResponse struct {
	Mode     string  `json:"mode"`
	Interval float64 `json:"interval"`
	Time     string  `json:"time"`
	Keep     int     `json:"keep"`
	Path     string  `json:"path"`
}

// BackupConfigResponse is the backup section's API view.
type BackupConfigResponse struct {
	Enabled   bool                     `json:"enabled"`
	Path      string                   `json:"path"`
	Schedules []BackupScheduleResponse `json:"schedules"`
}

// MirrorConfigResponse is the mirror section's API view.
type MirrorConfigResponse struct {
	Enabled  bool    `json:"enabled"`
	Path     string  `json:"path"`
	Interval float64 `json:"interval"`
	Time     string  `json:"time"`
}

// LlmModelsResponse is the model catalog as the config UI shows it.
type LlmModelsResponse struct {
	Adapters  map[string][]string        `json:"adapters"`
	Providers map[string][]LlmModelEntry `json:"providers"`
}

// LlmModelEntry is one model in the UI's model catalog view.
type LlmModelEntry struct {
	ID           string `json:"id"`
	Capabilities struct {
		CanDisableReasoning    bool     `json:"disable_reasoning"`
		ReasoningEfforts       []string `json:"reasoning_efforts,omitempty"`
		MaxInputTokens         int      `json:"max_input_tokens"`
		MaxOutputTokens        int      `json:"max_output_tokens"`
		SupportsTemperature    bool     `json:"supports_temperature"`
		SupportsResponseSchema bool     `json:"supports_response_schema"`
	} `json:"capabilities"`
}

// ConfigResponseFrom maps the finalized tree into the API view.
func ConfigResponseFrom(cfg *config.Config) ConfigResponse {
	var resp ConfigResponse
	resp.Consumer.Workers = cfg.Consumer.Workers
	resp.Consumer.MaxFilesPerBatch = cfg.Consumer.MaxFilesPerBatch
	resp.Consumer.SupportedFiles = cfg.Consumer.SupportedFiles
	resp.Consumer.Converter.Enabled = cfg.Consumer.Converter.Enabled
	resp.Consumer.Converter.Binary = cfg.Consumer.Converter.Binary
	resp.Consumer.Converter.Timeout = cfg.Consumer.Converter.Timeout
	resp.Consumer.Polling.Enabled = cfg.Consumer.Polling.Enabled
	resp.Consumer.Polling.Interval = cfg.Consumer.Polling.Interval
	resp.Consumer.Polling.Windows = make([]PollingWindowResponse, len(cfg.Consumer.Polling.Windows))
	for i, w := range cfg.Consumer.Polling.Windows {
		resp.Consumer.Polling.Windows[i] = PollingWindowResponse{Start: w.Start, End: w.End}
	}
	resp.Consumer.Reclaim.Enabled = cfg.Consumer.Reclaim.Enabled
	resp.Consumer.Reclaim.MaxRetries = cfg.Consumer.Reclaim.MaxRetries
	resp.Consumer.Reclaim.StaleTaskAfter = cfg.Consumer.Reclaim.StaleTaskAfter
	resp.Consumer.TextExtractor.Engine = cfg.Consumer.TextExtractor.Engine
	resp.Consumer.TextExtractor.Timeout = cfg.Consumer.TextExtractor.Timeout
	resp.Consumer.PdfOptimizer.Engine = cfg.Consumer.PdfOptimizer.Engine
	resp.Consumer.PdfOptimizer.Fallback = cfg.Consumer.PdfOptimizer.Fallback
	resp.Consumer.PdfOptimizer.Timeout = cfg.Consumer.PdfOptimizer.Timeout
	resp.Consumer.OCR.Engine = cfg.Consumer.OCR.Engine
	resp.Consumer.OCR.Languages = cfg.Consumer.OCR.Languages
	resp.Consumer.OCR.DataDir = cfg.Consumer.OCR.DataDir
	resp.Consumer.OCR.Timeout = cfg.Consumer.OCR.Timeout
	resp.Consumer.OCR.OcrWorkers = cfg.Consumer.OCR.OcrWorkers
	resp.Consumer.Thumbnail.Enabled = cfg.Consumer.Thumbnail.Enabled
	resp.Consumer.Thumbnail.Engine = cfg.Consumer.Thumbnail.Engine
	resp.Consumer.Thumbnail.DPI = cfg.Consumer.Thumbnail.DPI
	resp.Consumer.Thumbnail.MaxWidth = cfg.Consumer.Thumbnail.MaxWidth
	resp.Consumer.Thumbnail.Quality = cfg.Consumer.Thumbnail.Quality
	resp.Consumer.Thumbnail.Timeout = cfg.Consumer.Thumbnail.Timeout
	resp.Consumer.Thumbnail.Workers = cfg.Consumer.Thumbnail.Workers
	resp.Consumer.Thumbnail.BackfillInterval = cfg.Consumer.Thumbnail.BackfillInterval
	resp.Consumer.Thumbnail.BackfillTime = cfg.Consumer.Thumbnail.BackfillTime
	resp.Enricher.Workers = cfg.Enricher.Workers
	resp.Enricher.TextReducer.Engine = cfg.Enricher.TextReducer.Engine
	resp.Enricher.TextReducer.Timeout = cfg.Enricher.TextReducer.Timeout
	resp.Enricher.TextReducer.TargetWords = cfg.Enricher.TextReducer.TargetWords
	resp.Enricher.ContentAnalyzer.Enabled = cfg.Enricher.ContentAnalyzer.Enabled
	resp.Enricher.ContentAnalyzer.Timeout = cfg.Enricher.ContentAnalyzer.Timeout
	resp.Enricher.ContentAnalyzer.MinTags = cfg.Enricher.ContentAnalyzer.MinTags
	resp.Enricher.ContentAnalyzer.PromptTemplate = cfg.Enricher.ContentAnalyzer.PromptTemplate
	resp.Enricher.ContentAnalyzer.PauseOnCreditError = cfg.Enricher.ContentAnalyzer.PauseOnCreditError
	resp.Enricher.ContentAnalyzer.DocTypeRefinement = DocTypeRefinementResponse{
		Enabled:   cfg.Enricher.ContentAnalyzer.DocTypeRefinement.Enabled,
		HeadWords: cfg.Enricher.ContentAnalyzer.DocTypeRefinement.HeadWords,
		TailWords: cfg.Enricher.ContentAnalyzer.DocTypeRefinement.TailWords,
	}
	resp.Enricher.ContentAnalyzer.Llm.Adapter = cfg.Enricher.ContentAnalyzer.Llm.Adapter
	resp.Enricher.ContentAnalyzer.Llm.Provider = cfg.Enricher.ContentAnalyzer.Llm.Provider
	resp.Enricher.ContentAnalyzer.Llm.Model = cfg.Enricher.ContentAnalyzer.Llm.Model
	resp.Enricher.ContentAnalyzer.Llm.Token = cfg.Enricher.ContentAnalyzer.Llm.Token
	resp.Enricher.ContentAnalyzer.Llm.Reasoning = cfg.Enricher.ContentAnalyzer.Llm.Reasoning
	resp.Enricher.ContentAnalyzer.Llm.Temperature = cfg.Enricher.ContentAnalyzer.Llm.Temperature
	resp.Enricher.ContentAnalyzer.Llm.RequestDelay = cfg.Enricher.ContentAnalyzer.Llm.RequestDelay
	for i := range cfg.Enricher.ContentAnalyzer.Fallbacks {
		fb := &cfg.Enricher.ContentAnalyzer.Fallbacks[i]
		resp.Enricher.ContentAnalyzer.Fallbacks = append(resp.Enricher.ContentAnalyzer.Fallbacks, FallbackConfigResponse{
			Enabled: fb.Enabled,
			Llm: LlmConfigResponse{
				Adapter:      fb.Llm.Adapter,
				Provider:     fb.Llm.Provider,
				Model:        fb.Llm.Model,
				Token:        fb.Llm.Token,
				Reasoning:    fb.Llm.Reasoning,
				Temperature:  fb.Llm.Temperature,
				RequestDelay: fb.Llm.RequestDelay,
			},
		})
	}
	resp.Enricher.TagMatcher.Timeout = cfg.Enricher.TagMatcher.Timeout
	resp.Enricher.TagMatcher.ReduceTargetWords = cfg.Enricher.TagMatcher.ReduceTargetWords
	resp.Enricher.TagMatcher.ChunkSize = cfg.Enricher.TagMatcher.ChunkSize
	resp.Enricher.TagMatcher.TopN = cfg.Enricher.TagMatcher.TopN
	resp.Enricher.TagMatcher.Hugot.Model = cfg.Enricher.TagMatcher.Hugot.Model
	resp.Enricher.TagMatcher.Hugot.Backend = cfg.Enricher.TagMatcher.Hugot.Backend
	resp.Enricher.TagAdjudicator.Enabled = cfg.Enricher.TagAdjudicator.Enabled
	resp.Enricher.TagAdjudicator.Timeout = cfg.Enricher.TagAdjudicator.Timeout
	resp.Enricher.TagAdjudicator.Llm.Adapter = cfg.Enricher.TagAdjudicator.Llm.Adapter
	resp.Enricher.TagAdjudicator.Llm.Provider = cfg.Enricher.TagAdjudicator.Llm.Provider
	resp.Enricher.TagAdjudicator.Llm.Model = cfg.Enricher.TagAdjudicator.Llm.Model
	resp.Enricher.TagAdjudicator.Llm.Token = cfg.Enricher.TagAdjudicator.Llm.Token
	resp.Enricher.TagAdjudicator.Llm.RequestDelay = cfg.Enricher.TagAdjudicator.Llm.RequestDelay
	resp.Enricher.TagQuality.Enabled = cfg.Enricher.TagQuality.Enabled
	resp.Enricher.TagQuality.Timeout = cfg.Enricher.TagQuality.Timeout
	resp.Enricher.TagQuality.Llm.Adapter = cfg.Enricher.TagQuality.Llm.Adapter
	resp.Enricher.TagQuality.Llm.Provider = cfg.Enricher.TagQuality.Llm.Provider
	resp.Enricher.TagQuality.Llm.Model = cfg.Enricher.TagQuality.Llm.Model
	resp.Enricher.TagQuality.Llm.Token = cfg.Enricher.TagQuality.Llm.Token
	resp.Enricher.TagQuality.Llm.RequestDelay = cfg.Enricher.TagQuality.Llm.RequestDelay
	resp.Enricher.TagReconcile.Enabled = cfg.Enricher.TagReconcile.Enabled
	resp.Enricher.TagReconcile.ReportOnly = cfg.Enricher.TagReconcile.ReportOnly
	resp.Enricher.TagReconcile.Interval = cfg.Enricher.TagReconcile.Interval
	resp.Enricher.TagReconcile.Time = cfg.Enricher.TagReconcile.Time
	resp.Enricher.TagReconcile.MaxRejudge = cfg.Enricher.TagReconcile.MaxRejudge
	resp.Enricher.TagReconcile.MaxRetag = cfg.Enricher.TagReconcile.MaxRetag
	resp.Enricher.TagReconcile.DeadTagGraceDays = cfg.Enricher.TagReconcile.DeadTagGraceDays
	resp.Enricher.TagReconcile.ProvisionalGraceDays = cfg.Enricher.TagReconcile.ProvisionalGraceDays
	resp.Enricher.TagMeasure.Enabled = cfg.Enricher.TagMeasure.Enabled
	resp.Enricher.TagMeasure.Interval = cfg.Enricher.TagMeasure.Interval
	resp.Enricher.TagMeasure.Time = cfg.Enricher.TagMeasure.Time
	resp.Enricher.TagMeasure.AfterEnrichmentBatch = cfg.Enricher.TagMeasure.AfterEnrichmentBatch
	resp.Enricher.TagAudit.Enabled = cfg.Enricher.TagAudit.Enabled
	resp.Enricher.TagAudit.Interval = cfg.Enricher.TagAudit.Interval
	resp.Enricher.TagAudit.Time = cfg.Enricher.TagAudit.Time
	resp.Enricher.TagAudit.SampleSize = cfg.Enricher.TagAudit.SampleSize
	resp.Enricher.TagMaintenance.Enabled = cfg.Enricher.TagMaintenance.Enabled
	resp.Enricher.TagMaintenance.Interval = cfg.Enricher.TagMaintenance.Interval
	resp.Enricher.TagMaintenance.Time = cfg.Enricher.TagMaintenance.Time
	resp.Storage.ConsumptionDir = cfg.Storage.ConsumptionDir
	resp.Storage.StorageDir = cfg.Storage.StorageDir
	resp.Storage.MigrationMode = cfg.Storage.MigrationMode
	resp.Storage.Trash.RetentionDays = cfg.Storage.Trash.RetentionDays
	resp.Database.Host = cfg.Db.Host
	resp.Database.Port = cfg.Db.Port
	resp.Database.User = cfg.Db.User
	resp.Database.Database = cfg.Db.Database
	resp.Database.SSLMode = cfg.Db.SSLMode
	resp.Database.Runtime = cfg.Db.Runtime
	resp.Database.Container = cfg.Db.Container
	resp.App.Initialized = cfg.App.ConfigDir != ""
	resp.App.LogLevel = cfg.App.LogLevel
	resp.App.Logging = LoggingConfigResponse{
		MaxSize:    cfg.App.Logging.MaxSize,
		MaxBackups: cfg.App.Logging.MaxBackups,
		MaxAge:     cfg.App.Logging.MaxAge,
		Compress:   cfg.App.Logging.Compress,
	}
	resp.Server.Host = cfg.Srv.Host
	resp.Server.Port = cfg.Srv.Port
	resp.Server.MaxUploadSize = cfg.Srv.MaxUploadSize
	resp.Server.AuthEnabled = cfg.Srv.AuthEnabled
	resp.Server.MaxDownloadFiles = cfg.Srv.MaxDownloadFiles
	resp.Server.MaxDownloadSizeMB = cfg.Srv.MaxDownloadSizeMB
	resp.Server.MaxConcurrentBatches = cfg.Srv.MaxConcurrentBatches
	resp.Server.MaxBatchDelete = cfg.Srv.MaxBatchDelete
	resp.Backup.Enabled = cfg.Backup.Enabled
	resp.Backup.Path = cfg.Backup.Path
	resp.Backup.Schedules = make([]BackupScheduleResponse, len(cfg.Backup.Schedules))
	for i, s := range cfg.Backup.Schedules {
		resp.Backup.Schedules[i] = BackupScheduleResponse{
			Mode:     s.Mode,
			Interval: s.Interval,
			Time:     s.Time,
			Keep:     s.Keep,
			Path:     s.Path,
		}
	}
	resp.Mirror.Enabled = cfg.Mirror.Enabled
	resp.Mirror.Path = cfg.Mirror.Path
	resp.Mirror.Interval = cfg.Mirror.Interval
	resp.Mirror.Time = cfg.Mirror.Time
	resp.AvailableEngines = config.AvailableEngines
	resp.AvailableFileTypes = buildAvailableFileTypes()
	return resp
}

func buildAvailableFileTypes() []AvailableFileType {
	required := map[string]bool{mime.PDF: true}
	types := make([]AvailableFileType, 0, len(mime.Supported))
	for _, mi := range mime.Supported {
		types = append(types, AvailableFileType{
			MimeType:   mi.MimeType,
			Label:      mi.Label,
			Extensions: mime.ExtensionsFor(mi.MimeType),
			Required:   required[mi.MimeType],
		})
	}
	return types
}
