package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
)

type SavedSearchHandler struct {
	queries *database.Queries
	logger  *log.Logger
}

func NewSavedSearchHandler(queries *database.Queries, logger *log.Logger) *SavedSearchHandler {
	return &SavedSearchHandler{
		queries: queries,
		logger:  logger,
	}
}

func (h *SavedSearchHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req CreateSavedSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Debug(&reqID, "create saved search: invalid body: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = stripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	if req.Filter == nil {
		http.Error(w, "Filter is required", http.StatusBadRequest)
		return
	}

	id, err := h.queries.CreateSavedSearch(ctx, database.CreateSavedSearchParams{
		Name:       req.Name,
		FilterJson: string(req.Filter),
	})
	if err != nil {
		h.logger.Error(&reqID, "create saved search: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]int64{"id": id})
}

func (h *SavedSearchHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	searches, err := h.queries.ListSavedSearches(ctx)
	if err != nil {
		h.logger.Error(&reqID, "list saved searches: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]SavedSearchResponse, len(searches))
	for i, s := range searches {
		response[i] = SavedSearchResponse{
			ID:        s.ID,
			Name:      s.Name,
			Filter:    json.RawMessage(s.FilterJson),
			CreatedAt: s.CreatedAt,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *SavedSearchHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	if err := h.queries.DeleteSavedSearch(ctx, id); err != nil {
		h.logger.Error(&reqID, "delete saved search %d: %v", id, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type CreateSavedSearchRequest struct {
	Name   string          `json:"name"`
	Filter json.RawMessage `json:"filter"`
}

type SavedSearchResponse struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Filter    json.RawMessage `json:"filter"`
	CreatedAt time.Time       `json:"created_at"`
}
