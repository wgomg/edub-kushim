package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	itypes "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type ConsumeHandler struct {
	getConfig func() *config.Config
	logger    *utils.Logger
	workStore *task.Store
	queries   *database.Queries
	semaphore *pool.Semaphore
	services  *itypes.CrudServices
}

func NewConsumeHandler(getConfig func() *config.Config, logger *utils.Logger, workStore *task.Store, queries *database.Queries, semaphore *pool.Semaphore, services *itypes.CrudServices) *ConsumeHandler {
	return &ConsumeHandler{
		getConfig: getConfig,
		logger:    logger,
		workStore: workStore,
		queries:   queries,
		semaphore: semaphore,
		services:  services,
	}
}

func (h *ConsumeHandler) enqueueBatchFiles(ctx context.Context, batchID string, paths []string, reqID string) (enqueued int) {
	for i, path := range paths {
		md5hash, md5Err := utils.CalculateMD5(path)
		if md5Err != nil {
			h.logger.Error(&reqID, "md5 %s: %v", path, md5Err)
			continue
		}

		existingDocs, _ := h.queries.GetDocumentByMD5Checksum(ctx, md5hash)
		if len(existingDocs) > 0 {
			h.logger.Info(&reqID, "skipping %s (duplicate of document %s)", path, existingDocs[0].DocumentID)
			continue
		}

		consumeTaskID := uuid.New().String()
		enrichTaskID := uuid.New().String()
		documentID := uuid.New().String()

		consumePayload, _ := json.Marshal(map[string]any{
			"file_path":    path,
			"file_index":   i + 1,
			"on_completed": enrichTaskID,
			"document_id":  documentID,
		})
		_, err := h.workStore.CreateTask(ctx, "consume", batchID, consumePayload, consumeTaskID, "pending", "consume:"+md5hash)
		if err != nil {
			h.logger.Error(&reqID, "enqueue %s: %v", path, err)
			continue
		}

		enrichPayload, _ := json.Marshal(map[string]any{
			"waiting_for": consumeTaskID,
			"file_name":   filepath.Base(path),
			"file_index":  i + 1,
			"document_id": documentID,
		})
		if _, err := h.workStore.CreateTask(ctx, "enrich", batchID, enrichPayload, enrichTaskID, "waiting", ""); err != nil {
			h.logger.Error(&reqID, "create enrich task for %s: %v", path, err)
			continue
		}
		enqueued++
	}
	return enqueued
}

func (h *ConsumeHandler) forkWorker(batchID string) error {
	kushimPath, err := utils.KushimBinaryPath()
	if err != nil {
		return err
	}

	cmd := exec.Command(kushimPath, "consume", "--batch", batchID)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork worker: %w", err)
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			h.logger.Error(nil, "worker for batch %s exited: %v", batchID, err)
		} else {
			h.logger.Info(nil, "worker for batch %s completed", batchID)
		}
		h.semaphore.Release()
	}()

	return nil
}

func (h *ConsumeHandler) Consume(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Consume requested")

	cfg := h.getConfig()

	if missing := config.MissingExternalToolErrors(cfg); len(missing) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{
			"error":         "Cannot consume: required external tools are not installed.",
			"missing_tools": missing,
		})
		return
	}

	if !h.semaphore.Acquire() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": "too many concurrent batches — try again later",
		})
		return
	}

	paths, err := utils.ListFilePaths(
		cfg.Storage.ConsumptionDir,
		cfg.Consumer.SupportedFiles,
		cfg.Consumer.MaxFilesPerBatch,
	)
	if err != nil {
		h.semaphore.Release()
		h.logger.Error(&reqID, "Failed to scan inbox: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if len(paths) == 0 {
		h.semaphore.Release()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"batch_id":    nil,
			"total_files": 0,
			"message":     "no files found",
		})
		return
	}

	batchID := uuid.New().String()

	h.services.Batch.Create(ctx, batchID, "api", "queued")

	enqueued := h.enqueueBatchFiles(ctx, batchID, paths, reqID)

	if enqueued == 0 {
		h.semaphore.Release()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": "no files could be enqueued",
		})
		return
	}

	if err := h.forkWorker(batchID); err != nil {
		h.semaphore.Release()
		h.logger.Error(&reqID, "fork worker for batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"batch_id":    batchID,
		"total_files": len(paths),
		"enqueued":    enqueued,
		"_links": map[string]string{
			"tasks": "/api/v1/tasks?batch=" + batchID,
		},
	})

	h.logger.Info(&reqID, "forked worker for batch %s (%d files, %d enqueued)", batchID, len(paths), enqueued)
}

func (h *ConsumeHandler) Upload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Upload requested")

	cfg := h.getConfig()

	if missing := config.MissingExternalToolErrors(cfg); len(missing) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{
			"error":         "Cannot consume: required external tools are not installed.",
			"missing_tools": missing,
		})
		return
	}

	if !h.semaphore.Acquire() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": "too many concurrent batches — try again later",
		})
		return
	}

	maxBytes := cfg.Srv.MaxUploadSize * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	mr, err := r.MultipartReader()
	if err != nil {
		h.semaphore.Release()
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
			os.Remove(p)
		}
		for _, p := range acceptedPaths {
			os.Remove(p)
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
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				h.semaphore.Release()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				json.NewEncoder(w).Encode(map[string]any{
					"error": fmt.Sprintf("upload exceeds max_upload_size (%d MB)", cfg.Srv.MaxUploadSize),
				})
				return
			}
			http.Error(w, "Error reading multipart", http.StatusBadRequest)
			h.semaphore.Release()
			return
		}
		partsSeen++

		sanitizedName := filepath.Base(part.FileName())
		if sanitizedName == "" || sanitizedName == "." || sanitizedName == ".." {
			rejected = append(rejected, map[string]any{"name": part.FileName(), "reason": "invalid filename"})
			part.Close()
			continue
		}

		tmpFile, err := os.CreateTemp(inboxDir, "upload-*")
		if err != nil {
			h.logger.Error(&reqID, "create temp file: %v", err)
			part.Close()
			continue
		}
		tmpPath := tmpFile.Name()
		temps = append(temps, tmpPath)

		_, copyErr := io.Copy(tmpFile, part)
		tmpFile.Close()
		part.Close()

		if copyErr != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(copyErr, &maxBytesErr) {
				h.semaphore.Release()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				json.NewEncoder(w).Encode(map[string]any{
					"error": fmt.Sprintf("upload exceeds max_upload_size (%d MB)", cfg.Srv.MaxUploadSize),
				})
				return
			}
			h.logger.Error(&reqID, "copy part: %v", copyErr)
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
			h.logger.Error(&reqID, "rename %s -> %s: %v", tmpPath, finalPath, err)
			continue
		}
		accepted = append(accepted, finalPath)
		acceptedPaths = append(acceptedPaths, finalPath)
	}

	if max := cfg.Consumer.MaxFilesPerBatch; max > 0 && len(accepted) > max {
		h.semaphore.Release()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{
			"error":    fmt.Sprintf("too many files — max %d per batch", max),
			"accepted": len(accepted),
			"rejected": rejected,
		})
		return
	}

	if len(accepted) == 0 {
		h.semaphore.Release()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{
			"error":    "no supported files",
			"rejected": rejected,
		})
		return
	}

	batchID := uuid.New().String()
	if err := h.services.Batch.Create(ctx, batchID, "api-upload", "queued"); err != nil {
		h.semaphore.Release()
		h.logger.Error(&reqID, "create batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	enqueued := h.enqueueBatchFiles(ctx, batchID, accepted, reqID)

	acceptedPaths = nil

	if enqueued == 0 {
		h.semaphore.Release()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": "no files could be enqueued",
		})
		return
	}

	if err := h.forkWorker(batchID); err != nil {
		h.semaphore.Release()
		h.logger.Error(&reqID, "fork worker for batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"batch_id": batchID,
		"accepted": len(accepted),
		"rejected": rejected,
		"_links": map[string]string{
			"tasks": "/api/v1/tasks?batch=" + batchID,
		},
	})

	h.logger.Info(&reqID, "upload batch %s: %d accepted, %d rejected, worker forked", batchID, len(accepted), len(rejected))
}
