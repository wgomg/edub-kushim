package handlers

import (
	"encoding/json"
	"net/http"

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

		tags, _ := h.queries.GetDocumentTags(r.Context(), doc.ID)
		tagResponses := make([]types.TagResponse, len(tags))
		for j, t := range tags {
			tagResponses[j] = types.TagResponse{
				ID:   t.ID,
				Name: t.Name,
			}
		}

		people, _ := h.queries.GetDocumentPeopleWithType(r.Context(), doc.ID)
		personResponses := make([]types.PersonResponse, len(people))
		for j, p := range people {
			personResponses[j] = types.PersonResponse{
				ID:                    p.ID,
				Name:                  p.Name,
				NameNative:            p.NameNative.String,
				PersonTypeID:          p.PeopleTypeID,
				PersonTypeName:        p.PeopleTypeName,
				PersonTypeDescription: p.PeopleTypeDescription,
			}
		}

		response[i] = types.DocumentResponse{
			ID:             doc.DocumentID,
			Title:          doc.Title,
			MD5Checksum:    doc.Md5Checksum,
			SHA512Checksum: doc.Sha512Checksum,
			MimeType:       doc.MimeType,
			FileSize:       doc.FileSize,
			PageCount:      doc.PageCount,
			WordCount:      doc.WordCount,
			CharCount:      doc.CharCount,
			Language:       doc.Language,
			Tags:           tagResponses,
			People:         personResponses,
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

	documentId := r.PathValue("id")
	if documentId == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	doc, err := h.queries.GetDocumentWithDetails(r.Context(), documentId)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get document %d: %v", documentId, err)
		http.Error(w, "Document not found", http.StatusNotFound)
		return
	}

	tags, err := h.queries.GetDocumentTags(r.Context(), doc.ID)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get tags for document %d: %v", doc.DocumentID, err)
	}

	people, err := h.queries.GetDocumentPeopleWithType(r.Context(), doc.ID)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get people for document %d: %v", doc.DocumentID, err)
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
			NameNative:            p.NameNative.String,
			PersonTypeID:          p.PeopleTypeID,
			PersonTypeName:        p.PeopleTypeName,
			PersonTypeDescription: p.PeopleTypeDescription,
		}
	}

	response := types.DocumentResponse{
		ID:               doc.DocumentID,
		Title:            doc.Title,
		MD5Checksum:      doc.Md5Checksum,
		SHA512Checksum:   doc.Sha512Checksum,
		MimeType:         doc.MimeType,
		FileSize:         doc.FileSize,
		PageCount:        doc.PageCount,
		WordCount:        doc.WordCount,
		CharCount:        doc.CharCount,
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
			PageCount:      r.PageCount,
			WordCount:      r.WordCount,
			CharCount:      r.CharCount,
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

func (h *DocumentHandler) SearchDocumentsStructured(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Structured search requested")

	var filter search.Filter
	if err := json.NewDecoder(r.Body).Decode(&filter); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}

	results, total, err := h.engine.SearchStructured(ctx, filter)
	if err != nil {
		h.logger.Error(&reqID, "Structured search failed: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := make([]types.FTSDocumentResponse, len(results))
	for i, r := range results {
		docTypeID := r.DocumentTypeID

		tags, _ := h.queries.GetDocumentTags(ctx, r.ID)
		tagResponses := make([]types.TagResponse, len(tags))
		for j, t := range tags {
			tagResponses[j] = types.TagResponse{
				ID:   t.ID,
				Name: t.Name,
			}
		}

		people, _ := h.queries.GetDocumentPeopleWithType(ctx, r.ID)
		personResponses := make([]types.PersonResponse, len(people))
		for j, p := range people {
			personResponses[j] = types.PersonResponse{
				ID:                    p.ID,
				Name:                  p.Name,
				NameNative:            p.NameNative.String,
				PersonTypeID:          p.PeopleTypeID,
				PersonTypeName:        p.PeopleTypeName,
				PersonTypeDescription: p.PeopleTypeDescription,
			}
		}

		response[i] = types.FTSDocumentResponse{
			ID:             r.DocumentID,
			Title:          r.Title,
			MD5Checksum:    r.MD5Checksum,
			SHA512Checksum: r.SHA512Checksum,
			MimeType:       r.MimeType,
			FileSize:       r.FileSize,
			PageCount:      r.PageCount,
			WordCount:      r.WordCount,
			CharCount:      r.CharCount,
			Language:       r.Language,
			DocumentTypeID: &docTypeID,
			Tags:           tagResponses,
			People:         personResponses,
			CreatedAt:      r.CreatedAt.Format("2006-01-02T15:04:05Z"),
			ModifiedAt:     r.ModifiedAt.Format("2006-01-02T15:04:05Z"),
			Rank:           r.Rank,
			Snippet:        r.Snippet,
			TextContent:    "",
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	searchResponse := types.SearchResponse{
		Results: response,
		Total:   total,
	}

	if err := json.NewEncoder(w).Encode(searchResponse); err != nil {
		h.logger.Error(&reqID, "Failed to encode search response: %v", err)
	}
}

func (h *DocumentHandler) GetDocumentFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	documentId := r.PathValue("id")
	if documentId == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	doc, err := h.queries.GetDocument(ctx, documentId)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get document %d: %v", documentId, err)
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
