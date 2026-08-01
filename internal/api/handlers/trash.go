package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TrashHandler struct {
	logger    *utils.Logger
	trashSvc  *service.TrashService
	getConfig func() *config.Config
}

func NewTrashHandler(logger *utils.Logger, trashSvc *service.TrashService, getConfig func() *config.Config) *TrashHandler {
	return &TrashHandler{logger: logger, trashSvc: trashSvc, getConfig: getConfig}
}

func mapTrashDocument(row database.ListTrashDocumentsRow) types.TrashDocumentResponse {
	return types.TrashDocumentResponse{
		ID:           row.ID,
		DocumentID:   row.DocumentID,
		Title:        row.Title,
		OriginalType: row.OriginalType,
		FileSize:     row.FileSize,
		PageCount:    row.PageCount,
		Language:     row.Language,
		DeletedAt:    row.DeletedAt.Time,
		CreatedAt:    row.CreatedAt.Time,
	}
}

func (h *TrashHandler) ListTrash(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	pb := utils.GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 0)

	documents, err := h.trashSvc.ListTrash(ctx, int32(limit), int32(offset))
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list trash documents", err)
		return
	}

	total, err := h.trashSvc.CountTrash(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "count trash documents", err)
		return
	}

	response := make([]types.TrashDocumentResponse, len(documents))
	for i, doc := range documents {
		response[i] = mapTrashDocument(doc)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(types.TrashListResponse{
		Documents: response,
		Total:     total,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
}

func (h *TrashHandler) GetTrashDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	documentID := r.PathValue("id")
	if documentID == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	doc, err := h.trashSvc.GetTrashDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get trash document", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":               doc.ID,
		"document_id":      doc.DocumentID,
		"title":            doc.Title,
		"md5_checksum":     doc.Md5Checksum,
		"sha512_checksum":  doc.Sha512Checksum,
		"original_type":    doc.OriginalType,
		"file_size":        doc.FileSize,
		"page_count":       doc.PageCount,
		"word_count":       doc.WordCount,
		"char_count":       doc.CharCount,
		"language":         doc.Language,
		"document_type_id": doc.DocumentTypeID,
		"created_at":       doc.CreatedAt.Time,
		"modified_at":      doc.ModifiedAt.Time,
		"deleted_at":       doc.DeletedAt.Time,
	})
}

func (h *TrashHandler) RestoreDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	documentID := r.PathValue("id")
	if documentID == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	if err := h.trashSvc.RestoreDocument(ctx, documentID); err != nil {
		writeServiceError(w, h.logger, &reqID, "restore document", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *TrashHandler) PermanentlyDeleteDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	documentID := r.PathValue("id")
	if documentID == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	if err := h.trashSvc.PermanentlyDelete(ctx, documentID); err != nil {
		writeServiceError(w, h.logger, &reqID, "permanently delete document", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *TrashHandler) PurgeExpired(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	count, err := h.trashSvc.PurgeExpired(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "purge expired documents", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"purged": count})
}

func (h *TrashHandler) BatchPermanentlyDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req types.BatchDeleteRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ids := uniqueStrings(req.DocumentIDs)
	cfg := h.getConfig()

	if len(ids) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "document_ids is required"})
		return
	}

	if len(ids) > cfg.Srv.MaxBatchDelete {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "too many documents, max: " + strconv.Itoa(cfg.Srv.MaxBatchDelete),
		})
		return
	}

	result := types.BatchDeleteResult{}
	for _, id := range ids {
		if err := h.trashSvc.PermanentlyDelete(ctx, id); err != nil {
			h.logger.Error(&reqID, "batch permanent delete %s: %v", id, err)
			result.Failed = append(result.Failed, types.BatchDeleteError{ID: id, Error: "delete failed"})
			continue
		}
		result.Deleted++
	}

	w.Header().Set("Content-Type", "application/json")
	if result.Deleted == 0 && len(result.Failed) > 0 {
		w.WriteHeader(http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	json.NewEncoder(w).Encode(result)
}

func (h *TrashHandler) BatchRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req types.BatchDeleteRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	ids := uniqueStrings(req.DocumentIDs)
	cfg := h.getConfig()

	if len(ids) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "document_ids is required"})
		return
	}

	if len(ids) > cfg.Srv.MaxBatchDelete {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "too many documents, max: " + strconv.Itoa(cfg.Srv.MaxBatchDelete),
		})
		return
	}

	result := types.TrashRestoreResponse{}
	for _, id := range ids {
		if err := h.trashSvc.RestoreDocument(ctx, id); err != nil {
			h.logger.Error(&reqID, "batch restore %s: %v", id, err)
			result.Failed = append(result.Failed, types.BatchDeleteError{ID: id, Error: "restore failed"})
			continue
		}
		result.Restored++
	}

	w.Header().Set("Content-Type", "application/json")
	if result.Restored == 0 && len(result.Failed) > 0 {
		w.WriteHeader(http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	json.NewEncoder(w).Encode(result)
}
