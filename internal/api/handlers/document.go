package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	itypes "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/sanitize"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type DocumentHandler struct {
	queries  *database.Queries
	logger   *utils.Logger
	engine   *search.Engine
	services *itypes.CrudServices
}

func NewDocumentHandler(queries *database.Queries, logger *utils.Logger, engine *search.Engine, services *itypes.CrudServices) *DocumentHandler {
	return &DocumentHandler{
		queries:  queries,
		logger:   logger,
		engine:   engine,
		services: services,
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
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
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
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	if doc.MimeType != "application/pdf" {
		http.Error(w, "File type not supported for viewing", http.StatusUnsupportedMediaType)
		return
	}

	safeTitle := strings.NewReplacer("\r\n", "", "\n", "", "\"", "").Replace(doc.Title)
	w.Header().Set("Content-Disposition", `inline; filename="`+safeTitle+`"`)
	http.ServeFile(w, r, doc.StoragePath)
}

func (h *DocumentHandler) UpdateDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	documentID := r.PathValue("id")
	if documentID == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	var req types.DocumentUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Title = sanitize.StripTags(strings.TrimSpace(req.Title))
	if req.Title == "" {
		http.Error(w, "Title is required", http.StatusBadRequest)
		return
	}

	req.Language = sanitize.StripTags(strings.TrimSpace(req.Language))
	req.TextContent = sanitize.StripTagsPtr(req.TextContent)

	if req.DocumentTypeID < 1 {
		http.Error(w, "Invalid document type", http.StatusBadRequest)
		return
	}

	docType, err := h.queries.GetDocumentType(ctx, req.DocumentTypeID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document type", errs.FromDB(err, "get document type"))
		return
	}
	if docType.ID == 0 {
		http.Error(w, "Document type not found", http.StatusNotFound)
		return
	}

	if req.Language == "" {
		req.Language = "und"
	}

	current, err := h.queries.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	var textContent sql.NullString
	if req.TextContent != nil {
		textContent = sql.NullString{String: *req.TextContent, Valid: true}
	} else {
		textContent = current.TextContent
	}

	err = h.queries.UpdateDocumentEditable(ctx, database.UpdateDocumentEditableParams{
		Title:          req.Title,
		DocumentTypeID: req.DocumentTypeID,
		Language:       req.Language,
		TextContent:    textContent,
		DocumentID:     documentID,
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "update document", errs.FromDB(err, "update document"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DocumentHandler) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	documentID := r.PathValue("id")
	if documentID == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	doc, err := h.queries.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	err = h.queries.DeleteDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "delete document", errs.FromDB(err, "delete document"))
		return
	}

	if doc.OriginalPath != "" {
		if err := os.Remove(doc.OriginalPath); err != nil {
			h.logger.Error(&reqID, "Warning: failed to remove original file %s: %v", doc.OriginalPath, err)
		}
	}
	if doc.StoragePath != "" {
		if err := os.Remove(doc.StoragePath); err != nil {
			h.logger.Error(&reqID, "Warning: failed to remove storage file %s: %v", doc.StoragePath, err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DocumentHandler) AddDocumentTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	documentID := r.PathValue("id")
	if documentID == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	var req types.AddDocumentTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	doc, err := h.queries.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	_, err = h.services.Tag.Get(ctx, req.TagID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get tag", err)
		return
	}

	err = h.queries.AddDocumentTag(ctx, database.AddDocumentTagParams{
		DocumentID: doc.ID,
		TagID:      req.TagID,
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "add document tag", errs.FromDB(err, "add document tag"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DocumentHandler) RemoveDocumentTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	documentID := r.PathValue("id")
	if documentID == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	var req types.RemoveDocumentTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	doc, err := h.queries.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	err = h.queries.RemoveDocumentTag(ctx, database.RemoveDocumentTagParams{
		DocumentID: doc.ID,
		TagID:      req.TagID,
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "remove document tag", errs.FromDB(err, "remove document tag"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DocumentHandler) AddDocumentPeople(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	documentID := r.PathValue("id")
	if documentID == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	var req types.AddDocumentPeopleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	doc, err := h.queries.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	_, err = h.queries.GetPeople(ctx, req.PeopleID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get person", errs.FromDB(err, "get person"))
		return
	}

	_, err = h.queries.GetPeopleType(ctx, req.PeopleTypeID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get people type", errs.FromDB(err, "get people type"))
		return
	}

	err = h.queries.AddDocumentPeople(ctx, database.AddDocumentPeopleParams{
		DocumentID:   doc.ID,
		PeopleID:     req.PeopleID,
		PeopleTypeID: req.PeopleTypeID,
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "add document people", errs.FromDB(err, "add document people"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DocumentHandler) RemoveDocumentPeople(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	documentID := r.PathValue("id")
	if documentID == "" {
		http.Error(w, "Document ID is required", http.StatusBadRequest)
		return
	}

	var req types.RemoveDocumentPeopleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	doc, err := h.queries.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	err = h.queries.RemoveDocumentPeople(ctx, database.RemoveDocumentPeopleParams{
		DocumentID:   doc.ID,
		PeopleID:     req.PeopleID,
		PeopleTypeID: req.PeopleTypeID,
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "remove document people", errs.FromDB(err, "remove document people"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
