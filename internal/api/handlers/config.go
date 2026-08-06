package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	itypes "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/configtask"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const configSource = "config"

type ConfigHandler struct {
	getConfig   func() *config.Config
	onConfigSet func(*config.Config)
	queries     *database.Queries
	logger      *utils.Logger
	dispatcher  *task.Dispatcher
	services    *itypes.CrudServices
	OnBootstrap func(configDir string) (*config.Config, *database.Client, *task.Dispatcher, error)
}

func NewConfigHandler(
	getConfig func() *config.Config,
	onConfigSet func(*config.Config),
	queries *database.Queries,
	logger *utils.Logger,
	dispatcher *task.Dispatcher,
	services *itypes.CrudServices,
) *ConfigHandler {
	return &ConfigHandler{
		getConfig:   getConfig,
		onConfigSet: onConfigSet,
		queries:     queries,
		logger:      logger,
		dispatcher:  dispatcher,
		services:    services,
	}
}

func (h *ConfigHandler) SetServices(client *database.Client, dispatcher *task.Dispatcher) {
	h.dispatcher = dispatcher
	if client != nil {
		h.queries = client.Queries
		h.services = &itypes.CrudServices{
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
func (h *ConfigHandler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	cfg := h.getConfig()
	if cfg == nil {
		cfg = config.DefaultConfig("")
	}

	resp := types.BootstrapResponse{
		AuthEnabled:  cfg.Srv.AuthEnabled,
		MissingTools: config.MissingExternalToolErrors(cfg),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (h *ConfigHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.getConfig()
	if cfg == nil {
		cfg = config.DefaultConfig("")
	}

	resp := types.ConfigResponseFrom(cfg)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (h *ConfigHandler) PutConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if configDir, ok := body["config_dir"].(string); ok && h.getConfig() == nil {
		var cfg *config.Config
		var client *database.Client
		var dispatcher *task.Dispatcher
		var err error

		if h.OnBootstrap != nil {
			cfg, client, dispatcher, err = h.OnBootstrap(configDir)
		} else {
			cfg, err = config.Bootstrap(configDir)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		h.onConfigSet(cfg)
		h.SetServices(client, dispatcher)

		svcPath, svcErr := config.GenerateServiceFiles(configDir)
		if svcErr != nil {
			h.logger.Warn(nil, "failed to generate service files: %v", svcErr)
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

	if newDB, err := dbParamsFromBody(body, h.getConfig().Db); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if config.DatabaseConnectionChanged(h.getConfig().Db, newDB) {
		if h.dispatcher == nil {
			http.Error(w, "database not ready — retry after setup completes", http.StatusServiceUnavailable)
			return
		}
		immediate := make(map[string]any)
		for k, v := range body {
			if strings.HasPrefix(k, "database.") || strings.HasPrefix(k, "storage.") {
				continue
			}
			immediate[k] = v
		}
		if err := config.SaveMap(configDir, immediate); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.enqueueDBMigration(ctx, w, body, newDB)
		return
	}

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
		h.logger.Warn(nil, "failed to generate service files: %v", svcErr)
	}
	missing := config.MissingExternalToolErrors(cfg)

	if h.dispatcher == nil {
		resp := map[string]any{
			"configured":    true,
			"missing_tools": missing,
		}
		if svcPath != "" {
			resp["service_files_path"] = svcPath
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	enqueued := h.enqueueConfigTasks(ctx, cfg)

	if enqueued > 0 {
		resp := map[string]any{
			"pending_tasks": enqueued,
			"missing_tools": missing,
		}
		if svcPath != "" {
			resp["service_files_path"] = svcPath
		}
		writeJSON(w, http.StatusCreated, resp)
	} else {
		resp := map[string]any{
			"configured":    true,
			"missing_tools": missing,
		}
		if svcPath != "" {
			resp["service_files_path"] = svcPath
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func (h *ConfigHandler) enqueueConfigTasks(ctx context.Context, cfg *config.Config) int {
	enqueued := 0
	batchID := uuid.New().String()

	h.services.Batch.Create(ctx, batchID, configSource, "queued")

	for _, lang := range config.MissingTessdataLanguages(cfg) {
		key := "config:tessdata:" + lang
		if h.handleConfigTask(ctx, batchID, key, map[string]string{
			"config_dir": cfg.App.ConfigDir,
			"op":         "tessdata",
			"lang":       lang,
		}) {
			enqueued++
		}
	}

	if config.MissingHugotModel(cfg) {
		if h.handleConfigTask(ctx, batchID, "config:hugot", map[string]string{
			"config_dir": cfg.App.ConfigDir,
			"op":         "hugot",
		}) {
			enqueued++
		}
	}

	return enqueued
}

func (h *ConfigHandler) handleConfigTask(ctx context.Context, batchId, dedupKey string, payloadFields map[string]string) bool {
	existing, err := h.queries.GetConfigTaskByDedupKey(ctx, sql.NullString{String: dedupKey, Valid: true})
	if err == nil {
		switch existing.Status {
		case "pending", "processing":
			return false
		default:
			if err := h.queries.RetryTask(ctx, existing.ID); err != nil {
				h.logger.Error(nil, "retry config task %d: %v", existing.ID, err)
				return false
			}
			return true
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		h.logger.Error(nil, "lookup config task for dedup key %s: %v", dedupKey, err)
	}

	payload, _ := json.Marshal(payloadFields)
	if _, err := h.dispatcher.Enqueue(ctx, configtask.TaskTypeConfig, batchId, payload, ""); err != nil {
		h.logger.Error(nil, "enqueue config task %s: %v", dedupKey, err)
		return false
	}
	return true
}

func dbParamsFromBody(body map[string]any, current config.DatabaseConfig) (config.DatabaseConfig, error) {
	db := config.DatabaseConfig{
		Host:     asString(body["database.host"]),
		User:     asString(body["database.user"]),
		Password: asString(body["database.password"]),
		Database: asString(body["database.database"]),
		SSLMode:  asString(body["database.sslmode"]),
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
	return db, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func (h *ConfigHandler) enqueueDBMigration(ctx context.Context, w http.ResponseWriter, body map[string]any, newDB config.DatabaseConfig) {
	existing, err := h.queries.GetConfigTaskByDedupKey(ctx, sql.NullString{String: configtask.DedupKeyMigrateDB, Valid: true})
	switch {
	case err == nil && existing.Status == "processing":
		writeJSON(w, http.StatusConflict, map[string]any{"error": "database migration already in progress"})
		return
	case err == nil:
		deleted, delErr := h.queries.DeleteConfigTaskByDedupKey(ctx, sql.NullString{String: configtask.DedupKeyMigrateDB, Valid: true})
		if delErr != nil {
			h.logger.Error(nil, "delete stale migration task: %v", delErr)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to queue database migration"})
			return
		}
		if deleted == 0 && existing.Status == "pending" {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "database migration already in progress"})
			return
		}
	case !errors.Is(err, sql.ErrNoRows):
		h.logger.Error(nil, "lookup migration task: %v", err)
	}

	cfg := h.getConfig()
	payload := configtask.MigrateDBPayload{
		Op:                "migrate-db",
		ConfigDir:         cfg.App.ConfigDir,
		Host:              newDB.Host,
		Port:              strconv.Itoa(newDB.Port),
		User:              newDB.User,
		Password:          newDB.Password,
		Database:          newDB.Database,
		SSLMode:           newDB.SSLMode,
		OldStorageDir:     cfg.Storage.StorageDir,
		NewStorageDir:     asString(body["storage.storage_dir"]),
		OldConsumptionDir: cfg.Storage.ConsumptionDir,
		NewConsumptionDir: asString(body["storage.consumption_dir"]),
	}
	payloadJSON, _ := json.Marshal(payload)

	batchID := uuid.New().String()
	h.services.Batch.Create(ctx, batchID, configSource, "queued")

	if _, err := h.dispatcher.Enqueue(ctx, configtask.TaskTypeConfig, batchID, payloadJSON, ""); err != nil {
		h.logger.Error(nil, "enqueue migration task: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to enqueue database migration"})
		return
	}

	h.logger.Info(nil, "database migration queued: %s -> %s@%s:%d/%s", cfg.Db.Database, newDB.User, newDB.Host, newDB.Port, newDB.Database)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"pending_tasks": 1,
		"message":       "database migration queued — settings apply once the data copy completes",
	})
}

func (h *ConfigHandler) ConfigStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cfg := h.getConfig()
	configured := cfg != nil && len(cfg.Consumer.OCR.Languages) > 0

	pendingTasks := 0
	var errors []string

	resp := types.ConfigStatusResponse{
		Configured:   configured,
		PendingTasks: pendingTasks,
		Errors:       errors,
	}

	if h.queries != nil {
		rows, err := h.queries.CountTasksByStatus(ctx)
		if err != nil {
			resp.Errors = append(resp.Errors, err.Error())
		} else {
			for _, row := range rows {
				if row.Status == "pending" || row.Status == "processing" {
					resp.PendingTasks += int(row.Count)
				}
			}
		}

		failedTasks, err := h.queries.ListAllTasksByStatusAndType(ctx, database.ListAllTasksByStatusAndTypeParams{
			Status:   "failed",
			TaskType: configtask.TaskTypeConfig,
		})
		if err != nil {
			h.logger.Error(nil, "list failed config tasks: %v", err)
		} else {
			respFailed := make([]types.FailedTaskSummary, 0, len(failedTasks))
			for _, t := range failedTasks {
				summary := types.FailedTaskSummary{TaskID: t.TaskID}
				var p struct {
					Op   string `json:"op"`
					Lang string `json:"lang"`
				}
				if t.Payload != nil {
					json.Unmarshal(*t.Payload, &p)
				}
				summary.Op = p.Op
				summary.Lang = p.Lang
				if t.Error.Valid {
					summary.Error = t.Error.String
				}
				respFailed = append(respFailed, summary)
			}
			resp.FailedTasks = respFailed
		}
	}

	if cfg != nil {
		allTools := config.MissingExternalTools(cfg)
		resp.Tools = allTools
		resp.MissingTools = config.FilterToolErrors(allTools)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (h *ConfigHandler) RetryFailedConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.queries == nil {
		http.Error(w, "not initialized", http.StatusBadRequest)
		return
	}

	failedTasks, err := h.queries.ListAllTasksByStatusAndType(ctx, database.ListAllTasksByStatusAndTypeParams{
		Status:   "failed",
		TaskType: configtask.TaskTypeConfig,
	})
	if err != nil {
		h.logger.Error(nil, "list failed config tasks: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	retried := 0
	for _, t := range failedTasks {
		if err := task.Retry(ctx, h.queries, h.logger, t.TaskID); err != nil {
			h.logger.Error(nil, "retry config task %s: %v", t.TaskID, err)
			continue
		}
		retried++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"retried": retried})
}

func (h *ConfigHandler) CreateAdminUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.services == nil || h.services.User == nil {
		http.Error(w, "service not initialized", http.StatusBadRequest)
		return
	}

	var req types.CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Username = utils.StripTags(strings.TrimSpace(req.Username))
	if req.Username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	user, err := h.services.User.Create(ctx, req.Username, req.Password, "admin")
	if err != nil {
		if errs.KindOf(err) == errs.KindConflict {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "username already exists"})
			return
		}
		writeServiceError(w, h.logger, nil, "create admin user", err)
		return
	}

	created := ""
	if user.CreatedAt.Valid {
		created = user.CreatedAt.Time.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(types.UserResponse{
		ID:        user.ID,
		Username:  user.Username,
		Role:      "admin",
		CreatedAt: created,
	})
}

func sanitizeConfigStrings(v any) any {
	switch m := v.(type) {
	case string:
		return utils.StripTags(m)
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
