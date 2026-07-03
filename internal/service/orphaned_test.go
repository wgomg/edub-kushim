package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type mockTaskCreator struct {
	calls     []mockTaskCall
	createErr error
}

type mockTaskCall struct {
	TaskType string
	BatchID  string
	Payload  json.RawMessage
	TaskID   string
	Status   string
	DedupKey string
	Source   string
}

func (m *mockTaskCreator) CreateTask(_ context.Context, taskType, batchID string, payload json.RawMessage, taskID, status, dedupKey string) (string, error) {
	m.calls = append(m.calls, mockTaskCall{TaskType: taskType, BatchID: batchID, Payload: payload, TaskID: taskID, Status: status, DedupKey: dedupKey})
	return taskID, nil
}

func (m *mockTaskCreator) Create(_ context.Context, id, source, status string) error {
	m.calls = append(m.calls, mockTaskCall{BatchID: id, Source: source, Status: status})
	return m.createErr
}

func newTestOrphaned(t *testing.T) (*Orphaned, *config.Config, *database.Client, *mockTaskCreator, func()) {
	t.Helper()
	client := database.NewTestClient(t)
	logger := testutil.NewTestLogger()

	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	cfg.Storage.StorageDir = filepath.Join(configDir, "storage")
	cfg.Storage.ConsumptionDir = filepath.Join(configDir, "inbox")
	os.MkdirAll(cfg.Storage.StorageDir, 0755)
	os.MkdirAll(cfg.Storage.ConsumptionDir, 0755)
	os.MkdirAll(filepath.Join(cfg.Storage.StorageDir, "originals"), 0755)
	os.MkdirAll(filepath.Join(cfg.Storage.StorageDir, "processed"), 0755)

	mock := &mockTaskCreator{}
	svc := NewOrphaned(client.Queries, cfg, logger, mock, mock)

	cleanup := func() { client.DB().Close() }
	return svc, cfg, client, mock, cleanup
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
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
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
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
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
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
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
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
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
	svc, _, _, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	err := svc.Delete(ctx, 99999)
	testutil.AssertError(t, err, "delete nonexistent")
}

func TestOrphaned_Restore_UUIDOnly(t *testing.T) {
	svc, cfg, _, mock, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", uuid+".pdf")
	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "quarantined")

	err := svc.Restore(ctx, files[0].ID)
	testutil.AssertNoError(t, err, "restore")

	files, _ = svc.List(ctx)
	testutil.AssertEqual(t, len(files), 0, "status changed from pending")

	destPath := filepath.Join(cfg.Storage.ConsumptionDir, uuid+".pdf")
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Fatal("file should be copied to consumption dir")
	}

	md5, err := utils.CalculateMD5(destPath)
	testutil.AssertNoError(t, err, "compute md5")

	testutil.AssertEqual(t, len(mock.calls), 3, "should have 3 mock calls (consume + enrich + batch)")

	consumeCall := mock.calls[0]
	testutil.AssertEqual(t, consumeCall.TaskType, "consume", "call 1 type")
	testutil.AssertEqual(t, consumeCall.Status, "pending", "consume status")
	testutil.AssertEqual(t, consumeCall.DedupKey, "consume:"+md5, "consume dedup key")

	var consumePayload map[string]any
	json.Unmarshal(consumeCall.Payload, &consumePayload)

	enrichCall := mock.calls[1]
	testutil.AssertEqual(t, enrichCall.TaskType, "enrich", "call 2 type")
	testutil.AssertEqual(t, enrichCall.Status, "waiting", "enrich status")
	testutil.AssertEqual(t, enrichCall.BatchID, consumeCall.BatchID, "enrich should share batch id with consume")

	var enrichPayload map[string]any
	json.Unmarshal(enrichCall.Payload, &enrichPayload)
	testutil.AssertEqual(t, enrichPayload["waiting_for"], consumeCall.TaskID, "enrich waiting_for references consume task")
	testutil.AssertEqual(t, consumePayload["on_completed"], enrichCall.TaskID, "consume on_completed references enrich task")
	testutil.AssertEqual(t, consumePayload["document_id"], uuid, "consume document_id")
	testutil.AssertEqual(t, enrichPayload["document_id"], uuid, "enrich document_id")

	batchCall := mock.calls[2]
	testutil.AssertEqual(t, batchCall.Source, "orphaned-restore", "batch source")
	testutil.AssertEqual(t, batchCall.Status, "queued", "batch status")
	testutil.AssertEqual(t, batchCall.BatchID, consumeCall.BatchID, "batch id matches tasks")
}

func TestOrphaned_Restore_RejectsDBID(t *testing.T) {
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", "42.pdf")
	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "quarantined")

	err := svc.Restore(ctx, files[0].ID)
	testutil.AssertError(t, err, "restore dbid should fail")
}

func TestOrphaned_Restore_RejectsExistingDocument(t *testing.T) {
	svc, cfg, client, _, cleanup := newTestOrphaned(t)
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
		MimeType:       "application/pdf",
		FileSize:       1024,
		OriginalPath:   "/tmp/orig.pdf",
		StoragePath:    "/tmp/storage.pdf",
	})

	err := svc.Restore(ctx, files[0].ID)
	testutil.AssertError(t, err, "restore should fail when doc exists")
}

func TestOrphaned_Restore_BatchFailure(t *testing.T) {
	svc, cfg, _, mock, cleanup := newTestOrphaned(t)
	defer cleanup()
	ctx := context.Background()

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	createTestOrphanFile(t, cfg.Storage.StorageDir, "originals", uuid+".pdf")
	svc.ScanAndQuarantine(ctx)
	files, _ := svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "quarantined")

	mock.createErr = fmt.Errorf("batch creation failed")
	err := svc.Restore(ctx, files[0].ID)
	testutil.AssertError(t, err, "restore should fail on batch creation error")

	if _, err := os.Stat(filepath.Join(cfg.Storage.ConsumptionDir, uuid+".pdf")); !os.IsNotExist(err) {
		t.Fatal("consumption dir copy should be cleaned up after batch failure")
	}

	files, _ = svc.List(ctx)
	testutil.AssertEqual(t, len(files), 1, "orphaned file not marked restored after batch failure")
}

func TestOrphaned_MoveToInbox(t *testing.T) {
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
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
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
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
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
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
	svc, cfg, _, _, cleanup := newTestOrphaned(t)
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
	logger := utils.NewDiscardLogger()
	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	mock := &mockTaskCreator{}

	svc := NewOrphaned(client.Queries, cfg, logger, mock, mock)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}
