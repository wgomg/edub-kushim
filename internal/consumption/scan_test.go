package consumption

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func setupScanTest(t *testing.T) (*config.Config, *database.Client, func()) {
	t.Helper()
	cfg, cleanupCfg := testutil.NewTestConfig(t)
	client := database.NewTestClient(t)
	cleanup := func() {
		client.DB().Close()
		cleanupCfg()
	}
	return cfg, client, cleanup
}

func TestScanAndEnqueue_EmptyInbox(t *testing.T) {
	cfg, client, cleanup := setupScanTest(t)
	defer cleanup()

	logger := utils.NewDiscardLogger()
	batchID, count, err := ScanAndEnqueue(context.Background(), cfg, client, logger)
	if err != nil {
		t.Fatalf("ScanAndEnqueue: %v", err)
	}
	if batchID != "" {
		t.Errorf("batchID = %q, want empty", batchID)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestScanAndEnqueue_CreatesBatchAndTasks(t *testing.T) {
	cfg, client, cleanup := setupScanTest(t)
	defer cleanup()

	pdfPath := filepath.Join(cfg.Storage.ConsumptionDir, "new-doc.pdf")
	testutil.CreateTestPDF(t, pdfPath, "unique content alpha")

	logger := utils.NewDiscardLogger()
	batchID, count, err := ScanAndEnqueue(context.Background(), cfg, client, logger)
	if err != nil {
		t.Fatalf("ScanAndEnqueue: %v", err)
	}
	if batchID == "" {
		t.Fatal("batchID is empty, want a UUID")
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	// Verify batch record exists
	batch, err := client.GetBatch(context.Background(), batchID)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if batch.Source != "polling" {
		t.Errorf("batch.Source = %q, want polling", batch.Source)
	}
	if batch.Status != "queued" {
		t.Errorf("batch.Status = %q, want queued", batch.Status)
	}

	// Verify tasks created (consume + enrich pair)
	tasks, err := client.GetTaskByBatchID(context.Background(), sql.NullString{String: batchID, Valid: true})
	if err != nil {
		t.Fatalf("GetTaskByBatchID: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("task count = %d, want 2 (consume + enrich)", len(tasks))
	}

	var consumeTask, enrichTask database.Task
	for _, task := range tasks {
		switch task.TaskType {
		case "consume":
			consumeTask = task
		case "enrich":
			enrichTask = task
		}
	}

	if consumeTask.ID == 0 {
		t.Fatal("consume task not found")
	}
	if consumeTask.Status != "pending" {
		t.Errorf("consume task status = %q, want pending", consumeTask.Status)
	}
	if !consumeTask.DedupKey.Valid {
		t.Error("consume task dedup_key should be set")
	}

	if enrichTask.ID == 0 {
		t.Fatal("enrich task not found")
	}
	if enrichTask.Status != "waiting" {
		t.Errorf("enrich task status = %q, want waiting", enrichTask.Status)
	}
}

func TestScanAndEnqueue_SkipsDuplicates(t *testing.T) {
	cfg, client, cleanup := setupScanTest(t)
	defer cleanup()

	// Insert a document with a known MD5
	pdfPath := filepath.Join(cfg.Storage.ConsumptionDir, "dup-doc.pdf")
	testutil.CreateTestPDF(t, pdfPath, "duplicate content")
	md5, err := utils.CalculateMD5(pdfPath)
	if err != nil {
		t.Fatalf("CalculateMD5: %v", err)
	}

	docType, err := client.ListAllDocumentTypes(context.Background())
	if err != nil || len(docType) == 0 {
		t.Fatal("no document types found")
	}
	_, err = client.CreateDocument(context.Background(), database.CreateDocumentParams{
		DocumentID:  uuid.New().String(),
		Title:       "existing-doc",
		Md5Checksum: md5,
		OriginalPath: "/tmp/existing.pdf",
		StoragePath:  "/tmp/existing-stored.pdf",
		FileSize:     1024,
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	logger := utils.NewDiscardLogger()
	batchID, count, err := ScanAndEnqueue(context.Background(), cfg, client, logger)
	if err != nil {
		t.Fatalf("ScanAndEnqueue: %v", err)
	}
	if batchID != "" {
		t.Errorf("batchID = %q, want empty (all duplicates)", batchID)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestScanAndEnqueue_MixedNewAndDuplicate(t *testing.T) {
	cfg, client, cleanup := setupScanTest(t)
	defer cleanup()

	// Insert a document with a known MD5
	dupPath := filepath.Join(cfg.Storage.ConsumptionDir, "existing.pdf")
	testutil.CreateTestPDF(t, dupPath, "existing content")
	md5, err := utils.CalculateMD5(dupPath)
	if err != nil {
		t.Fatalf("CalculateMD5: %v", err)
	}

	docType, err := client.ListAllDocumentTypes(context.Background())
	if err != nil || len(docType) == 0 {
		t.Fatal("no document types found")
	}
	_, err = client.CreateDocument(context.Background(), database.CreateDocumentParams{
		DocumentID:  uuid.New().String(),
		Title:       "existing-doc",
		Md5Checksum: md5,
		OriginalPath: "/tmp/existing.pdf",
		StoragePath:  "/tmp/existing-stored.pdf",
		FileSize:     1024,
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	// Add a new file
	newPath := filepath.Join(cfg.Storage.ConsumptionDir, "new-doc.pdf")
	testutil.CreateTestPDF(t, newPath, "brand new content")

	logger := utils.NewDiscardLogger()
	batchID, count, err := ScanAndEnqueue(context.Background(), cfg, client, logger)
	if err != nil {
		t.Fatalf("ScanAndEnqueue: %v", err)
	}
	if batchID == "" {
		t.Fatal("batchID is empty, want a UUID")
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 (only new file)", count)
	}

	batch, err := client.GetBatch(context.Background(), batchID)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if batch.Status != "queued" {
		t.Errorf("batch.Status = %q, want queued", batch.Status)
	}
}

func TestScanAndEnqueue_MultipleFiles(t *testing.T) {
	cfg, client, cleanup := setupScanTest(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		pdfPath := filepath.Join(cfg.Storage.ConsumptionDir, filepath.Base(
			filepath.Join("", "doc"+string(rune('a'+i))+".pdf")))
		testutil.CreateTestPDF(t, pdfPath, "content"+string(rune('a'+i)))
	}

	logger := utils.NewDiscardLogger()
	batchID, count, err := ScanAndEnqueue(context.Background(), cfg, client, logger)
	if err != nil {
		t.Fatalf("ScanAndEnqueue: %v", err)
	}
	if batchID == "" {
		t.Fatal("batchID is empty")
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}

	tasks, err := client.GetTaskByBatchID(context.Background(), sql.NullString{String: batchID, Valid: true})
	if err != nil {
		t.Fatalf("GetTaskByBatchID: %v", err)
	}
	// 3 files × 2 tasks each (consume + enrich) = 6
	if len(tasks) != 6 {
		t.Errorf("task count = %d, want 6", len(tasks))
	}
}

func TestQueryDuplicatesByMD5_Empty(t *testing.T) {
	_, client, cleanup := setupScanTest(t)
	defer cleanup()

	duplicates, err := queryDuplicatesByMD5(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("queryDuplicatesByMD5: %v", err)
	}
	if duplicates != nil {
		t.Errorf("duplicates = %v, want nil", duplicates)
	}
}

func TestQueryDuplicatesByMD5_NoMatches(t *testing.T) {
	_, client, cleanup := setupScanTest(t)
	defer cleanup()

	duplicates, err := queryDuplicatesByMD5(context.Background(), client, []string{"aaa", "bbb"})
	if err != nil {
		t.Fatalf("queryDuplicatesByMD5: %v", err)
	}
	if len(duplicates) != 0 {
		t.Errorf("duplicates count = %d, want 0", len(duplicates))
	}
}

func TestQueryDuplicatesByMD5_WithMatch(t *testing.T) {
	_, client, cleanup := setupScanTest(t)
	defer cleanup()

	docType, err := client.ListAllDocumentTypes(context.Background())
	if err != nil || len(docType) == 0 {
		t.Fatal("no document types found")
	}

	docID := uuid.New().String()
	_, err = client.CreateDocument(context.Background(), database.CreateDocumentParams{
		DocumentID:  docID,
		Title:       "test",
		Md5Checksum: "abc123",
		OriginalPath: "/tmp/test.pdf",
		StoragePath:  "/tmp/test-stored.pdf",
		FileSize:     1024,
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	duplicates, err := queryDuplicatesByMD5(context.Background(), client, []string{"abc123", "notfound"})
	if err != nil {
		t.Fatalf("queryDuplicatesByMD5: %v", err)
	}
	if len(duplicates) != 1 {
		t.Fatalf("duplicates count = %d, want 1", len(duplicates))
	}
	if duplicates["abc123"] != docID {
		t.Errorf("duplicates[abc123] = %q, want %q", duplicates["abc123"], docID)
	}
}

