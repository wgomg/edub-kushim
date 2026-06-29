package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type OrphanedHandler struct {
	svc    *service.Orphaned
	logger *utils.Logger
}

func NewOrphanedHandler(svc *service.Orphaned, logger *utils.Logger) *OrphanedHandler {
	return &OrphanedHandler{svc: svc, logger: logger}
}

func mapOrphanedFile(f database.OrphanedFile) map[string]any {
	m := map[string]any{
		"id":                f.ID,
		"document_key":      f.DocumentKey,
		"document_key_type": f.DocumentKeyType,
		"file_path":         f.FilePath,
		"original_path":     f.OriginalPath,
		"source_dir":        f.SourceDir,
		"file_size":         f.FileSize,
		"mime_type":         f.MimeType,
		"status":            f.Status,
	}
	if f.DetectedAt.Valid {
		m["detected_at"] = f.DetectedAt.Time
	}
	if f.ActionAt.Valid {
		m["action_at"] = f.ActionAt.Time
	}
	if f.ActionType.Valid {
		m["action_type"] = f.ActionType.String
	}
	return m
}

func (h *OrphanedHandler) ListOrphaned(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	files, err := h.svc.List(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list orphaned files", err)
		return
	}

	result := make([]map[string]any, len(files))
	for i, f := range files {
		result[i] = mapOrphanedFile(f)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *OrphanedHandler) ScanOrphaned(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	count, err := h.svc.ScanAndQuarantine(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "scan orphaned files", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"quarantined": count,
	})
}

func (h *OrphanedHandler) DeleteOrphaned(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	if err := h.svc.Delete(ctx, id); err != nil {
		writeServiceError(w, h.logger, &reqID, "delete orphaned file", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *OrphanedHandler) RestoreOrphaned(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	if err := h.svc.Restore(ctx, id); err != nil {
		writeServiceError(w, h.logger, &reqID, "restore orphaned file", err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *OrphanedHandler) MoveToInbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	if err := h.svc.MoveToInbox(ctx, id); err != nil {
		writeServiceError(w, h.logger, &reqID, "move orphaned to inbox", err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *OrphanedHandler) DeleteAllOrphaned(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	count, err := h.svc.DeleteAll(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "delete all orphaned files", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"deleted": count,
	})
}

func (h *OrphanedHandler) MoveAllToInbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	count, err := h.svc.MoveAllToInbox(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "move all orphaned to inbox", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"moved": count,
	})
}
