package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	itypes "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/documenttypes"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type DocumentTypeHandler struct {
	services *itypes.CrudServices
	logger   *utils.Logger
}

func NewDocumentTypeHandler(services *itypes.CrudServices, logger *utils.Logger) *DocumentTypeHandler {
	return &DocumentTypeHandler{services: services, logger: logger}
}

func (h *DocumentTypeHandler) List(w http.ResponseWriter, r *http.Request) {
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

	var result []types.DocumentTypeResponse

	if q != "" {
		dts, err := h.services.DocumentType.Search(ctx, q, limit)
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "list document types", err)
			return
		}
		result = make([]types.DocumentTypeResponse, len(dts))
		for i, dt := range dts {
			result[i] = types.DocumentTypeResponse{ID: dt.ID, Name: dt.Name, Description: dt.Description}
		}
	} else {
		dts, err := h.services.DocumentType.List(ctx, limit, offset)
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "list document types", err)
			return
		}
		result = make([]types.DocumentTypeResponse, len(dts))
		for i, dt := range dts {
			result[i] = types.DocumentTypeResponse{ID: dt.ID, Name: dt.Name, Description: dt.Description}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *DocumentTypeHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req types.CreateDocumentTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "Document type name is required", http.StatusBadRequest)
		return
	}

	results, err := h.services.DocumentType.Create(ctx, []documenttypes.CreateDocumentTypeInput{
		{Name: req.Name, Description: req.Description},
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "create document type", err)
		return
	}

	switch results[0].Status {
	case documenttypes.Created:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(types.DocumentTypeResponse{
			ID: results[0].DocumentType.ID, Name: results[0].DocumentType.Name, Description: results[0].DocumentType.Description,
		})
	case documenttypes.Conflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"id":          results[0].DocumentType.ID,
			"name":        results[0].DocumentType.Name,
			"description": results[0].DocumentType.Description,
		})
	default:
		http.Error(w, "Document type name is required", http.StatusBadRequest)
	}
}

func (h *DocumentTypeHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req types.UpdateDocumentTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "Document type name is required", http.StatusBadRequest)
		return
	}

	results, err := h.services.DocumentType.Update(ctx, []documenttypes.UpdatePair{
		{ID: id, Name: req.Name, Description: req.Description},
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "update document type", err)
		return
	}

	switch results[0].Status {
	case documenttypes.Updated, documenttypes.Noop:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(types.DocumentTypeResponse{
			ID: results[0].DocumentType.ID, Name: results[0].DocumentType.Name, Description: results[0].DocumentType.Description,
		})
	case documenttypes.UpdateConflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"id":          results[0].DocumentType.ID,
			"name":        results[0].DocumentType.Name,
			"description": results[0].DocumentType.Description,
		})
	case documenttypes.UpdateNotFound:
		http.Error(w, "Document type not found", http.StatusNotFound)
	default:
		http.Error(w, "Document type name is required", http.StatusBadRequest)
	}
}

func (h *DocumentTypeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

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
	case documenttypes.Deleted:
		w.WriteHeader(http.StatusNoContent)
	case documenttypes.DeleteConflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "in use"})
	default:
		http.Error(w, "Document type not found", http.StatusNotFound)
	}
}
