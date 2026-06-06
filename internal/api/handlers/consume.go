package handlers

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type ConsumeHandler struct {
	cfg        *config.Config
	logger     *utils.Logger
	dispatcher *task.Dispatcher
}

func NewConsumeHandler(cfg *config.Config, logger *utils.Logger, dispatcher *task.Dispatcher) *ConsumeHandler {
	return &ConsumeHandler{
		cfg:        cfg,
		logger:     logger,
		dispatcher: dispatcher,
	}
}

func (h *ConsumeHandler) Consume(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Consume requested")

	files, err := consumption.GetFiles(
		h.cfg.Storage.ConsumptionDir,
		h.cfg.Consumer.SupportedFiles,
	)
	if err != nil {
		h.logger.Error(&reqID, "Failed to scan inbox: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if len(files) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"batch_id":    nil,
			"total_files": 0,
			"message":     "no files found",
		})
		return
	}

	batchID := uuid.New().String()
	enqueued := 0
	for i, f := range files {
		consumeTaskID := uuid.New().String()
		enrichTaskID := uuid.New().String()

		consumePayload, _ := json.Marshal(map[string]any{
			"file_path":    f.OriginalPath,
			"file_index":   i + 1,
			"on_completed": enrichTaskID,
		})
		_, err := h.dispatcher.Enqueue(ctx, "consume", batchID, consumePayload, consumeTaskID)
		if err != nil {
			h.logger.Error(&reqID, "enqueue %s: %v", f.OriginalPath, err)
			continue
		}
		enqueued++

		enrichPayload, _ := json.Marshal(map[string]any{
			"waiting_for": consumeTaskID,
			"file_name":   filepath.Base(f.OriginalPath),
			"file_index":  i + 1,
		})
		if _, err := h.dispatcher.Enqueue(ctx, "enrich", batchID, enrichPayload, enrichTaskID, "waiting"); err != nil {
			h.logger.Error(&reqID, "create enrich task for %s: %v", f.OriginalPath, err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"batch_id":    batchID,
		"total_files": len(files),
		"enqueued":    enqueued,
		"_links": map[string]string{
			"tasks": "/api/v1/tasks?batch=" + batchID,
		},
	})

	h.logger.Info(&reqID, "enqueued %d/%d files (batch %s)", enqueued, len(files), batchID)
}
