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
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/log"
)

const (
	trashPurgeInterval = 1 * time.Hour
	trashGracePeriod   = 5 * time.Minute
)

// Trash is the soft-delete lifecycle: listing, soft-deleting, restoring,
// permanent deletion, and the hourly expired purge. RestoreDocument and
// PermanentlyDelete run under a blocking task protocol against the batch
// owner.
type Trash struct {
	client   *database.Client
	cfg      *config.Config
	logger   *log.Logger
	orphaned *Orphaned
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewTrash builds the trash service over the orphaned service whose
// thumbnail sweep the purge runs; the background purge runs only after
// StartBackgroundPurge.
func NewTrash(client *database.Client, cfg *config.Config, logger *log.Logger, orphaned *Orphaned) *Trash {
	return &Trash{client: client, cfg: cfg, logger: logger, orphaned: orphaned}
}

// ListTrash pages the trashed documents.
func (s *Trash) ListTrash(ctx context.Context, limit, offset int32) ([]database.ListTrashDocumentsRow, error) {
	return s.client.ListTrashDocuments(ctx, database.ListTrashDocumentsParams{Limit: limit, Offset: offset})
}

// GetTrashDocument returns one trashed document's row.
func (s *Trash) GetTrashDocument(ctx context.Context, documentID string) (database.GetTrashDocumentRow, error) {
	row, err := s.client.GetTrashDocument(ctx, documentID)
	if err != nil {
		return row, errs.FromDB(err, "get trash document")
	}
	return row, nil
}

// CountTrash counts the trashed documents.
func (s *Trash) CountTrash(ctx context.Context) (int64, error) {
	return s.client.CountTrashDocuments(ctx)
}

// SoftDelete moves a document into the trash layout and flips its row to
// trashed inside one transaction; either side failing rolls both back.
func (s *Trash) SoftDelete(ctx context.Context, documentID string) error {
	doc, err := s.client.GetDocument(ctx, documentID)
	if err != nil {
		return errs.FromDB(err, "get document")
	}

	var thumbnailPath string
	if doc.HasThumbnail && doc.CreatedAt.Valid {
		thumbnailPath = storage.ThumbnailPath(s.cfg.Storage.StorageDir, doc.CreatedAt.Time, documentID)
	}

	newOriginal, newStorage, err := storage.MoveToTrash(s.cfg.Storage.StorageDir, documentID, doc.OriginalPath, doc.StoragePath, thumbnailPath)
	if err != nil {
		return errs.EInternal("soft delete", err)
	}

	if err := s.client.SoftDeleteDocument(ctx, database.SoftDeleteDocumentParams{
		DocumentID:   documentID,
		OriginalPath: newOriginal,
		StoragePath:  newStorage,
	}); err != nil {
		storage.RestoreFromTrash(s.cfg.Storage.StorageDir, documentID, newOriginal, newStorage, thumbnailPath)
		return errs.FromDB(err, "soft delete document")
	}

	return nil
}

// RestoreDocument moves a trashed document back into the live layout and
// flips its row back to live, again transactionally.
func (s *Trash) RestoreDocument(ctx context.Context, documentID string) error {
	doc, err := s.GetTrashDocument(ctx, documentID)
	if err != nil {
		return err
	}

	var thumbnailPath string
	if doc.CreatedAt.Valid {
		thumbnailPath = storage.ThumbnailPath(s.cfg.Storage.StorageDir, doc.CreatedAt.Time, documentID)
	}

	newOriginal, newStorage, err := storage.RestoreFromTrash(s.cfg.Storage.StorageDir, documentID, doc.OriginalPath, doc.StoragePath, thumbnailPath)
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

// PermanentlyDelete removes a trashed document's rows, its thumbnail and
// its files (trash and live layout) inside one transaction.
func (s *Trash) PermanentlyDelete(ctx context.Context, documentID string) error {
	doc, err := s.GetTrashDocument(ctx, documentID)
	if err != nil {
		return err
	}

	if err := s.client.PermanentlyDeleteDocument(ctx, documentID); err != nil {
		return errs.FromDB(err, "permanently delete document")
	}

	storage.RemoveFromTrash(s.cfg.Storage.StorageDir, documentID)

	if doc.CreatedAt.Valid {
		thumbnailPath := storage.ThumbnailPath(s.cfg.Storage.StorageDir, doc.CreatedAt.Time, documentID)
		if err := os.Remove(thumbnailPath); err != nil && !os.IsNotExist(err) {
			s.logger.Warn("", "permanently delete: remove thumbnail %s: %v", thumbnailPath, err)
		}
	}

	return nil
}

// PurgeExpired removes the trash rows whose retention grace has passed
// and returns how many were purged; it runs the orphaned-thumbnail sweep
// on every cycle.
func (s *Trash) PurgeExpired(ctx context.Context) (int64, error) {
	deleted, err := s.client.PurgeExpiredDocuments(ctx, strconv.Itoa(s.cfg.Storage.Trash.RetentionDays))
	if err != nil {
		return 0, errs.FromDB(err, "purge expired documents")
	}

	if deleted > 0 {
		s.cleanupOrphanedTrashDirs(ctx)
	}

	paths, err := s.orphaned.CleanupOrphanedThumbnails(ctx, false)
	if err != nil {
		s.logger.Warn("", "thumbnail purge: %v", err)
	}
	if len(paths) > 0 {
		s.logger.Info("", "trash purge: %d orphaned thumbnails removed", len(paths))
	}

	return deleted, nil
}

func (s *Trash) cleanupOrphanedTrashDirs(ctx context.Context) {
	trashRoot := storage.TrashDir(s.cfg.Storage.StorageDir)
	entries, err := os.ReadDir(trashRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			s.logger.Warn("", "trash purge: read trash dir: %v", err)
		}
		return
	}

	if len(entries) == 0 {
		return
	}

	activeIDs, err := s.activeTrashDocumentIDs(ctx)
	if err != nil {
		s.logger.Warn("", "trash purge: query active trash ids: %v", err)
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
			s.logger.Warn("", "trash purge: remove dir %s: %v", entry.Name(), err)
		}
	}
}

func (s *Trash) activeTrashDocumentIDs(ctx context.Context) (map[string]struct{}, error) {
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

// StartBackgroundPurge runs the hourly expired-trash purge until Close;
// calling it twice leaks a goroutine.
func (s *Trash) StartBackgroundPurge() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(trashPurgeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				count, err := s.PurgeExpired(s.ctx)
				if err != nil {
					s.logger.Error("", "trash purge: %v", err)
				} else if count > 0 {
					s.logger.Info("", "trash purge: %d expired documents permanently deleted", count)
				}
			case <-s.ctx.Done():
				return
			}
		}
	}()
}

// Close stops the background purge. Safe before Start.
func (s *Trash) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
