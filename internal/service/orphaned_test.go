package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/storage"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

func newTestOrphaned(t *testing.T) (*Orphaned, *config.Config, *database.Client, func()) {
	t.Helper()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	logger := testutil.NewTestLogger()

	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	cfg.Storage.StorageDir = filepath.Join(configDir, "storage")
	cfg.Storage.ConsumptionDir = filepath.Join(configDir, "inbox")
	os.MkdirAll(cfg.Storage.StorageDir, 0755)
	os.MkdirAll(cfg.Storage.ConsumptionDir, 0755)
	os.MkdirAll(filepath.Join(cfg.Storage.StorageDir, "originals"), 0755)
	os.MkdirAll(filepath.Join(cfg.Storage.StorageDir, "processed"), 0755)

	svc := NewOrphaned(client.Queries, cfg, logger, newServiceEnqueuer(t, client))

	cleanup := func() { client.DB().Close() }
	return svc, cfg, client, cleanup
}

func createTestOrphanFile(t *testing.T, storageDir, sourceDir, filename string) string {
	t.Helper()
	dir := filepath.Join(storageDir, sourceDir)
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, filename)
	testutil.CreateTestPDF(t, path, "test content")
	past := time.Now().Add(-1 * time.Minute)
	os.Chtimes(path, past, past)
	return path
}

func TestOrphaned_ScanAndQuarantine_QuarantinesOrphans(t *testing.T) {
	svc, cfg, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	uuid1 := "550e8400-e29b-41d4-a716-446655440000"
	uuid2 := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", uuid1+".pdf")
	createTestOrphanFile(t, cfg.Storage.StorageDir, "processed", uuid2+".pdf")

	count, err := svc.ScanAndQuarantine(ctx)
	testutil.AssertNoError(t, err, "scan")
	testutil.AssertEqual(t, count, 2, "quarantined count")

	files, err := svc.List(ctx)
	testutil.AssertNoError(t, err, "list")
	testutil.AssertEqual(t, len(files), 2, "db records")

	quarantineDir := filepath.Join(cfg.Storage.StorageDir, "orphaned")
	if _, err := os.Stat(filepath.Join(quarantineDir, "originals", uuid1+".pdf")); os.IsNotExist(err) {
		t.Fatal("file should be in quarantine originals/")
	}
	if _, err := os.Stat(filepath.Join(quarantineDir, "processed", uuid2+".pdf")); os.IsNotExist(err) {
		t.Fatal("file should be in quarantine processed/")
	}
}

func TestOrphaned_ScanAndQuarantine_SkipsKnownDocuments(t *testing.T) {
	svc, cfg, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	client := database.NewTestClient(t)
	defer client.DB().Close()
	_, docUUID := database.CreateTestDocument(t, client.Queries, "known.pdf")

	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", docUUID+".pdf")

	count, err := svc.ScanAndQuarantine(ctx)
	testutil.AssertNoError(t, err, "scan")
	testutil.AssertEqual(t, count, 0, "known doc should not be quarantined")
}

func TestOrphaned_ScanAndQuarantine_SkipsDBIDNamedFiles(t *testing.T) {
	svc, cfg, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", "42.pdf")

	count, err := svc.ScanAndQuarantine(ctx)
	testutil.AssertNoError(t, err, "scan")
	testutil.AssertEqual(t, count, 1, "dbid file without matching doc should be quarantined")

	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "db record")
	testutil.AssertEqual(t, files[0].DocumentKeyType, "dbid", "key type")
	testutil.AssertEqual(t, files[0].DocumentKey, "42", "document key")
}

func TestOrphaned_Delete(t *testing.T) {
	svc, cfg, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", uuid+".pdf")

	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "before delete")

	err := svc.Delete(ctx, files[0].ID)
	testutil.AssertNoError(t, err, "delete")

	files, _ = svc.List(ctx)
	testutil.AssertEqual(t, len(files), 0, "after delete")
}

func TestOrphaned_Delete_NotFound(t *testing.T) {
	svc, _, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	err := svc.Delete(ctx, 99999)
	testutil.AssertError(t, err, "delete nonexistent")
}

func TestOrphaned_Restore_UUIDOnly(t *testing.T) {
	svc, cfg, client, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	uuidValue := "550e8400-e29b-41d4-a716-446655440000"
	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", uuidValue+".pdf")
	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "quarantined")

	err := svc.Restore(ctx, testBatchSpec(t, types.Batch.Origin.API), files[0].ID)
	testutil.AssertNoError(t, err, "restore")

	files, _ = svc.List(ctx)
	testutil.AssertEqual(t, len(files), 0, "status changed from pending")

	destPath := filepath.Join(cfg.Storage.ConsumptionDir, uuidValue+".pdf")
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Fatal("file should be copied to consumption dir")
	}

	md5hash, err := storage.CalculateMD5(destPath)
	testutil.AssertNoError(t, err, "compute md5")

	consumeTask, err := client.Queries.GetConfigTaskByDedupKey(ctx, database.GetConfigTaskByDedupKeyParams{
		TaskType: types.Task.Type.Consume,
		DedupKey: sql.NullString{String: "consume:" + md5hash, Valid: true},
	})
	testutil.AssertNoError(t, err, "find consume task")
	testutil.AssertEqual(t, consumeTask.DedupKey.String, "consume:"+md5hash, "consume dedup key")

	tasks, err := client.Queries.ListTasksByBatch(ctx, database.ListTasksByBatchParams{
		BatchID: sql.NullString{String: consumeTask.BatchID.String, Valid: true},
		Limit:   100,
	})
	testutil.AssertNoError(t, err, "list batch tasks")
	testutil.AssertEqual(t, len(tasks), 3, "consume, enrich and thumbnail")

	byType := make(map[types.TaskType]database.Task, len(tasks))
	for _, created := range tasks {
		byType[created.TaskType] = created
	}
	consume := byType[types.Task.Type.Consume]
	enrich := byType[types.Task.Type.Enrich]
	thumbnail := byType[types.Task.Type.Thumbnail]

	testutil.AssertEqual(t, string(consume.Status), "pending", "consume status")
	testutil.AssertEqual(t, string(enrich.Status), "waiting", "enrich status")
	testutil.AssertEqual(t, string(thumbnail.Status), "waiting", "thumbnail status")
	testutil.AssertEqual(t, enrich.BatchID.String, consume.BatchID.String, "enrich shares the consume batch")
	testutil.AssertEqual(t, thumbnail.BatchID.String, consume.BatchID.String, "thumbnail shares the consume batch")

	var consumePayload map[string]any
	json.Unmarshal(*consume.Payload, &consumePayload)
	var enrichPayload map[string]any
	json.Unmarshal(*enrich.Payload, &enrichPayload)
	var thumbnailPayload map[string]any
	json.Unmarshal(*thumbnail.Payload, &thumbnailPayload)

	testutil.AssertEqual(t, enrich.ParentTaskID.String, consume.TaskID, "enrich links to the consume task")
	testutil.AssertEqual(t, thumbnail.ParentTaskID.String, consume.TaskID, "thumbnail links to the consume task")
	testutil.AssertEqual(t, consumePayload["document_id"], uuidValue, "consume document_id")
	testutil.AssertEqual(t, enrichPayload["document_id"], uuidValue, "enrich document_id")
	testutil.AssertEqual(t, thumbnailPayload["document_id"], uuidValue, "thumbnail document_id")
	inherited, ok := thumbnailPayload[task.InheritPayloadKey].([]any)
	if !ok || len(inherited) != 1 || inherited[0] != "storage_path" {
		t.Errorf("thumbnail inherit = %v, want [storage_path]", thumbnailPayload[task.InheritPayloadKey])
	}
	for name, payload := range map[string]map[string]any{"consume": consumePayload, "enrich": enrichPayload, "thumbnail": thumbnailPayload} {
		for _, key := range []string{"waiting_for", "on_completed", "on_completed_thumbnail"} {
			if _, present := payload[key]; present {
				t.Errorf("%s payload still carries the retired %q key", name, key)
			}
		}
	}

	batch, err := client.Queries.GetBatch(ctx, consume.BatchID.String)
	testutil.AssertNoError(t, err, "get batch")
	testutil.AssertEqual(t, string(batch.Origin), "api", "batch origin")
	testutil.AssertEqual(t, string(batch.Status), "queued", "batch status")
}

func TestOrphaned_Restore_RejectsDBID(t *testing.T) {
	svc, cfg, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", "42.pdf")
	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "quarantined")

	err := svc.Restore(ctx, testBatchSpec(t, types.Batch.Origin.API), files[0].ID)
	testutil.AssertError(t, err, "restore dbid should fail")
}

func TestOrphaned_Restore_RejectsExistingDocument(t *testing.T) {
	svc, cfg, client, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", uuid+".pdf")
	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "quarantined")

	client.Queries.CreateDocument(ctx, database.CreateDocumentParams{
		DocumentID:     uuid,
		Title:          "conflict.pdf",
		Md5Checksum:    "d41d8cd98f00b204e9800998ecf8427e",
		Sha512Checksum: "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e",
		OriginalType:   "application/pdf",
		FileSize:       1024,
		OriginalPath:   "/tmp/orig.pdf",
		StoragePath:    "/tmp/storage.pdf",
	})

	err := svc.Restore(ctx, testBatchSpec(t, types.Batch.Origin.API), files[0].ID)
	testutil.AssertError(t, err, "restore should fail when doc exists")
}

func TestOrphaned_Restore_QueuedConsumeConflict(t *testing.T) {
	svc, cfg, client, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	uuidValue := "550e8400-e29b-41d4-a716-446655440000"
	sourcePath := createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", uuidValue+".pdf")
	md5hash, err := storage.CalculateMD5(sourcePath)
	testutil.AssertNoError(t, err, "compute md5")

	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "quarantined")

	payload := json.RawMessage(`{}`)
	_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID:   "pre-existing-consume",
		TaskType: types.Task.Type.Consume,
		Status:   types.Task.Status.Pending,
		Payload:  &payload,
		DedupKey: sql.NullString{String: "consume:" + md5hash, Valid: true},
	})
	testutil.AssertNoError(t, err, "seed pending consume task")

	err = svc.Restore(ctx, testBatchSpec(t, types.Batch.Origin.API), files[0].ID)
	testutil.AssertError(t, err, "restore should fail when the consume task is already queued")

	if _, statErr := os.Stat(filepath.Join(cfg.Storage.ConsumptionDir, uuidValue+".pdf")); !os.IsNotExist(statErr) {
		t.Fatal("consumption dir copy should be cleaned up after the enqueue conflict")
	}

	files, _ = svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "orphaned file not marked restored after the conflict")
}

func TestOrphaned_MoveToInbox(t *testing.T) {
	svc, cfg, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", uuid+".pdf")
	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "quarantined")

	err := svc.MoveToInbox(ctx, files[0].ID)
	testutil.AssertNoError(t, err, "move to inbox")

	files, _ = svc.List(ctx)
	testutil.AssertEqual(t, len(files), 0, "status changed from pending")

	entries, _ := os.ReadDir(cfg.Storage.ConsumptionDir)
	pdfCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".pdf" {
			pdfCount++
		}
	}
	testutil.AssertEqual(t, pdfCount, 1, "pdf in consumption dir")
}

func TestOrphaned_DeleteAll(t *testing.T) {
	svc, cfg, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", "550e8400-e29b-41d4-a716-446655440000.pdf")
	createTestOrphanFile(t, cfg.Storage.StorageDir, "processed", "6ba7b810-9dad-11d1-80b4-00c04fd430c8.pdf")
	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 2, "quarantined")

	deleted, err := svc.DeleteAll(ctx)
	testutil.AssertNoError(t, err, "delete all")
	testutil.AssertEqual(t, deleted, 2, "deleted count")

	files, _ = svc.List(ctx)
	testutil.AssertEqual(t, len(files), 0, "all pending gone")
}

func TestOrphaned_MoveAllToInbox(t *testing.T) {
	svc, cfg, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", "550e8400-e29b-41d4-a716-446655440000.pdf")
	createTestOrphanFile(t, cfg.Storage.StorageDir, "processed", "6ba7b810-9dad-11d1-80b4-00c04fd430c8.pdf")
	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 2, "quarantined")

	moved, err := svc.MoveAllToInbox(ctx)
	testutil.AssertNoError(t, err, "move all")
	testutil.AssertEqual(t, moved, 2, "moved count")

	files, _ = svc.List(ctx)
	testutil.AssertEqual(t, len(files), 0, "all pending gone")

	entries, _ := os.ReadDir(cfg.Storage.ConsumptionDir)
	pdfCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".pdf" {
			pdfCount++
		}
	}
	testutil.AssertEqual(t, pdfCount, 2, "pdfs in consumption dir")
}

func TestOrphaned_List(t *testing.T) {
	svc, cfg, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	files, err := svc.List(ctx)
	testutil.AssertNoError(t, err, "list empty")
	testutil.AssertEqual(t, len(files), 0, "empty list")

	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", "550e8400-e29b-41d4-a716-446655440000.pdf")
	svc.ScanAndQuarantine(ctx)

	files, err = svc.List(ctx)
	testutil.AssertNoError(t, err, "list after scan")
	testutil.AssertEqual(t, len(files), 1, "one record")
}

func TestNewOrphaned(t *testing.T) {
	client := database.NewTestClient(t)
	defer client.DB().Close()
	logger := log.NewDiscardLogger()
	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)

	svc := NewOrphaned(client.Queries, cfg, logger, newServiceEnqueuer(t, client))
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}
