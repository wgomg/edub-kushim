package service

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	thumbnailGracePeriod = 30 * time.Second
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

func (s *TrashService) SoftDelete(ctx context.Context, documentID string) error {
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

func (s *TrashService) RestoreDocument(ctx context.Context, documentID string) error {
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

func (s *TrashService) PermanentlyDelete(ctx context.Context, documentID string) error {
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
			s.logger.Warn(nil, "permanently delete: remove thumbnail %s: %v", thumbnailPath, err)
		}
	}

	return nil
}

func (s *TrashService) PurgeExpired(ctx context.Context) (int64, error) {
	deleted, err := s.client.PurgeExpiredDocuments(ctx, strconv.Itoa(s.cfg.Storage.Trash.RetentionDays))
	if err != nil {
		return 0, errs.FromDB(err, "purge expired documents")
	}

	if deleted > 0 {
		s.cleanupOrphanedTrashDirs(ctx)
	}

	paths, _ := s.CleanupOrphanedThumbnails(ctx, false)
	if len(paths) > 0 {
		s.logger.Info(nil, "trash purge: %d orphaned thumbnails removed", len(paths))
	}

	return deleted, nil
}

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

func (s *TrashService) CleanupOrphanedThumbnails(ctx context.Context, dryRun bool) (removedPaths []string, err error) {
	thumbRoot := filepath.Join(s.cfg.Storage.StorageDir, storage.DirThumbnails)
	if _, err := os.Stat(thumbRoot); err != nil {
		if !os.IsNotExist(err) {
			s.logger.Warn(nil, "thumbnail purge: stat thumbnails dir: %v", err)
		}
		return nil, nil
	}

	var docIDs []string
	err = filepath.WalkDir(thumbRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.logger.Warn(nil, "thumbnail purge: walk %s: %v", path, err)
			return nil
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".jpg" {
			return nil
		}
		docIDs = append(docIDs, strings.TrimSuffix(d.Name(), ".jpg"))
		return nil
	})
	if err != nil {
		s.logger.Warn(nil, "thumbnail purge: walk thumbnails dir: %v", err)
		return nil, nil
	}
	if len(docIDs) == 0 {
		return nil, nil
	}

	existingIDs, err := s.documentIDsExist(ctx, docIDs)
	if err != nil {
		s.logger.Warn(nil, "thumbnail purge: query document ids: %v", err)
		return nil, nil
	}

	err = filepath.WalkDir(thumbRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.logger.Warn(nil, "thumbnail purge: walk %s: %v", path, err)
			return nil
		}
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".jpg" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			s.logger.Warn(nil, "thumbnail purge: stat %s: %v", path, err)
			return nil
		}
		if time.Since(info.ModTime()) < thumbnailGracePeriod {
			return nil
		}
		docID := strings.TrimSuffix(d.Name(), ".jpg")
		if _, ok := existingIDs[docID]; ok {
			return nil
		}
		removedPaths = append(removedPaths, path)
		if dryRun {
			return nil
		}
		if err := os.Remove(path); err != nil {
			s.logger.Warn(nil, "thumbnail purge: remove %s: %v", path, err)
			return nil
		}
		removeEmptyThumbnailDirs(filepath.Dir(path))
		return nil
	})
	if err != nil {
		s.logger.Warn(nil, "thumbnail purge: walk thumbnails dir: %v", err)
	}
	return removedPaths, nil
}

func (s *TrashService) documentIDsExist(ctx context.Context, docIDs []string) (map[string]struct{}, error) {
	ids := make(map[string]struct{}, len(docIDs))

	for i := 0; i < len(docIDs); i += docIDBatchSize {
		end := min(i+docIDBatchSize, len(docIDs))
		batch := docIDs[i:end]

		placeholders := make([]string, len(batch))
		for j := range batch {
			placeholders[j] = fmt.Sprintf("$%d", j+1)
		}
		query := "SELECT document_id FROM document WHERE document_id IN (" + strings.Join(placeholders, ",") + ")"
		args := make([]any, len(batch))
		for j, id := range batch {
			args[j] = id
		}

		rows, err := s.client.DB().QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("query document ids batch %d: %w", i, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan document id: %w", err)
			}
			ids[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate document ids: %w", err)
		}
		rows.Close()
	}
	return ids, nil
}

const docIDBatchSize = 1000

func removeEmptyThumbnailDirs(dirPath string) {
	for range 4 {
		if os.Remove(dirPath) != nil {
			return
		}
		dirPath = filepath.Dir(dirPath)
	}
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
