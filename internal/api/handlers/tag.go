package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	itypes "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tagmatch/rpc"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func isMatcherUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, rpc.ErrMatcherUnavailable)
}

func matcherUnavailableResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	json.NewEncoder(w).Encode(map[string]string{
		"error": "matcher unavailable — tag store is offline",
	})
}

type TagHandler struct {
	services *itypes.CrudServices
	logger   *utils.Logger
}

func NewTagHandler(services *itypes.CrudServices, logger *utils.Logger) *TagHandler {
	return &TagHandler{services: services, logger: logger}
}

func (h *TagHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	pb := utils.GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	q := pb.Get("q", "")
	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 100000)

	var result []database.Tag
	var err error

	if q != "" {
		result, err = h.services.Tag.Search(ctx, q, limit)
	} else {
		result, err = h.services.Tag.List(ctx, limit, offset)
	}
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list tags", err)
		return
	}

	response := make([]types.TagResponse, len(result))
	for i, t := range result {
		response[i] = types.TagResponse{ID: t.ID, Name: t.Name}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *TagHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req types.CreateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "Tag name is required", http.StatusBadRequest)
		return
	}

	results, err := h.services.Tag.Create(ctx, []string{req.Name})
	if err != nil {
		if isMatcherUnavailable(err) {
			matcherUnavailableResponse(w)
			return
		}
		writeServiceError(w, h.logger, &reqID, "create tag", err)
		return
	}

	switch results[0].Status {
	case tags.Created:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.TagResponse{ID: results[0].Tag.ID, Name: results[0].Tag.Name})
	case tags.Conflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"id":   results[0].Tag.ID,
			"name": results[0].Tag.Name,
		})
	default:
		http.Error(w, "Tag name is required", http.StatusBadRequest)
	}
}

func (h *TagHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req types.UpdateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "Tag name is required", http.StatusBadRequest)
		return
	}

	results, err := h.services.Tag.Update(ctx, []tags.UpdatePair{{ID: id, Name: req.Name}})
	if err != nil {
		if isMatcherUnavailable(err) {
			matcherUnavailableResponse(w)
			return
		}
		writeServiceError(w, h.logger, &reqID, "update tag", err)
		return
	}

	switch results[0].Status {
	case tags.Updated, tags.Noop:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(types.TagResponse{ID: results[0].Tag.ID, Name: results[0].Tag.Name})
	case tags.UpdateConflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"id":   results[0].Tag.ID,
			"name": results[0].Tag.Name,
		})
	case tags.UpdateNotFound:
		http.Error(w, "Tag not found", http.StatusNotFound)
	default:
		http.Error(w, "Tag name is required", http.StatusBadRequest)
	}
}

func (h *TagHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	results, err := h.services.Tag.Delete(ctx, []int64{id})
	if err != nil {
		if isMatcherUnavailable(err) {
			matcherUnavailableResponse(w)
			return
		}
		writeServiceError(w, h.logger, &reqID, "delete tag", err)
		return
	}

	switch results[0].Status {
	case tags.Deleted:
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Tag not found", http.StatusNotFound)
	}
}
