package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TaskCreator creates tasks; the task dispatcher satisfies it.
type TaskCreator interface {
	CreateTask(ctx context.Context, taskType types.TaskType, batchID string, payload json.RawMessage,
		taskID string, status types.TaskStatus, dedupKey string) (string, error)
}

// BatchCreator creates batch rows.
type BatchCreator interface {
	Create(ctx context.Context, id string, source types.BatchSource, status types.BatchStatus) error
}

// Orphaned administers the quarantined files the orphaned sweep finds:
// list, quarantine, delete, move back into the consumption directory, and
// re-consume through the task creators.
type Orphaned struct {
	queries      *database.Queries
	cfg          *config.Config
	logger       *log.Logger
	taskCreator  TaskCreator
	batchCreator BatchCreator
}

// NewOrphaned wires the admin to the storage config and the task and
// batch creators.
func NewOrphaned(queries *database.Queries, cfg *config.Config, logger *log.Logger, taskCreator TaskCreator, batchCreator BatchCreator) *Orphaned {
	return &Orphaned{
		queries:      queries,
		cfg:          cfg,
		logger:       logger,
		taskCreator:  taskCreator,
		batchCreator: batchCreator,
	}
}

// List reads the quarantined files from the database view.
func (s *Orphaned) List(ctx context.Context) ([]database.OrphanedFile, error) {
	files, err := s.queries.ListOrphanedFiles(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list orphaned files")
	}
	return files, nil
}

// ScanAndQuarantine walks the storage layout, ignores files whose UUID
// keys name known documents or live rows, and quarantines the rest;
// dbid-named files are never quarantined. It returns how many were
// quarantined.
func (s *Orphaned) ScanAndQuarantine(ctx context.Context) (int, error) {
	storageDir := s.cfg.Storage.StorageDir
	infos, errsCh := storage.WalkStorageDir(storageDir)

	quarantined := 0
	for info := range infos {
		ok, err := s.quarantineFile(ctx, storageDir, info)
		if err != nil {
			s.logger.Error("", "quarantine file %s: %v", info.FilePath, err)
			continue
		}
		if ok {
			quarantined++
		}
	}

	if err, ok := <-errsCh; ok && err != nil {
		return quarantined, fmt.Errorf("walk storage dir: %w", err)
	}

	return quarantined, nil
}

func (s *Orphaned) quarantineFile(ctx context.Context, storageDir string, info storage.OrphanedFileInfo) (bool, error) {
	var docExists bool

	switch info.DocumentKeyType {
	case "uuid":
		_, err := s.queries.GetDocument(ctx, info.DocumentKey)
		if err == nil {
			docExists = true
		} else if errs.KindOf(errs.FromDB(err, "get document")) != errs.KindNotFound {
			return false, err
		}
	case "dbid":
		id, parseErr := strconv.ParseInt(info.DocumentKey, 10, 64)
		if parseErr != nil {
			return false, fmt.Errorf("parse dbid %s: %w", info.DocumentKey, parseErr)
		}
		_, err := s.queries.GetDocumentById(ctx, id)
		if err == nil {
			docExists = true
		} else if errs.KindOf(errs.FromDB(err, "get document by id")) != errs.KindNotFound {
			return false, err
		}
	}

	if docExists {
		return false, nil
	}

	newPath, err := storage.QuarantineFile(storageDir, info)
	if err != nil {
		return false, fmt.Errorf("quarantine file: %w", err)
	}

	_, err = s.queries.CreateOrphanedFile(ctx, database.CreateOrphanedFileParams{
		DocumentKey:     info.DocumentKey,
		DocumentKeyType: info.DocumentKeyType,
		FilePath:        newPath,
		OriginalPath:    info.OriginalPath,
		SourceDir:       info.SourceDir,
		FileSize:        info.FileSize,
		OriginalType:    info.OriginalType,
	})
	if err != nil {
		storage.RemoveOrphanedFile(newPath)
		return false, errs.FromDB(err, "create orphaned file record")
	}

	return true, nil
}

// Delete permanently removes one quarantined file.
func (s *Orphaned) Delete(ctx context.Context, id int64) error {
	row, err := s.queries.GetOrphanedFile(ctx, id)
	if err != nil {
		return errs.FromDB(err, "get orphaned file")
	}

	if err := storage.RemoveOrphanedFile(row.FilePath); err != nil {
		return err
	}

	if err := s.queries.MarkOrphanedFileDeleted(ctx, id); err != nil {
		return errs.FromDB(err, "mark orphaned file deleted")
	}

	return nil
}

// Restore re-consumes one quarantined file through the task creators in
// a fresh batch; only uuid-named files restore, and a name that resolves
// to a live document is refused.
func (s *Orphaned) Restore(ctx context.Context, id int64) error {
	row, err := s.queries.GetOrphanedFile(ctx, id)
	if err != nil {
		return errs.FromDB(err, "get orphaned file")
	}

	if row.DocumentKeyType != "uuid" {
		return errs.EInvalid("restore orphaned", fmt.Errorf("only uuid-named files can be restored"))
	}

	_, err = s.queries.GetDocument(ctx, row.DocumentKey)
	if err == nil {
		return errs.EConflict("restore orphaned", fmt.Errorf("document with id %s already exists", row.DocumentKey))
	} else if errs.KindOf(errs.FromDB(err, "get document")) != errs.KindNotFound {
		return err
	}

	destPath, err := storage.CopyToConsumptionDir(s.cfg.Storage.ConsumptionDir, row.FilePath)
	if err != nil {
		return fmt.Errorf("copy to consumption dir: %w", err)
	}

	md5, err := storage.CalculateMD5(destPath)
	if err != nil {
		storage.RemoveOrphanedFile(destPath)
		return fmt.Errorf("compute md5: %w", err)
	}

	batchID := uuid.New().String()
	consumeTaskID := uuid.New().String()
	enrichTaskID := uuid.New().String()
	thumbnailTaskID := uuid.New().String()

	consumePayloadMap := map[string]any{
		"file_path":    destPath,
		"file_index":   1,
		"on_completed": enrichTaskID,
		"document_id":  row.DocumentKey,
	}
	if s.cfg.Consumer.Thumbnail.Enabled {
		consumePayloadMap["on_completed_thumbnail"] = thumbnailTaskID
	}
	consumePayload, _ := json.Marshal(consumePayloadMap)

	_, err = s.taskCreator.CreateTask(ctx, types.Task.Type.Consume, batchID, consumePayload, consumeTaskID, types.Task.Status.Pending, "consume:"+md5)
	if err != nil {
		storage.RemoveOrphanedFile(destPath)
		return fmt.Errorf("create consume task: %w", err)
	}

	enrichPayload, _ := json.Marshal(map[string]any{
		"waiting_for": consumeTaskID,
		"file_name":   filepath.Base(destPath),
		"file_index":  1,
		"document_id": row.DocumentKey,
	})

	_, err = s.taskCreator.CreateTask(ctx, types.Task.Type.Enrich, batchID, enrichPayload, enrichTaskID, types.Task.Status.Waiting, "")
	if err != nil {
		storage.RemoveOrphanedFile(destPath)
		return fmt.Errorf("create enrich task: %w", err)
	}

	if s.cfg.Consumer.Thumbnail.Enabled {
		thumbnailPayload, _ := json.Marshal(map[string]any{
			"waiting_for": consumeTaskID,
			"file_name":   filepath.Base(destPath),
			"file_index":  1,
		})
		_, err = s.taskCreator.CreateTask(ctx, types.Task.Type.Thumbnail, batchID, thumbnailPayload, thumbnailTaskID, types.Task.Status.Waiting, "")
		if err != nil {
			storage.RemoveOrphanedFile(destPath)
			return fmt.Errorf("create thumbnail task: %w", err)
		}
	}

	if err := s.batchCreator.Create(ctx, batchID, types.Batch.Source.OrphanedRestore, types.Batch.Status.Queued); err != nil {
		storage.RemoveOrphanedFile(destPath)
		return fmt.Errorf("create batch: %w", err)
	}

	if err := s.queries.MarkOrphanedFileRestored(ctx, id); err != nil {
		return errs.FromDB(err, "mark orphaned file restored")
	}

	return nil
}

// MoveToInbox copies one quarantined file back into the consumption
// directory without consuming it.
func (s *Orphaned) MoveToInbox(ctx context.Context, id int64) error {
	row, err := s.queries.GetOrphanedFile(ctx, id)
	if err != nil {
		return errs.FromDB(err, "get orphaned file")
	}

	_, err = storage.CopyToConsumptionDir(s.cfg.Storage.ConsumptionDir, row.FilePath)
	if err != nil {
		return fmt.Errorf("copy to consumption dir: %w", err)
	}

	if err := s.queries.MarkOrphanedFileReingested(ctx, id); err != nil {
		return errs.FromDB(err, "mark orphaned file reingested")
	}

	return nil
}

// DeleteAll removes every quarantined file and returns how many.
func (s *Orphaned) DeleteAll(ctx context.Context) (int, error) {
	files, err := s.queries.ListOrphanedFiles(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "list orphaned files")
	}

	for _, f := range files {
		storage.RemoveOrphanedFile(f.FilePath)
	}

	if err := s.queries.MarkAllOrphanedFilesDeleted(ctx); err != nil {
		return 0, errs.FromDB(err, "mark all orphaned files deleted")
	}

	return len(files), nil
}

// MoveAllToInbox moves every quarantined file back into the consumption
// directory and returns how many.
func (s *Orphaned) MoveAllToInbox(ctx context.Context) (int, error) {
	files, err := s.queries.ListOrphanedFiles(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "list orphaned files")
	}

	moved := 0
	for _, f := range files {
		if err := s.MoveToInbox(ctx, f.ID); err != nil {
			s.logger.Error("", "move to inbox orphaned %d: %v", f.ID, err)
			continue
		}
		moved++
	}

	return moved, nil
}

// ScanAndQuarantineAsync runs one sweep in the background; failures are
// logged, never surfaced.
func (s *Orphaned) ScanAndQuarantineAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		count, err := s.ScanAndQuarantine(ctx)
		if err != nil {
			s.logger.Error("", "orphaned scan: %v", err)
			return
		}
		if count > 0 {
			s.logger.Info("", "orphaned scan: %d files quarantined", count)
		}
	}()
}
