package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type ConsumeHandler struct {
	consumer *consumption.Consumer
	logger   *utils.Logger
}

func NewConsumeHandler(consumer *consumption.Consumer, logger *utils.Logger) *ConsumeHandler {
	return &ConsumeHandler{
		consumer: consumer,
		logger:   logger,
	}
}

func (h *ConsumeHandler) Consume(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Consume requested")

	if err := h.consumer.Consume(&reqID); err != nil {
		h.logger.Error(&reqID, "Consume failed: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := map[string]string{
		"status": "ok",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error(&reqID, "Failed to encode consume response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
