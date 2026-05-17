package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/queue"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type ConsumeHandler struct {
	consumer *consumption.Consumer
	queue    *queue.Queue
	cfg      *config.Config
	logger   *utils.Logger
}

func NewConsumeHandler(consumer *consumption.Consumer, queue *queue.Queue, cfg *config.Config, logger *utils.Logger) *ConsumeHandler {
	return &ConsumeHandler{
		consumer: consumer,
		queue:    queue,
		cfg:      cfg,
		logger:   logger,
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
		json.NewEncoder(w).Encode(map[string]interface{}{
			"batch_id":    nil,
			"total_files": 0,
			"message":     "no files found",
		})
		return
	}

	batchID := uuid.New().String()
	enqueued := h.queue.EnqueueFilePaths(ctx, "consume", batchID, consumption.FilePaths(files))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"batch_id":    batchID,
		"total_files": len(files),
		"enqueued":    enqueued,
		"_links": map[string]string{
			"tasks": "/api/v1/tasks?batch=" + batchID,
		},
	})

	h.logger.Info(&reqID, "enqueued %d/%d files (batch %s)", enqueued, len(files), batchID)
}
