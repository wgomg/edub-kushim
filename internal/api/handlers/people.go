package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
)

// PeopleHandler is the people API: person CRUD and the people-type
// taxonomy CRUD.
type PeopleHandler struct {
	services *service.CrudServices
	logger   *log.Logger
}

// NewPeopleHandler wires the people handler to the services.
func NewPeopleHandler(services *service.CrudServices, logger *log.Logger) *PeopleHandler {
	return &PeopleHandler{services: services, logger: logger}
}

// List serves the paged people listing with document counts.
func (h *PeopleHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	q := pb.Get("q", "")
	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 100000)

	var total int64
	var err error

	var responses []PersonResponse

	if q != "" {
		people, searchErr := h.services.People.SearchByNameWithDocumentCount(ctx, q, int32(limit), int32(offset))
		err = searchErr
		if err == nil {
			total, err = h.services.People.CountByName(ctx, q)
		}
		if err == nil {
			responses = make([]PersonResponse, len(people))
			for i, p := range people {
				nameNative := ""
				if p.NameNative.Valid {
					nameNative = p.NameNative.String
				}
				responses[i] = PersonResponse{ID: p.ID, Name: p.Name, NameNative: nameNative, DocumentCount: p.DocumentCount}
			}
		}
	} else {
		people, listErr := h.services.People.ListWithDocumentCount(ctx, int32(limit), int32(offset))
		err = listErr
		if err == nil {
			total, err = h.services.People.Count(ctx)
		}
		if err == nil {
			responses = make([]PersonResponse, len(people))
			for i, p := range people {
				nameNative := ""
				if p.NameNative.Valid {
					nameNative = p.NameNative.String
				}
				responses[i] = PersonResponse{ID: p.ID, Name: p.Name, NameNative: nameNative, DocumentCount: p.DocumentCount}
			}
		}
	}
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list people", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PersonListResponse{Results: responses, Total: total})
}

// Create creates one person from the JSON body.
func (h *PeopleHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	var req CreatePersonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = stripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "Person name is required", http.StatusBadRequest)
		return
	}

	req.NameNative = stripTags(req.NameNative)

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
		json.NewEncoder(w).Encode(PersonResponse{
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

// Update renames one person.
func (h *PeopleHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req UpdatePersonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = stripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "Person name is required", http.StatusBadRequest)
		return
	}

	req.NameNative = stripTags(req.NameNative)

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
		json.NewEncoder(w).Encode(PersonResponse{
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

// Delete removes one person.
func (h *PeopleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

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

// ListPeopleTypes serves the people-type taxonomy with paging.
func (h *PeopleHandler) ListPeopleTypes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	q := pb.Get("q", "")
	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 100000)

	var result []PeopleTypeResponse

	if q != "" {
		pts, err := h.services.PeopleType.Search(ctx, q, int32(limit))
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "list people types", err)
			return
		}
		result = make([]PeopleTypeResponse, len(pts))
		for i, pt := range pts {
			result[i] = PeopleTypeResponse{ID: pt.ID, Name: pt.Name, Description: pt.Description}
		}
	} else {
		pts, err := h.services.PeopleType.List(ctx, int32(limit), int32(offset))
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "list people types", err)
			return
		}
		result = make([]PeopleTypeResponse, len(pts))
		for i, pt := range pts {
			result[i] = PeopleTypeResponse{ID: pt.ID, Name: pt.Name, Description: pt.Description}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// CreatePeopleType creates one people type from the JSON body.
func (h *PeopleHandler) CreatePeopleType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	var req CreatePeopleTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = stripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "People type name is required", http.StatusBadRequest)
		return
	}
	if strings.EqualFold(req.Name, database.PersonAnyType) {
		http.Error(w, fmt.Sprintf("People type name %q is reserved", database.PersonAnyType), http.StatusBadRequest)
		return
	}

	req.Description = stripTags(req.Description)

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
		json.NewEncoder(w).Encode(PeopleTypeResponse{
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

// UpdatePeopleType renames one people type.
func (h *PeopleHandler) UpdatePeopleType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req UpdatePeopleTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = stripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "People type name is required", http.StatusBadRequest)
		return
	}
	if strings.EqualFold(req.Name, database.PersonAnyType) {
		http.Error(w, fmt.Sprintf("People type name %q is reserved", database.PersonAnyType), http.StatusBadRequest)
		return
	}

	req.Description = stripTags(req.Description)

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
		json.NewEncoder(w).Encode(PeopleTypeResponse{
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

// DeletePeopleType removes one people type.
func (h *PeopleHandler) DeletePeopleType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

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

// CreatePersonRequest is the body of a person create: romanized name and
// the optional native-script name.
type CreatePersonRequest struct {
	Name       string `json:"name"`
	NameNative string `json:"name_native"`
}

// UpdatePersonRequest is the body of a person update.
type UpdatePersonRequest struct {
	Name       string `json:"name"`
	NameNative string `json:"name_native"`
}

// CreatePeopleTypeRequest is the body of a people type create.
type CreatePeopleTypeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// UpdatePeopleTypeRequest is the body of a people type update.
type UpdatePeopleTypeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// PeopleTypeResponse is a people type as the API shows it.
type PeopleTypeResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
