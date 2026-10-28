package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// ConsumeHandler is the intake API: consuming the consumption directory
// and uploading files into it.
type ConsumeHandler struct {
	getConfig func() *config.Config
	logger    *log.Logger
	enqueuer  *task.Enqueuer
	queries   *database.Queries
	services  *service.CrudServices
}

// NewConsumeHandler wires the intake handler to the dynamic config, the transactional enqueuer, the
// queries and the services.
func NewConsumeHandler(getConfig func() *config.Config, logger *log.Logger, enqueuer *task.Enqueuer, queries *database.Queries, services *service.CrudServices) *ConsumeHandler {
	return &ConsumeHandler{
		getConfig: getConfig,
		logger:    logger,
		enqueuer:  enqueuer,
		queries:   queries,
		services:  services,
	}
}

// enqueueBatchFiles enqueues one batch for the accepted paths, with the consume task plus its waiting
// enrich and thumbnail children per file. It returns how many files were actually queued, since files
// skipped as existing documents, or whose consume task lost a dedup conflict, do not count.
func (h *ConsumeHandler) enqueueBatchFiles(ctx context.Context, spec task.BatchSpec, paths []string, reqID string) (result task.EnqueueResult, enqueued int, err error) {
	cfg := h.getConfig()
	thumbnailsEnabled := cfg.Consumer.Thumbnail.Enabled

	if cfg.Enricher.TagMeasure.AfterEnrichmentBatch {
		spec, err = task.WithFollowUp(spec, curation.MeasureSpecs())
		if err != nil {
			return task.EnqueueResult{}, 0, err
		}
	}

	var tasks []task.TaskSpec
	consumeTaskIDs := make(map[string]struct{}, len(paths))
	for i, path := range paths {
		md5hash, md5Err := storage.CalculateMD5(path)
		if md5Err != nil {
			h.logger.Error(reqID, "md5 %s: %v", path, md5Err)
			continue
		}

		existingDocs, _ := h.queries.GetDocumentByMD5Checksum(ctx, md5hash)
		if len(existingDocs) > 0 {
			h.logger.Info(reqID, "skipping %s (duplicate of document %s)", path, existingDocs[0].DocumentID)
			continue
		}

		fileTasks := consumption.IngestTaskSpecs(consumption.IngestFile{
			Path:       path,
			Index:      i + 1,
			MD5:        md5hash,
			DocumentID: uuid.New().String(),
		}, thumbnailsEnabled)
		consumeTaskIDs[fileTasks[0].TaskID] = struct{}{}
		tasks = append(tasks, fileTasks...)
	}

	result, err = h.enqueuer.EnqueueBatch(ctx, spec, tasks)
	if err != nil {
		return task.EnqueueResult{}, 0, err
	}
	for _, taskID := range result.TaskIDs {
		if _, ok := consumeTaskIDs[taskID]; ok {
			enqueued++
		}
	}
	return result, enqueued, nil
}

// Consume scans the consumption directory and enqueues one batch: one consume task per new file with
// its waiting enrich (and thumbnail) children, deduplicating by MD5 content. Unprocessable when the
// required tools are missing; an empty inbox reports no batch.
func (h *ConsumeHandler) Consume(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)
	h.logger.Debug(reqID, "Consume requested")

	cfg := h.getConfig()

	if missing := config.MissingExternalToolErrors(cfg); len(missing) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":         "Cannot consume: required external tools are not installed.",
			"missing_tools": missing,
		})
		return
	}

	paths, err := storage.ListFilePaths(
		cfg.Storage.ConsumptionDir,
		cfg.Consumer.SupportedFiles,
		cfg.Consumer.MaxFilesPerBatch,
	)
	if err != nil {
		h.logger.Error(reqID, "Failed to scan inbox: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if len(paths) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"batch_id":    nil,
			"total_files": 0,
			"message":     "no files found",
		})
		return
	}

	spec, err := task.NewBatchSpec(types.Batch.Origin.API, "")
	if err != nil {
		h.logger.Error(reqID, "build batch spec: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	result, enqueued, err := h.enqueueBatchFiles(ctx, spec, paths, reqID)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "enqueue consume batch", err)
		return
	}

	if result.BatchID == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "no files could be enqueued",
		})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"batch_id":    result.BatchID,
		"total_files": len(paths),
		"enqueued":    enqueued,
		"_links": map[string]string{
			"tasks": "/api/v1/tasks?batch=" + result.BatchID,
		},
	})

	h.logger.Info(reqID, "enqueued batch %s (%d files, %d enqueued)", result.BatchID, len(paths), enqueued)
}

// Upload writes an uploaded file into the consumption directory and
// returns its served name.
func (h *ConsumeHandler) Upload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)
	h.logger.Debug(reqID, "Upload requested")

	cfg := h.getConfig()

	if missing := config.MissingExternalToolErrors(cfg); len(missing) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":         "Cannot consume: required external tools are not installed.",
			"missing_tools": missing,
		})
		return
	}

	maxBytes := cfg.Srv.MaxUploadSize * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "Invalid multipart request", http.StatusBadRequest)
		return
	}

	inboxDir := cfg.Storage.ConsumptionDir

	supportedExts := make(map[string]bool, len(cfg.Consumer.SupportedFiles))
	for _, ext := range cfg.Consumer.SupportedFiles {
		supportedExts[strings.ToLower(ext)] = true
	}

	const maxParts = 1000

	var temps []string
	var acceptedPaths []string
	defer func() {
		for _, p := range temps {
			_ = os.Remove(p)
		}
		for _, p := range acceptedPaths {
			_ = os.Remove(p)
		}
	}()

	var accepted []string
	var rejected []map[string]any

	partsSeen := 0
	for {
		if partsSeen >= maxParts {
			break
		}
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
					"error": fmt.Sprintf("upload exceeds max_upload_size (%d MB)", cfg.Srv.MaxUploadSize),
				})
				return
			}
			http.Error(w, "Error reading multipart", http.StatusBadRequest)
			return
		}
		partsSeen++

		sanitizedName := filepath.Base(part.FileName())
		if sanitizedName == "" || sanitizedName == "." || sanitizedName == ".." {
			rejected = append(rejected, map[string]any{"name": part.FileName(), "reason": "invalid filename"})
			_ = part.Close()
			continue
		}

		tmpFile, err := os.CreateTemp(inboxDir, "upload-*")
		if err != nil {
			h.logger.Error(reqID, "create temp file: %v", err)
			_ = part.Close()
			continue
		}
		tmpPath := tmpFile.Name()
		temps = append(temps, tmpPath)

		_, copyErr := io.Copy(tmpFile, part)
		if closeErr := tmpFile.Close(); copyErr == nil && closeErr != nil {
			copyErr = closeErr
		}
		_ = part.Close()

		if copyErr != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](copyErr); ok {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
					"error": fmt.Sprintf("upload exceeds max_upload_size (%d MB)", cfg.Srv.MaxUploadSize),
				})
				return
			}
			h.logger.Error(reqID, "copy part: %v", copyErr)
			continue
		}

		mtype, err := mimetype.DetectFile(tmpPath)
		if err != nil {
			rejected = append(rejected, map[string]any{"name": sanitizedName, "reason": "could not detect file type"})
			continue
		}
		ext := strings.ToLower(mtype.Extension())
		if !supportedExts[ext] {
			rejected = append(rejected, map[string]any{"name": sanitizedName, "reason": fmt.Sprintf("unsupported type: %s", ext)})
			continue
		}

		finalName := fmt.Sprintf("upload-%s-%s", uuid.New().String(), sanitizedName)
		finalPath := filepath.Join(inboxDir, finalName)
		if err := os.Rename(tmpPath, finalPath); err != nil {
			h.logger.Error(reqID, "rename %s -> %s: %v", tmpPath, finalPath, err)
			continue
		}
		accepted = append(accepted, finalPath)
		acceptedPaths = append(acceptedPaths, finalPath)
	}

	if max := cfg.Consumer.MaxFilesPerBatch; max > 0 && len(accepted) > max {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":    fmt.Sprintf("too many files — max %d per batch", max),
			"accepted": len(accepted),
			"rejected": rejected,
		})
		return
	}

	if len(accepted) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":    "no supported files",
			"rejected": rejected,
		})
		return
	}

	spec, err := task.NewBatchSpec(types.Batch.Origin.API, "")
	if err != nil {
		h.logger.Error(reqID, "build batch spec: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	result, enqueued, err := h.enqueueBatchFiles(ctx, spec, accepted, reqID)
	if err != nil {
		h.logger.Error(reqID, "enqueue upload batch: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	acceptedPaths = nil

	if result.BatchID == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "no files could be enqueued",
		})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"batch_id": result.BatchID,
		"accepted": len(accepted),
		"rejected": rejected,
		"_links": map[string]string{
			"tasks": "/api/v1/tasks?batch=" + result.BatchID,
		},
	})

	h.logger.Info(reqID, "upload batch %s: %d accepted, %d enqueued, %d rejected", result.BatchID, len(accepted), enqueued, len(rejected))
}
