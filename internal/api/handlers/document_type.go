package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
)

// DocumentTypeHandler is the document-type taxonomy API.
type DocumentTypeHandler struct {
	services *service.CrudServices
	logger   *log.Logger
}

// NewDocumentTypeHandler wires the handler to the services.
func NewDocumentTypeHandler(services *service.CrudServices, logger *log.Logger) *DocumentTypeHandler {
	return &DocumentTypeHandler{services: services, logger: logger}
}

// List serves the paged document types with document counts.
func (h *DocumentTypeHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	q := pb.Get("q", "")
	limit := pb.GetInt64("limit", 50, 1, 100)

	var result []DocumentTypeResponse

	if q != "" {
		dts, err := h.services.DocumentType.SearchByNameWithDocumentCount(ctx, q, int32(limit))
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "list document types", err)
			return
		}
		result = make([]DocumentTypeResponse, len(dts))
		for i, dt := range dts {
			result[i] = DocumentTypeResponse{ID: dt.ID, Name: dt.Name, Description: dt.Description, DocumentCount: dt.DocumentCount}
		}
	} else {
		dts, err := h.services.DocumentType.ListAllWithDocumentCount(ctx)
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "list document types", err)
			return
		}
		result = make([]DocumentTypeResponse, len(dts))
		for i, dt := range dts {
			result[i] = DocumentTypeResponse{ID: dt.ID, Name: dt.Name, Description: dt.Description, DocumentCount: dt.DocumentCount}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// Create creates one document type from the JSON body.
func (h *DocumentTypeHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	var req CreateDocumentTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = stripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "Document type name is required", http.StatusBadRequest)
		return
	}

	req.Description = stripTags(req.Description)

	results, err := h.services.DocumentType.Create(ctx, []service.CreateDocumentTypeInput{
		{Name: req.Name, Description: req.Description},
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "create document type", err)
		return
	}

	switch results[0].Status {
	case service.Created:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(DocumentTypeResponse{
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
		http.Error(w, "Document type name is required", http.StatusBadRequest)
	}
}

// Update renames one document type.
func (h *DocumentTypeHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req UpdateDocumentTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = stripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "Document type name is required", http.StatusBadRequest)
		return
	}

	req.Description = stripTags(req.Description)

	results, err := h.services.DocumentType.Update(ctx, []service.DocumentTypeUpdatePair{
		{ID: id, Name: req.Name, Description: req.Description},
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "update document type", err)
		return
	}

	switch results[0].Status {
	case service.Updated, service.Noop:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(DocumentTypeResponse{
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
		http.Error(w, "Document type not found", http.StatusNotFound)
	default:
		http.Error(w, "Document type name is required", http.StatusBadRequest)
	}
}

// Delete removes one document type.
func (h *DocumentTypeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := requestID(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	results, err := h.services.DocumentType.Delete(ctx, []int64{id})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "delete document type", err)
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
		http.Error(w, "Document type not found", http.StatusNotFound)
	}
}

// CreateDocumentTypeRequest is the body of a document type create.
type CreateDocumentTypeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// UpdateDocumentTypeRequest is the body of a document type update.
type UpdateDocumentTypeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// DocumentTypeResponse is a document type as the API shows it, with the
// documents carrying it.
type DocumentTypeResponse struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	DocumentCount int64  `json:"document_count"`
}
