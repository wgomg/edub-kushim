package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/wgomg/edub-kushim/internal/utils"
)

type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Time    string `json:"time"`
}

func HealthHandler(w http.ResponseWriter, r *http.Request, logger *utils.Logger) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	logger.Debug(&reqID, "Health check requested")

	response := HealthResponse{
		Status:  "healthy",
		Version: "0.1.0",
		Time:    time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error(&reqID, "Failed to encode health response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
