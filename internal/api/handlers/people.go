package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	itypes "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type PeopleHandler struct {
	services *itypes.CrudServices
	logger   *utils.Logger
}

func NewPeopleHandler(services *itypes.CrudServices, logger *utils.Logger) *PeopleHandler {
	return &PeopleHandler{services: services, logger: logger}
}

func (h *PeopleHandler) List(w http.ResponseWriter, r *http.Request) {
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

	if q != "" {
		people, err := h.services.People.Search(ctx, q, limit)
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "list people", err)
			return
		}
		response := make([]types.PersonResponse, len(people))
		for i, p := range people {
			nameNative := ""
			if p.NameNative.Valid {
				nameNative = p.NameNative.String
			}
			response[i] = types.PersonResponse{ID: p.ID, Name: p.Name, NameNative: nameNative}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}

	people, err := h.services.People.List(ctx, limit, offset)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list people", err)
		return
	}

	response := make([]types.PersonResponse, len(people))
	for i, p := range people {
		nameNative := ""
		if p.NameNative.Valid {
			nameNative = p.NameNative.String
		}
		response[i] = types.PersonResponse{ID: p.ID, Name: p.Name, NameNative: nameNative}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *PeopleHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req types.CreatePersonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = utils.StripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "Person name is required", http.StatusBadRequest)
		return
	}

	req.NameNative = utils.StripTags(req.NameNative)

	results, err := h.services.People.Create(ctx, []service.CreatePersonInput{
		{Name: req.Name, NameNative: req.NameNative},
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "create person", err)
		return
	}

	switch results[0].Status {
	case service.Created:
		nameNative := ""
		if results[0].Entity.NameNative.Valid {
			nameNative = results[0].Entity.NameNative.String
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.PersonResponse{
			ID: results[0].Entity.ID, Name: results[0].Entity.Name, NameNative: nameNative,
		})
	case service.Conflict:
		nameNative := ""
		if results[0].Entity.NameNative.Valid {
			nameNative = results[0].Entity.NameNative.String
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"id":          results[0].Entity.ID,
			"name":        results[0].Entity.Name,
			"name_native": nameNative,
		})
	default:
		http.Error(w, "Person name is required", http.StatusBadRequest)
	}
}

func (h *PeopleHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req types.UpdatePersonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = utils.StripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "Person name is required", http.StatusBadRequest)
		return
	}

	req.NameNative = utils.StripTags(req.NameNative)

	results, err := h.services.People.Update(ctx, []service.PeopleUpdatePair{
		{ID: id, Name: req.Name, NameNative: req.NameNative},
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "update person", err)
		return
	}

	switch results[0].Status {
	case service.Updated, service.Noop:
		nameNative := ""
		if results[0].Entity.NameNative.Valid {
			nameNative = results[0].Entity.NameNative.String
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(types.PersonResponse{
			ID: results[0].Entity.ID, Name: results[0].Entity.Name, NameNative: nameNative,
		})
	case service.UpdateConflict:
		nameNative := ""
		if results[0].Entity.NameNative.Valid {
			nameNative = results[0].Entity.NameNative.String
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"id":          results[0].Entity.ID,
			"name":        results[0].Entity.Name,
			"name_native": nameNative,
		})
	case service.UpdateNotFound:
		http.Error(w, "Person not found", http.StatusNotFound)
	default:
		http.Error(w, "Person name is required", http.StatusBadRequest)
	}
}

func (h *PeopleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	results, err := h.services.People.Delete(ctx, []int64{id})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "delete person", err)
		return
	}

	switch results[0].Status {
	case service.Deleted:
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Person not found", http.StatusNotFound)
	}
}

func (h *PeopleHandler) ListPeopleTypes(w http.ResponseWriter, r *http.Request) {
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

	var result []types.PeopleTypeResponse

	if q != "" {
		pts, err := h.services.PeopleType.Search(ctx, q, limit)
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "list people types", err)
			return
		}
		result = make([]types.PeopleTypeResponse, len(pts))
		for i, pt := range pts {
			result[i] = types.PeopleTypeResponse{ID: pt.ID, Name: pt.Name, Description: pt.Description}
		}
	} else {
		pts, err := h.services.PeopleType.List(ctx, limit, offset)
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "list people types", err)
			return
		}
		result = make([]types.PeopleTypeResponse, len(pts))
		for i, pt := range pts {
			result[i] = types.PeopleTypeResponse{ID: pt.ID, Name: pt.Name, Description: pt.Description}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *PeopleHandler) CreatePeopleType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req types.CreatePeopleTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = utils.StripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "People type name is required", http.StatusBadRequest)
		return
	}

	req.Description = utils.StripTags(req.Description)

	results, err := h.services.PeopleType.Create(ctx, []service.CreatePeopleTypeInput{
		{Name: req.Name, Description: req.Description},
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "create people type", err)
		return
	}

	switch results[0].Status {
	case service.Created:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.PeopleTypeResponse{
			ID: results[0].Entity.ID, Name: results[0].Entity.Name, Description: results[0].Entity.Description,
		})
	case service.Conflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"id":          results[0].Entity.ID,
			"name":        results[0].Entity.Name,
			"description": results[0].Entity.Description,
		})
	default:
		http.Error(w, "People type name is required", http.StatusBadRequest)
	}
}

func (h *PeopleHandler) UpdatePeopleType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req types.UpdatePeopleTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = utils.StripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "People type name is required", http.StatusBadRequest)
		return
	}

	req.Description = utils.StripTags(req.Description)

	results, err := h.services.PeopleType.Update(ctx, []service.PeopleTypeUpdatePair{
		{ID: id, Name: req.Name, Description: req.Description},
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "update people type", err)
		return
	}

	switch results[0].Status {
	case service.Updated, service.Noop:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(types.PeopleTypeResponse{
			ID: results[0].Entity.ID, Name: results[0].Entity.Name, Description: results[0].Entity.Description,
		})
	case service.UpdateConflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"id":          results[0].Entity.ID,
			"name":        results[0].Entity.Name,
			"description": results[0].Entity.Description,
		})
	case service.UpdateNotFound:
		http.Error(w, "People type not found", http.StatusNotFound)
	default:
		http.Error(w, "People type name is required", http.StatusBadRequest)
	}
}

func (h *PeopleHandler) DeletePeopleType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	results, err := h.services.PeopleType.Delete(ctx, []int64{id})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "delete people type", err)
		return
	}

	switch results[0].Status {
	case service.Deleted:
		w.WriteHeader(http.StatusNoContent)
	case service.DeleteConflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "in use"})
	default:
		http.Error(w, "People type not found", http.StatusNotFound)
	}
}
