package handlers

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	itypes "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type DocumentHandler struct {
	client    *database.Client
	logger    *utils.Logger
	engine    *search.Engine
	services  *itypes.CrudServices
	getConfig func() *config.Config
}

func NewDocumentHandler(client *database.Client, logger *utils.Logger, engine *search.Engine, services *itypes.CrudServices, getConfig func() *config.Config) *DocumentHandler {
	return &DocumentHandler{
		client:    client,
		logger:    logger,
		engine:    engine,
		services:  services,
		getConfig: getConfig,
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

	documents, err := h.client.ListDocumentsWithSort(
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

		tags, _ := h.client.GetDocumentTags(r.Context(), doc.ID)
		tagResponses := make([]types.TagResponse, len(tags))
		for j, t := range tags {
			tagResponses[j] = types.TagResponse{
				ID:   t.ID,
				Name: t.Name,
			}
		}

		people, _ := h.client.GetDocumentPeopleWithType(r.Context(), doc.ID)
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

	doc, err := h.client.GetDocumentWithDetails(r.Context(), documentId)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	tags, err := h.client.GetDocumentTags(r.Context(), doc.ID)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get tags for document %d: %v", doc.DocumentID, err)
	}

	people, err := h.client.GetDocumentPeopleWithType(r.Context(), doc.ID)
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

func (h *DocumentHandler) FilterLanguages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Filter languages requested")

	languages, err := h.client.DistinctLanguages(ctx)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get distinct languages: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(languages); err != nil {
		h.logger.Error(&reqID, "Failed to encode languages: %v", err)
	}
}

func (h *DocumentHandler) FilterMimeTypes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Filter MIME types requested")

	mimeTypes, err := h.client.DistinctMimeTypes(ctx)
	if err != nil {
		h.logger.Error(&reqID, "Failed to get distinct MIME types: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(mimeTypes); err != nil {
		h.logger.Error(&reqID, "Failed to encode MIME types: %v", err)
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

		tags, _ := h.client.GetDocumentTags(ctx, r.ID)
		tagResponses := make([]types.TagResponse, len(tags))
		for j, t := range tags {
			tagResponses[j] = types.TagResponse{
				ID:   t.ID,
				Name: t.Name,
			}
		}

		people, _ := h.client.GetDocumentPeopleWithType(ctx, r.ID)
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

	doc, err := h.client.GetDocument(ctx, documentId)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	if doc.MimeType != "application/pdf" {
		http.Error(w, "File type not supported for viewing", http.StatusUnsupportedMediaType)
		return
	}

	safeTitle := sanitizeFilename(doc.Title)
	disposition := "inline"
	if r.URL.Query().Get("download") == "true" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", disposition+`; filename="`+safeTitle+`"`)
	http.ServeFile(w, r, doc.StoragePath)
}

func (h *DocumentHandler) DownloadDocuments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req types.DocumentDownloadRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit on request body

	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		idsJSON := r.FormValue("document_ids")
		if idsJSON == "" {
			http.Error(w, "document_ids is required", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal([]byte(idsJSON), &req.DocumentIDs); err != nil {
			http.Error(w, "Invalid document_ids format", http.StatusBadRequest)
			return
		}
	}

	if len(req.DocumentIDs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "document_ids is required"})
		return
	}

	ids := uniqueStrings(req.DocumentIDs)
	cfg := h.getConfig()

	if len(ids) > cfg.Srv.MaxDownloadFiles {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "too many documents, max: " + strconv.Itoa(cfg.Srv.MaxDownloadFiles),
		})
		return
	}

	var docs []database.Document
	var invalidIDs []string
	var totalSize int64

	for _, id := range ids {
		doc, err := h.client.GetDocument(ctx, id)
		if err != nil {
			if errs.KindOf(errs.FromDB(err, "get document")) == errs.KindNotFound {
				invalidIDs = append(invalidIDs, id)
			} else {
				h.logger.Error(&reqID, "download batch: get document %s: %v", id, err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "internal server error"})
				return
			}
			continue
		}
		docs = append(docs, doc)
		totalSize += doc.FileSize
	}

	if len(invalidIDs) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "documents not found: " + strings.Join(invalidIDs, ", "),
		})
		return
	}

	maxSizeBytes := cfg.Srv.MaxDownloadSizeMB * 1024 * 1024
	if totalSize > maxSizeBytes {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "total size exceeds limit: " + strconv.FormatInt(totalSize, 10) + " > " + strconv.FormatInt(maxSizeBytes, 10),
		})
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="documents.zip"`)

	zw := zip.NewWriter(w)
	var written int

	for _, doc := range docs {
		if doc.StoragePath == "" {
			h.logger.Info(&reqID, "download batch: empty storage path for doc %s, skipping", doc.DocumentID)
			continue
		}
		if _, err := os.Stat(doc.StoragePath); err != nil {
			h.logger.Info(&reqID, "download batch: file missing for doc %s (%s): %v", doc.DocumentID, doc.StoragePath, err)
			continue
		}

		ext := extFromMimeType(doc.MimeType)
		name := sanitizeFilename(doc.Title) + "_" + doc.DocumentID[:8] + ext
		fw, err := zw.Create(name)
		if err != nil {
			h.logger.Error(&reqID, "download batch: create zip entry %s: %v", name, err)
			continue
		}

		fr, err := os.Open(doc.StoragePath)
		if err != nil {
			h.logger.Error(&reqID, "download batch: open file %s: %v", doc.StoragePath, err)
			continue
		}

		if _, err := io.Copy(fw, fr); err != nil {
			fr.Close()
			h.logger.Error(&reqID, "download batch: copy file %s: %v", doc.StoragePath, err)
			continue
		}
		fr.Close()
		written++
	}

	if written == 0 {
		zw.Close()
		h.logger.Error(&reqID, "download batch: no files could be included in ZIP")
		http.Error(w, "none of the requested documents could be downloaded", http.StatusNotFound)
		return
	}

	if err := zw.Close(); err != nil {
		h.logger.Error(&reqID, "download batch: close zip writer: %v", err)
	}
}

func sanitizeFilename(title string) string {
	return strings.NewReplacer("\r\n", "", "\n", "", "\"", "", "/", "_", "\\", "_", "\x00", "").Replace(title)
}

func extFromMimeType(mimeType string) string {
	switch mimeType {
	case "image/tiff":
		return ".tiff"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	default:
		return ".pdf"
	}
}

func uniqueStrings(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	result := make([]string, 0, len(s))
	for _, v := range s {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			result = append(result, v)
		}
	}
	return result
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

	req.Title = utils.StripTags(strings.TrimSpace(req.Title))
	if req.Title == "" {
		http.Error(w, "Title is required", http.StatusBadRequest)
		return
	}

	req.Language = utils.StripTags(strings.TrimSpace(req.Language))
	req.TextContent = utils.StripTagsPtr(req.TextContent)

	if req.DocumentTypeID < 1 {
		http.Error(w, "Invalid document type", http.StatusBadRequest)
		return
	}

	docType, err := h.client.GetDocumentType(ctx, req.DocumentTypeID)
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

	current, err := h.client.GetDocument(ctx, documentID)
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

	err = h.client.UpdateDocumentEditable(ctx, database.UpdateDocumentEditableParams{
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

	doc, err := h.client.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	err = h.client.DeleteDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "delete document", errs.FromDB(err, "delete document"))
		return
	}

	if doc.OriginalPath != "" {
		if err := os.Remove(doc.OriginalPath); err != nil {
			h.logger.Warn(&reqID, "failed to remove original file %s: %v", doc.OriginalPath, err)
		}
	}
	if doc.StoragePath != "" {
		if err := os.Remove(doc.StoragePath); err != nil {
			h.logger.Warn(&reqID, "failed to remove storage file %s: %v", doc.StoragePath, err)
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

	doc, err := h.client.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	_, err = h.services.Tag.Get(ctx, req.TagID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get tag", err)
		return
	}

	err = h.client.AddDocumentTag(ctx, database.AddDocumentTagParams{
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

	doc, err := h.client.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	err = h.client.RemoveDocumentTag(ctx, database.RemoveDocumentTagParams{
		DocumentID: doc.ID,
		TagID:      req.TagID,
	})
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "remove document tag", errs.FromDB(err, "remove document tag"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DocumentHandler) BatchDeleteDocuments(w http.ResponseWriter, r *http.Request) {
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
		doc, err := h.client.GetDocument(ctx, id)
		if err != nil {
			if errs.KindOf(errs.FromDB(err, "get document")) == errs.KindNotFound {
				result.Failed = append(result.Failed, types.BatchDeleteError{ID: id, Error: "not found"})
			} else {
				h.logger.Error(&reqID, "batch delete: get document %s: %v", id, err)
				result.Failed = append(result.Failed, types.BatchDeleteError{ID: id, Error: "internal error"})
			}
			continue
		}

		if err := h.client.DeleteDocument(ctx, id); err != nil {
			h.logger.Error(&reqID, "batch delete: delete document %s: %v", id, err)
			result.Failed = append(result.Failed, types.BatchDeleteError{ID: id, Error: "delete failed"})
			continue
		}

		if doc.OriginalPath != "" {
			if err := os.Remove(doc.OriginalPath); err != nil {
				h.logger.Warn(&reqID, "failed to remove original file %s: %v", doc.OriginalPath, err)
			}
		}
		if doc.StoragePath != "" {
			if err := os.Remove(doc.StoragePath); err != nil {
				h.logger.Warn(&reqID, "failed to remove storage file %s: %v", doc.StoragePath, err)
			}
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

func (h *DocumentHandler) BatchAssignTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req types.BatchTagRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.DocumentIDs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "document_ids is required"})
		return
	}

	if len(req.TagIDs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "tag_ids is required"})
		return
	}

	if req.Mode != "add" && req.Mode != "replace" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "mode must be 'add' or 'replace'"})
		return
	}

	for _, tagID := range req.TagIDs {
		if _, err := h.services.Tag.Get(ctx, tagID); err != nil {
			writeServiceError(w, h.logger, &reqID, "validate tag", err)
			return
		}
	}

	ids := uniqueStrings(req.DocumentIDs)
	result := types.BatchTagResult{}

	for _, id := range ids {
		doc, err := h.client.GetDocument(ctx, id)
		if err != nil {
			if errs.KindOf(errs.FromDB(err, "get document")) == errs.KindNotFound {
				result.Failed = append(result.Failed, types.BatchTagError{ID: id, Error: "not found"})
			} else {
				h.logger.Error(&reqID, "batch tag: get document %s: %v", id, err)
				result.Failed = append(result.Failed, types.BatchTagError{ID: id, Error: "internal error"})
			}
			continue
		}

		failed := false

		if req.Mode == "replace" {
			tx, err := 	h.client.BeginTx(ctx, nil)
			if err != nil {
				h.logger.Error(&reqID, "batch tag: begin tx for doc %s: %v", id, err)
				result.Failed = append(result.Failed, types.BatchTagError{ID: id, Error: "internal error"})
				continue
			}

			tq := h.client.WithTx(tx)

			if err := tq.ClearDocumentTags(ctx, doc.ID); err != nil {
				tx.Rollback()
				h.logger.Error(&reqID, "batch tag: clear tags for doc %s: %v", id, err)
				result.Failed = append(result.Failed, types.BatchTagError{ID: id, Error: "failed to clear tags"})
				failed = true
			}

			if !failed {
				for _, tagID := range req.TagIDs {
					if err := tq.AddDocumentTag(ctx, database.AddDocumentTagParams{
						DocumentID: doc.ID,
						TagID:      tagID,
					}); err != nil {
						tx.Rollback()
						h.logger.Error(&reqID, "batch tag: add tag %d to doc %s: %v", tagID, id, err)
						result.Failed = append(result.Failed, types.BatchTagError{ID: id, Error: "failed to add tag"})
						failed = true
						break
					}
				}
			}

			if !failed {
				if err := tx.Commit(); err != nil {
					h.logger.Error(&reqID, "batch tag: commit tx for doc %s: %v", id, err)
					result.Failed = append(result.Failed, types.BatchTagError{ID: id, Error: "internal error"})
					failed = true
				}
			}
		} else {
			for _, tagID := range req.TagIDs {
				if err := h.client.AddDocumentTag(ctx, database.AddDocumentTagParams{
					DocumentID: doc.ID,
					TagID:      tagID,
				}); err != nil {
					h.logger.Error(&reqID, "batch tag: add tag %d to doc %s: %v", tagID, id, err)
					result.Failed = append(result.Failed, types.BatchTagError{ID: id, Error: "failed to add tag"})
					failed = true
					break
				}
			}
		}

		if !failed {
			result.Assigned++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if result.Assigned == 0 && len(result.Failed) > 0 {
		w.WriteHeader(http.StatusBadRequest)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	json.NewEncoder(w).Encode(result)
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

	doc, err := h.client.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	_, err = h.client.GetPeople(ctx, req.PeopleID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get person", errs.FromDB(err, "get person"))
		return
	}

	_, err = h.client.GetPeopleType(ctx, req.PeopleTypeID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get people type", errs.FromDB(err, "get people type"))
		return
	}

	err = h.client.AddDocumentPeople(ctx, database.AddDocumentPeopleParams{
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

	doc, err := h.client.GetDocument(ctx, documentID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get document", errs.FromDB(err, "get document"))
		return
	}

	err = h.client.RemoveDocumentPeople(ctx, database.RemoveDocumentPeopleParams{
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
