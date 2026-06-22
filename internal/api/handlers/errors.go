package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func writeServiceError(w http.ResponseWriter, logger *utils.Logger, reqID *string, op string, err error) {
	kind := errs.KindOf(err)
	switch kind {
	case errs.KindNotFound:
		http.Error(w, op+" not found", http.StatusNotFound)
	case errs.KindConflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "conflict"})
	case errs.KindInvalid:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid"})
	default:
		logger.Error(reqID, "%s: %v", op, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
