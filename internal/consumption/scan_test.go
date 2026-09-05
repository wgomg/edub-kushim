package consumption

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/storage"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func setupScanTest(t *testing.T) (*config.Config, *database.Client, func()) {
	t.Helper()
	cfg, cleanupCfg := testutil.NewTestConfig(t)
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
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

	// Verify tasks created (consume + enrich + thumbnail)
	tasks, err := client.GetTaskByBatchID(context.Background(), sql.NullString{String: batchID, Valid: true})
	if err != nil {
		t.Fatalf("GetTaskByBatchID: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("task count = %d, want 3 (consume + enrich + thumbnail)", len(tasks))
	}

	var consumeTask, enrichTask, thumbnailTask database.Task
	for _, task := range tasks {
		switch task.TaskType {
		case "consume":
			consumeTask = task
		case "enrich":
			enrichTask = task
		case "thumbnail":
			thumbnailTask = task
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

	if thumbnailTask.ID == 0 {
		t.Fatal("thumbnail task not found")
	}
	if thumbnailTask.Status != "waiting" {
		t.Errorf("thumbnail task status = %q, want waiting", thumbnailTask.Status)
	}
}

func TestScanAndEnqueue_SkipsDuplicates(t *testing.T) {
	cfg, client, cleanup := setupScanTest(t)
	defer cleanup()

	// Insert a document with the file's real MD5 and SHA512
	pdfPath := filepath.Join(cfg.Storage.ConsumptionDir, "dup-doc.pdf")
	testutil.CreateTestPDF(t, pdfPath, "duplicate content")
	md5, err := utils.CalculateMD5(pdfPath)
	if err != nil {
		t.Fatalf("CalculateMD5: %v", err)
	}
	sha512, err := calculateSHA512(pdfPath)
	if err != nil {
		t.Fatalf("calculateSHA512: %v", err)
	}

	docType, err := client.ListAllDocumentTypes(context.Background())
	if err != nil || len(docType) == 0 {
		t.Fatal("no document types found")
	}
	_, err = client.CreateDocument(context.Background(), database.CreateDocumentParams{
		DocumentID:     uuid.New().String(),
		Title:          "existing-doc",
		Md5Checksum:    md5,
		Sha512Checksum: sha512,
		OriginalPath:   "/tmp/existing.pdf",
		StoragePath:    "/tmp/existing-stored.pdf",
		FileSize:       1024,
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

	// Duplicate file must be moved out of the inbox into errors/duplicated/
	if _, err := os.Stat(pdfPath); !os.IsNotExist(err) {
		t.Fatal("duplicate should have been moved out of the inbox")
	}
	dupesDir := filepath.Join(cfg.Storage.StorageDir, storage.DirErrors, storage.DirErrorsDuplicates)
	entries, _ := os.ReadDir(dupesDir)
	if len(entries) == 0 {
		t.Fatal("expected at least one file in duplicate error directory")
	}
}

func TestScanAndEnqueue_MixedNewAndDuplicate(t *testing.T) {
	cfg, client, cleanup := setupScanTest(t)
	defer cleanup()

	// Insert a document with the file's real MD5 and SHA512
	dupPath := filepath.Join(cfg.Storage.ConsumptionDir, "existing.pdf")
	testutil.CreateTestPDF(t, dupPath, "existing content")
	md5, err := utils.CalculateMD5(dupPath)
	if err != nil {
		t.Fatalf("CalculateMD5: %v", err)
	}
	sha512, err := calculateSHA512(dupPath)
	if err != nil {
		t.Fatalf("calculateSHA512: %v", err)
	}

	docType, err := client.ListAllDocumentTypes(context.Background())
	if err != nil || len(docType) == 0 {
		t.Fatal("no document types found")
	}
	_, err = client.CreateDocument(context.Background(), database.CreateDocumentParams{
		DocumentID:     uuid.New().String(),
		Title:          "existing-doc",
		Md5Checksum:    md5,
		Sha512Checksum: sha512,
		OriginalPath:   "/tmp/existing.pdf",
		StoragePath:    "/tmp/existing-stored.pdf",
		FileSize:       1024,
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

	// Duplicate file must be moved out of the inbox into errors/duplicated/
	if _, err := os.Stat(dupPath); !os.IsNotExist(err) {
		t.Fatal("duplicate should have been moved out of the inbox")
	}
	dupesDir := filepath.Join(cfg.Storage.StorageDir, storage.DirErrors, storage.DirErrorsDuplicates)
	entries, _ := os.ReadDir(dupesDir)
	if len(entries) == 0 {
		t.Fatal("expected at least one file in duplicate error directory")
	}
}

func TestScanAndEnqueue_MultipleFiles(t *testing.T) {
	cfg, client, cleanup := setupScanTest(t)
	defer cleanup()

	for i := range 3 {
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
	// 3 files × 3 tasks each (consume + enrich + thumbnail) = 9
	if len(tasks) != 9 {
		t.Errorf("task count = %d, want 9", len(tasks))
	}
}

func TestScanAndEnqueue_PausedBatchesSkip(t *testing.T) {
	cfg, client, cleanup := setupScanTest(t)
	defer cleanup()

	// Create a paused batch to trigger the guard.
	err := client.Queries.CreateBatch(context.Background(), database.CreateBatchParams{
		ID: "paused-guard-test", Source: "test", Status: "paused",
	})
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	// Add files that would normally be enqueued.
	pdfPath := filepath.Join(cfg.Storage.ConsumptionDir, "should-not-be-processed.pdf")
	testutil.CreateTestPDF(t, pdfPath, "content that should not be scanned")

	logger := utils.NewDiscardLogger()
	batchID, count, err := ScanAndEnqueue(context.Background(), cfg, client, logger)
	if err != nil {
		t.Fatalf("ScanAndEnqueue: %v", err)
	}
	if batchID != "" {
		t.Errorf("batchID = %q, want empty (paused batches block scan)", batchID)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 (no files should be enqueued)", count)
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
		DocumentID:     docID,
		Title:          "test",
		Md5Checksum:    "abc123",
		Sha512Checksum: "sha512value",
		OriginalPath:   "/tmp/test.pdf",
		StoragePath:    "/tmp/test-stored.pdf",
		FileSize:       1024,
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
	if duplicates["abc123"].documentID != docID {
		t.Errorf("duplicates[abc123].documentID = %q, want %q", duplicates["abc123"].documentID, docID)
	}
	if duplicates["abc123"].sha512 != "sha512value" {
		t.Errorf("duplicates[abc123].sha512 = %q, want %q", duplicates["abc123"].sha512, "sha512value")
	}
}
