package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type AutocompleteHandler struct {
	queries *database.Queries
	logger  *utils.Logger
}

func NewAutocompleteHandler(queries *database.Queries, logger *utils.Logger) *AutocompleteHandler {
	return &AutocompleteHandler{
		queries: queries,
		logger:  logger,
	}
}

func (h *AutocompleteHandler) ListTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pb := utils.GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	q := pb.Get("q", "")
	limit := int32(pb.GetInt64("limit", 20, 1, 100))

	if q == "" {
		tags, err := h.queries.ListAllTags(ctx)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		response := make([]types.TagResponse, len(tags))
		for i, t := range tags {
			response[i] = types.TagResponse{ID: t.ID, Name: t.Name}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	tags, err := h.queries.SearchTagsByName(ctx, database.SearchTagsByNameParams{
		Name:  q + "%",
		Limit: int64(limit),
	})
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]types.TagResponse, len(tags))
	for i, t := range tags {
		response[i] = types.TagResponse{ID: t.ID, Name: t.Name}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *AutocompleteHandler) ListPeople(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pb := utils.GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	q := pb.Get("q", "")
	limit := int32(pb.GetInt64("limit", 20, 1, 100))

	if q == "" {
		people, err := h.queries.ListAllPeople(ctx)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		response := make([]types.PersonRefResponse, len(people))
		for i, p := range people {
			response[i] = types.PersonRefResponse{ID: p.ID, Name: p.Name}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	people, err := h.queries.SearchPeopleByName(ctx, database.SearchPeopleByNameParams{
		Name:  q + "%",
		Limit: int64(limit),
	})
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]types.PersonRefResponse, len(people))
	for i, p := range people {
		response[i] = types.PersonRefResponse{ID: p.ID, Name: p.Name}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *AutocompleteHandler) ListPeopleTypes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	typesList, err := h.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]types.PeopleTypeRefResponse, len(typesList))
	for i, pt := range typesList {
		response[i] = types.PeopleTypeRefResponse{
			ID:          pt.ID,
			Name:        pt.Name,
			Description: pt.Description,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *AutocompleteHandler) ListDocumentTypes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	typesList, err := h.queries.ListAllDocumentTypes(ctx)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]types.DocumentTypeRefResponse, len(typesList))
	for i, dt := range typesList {
		response[i] = types.DocumentTypeRefResponse{
			ID:          dt.ID,
			Name:        dt.Name,
			Description: dt.Description,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
