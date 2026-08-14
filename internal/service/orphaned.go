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
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/storage"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TaskCreator interface {
	CreateTask(ctx context.Context, taskType, batchID string, payload json.RawMessage,
		taskID, status, dedupKey string) (string, error)
}

type BatchCreator interface {
	Create(ctx context.Context, id, source, status string) error
}

type Orphaned struct {
	queries      *database.Queries
	cfg          *config.Config
	logger       *utils.Logger
	taskCreator  TaskCreator
	batchCreator BatchCreator
}

func NewOrphaned(queries *database.Queries, cfg *config.Config, logger *utils.Logger, taskCreator TaskCreator, batchCreator BatchCreator) *Orphaned {
	return &Orphaned{
		queries:      queries,
		cfg:          cfg,
		logger:       logger,
		taskCreator:  taskCreator,
		batchCreator: batchCreator,
	}
}

func (s *Orphaned) List(ctx context.Context) ([]database.OrphanedFile, error) {
	files, err := s.queries.ListOrphanedFiles(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list orphaned files")
	}
	return files, nil
}

func (s *Orphaned) ScanAndQuarantine(ctx context.Context) (int, error) {
	storageDir := s.cfg.Storage.StorageDir
	infos, errsCh := storage.WalkStorageDir(storageDir)

	quarantined := 0
	for info := range infos {
		ok, err := s.quarantineFile(ctx, storageDir, info)
		if err != nil {
			s.logger.Error(nil, "quarantine file %s: %v", info.FilePath, err)
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

	md5, err := utils.CalculateMD5(destPath)
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

	_, err = s.taskCreator.CreateTask(ctx, "consume", batchID, consumePayload, consumeTaskID, "pending", "consume:"+md5)
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

	_, err = s.taskCreator.CreateTask(ctx, "enrich", batchID, enrichPayload, enrichTaskID, "waiting", "")
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
		_, err = s.taskCreator.CreateTask(ctx, "thumbnail", batchID, thumbnailPayload, thumbnailTaskID, "waiting", "")
		if err != nil {
			storage.RemoveOrphanedFile(destPath)
			return fmt.Errorf("create thumbnail task: %w", err)
		}
	}

	if err := s.batchCreator.Create(ctx, batchID, "orphaned-restore", "queued"); err != nil {
		storage.RemoveOrphanedFile(destPath)
		return fmt.Errorf("create batch: %w", err)
	}

	if err := s.queries.MarkOrphanedFileRestored(ctx, id); err != nil {
		return errs.FromDB(err, "mark orphaned file restored")
	}

	return nil
}

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

func (s *Orphaned) MoveAllToInbox(ctx context.Context) (int, error) {
	files, err := s.queries.ListOrphanedFiles(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "list orphaned files")
	}

	moved := 0
	for _, f := range files {
		if err := s.MoveToInbox(ctx, f.ID); err != nil {
			s.logger.Error(nil, "move to inbox orphaned %d: %v", f.ID, err)
			continue
		}
		moved++
	}

	return moved, nil
}

func (s *Orphaned) ScanAndQuarantineAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		count, err := s.ScanAndQuarantine(ctx)
		if err != nil {
			s.logger.Error(nil, "orphaned scan: %v", err)
			return
		}
		if count > 0 {
			s.logger.Info(nil, "orphaned scan: %d files quarantined", count)
		}
	}()
}
