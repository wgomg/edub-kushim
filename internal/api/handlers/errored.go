package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type ErroredHandler struct {
	svc    *service.ErroredFiles
	logger *utils.Logger
}

func NewErroredHandler(svc *service.ErroredFiles, logger *utils.Logger) *ErroredHandler {
	return &ErroredHandler{svc: svc, logger: logger}
}

func mapErroredFile(f service.ErroredFileInfo) map[string]any {
	return map[string]any{
		"name":        f.Name,
		"subdir":      f.Subdir,
		"size":        f.Size,
		"mime_type":   f.MimeType,
		"modified_at": f.ModifiedAt,
	}
}

func (h *ErroredHandler) ListErrored(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	files, err := h.svc.List(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list errored files", err)
		return
	}

	result := make([]map[string]any, len(files))
	for i, f := range files {
		result[i] = mapErroredFile(f)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *ErroredHandler) DownloadErrored(w http.ResponseWriter, r *http.Request) {
	reqID := r.Context().Value("reqid").(string)

	subdir := r.URL.Query().Get("subdir")
	filename := r.URL.Query().Get("file")

	if subdir == "" || filename == "" {
		http.Error(w, "subdir and file query params required", http.StatusBadRequest)
		return
	}

	path, err := h.svc.GetPath(subdir, filename)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "get errored file path", fmt.Errorf("invalid path: %w", err))
		return
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		h.logger.Error(&reqID, "open errored file %s: %v", path, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		h.logger.Error(&reqID, "stat errored file %s: %v", path, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	http.ServeContent(w, r, filename, stat.ModTime(), file)
}

func (h *ErroredHandler) DeleteErrored(w http.ResponseWriter, r *http.Request) {
	reqID := r.Context().Value("reqid").(string)

	subdir := r.URL.Query().Get("subdir")
	filename := r.URL.Query().Get("file")

	if subdir == "" || filename == "" {
		http.Error(w, "subdir and file query params required", http.StatusBadRequest)
		return
	}

	if err := h.svc.Delete(subdir, filename); err != nil {
		writeServiceError(w, h.logger, &reqID, "delete errored file", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ErroredHandler) DeleteAllErrored(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	count, err := h.svc.DeleteAll(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "delete all errored files", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"deleted": count,
	})
}
