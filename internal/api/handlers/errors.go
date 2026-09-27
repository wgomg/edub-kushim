package handlers

import (
	"net/http"

	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/log"
)

// writeServiceError maps a service error to its HTTP response: 404 for
// not-found, JSON conflict and invalid bodies for the marked kinds, and
// 500 with the error logged under the request id otherwise. An empty
// request id means unknown.
func writeServiceError(w http.ResponseWriter, logger *log.Logger, reqID string, op string, err error) {
	kind := errs.KindOf(err)
	switch kind {
	case errs.KindNotFound:
		http.Error(w, op+" not found", http.StatusNotFound)
	case errs.KindConflict:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "conflict"})
	case errs.KindInvalid:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	default:
		logger.Error(reqID, "%s: %v", op, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
