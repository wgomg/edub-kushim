package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func createTestThumbnail(t *testing.T, storageDir, docID string, mtime time.Time) string {
	t.Helper()
	dateDir := filepath.Join(storageDir, "thumbnails", "2026", "08", "17", "12")
	os.MkdirAll(dateDir, 0755)
	path := filepath.Join(dateDir, docID+".jpg")
	if err := os.WriteFile(path, []byte("fake thumbnail"), 0644); err != nil {
		t.Fatalf("write thumbnail: %v", err)
	}
	os.Chtimes(path, mtime, mtime)
	return path
}

func TestCleanupOrphanedThumbnails_RemovesOrphans(t *testing.T) {
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	orphanID := "00000000-0000-0000-0000-000000000001"
	past := time.Now().Add(-1 * time.Minute)
	path := createTestThumbnail(t, cfg.Storage.StorageDir, orphanID, past)

	paths, err := svc.CleanupOrphanedThumbnails(ctx, false)
	testutil.AssertNoError(t, err, "cleanup")
	testutil.AssertEqual(t, len(paths), 1, "should find 1 orphan")
	testutil.AssertEqual(t, paths[0], path, "returned path")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("orphaned thumbnail should be removed")
	}
}

func TestCleanupOrphanedThumbnails_DryRun(t *testing.T) {
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	orphanID := "00000000-0000-0000-0000-000000000002"
	past := time.Now().Add(-1 * time.Minute)
	path := createTestThumbnail(t, cfg.Storage.StorageDir, orphanID, past)

	paths, err := svc.CleanupOrphanedThumbnails(ctx, true)
	testutil.AssertNoError(t, err, "cleanup dry-run")
	testutil.AssertEqual(t, len(paths), 1, "should find 1 orphan")

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("dry-run should NOT remove the file")
	}
}

func TestCleanupOrphanedThumbnails_SkipsExistingDocs(t *testing.T) {
	svc, cfg, client, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	_, docID := database.CreateTestDocument(t, client.Queries, "existing.pdf")
	past := time.Now().Add(-1 * time.Minute)
	createTestThumbnail(t, cfg.Storage.StorageDir, docID, past)

	paths, err := svc.CleanupOrphanedThumbnails(ctx, false)
	testutil.AssertNoError(t, err, "cleanup")
	testutil.AssertEqual(t, len(paths), 0, "existing doc thumbnail should be kept")

	thumbPath := filepath.Join(cfg.Storage.StorageDir, "thumbnails", "2026", "08", "17", "12", docID+".jpg")
	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		t.Fatal("thumbnail for existing doc should still be present")
	}
}

func TestCleanupOrphanedThumbnails_RecencyGuard(t *testing.T) {
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	orphanID := "00000000-0000-0000-0000-000000000003"
	recent := time.Now()
	path := createTestThumbnail(t, cfg.Storage.StorageDir, orphanID, recent)

	paths, err := svc.CleanupOrphanedThumbnails(ctx, false)
	testutil.AssertNoError(t, err, "cleanup")
	testutil.AssertEqual(t, len(paths), 0, "recently modified file should be skipped")

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("recently modified file should not be removed")
	}
}

func TestCleanupOrphanedThumbnails_MissingDir(t *testing.T) {
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	cfg.Storage.StorageDir = filepath.Join(t.TempDir(), "nonexistent")

	paths, err := svc.CleanupOrphanedThumbnails(ctx, false)
	testutil.AssertNoError(t, err, "missing dir should not error")
	if paths != nil {
		t.Fatalf("expected nil paths for missing dir, got %d", len(paths))
	}
}

func TestCleanupOrphanedThumbnails_PrunesEmptyDirs(t *testing.T) {
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	orphanID := "00000000-0000-0000-0000-000000000004"
	past := time.Now().Add(-1 * time.Minute)
	createTestThumbnail(t, cfg.Storage.StorageDir, orphanID, past)

	_, err := svc.CleanupOrphanedThumbnails(ctx, false)
	testutil.AssertNoError(t, err, "cleanup")

	dateDir := filepath.Join(cfg.Storage.StorageDir, "thumbnails", "2026", "08", "17", "12")
	if _, err := os.Stat(dateDir); !os.IsNotExist(err) {
		t.Fatal("empty date dir should be pruned after removing orphan")
	}
}

func TestPurgeExpired_SweepsOrphanedThumbnailsEveryCycle(t *testing.T) {
	orphaned, cfg, client, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	trash := NewTrash(client, cfg, testutil.NewTestLogger(), orphaned)

	orphanID := "00000000-0000-0000-0000-000000000005"
	past := time.Now().Add(-1 * time.Minute)
	path := createTestThumbnail(t, cfg.Storage.StorageDir, orphanID, past)

	deleted, err := trash.PurgeExpired(ctx)
	testutil.AssertNoError(t, err, "purge")
	testutil.AssertEqual(t, deleted, int64(0), "expired documents purged")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("purge must run the orphaned-thumbnail sweep even when nothing was purged")
	}
}
