package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type DocumentHandler struct {
	queries *database.Queries
	logger  *utils.Logger
}

func NewDocumentHandler(queries *database.Queries, logger *utils.Logger) *DocumentHandler {
	return &DocumentHandler{
		queries: queries,
		logger:  logger,
	}
}

func (h *DocumentHandler) ListDocuments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	h.logger.Debug(&reqID, "List documents requested")

	pb := utils.GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 0)

	documents, err := h.queries.ListDocuments(
		r.Context(),
		database.ListDocumentsParams{Limit: limit, Offset: offset},
	)
	if err != nil {
		h.logger.Error(&reqID, "Failed to list documents: %w", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]types.DocumentResponse, len(documents))
	for i, doc := range documents {
		response[i] = types.DocumentResponse{
			ID:             doc.ID,
			Title:          doc.Title,
			MD5Checksum:    doc.Md5Checksum,
			SHA512Checksum: doc.Sha512Checksum,
			MimeType:       doc.MimeType,
			FileSize:       doc.FileSize,
			CreatedAt:      doc.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			ModifiedAt:     doc.ModifiedAt.Time.Format("2006-01-02T15:04:05Z"),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error(&reqID, "Failed to encode documents response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (h *DocumentHandler) GetDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Get document requested")

	idStr := r.PathValue("id")
	if idStr == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid document ID", http.StatusBadRequest)
		return
	}

	doc, err := h.queries.GetDocument(r.Context(), id)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get document %d: %v", id, err)
		http.Error(w, "Document not found", http.StatusNotFound)
		return
	}

	response := types.DocumentResponse{
		ID:             doc.ID,
		Title:          doc.Title,
		MD5Checksum:    doc.Md5Checksum,
		SHA512Checksum: doc.Sha512Checksum,
		MimeType:       doc.MimeType,
		FileSize:       doc.FileSize,
		CreatedAt:      doc.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		ModifiedAt:     doc.ModifiedAt.Time.Format("2006-01-02T15:04:05Z"),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error(&reqID, "Failed to encode document response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
