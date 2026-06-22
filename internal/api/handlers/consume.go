package handlers

import (
	"context"
	"encoding/json"
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
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type ConsumeHandler struct {
	cfg        *config.Config
	logger     *utils.Logger
	dispatcher *task.Dispatcher
	queries    *database.Queries
	owner      *task.Owner
}

func NewConsumeHandler(cfg *config.Config, logger *utils.Logger, dispatcher *task.Dispatcher, queries *database.Queries, owner *task.Owner) *ConsumeHandler {
	return &ConsumeHandler{
		cfg:        cfg,
		logger:     logger,
		dispatcher: dispatcher,
		queries:    queries,
		owner:      owner,
	}
}

func (h *ConsumeHandler) enqueueBatchFiles(ctx context.Context, batchID string, paths []string, reqID string) (enqueued int) {
	for i, path := range paths {
		consumeTaskID := uuid.New().String()
		enrichTaskID := uuid.New().String()
		documentID := uuid.New().String()

		consumePayload, _ := json.Marshal(map[string]any{
			"file_path":    path,
			"file_index":   i + 1,
			"on_completed": enrichTaskID,
			"document_id":  documentID,
		})
		_, err := h.dispatcher.Enqueue(ctx, "consume", batchID, consumePayload, consumeTaskID)
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
		if _, err := h.dispatcher.Enqueue(ctx, "enrich", batchID, enrichPayload, enrichTaskID, "waiting"); err != nil {
			h.logger.Error(&reqID, "create enrich task for %s: %v", path, err)
			continue
		}
		enqueued++
	}
	return enqueued
}

func (h *ConsumeHandler) Consume(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Consume requested")

	if missing := config.MissingExternalToolErrors(h.cfg); len(missing) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{
			"error":         "Cannot consume: required external tools are not installed.",
			"missing_tools": missing,
		})
		return
	}

	files, err := consumption.GetFiles(
		h.cfg.Storage.ConsumptionDir,
		h.cfg.Consumer.SupportedFiles,
	)
	if err != nil {
		h.logger.Error(&reqID, "Failed to scan inbox: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if len(files) == 0 {
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

	h.queries.CreateBatch(ctx, database.CreateBatchParams{
		ID:     batchID,
		Source: "api",
	})
	if err := h.owner.Acquire(ctx, batchID, task.StaleAfter); err != nil {
		h.logger.Error(&reqID, "acquire batch %s: %v", batchID, err)
	} else {
		h.logger.Info(&reqID, "acquired batch %s (owner %s pid %d)", batchID, h.owner.OwnerID, os.Getpid())
	}

	paths := consumption.FilePaths(files)
	enqueued := h.enqueueBatchFiles(ctx, batchID, paths, reqID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"batch_id":    batchID,
		"total_files": len(files),
		"enqueued":    enqueued,
		"_links": map[string]string{
			"tasks": "/api/v1/tasks?batch=" + batchID,
		},
	})

	h.logger.Info(&reqID, "enqueued %d/%d files (batch %s)", enqueued, len(files), batchID)
}

func (h *ConsumeHandler) Upload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	h.logger.Debug(&reqID, "Upload requested")

	if missing := config.MissingExternalToolErrors(h.cfg); len(missing) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{
			"error":         "Cannot consume: required external tools are not installed.",
			"missing_tools": missing,
		})
		return
	}

	maxBytes := h.cfg.Srv.MaxUploadSize * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "Invalid multipart request", http.StatusBadRequest)
		return
	}

	inboxDir := h.cfg.Storage.ConsumptionDir

	supportedExts := make(map[string]bool, len(h.cfg.Consumer.SupportedFiles))
	for _, ext := range h.cfg.Consumer.SupportedFiles {
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
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				json.NewEncoder(w).Encode(map[string]any{
					"error": fmt.Sprintf("upload exceeds max_upload_size (%d MB)", h.cfg.Srv.MaxUploadSize),
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
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				json.NewEncoder(w).Encode(map[string]any{
					"error": fmt.Sprintf("upload exceeds max_upload_size (%d MB)", h.cfg.Srv.MaxUploadSize),
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

	if len(accepted) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{
			"error":    "no supported files",
			"rejected": rejected,
		})
		return
	}

	batchID := uuid.New().String()
	if _, err := h.queries.CreateBatch(ctx, database.CreateBatchParams{
		ID:     batchID,
		Source: "api-upload",
	}); err != nil {
		h.logger.Error(&reqID, "create batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if err := h.owner.Acquire(ctx, batchID, task.StaleAfter); err != nil {
		h.logger.Error(&reqID, "acquire batch %s: %v", batchID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	h.logger.Info(&reqID, "acquired batch %s (owner %s pid %d)", batchID, h.owner.OwnerID, os.Getpid())

	h.enqueueBatchFiles(ctx, batchID, accepted, reqID)

	acceptedPaths = nil

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

	h.logger.Info(&reqID, "uploaded batch %s: %d accepted, %d rejected", batchID, len(accepted), len(rejected))
}
