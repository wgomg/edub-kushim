package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/configtask"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/sanitize"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const configSource = "config"

type ConfigHandler struct {
	cfg         *config.Config
	queries     *database.Queries
	logger      *utils.Logger
	dispatcher  *task.Dispatcher
	OnBootstrap func(configDir string) (*config.Config, *database.Queries, *task.Dispatcher, error)
}

func NewConfigHandler(cfg *config.Config, queries *database.Queries, logger *utils.Logger, dispatcher *task.Dispatcher) *ConfigHandler {
	return &ConfigHandler{
		cfg:        cfg,
		queries:    queries,
		logger:     logger,
		dispatcher: dispatcher,
	}
}

func (h *ConfigHandler) SetBootstrap(cfg *config.Config, queries *database.Queries, dispatcher *task.Dispatcher) {
	h.cfg = cfg
	h.queries = queries
	h.dispatcher = dispatcher
}

func (h *ConfigHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.cfg
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

	if configDir, ok := body["config_dir"].(string); ok && h.cfg == nil {
		var cfg *config.Config
		var queries *database.Queries
		var dispatcher *task.Dispatcher
		var err error

		if h.OnBootstrap != nil {
			cfg, queries, dispatcher, err = h.OnBootstrap(configDir)
		} else {
			cfg, err = config.Bootstrap(configDir)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		h.cfg = cfg
		if queries != nil {
			h.queries = queries
		}
		if dispatcher != nil {
			h.dispatcher = dispatcher
		}

		writeJSON(w, http.StatusOK, map[string]string{"config_dir": configDir})
		return
	}

	if h.cfg == nil {
		http.Error(w, "config not initialized — send config_dir first", http.StatusBadRequest)
		return
	}

	configDir := h.cfg.App.ConfigDir
	sanitizeConfigStrings(body)
	if err := config.SaveMap(configDir, body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cfg, err := config.Load(configDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.cfg = cfg
	missing := config.MissingExternalToolErrors(cfg)

	if h.dispatcher == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured":    true,
			"missing_tools": missing,
		})
		return
	}

	enqueued := h.enqueueConfigTasks(ctx, cfg)

	if enqueued > 0 {
		writeJSON(w, http.StatusCreated, map[string]any{
			"pending_tasks": enqueued,
			"missing_tools": missing,
		})
	} else {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured":    true,
			"missing_tools": missing,
		})
	}
}

func (h *ConfigHandler) enqueueConfigTasks(ctx context.Context, cfg *config.Config) int {
	enqueued := 0
	batchID := uuid.New().String()

	h.queries.CreateBatch(ctx, database.CreateBatchParams{
		ID:     batchID,
		Source: configSource,
	})

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

func (h *ConfigHandler) ConfigStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	configured := h.cfg != nil && len(h.cfg.Consumer.OCR.Languages) > 0

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
					json.Unmarshal(t.Payload, &p)
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

	if h.cfg != nil {
		allTools := config.MissingExternalTools(h.cfg)
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
		if err := task.Retry(ctx, h.queries, t.TaskID); err != nil {
			h.logger.Error(nil, "retry config task %s: %v", t.TaskID, err)
			continue
		}
		retried++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"retried": retried})
}

func sanitizeConfigStrings(v any) any {
	switch m := v.(type) {
	case string:
		return sanitize.StripTags(m)
	case map[string]any:
		for k, val := range m {
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
