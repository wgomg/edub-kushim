package handlers

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes v as the response body under the given status.
// Encoding errors are dropped: by the time the encoder runs, the status
// line and headers are already on the wire, so the caller has no way to
// report them.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
