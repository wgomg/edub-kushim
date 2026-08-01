package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/storage"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	trashPurgeInterval   = 1 * time.Hour
	trashGracePeriod     = 5 * time.Minute
)

type TrashService struct {
	client *database.Client
	cfg    *config.Config
	logger *utils.Logger
	ctx    context.Context
	cancel context.CancelFunc
}

func NewTrashService(client *database.Client, cfg *config.Config, logger *utils.Logger) *TrashService {
	return &TrashService{client: client, cfg: cfg, logger: logger}
}

func (s *TrashService) ListTrash(ctx context.Context, limit, offset int32) ([]database.ListTrashDocumentsRow, error) {
	return s.client.ListTrashDocuments(ctx, database.ListTrashDocumentsParams{Limit: limit, Offset: offset})
}

func (s *TrashService) GetTrashDocument(ctx context.Context, documentID string) (database.GetTrashDocumentRow, error) {
	row, err := s.client.GetTrashDocument(ctx, documentID)
	if err != nil {
		return row, errs.FromDB(err, "get trash document")
	}
	return row, nil
}

func (s *TrashService) CountTrash(ctx context.Context) (int64, error) {
	return s.client.CountTrashDocuments(ctx)
}

// SoftDelete moves a document's files to the trash directory and marks the row
// deleted. Files are moved before the DB update; if the update fails, files
// are rolled back to their original locations so no data is stranded.
func (s *TrashService) SoftDelete(ctx context.Context, documentID string) error {
	doc, err := s.client.GetDocument(ctx, documentID)
	if err != nil {
		return errs.FromDB(err, "get document")
	}

	newOriginal, newStorage, err := storage.MoveToTrash(s.cfg.Storage.StorageDir, documentID, doc.OriginalPath, doc.StoragePath)
	if err != nil {
		return errs.EInternal("soft delete", err)
	}

	if err := s.client.SoftDeleteDocument(ctx, database.SoftDeleteDocumentParams{
		DocumentID:   documentID,
		OriginalPath: newOriginal,
		StoragePath:  newStorage,
	}); err != nil {
		// Rollback: move files back so the document remains fully functional.
		storage.RestoreFromTrash(s.cfg.Storage.StorageDir, documentID, newOriginal, newStorage) //nolint:errcheck
		return errs.FromDB(err, "soft delete document")
	}

	return nil
}

// RestoreDocument moves a document's files back from the trash and clears its
// deleted_at marker.
func (s *TrashService) RestoreDocument(ctx context.Context, documentID string) error {
	doc, err := s.GetTrashDocument(ctx, documentID)
	if err != nil {
		return err
	}

	newOriginal, newStorage, err := storage.RestoreFromTrash(s.cfg.Storage.StorageDir, documentID, doc.OriginalPath, doc.StoragePath)
	if err != nil {
		return errs.EInternal("restore from trash", err)
	}

	if err := s.client.RestoreDocument(ctx, database.RestoreDocumentParams{
		DocumentID:   documentID,
		OriginalPath: newOriginal,
		StoragePath:  newStorage,
	}); err != nil {
		return errs.FromDB(err, "restore document")
	}

	return nil
}

// PermanentlyDelete removes a document from the trash: the DB row first
// (cascading to junction tables), then trash files. If file removal fails,
// the hourly orphan cleanup handles leftovers.
func (s *TrashService) PermanentlyDelete(ctx context.Context, documentID string) error {
	_, err := s.GetTrashDocument(ctx, documentID)
	if err != nil {
		return err
	}

	if err := s.client.PermanentlyDeleteDocument(ctx, documentID); err != nil {
		return errs.FromDB(err, "permanently delete document")
	}

	// Best-effort file cleanup — orphan sweep handles leftovers on failure.
	storage.RemoveFromTrash(s.cfg.Storage.StorageDir, documentID) //nolint:errcheck

	return nil
}

// PurgeExpired deletes rows past their retention period, then removes trash
// dirs whose document no longer has a database row. The two phases are
// decoupled so a document restored between them keeps its files.
func (s *TrashService) PurgeExpired(ctx context.Context) (int64, error) {
	deleted, err := s.client.PurgeExpiredDocuments(ctx, strconv.Itoa(s.cfg.Storage.Trash.RetentionDays))
	if err != nil {
		return 0, errs.FromDB(err, "purge expired documents")
	}

	if deleted > 0 {
		s.cleanupOrphanedTrashDirs(ctx)
	}

	return deleted, nil
}

// cleanupOrphanedTrashDirs removes trash directories whose document_id has no
// matching database row. Fetches active IDs in a single query (not per-entry)
// and skips directories whose files were recently modified to avoid racing
// with in-flight soft-deletes.
func (s *TrashService) cleanupOrphanedTrashDirs(ctx context.Context) {
	trashRoot := storage.TrashDir(s.cfg.Storage.StorageDir)
	entries, err := os.ReadDir(trashRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			s.logger.Warn(nil, "trash purge: read trash dir: %v", err)
		}
		return
	}

	if len(entries) == 0 {
		return
	}

	activeIDs, err := s.activeTrashDocumentIDs(ctx)
	if err != nil {
		s.logger.Warn(nil, "trash purge: query active trash ids: %v", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := activeIDs[entry.Name()]; ok {
			continue
		}
		dirPath := filepath.Join(trashRoot, entry.Name())
		if isRecentlyModified(dirPath, trashGracePeriod) {
			continue
		}
		if err := os.RemoveAll(dirPath); err != nil {
			s.logger.Warn(nil, "trash purge: remove dir %s: %v", entry.Name(), err)
		}
	}
}

func (s *TrashService) activeTrashDocumentIDs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.client.DB().QueryContext(ctx,
		`SELECT document_id FROM document WHERE deleted_at IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("query active trash ids: %w", err)
	}
	defer rows.Close()

	ids := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan active trash id: %w", err)
		}
		ids[id] = struct{}{}
	}
	return ids, rows.Err()
}

func isRecentlyModified(dirPath string, within time.Duration) bool {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return false
	}
	cutoff := time.Now().Add(-within)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			return true
		}
	}
	return false
}

func (s *TrashService) StartBackgroundPurge() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(trashPurgeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				count, err := s.PurgeExpired(s.ctx)
				if err != nil {
					s.logger.Error(nil, "trash purge: %v", err)
				} else if count > 0 {
					s.logger.Info(nil, "trash purge: %d expired documents permanently deleted", count)
				}
			case <-s.ctx.Done():
				return
			}
		}
	}()
}

func (s *TrashService) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
