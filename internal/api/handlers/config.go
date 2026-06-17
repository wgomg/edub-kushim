package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	taskhandlers "github.com/wgomg/edub-kushim/internal/task/handlers"
	"github.com/wgomg/edub-kushim/internal/utils"
)

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

	if h.dispatcher == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"configured": true})
		return
	}

	enqueued := h.enqueueConfigTasks(ctx, cfg)

	if enqueued > 0 {
		writeJSON(w, http.StatusCreated, map[string]int{"pending_tasks": enqueued})
	} else {
		writeJSON(w, http.StatusOK, map[string]bool{"configured": true})
	}
}

func (h *ConfigHandler) enqueueConfigTasks(ctx context.Context, cfg *config.Config) int {
	enqueued := 0

	for _, lang := range config.MissingTessdataLanguages(cfg) {
		payload, _ := json.Marshal(map[string]string{
			"config_dir": cfg.App.ConfigDir,
			"op":         "tessdata",
			"lang":       lang,
		})
		if _, err := h.dispatcher.Enqueue(ctx, taskhandlers.TaskTypeConfig, "", payload, ""); err != nil {
			h.logger.Error(nil, "enqueue tessdata download for %s: %v", lang, err)
			continue
		}
		enqueued++
	}

	if config.MissingHugotModel(cfg) {
		payload, _ := json.Marshal(map[string]string{
			"config_dir": cfg.App.ConfigDir,
			"op":         "hugot",
		})
		if _, err := h.dispatcher.Enqueue(ctx, taskhandlers.TaskTypeConfig, "", payload, ""); err != nil {
			h.logger.Error(nil, "enqueue hugot model download: %v", err)
		} else {
			enqueued++
		}
	}

	return enqueued
}

func (h *ConfigHandler) ConfigStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	configured := h.cfg != nil && len(h.cfg.Consumer.OCR.Languages) > 0

	pendingTasks := 0
	var errors []string

	if h.queries != nil {
		rows, err := h.queries.CountTasksByStatus(ctx)
		if err != nil {
			errors = append(errors, err.Error())
		} else {
			for _, row := range rows {
				if row.Status == "pending" || row.Status == "processing" {
					pendingTasks += int(row.Count)
				}
			}
		}
	}

	resp := types.ConfigStatusResponse{
		Configured:   configured,
		PendingTasks: pendingTasks,
		Errors:       errors,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
