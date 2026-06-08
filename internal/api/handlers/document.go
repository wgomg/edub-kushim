package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type DocumentHandler struct {
	queries *database.Queries
	logger  *utils.Logger
	engine  *search.Engine
}

func NewDocumentHandler(queries *database.Queries, logger *utils.Logger, engine *search.Engine) *DocumentHandler {
	return &DocumentHandler{
		queries: queries,
		logger:  logger,
		engine:  engine,
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
	sortBy := pb.Get("sort_by", "created_at")
	sortOrder := pb.Get("sort_order", "desc")

	documents, err := h.queries.ListDocumentsWithSort(
		r.Context(),
		database.ListDocumentsWithSortParams{
			Limit:     limit,
			Offset:    offset,
			SortBy:    sortBy,
			SortOrder: sortOrder,
		},
	)
	if err != nil {
		h.logger.Error(&reqID, "Failed to list documents: %w", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]types.DocumentResponse, len(documents))
	for i, doc := range documents {
		docTypeID := doc.DocumentTypeID

		response[i] = types.DocumentResponse{
			ID:             doc.ID,
			Title:          doc.Title,
			MD5Checksum:    doc.Md5Checksum,
			SHA512Checksum: doc.Sha512Checksum,
			MimeType:       doc.MimeType,
			FileSize:       doc.FileSize,
			Language:       doc.Language,
			DocumentTypeID: &docTypeID,
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

	doc, err := h.queries.GetDocumentWithDetails(r.Context(), id)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get document %d: %v", id, err)
		http.Error(w, "Document not found", http.StatusNotFound)
		return
	}

	tags, err := h.queries.GetDocumentTags(r.Context(), id)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get tags for document %d: %v", id, err)
	}

	people, err := h.queries.GetDocumentPeopleWithType(r.Context(), id)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get people for document %d: %v", id, err)
	}

	docTypeID := doc.DocumentTypeID

	var docTypeName *string
	if doc.DocumentTypeName.Valid {
		docTypeName = &doc.DocumentTypeName.String
	}

	tagResponses := make([]types.TagResponse, len(tags))
	for i, t := range tags {
		tagResponses[i] = types.TagResponse{
			ID:   t.ID,
			Name: t.Name,
		}
	}

	personResponses := make([]types.PersonResponse, len(people))
	for i, p := range people {
		personResponses[i] = types.PersonResponse{
			ID:                    p.ID,
			Name:                  p.Name,
			PersonTypeID:          p.PeopleTypeID,
			PersonTypeName:        p.PeopleTypeName,
			PersonTypeDescription: p.PeopleTypeDescription,
		}
	}

	response := types.DocumentResponse{
		ID:               doc.ID,
		Title:            doc.Title,
		MD5Checksum:      doc.Md5Checksum,
		SHA512Checksum:   doc.Sha512Checksum,
		MimeType:         doc.MimeType,
		FileSize:         doc.FileSize,
		Language:         doc.Language,
		DocumentTypeID:   &docTypeID,
		DocumentTypeName: docTypeName,
		Tags:             tagResponses,
		People:           personResponses,
		CreatedAt:        doc.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		ModifiedAt:       doc.ModifiedAt.Time.Format("2006-01-02T15:04:05Z"),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error(&reqID, "Failed to encode document response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (h *DocumentHandler) SearchDocuments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Search documents requested")

	pb := utils.GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	q := pb.Get("q", "")
	if q == "" {
		http.Error(w, "Query parameter 'q' is required", http.StatusBadRequest)
		return
	}

	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 0)

	results, err := h.engine.Search(ctx, q, int32(limit), int32(offset))
	if err != nil {
		h.logger.Error(&reqID, "Search failed: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]types.FTSDocumentResponse, len(results))
	for i, r := range results {
		docTypeID := r.DocumentTypeID

		response[i] = types.FTSDocumentResponse{
			ID:             r.DocumentID,
			Title:          r.Title,
			MD5Checksum:    r.MD5Checksum,
			SHA512Checksum: r.SHA512Checksum,
			MimeType:       r.MimeType,
			FileSize:       r.FileSize,
			Language:       r.Language,
			DocumentTypeID: &docTypeID,
			CreatedAt:      r.CreatedAt.Format("2006-01-02T15:04:05Z"),
			ModifiedAt:     r.ModifiedAt.Format("2006-01-02T15:04:05Z"),
			Rank:           r.Rank,
			Snippet:        r.Snippet,
			TextContent:    "",
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error(&reqID, "Failed to encode search response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (h *DocumentHandler) GetDocumentFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

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

	doc, err := h.queries.GetDocument(ctx, id)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get document %d: %v", id, err)
		http.Error(w, "Document not found", http.StatusNotFound)
		return
	}

	if doc.MimeType != "application/pdf" {
		http.Error(w, "File type not supported for viewing", http.StatusUnsupportedMediaType)
		return
	}

	w.Header().Set("Content-Disposition", `inline; filename="`+doc.Title+`"`)
	http.ServeFile(w, r, doc.StoragePath)
}
