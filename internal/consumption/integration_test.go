package consumption

import (
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// integrationTestRunner satisfies the consumption.runner interface.
type integrationTestRunner struct {
	extractText     string
	extractTextErr  error
	ocrTmpPath      string
	ocrErr          error
	optimizeTmpPath string
	optimizeErr     error
}

func (r *integrationTestRunner) ExtractText(ctx context.Context, path string) (*tools.TextExtractionResult, error) {
	if r.extractTextErr != nil {
		return nil, r.extractTextErr
	}
	text := r.extractText
	if text == "" {
		text = "extracted text from " + filepath.Base(path)
	}
	return &tools.TextExtractionResult{Text: &text}, nil
}

func (r *integrationTestRunner) OCR(ctx context.Context, docId, path string) (*tools.OCRResult, error) {
	if r.ocrErr != nil {
		return nil, r.ocrErr
	}
	p := r.ocrTmpPath
	if p == "" {
		tmp := filepath.Join(os.TempDir(), "ocr-"+uuid.New().String()+".pdf")
		os.WriteFile(tmp, testutil.MinimalTextPDF("ocr result"), 0644)
		p = tmp
	}
	return &tools.OCRResult{Success: true, TmpPath: &p}, nil
}

func (r *integrationTestRunner) OptimizePdf(ctx context.Context, docId, path string) (*tools.PdfOptimizationResult, error) {
	if r.optimizeErr != nil {
		return nil, r.optimizeErr
	}
	p := r.optimizeTmpPath
	if p == "" {
		tmp := filepath.Join(os.TempDir(), "opt-"+uuid.New().String()+".pdf")
		os.WriteFile(tmp, testutil.MinimalTextPDF("optimized"), 0644)
		p = tmp
	}
	return &tools.PdfOptimizationResult{Success: true, TmpPath: &p}, nil
}

func setupConsumerTest(t *testing.T) (*Consumer, *config.Config, *database.Client, func()) {
	t.Helper()

	cfg, cleanupCfg := testutil.NewTestConfig(t)

	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	logger := utils.NewDiscardLogger()

	runner := &integrationTestRunner{
		extractText: "This is sample extracted text from the test PDF document. It contains enough words to pass the minimum density check and allow the consumption pipeline to proceed normally.",
	}

	consumer, err := NewConsumerWithRunner(cfg, logger, client, runner)
	testutil.AssertNoError(t, err, "create consumer")

	cleanup := func() {
		cleanupCfg()
	}

	return consumer, cfg, client, cleanup
}

// --- Tests ---

func TestConsumerProcessHappyPath(t *testing.T) {
	consumer, cfg, client, cleanup := setupConsumerTest(t)
	defer cleanup()

	ctx := context.Background()
	pdfPath := filepath.Join(cfg.Storage.ConsumptionDir, "test-document.pdf")
	os.WriteFile(pdfPath, testutil.MinimalTextPDF("financial data and budget reports"), 0644)

	file, err := FileFromPath(pdfPath)
	testutil.AssertNoError(t, err, "file from path")

	docID := uuid.New().String()
	processed, err := consumer.Process(ctx, file, docID)
	testutil.AssertNoError(t, err, "consumer process")
	testutil.AssertEqual(t, processed.DocumentID, docID, "document ID")

	t.Run("document record created", func(t *testing.T) {
		doc, err := client.GetDocument(ctx, docID)
		testutil.AssertNoError(t, err, "get document")
		testutil.AssertEqual(t, doc.Title, "test-document.pdf", "title")
		testutil.AssertEqual(t, doc.MimeType, "application/pdf", "mime type")
		testutil.AssertEqual(t, doc.Md5Checksum, file.MD5Checksum, "md5 matches")
	})

	t.Run("files stored", func(t *testing.T) {
		if processed.StorageProcessedPath == nil {
			t.Fatal("expected storage path")
		}
		if _, err := os.Stat(*processed.StorageProcessedPath); os.IsNotExist(err) {
			t.Fatalf("processed file missing: %s", *processed.StorageProcessedPath)
		}
		if processed.StorageOriginalPath == nil {
			t.Fatal("expected original path")
		}
		if _, err := os.Stat(*processed.StorageOriginalPath); os.IsNotExist(err) {
			t.Fatalf("original file missing: %s", *processed.StorageOriginalPath)
		}
	})

	t.Run("original deleted from inbox after success", func(t *testing.T) {
		if _, err := os.Stat(pdfPath); !os.IsNotExist(err) {
			t.Fatal("original should have been deleted from inbox after success")
		}
	})

	t.Run("DB paths set and counts populated", func(t *testing.T) {
		doc, _ := client.GetDocument(ctx, docID)
		if doc.StoragePath == "" {
			t.Fatal("expected storage path in DB")
		}
		if doc.OriginalPath == "" {
			t.Fatal("expected original path in DB")
		}
		if doc.WordCount <= 0 {
			t.Fatalf("expected positive word count, got %d", doc.WordCount)
		}
	})
}

func TestConsumerDuplicateDetection(t *testing.T) {
	consumer, cfg, client, cleanup := setupConsumerTest(t)
	defer cleanup()

	ctx := context.Background()

	pdfPath := filepath.Join(cfg.Storage.ConsumptionDir, "dup.pdf")
	os.WriteFile(pdfPath, testutil.MinimalTextPDF("Same content same checksums"), 0644)
	file1, _ := FileFromPath(pdfPath)
	_, err := consumer.Process(ctx, file1, uuid.New().String())
	testutil.AssertNoError(t, err, "first process")

	pdfPath2 := filepath.Join(cfg.Storage.ConsumptionDir, "dup2.pdf")
	os.WriteFile(pdfPath2, testutil.MinimalTextPDF("Same content same checksums"), 0644)
	file2, _ := FileFromPath(pdfPath2)
	_, err = consumer.Process(ctx, file2, uuid.New().String())
	testutil.AssertError(t, err, "second process should fail as duplicate")

	count := docCount(t, client.Queries)
	testutil.AssertEqual(t, count, int64(1), "only one document after duplicate attempt")

	// Duplicate file should be in errors/duplicated/
	if _, err := os.Stat(pdfPath2); !os.IsNotExist(err) {
		t.Fatal("duplicate original should have been moved to error directory")
	}
	dupesDir := filepath.Join(cfg.Storage.StorageDir, errorDirName, errorDirNameDupes)
	entries, _ := os.ReadDir(dupesDir)
	if len(entries) == 0 {
		t.Fatal("expected at least one file in duplicate error directory")
	}
}

func TestConsumerEmptyTextGoesToOcr(t *testing.T) {
	consumer, cfg, _, cleanup := setupConsumerTest(t)
	defer cleanup()

	consumer.runner = &integrationTestRunner{extractText: ""}

	pdfPath := filepath.Join(cfg.Storage.ConsumptionDir, "ocr-test.pdf")
	os.WriteFile(pdfPath, testutil.MinimalTextPDF("Needs OCR"), 0644)
	file, _ := FileFromPath(pdfPath)
	_, err := consumer.Process(context.Background(), file, uuid.New().String())
	testutil.AssertNoError(t, err, "process with OCR fallback")
}

func TestConsumerTextExtractionFailure(t *testing.T) {
	consumer, cfg, _, cleanup := setupConsumerTest(t)
	defer cleanup()

	consumer.runner = &integrationTestRunner{extractTextErr: errSentinel{}}

	pdfPath := filepath.Join(cfg.Storage.ConsumptionDir, "fail.pdf")
	os.WriteFile(pdfPath, testutil.MinimalTextPDF("Will fail"), 0644)
	file, _ := FileFromPath(pdfPath)
	_, err := consumer.Process(context.Background(), file, uuid.New().String())
	testutil.AssertError(t, err, "extraction error should fail")

	// File should be moved to error quarantine
	if _, err := os.Stat(pdfPath); !os.IsNotExist(err) {
		t.Fatal("original should have been moved to error directory after failure")
	}
	errorDir := filepath.Join(cfg.Storage.StorageDir, errorDirName)
	entries, _ := os.ReadDir(errorDir)
	if len(entries) == 0 {
		t.Fatal("expected at least one file in error directory")
	}
}

func TestConsumerFileNotFound(t *testing.T) {
	consumer, _, _, cleanup := setupConsumerTest(t)
	defer cleanup()

	file := File{
		Name: "missing.pdf", OriginalPath: "/tmp/nonexistent-test-file.pdf",
		MimeType: "application/pdf", Date: time.Now(),
	}
	_, err := consumer.Process(context.Background(), file, uuid.New().String())
	testutil.AssertError(t, err, "missing file should fail")
}

func TestConsumerChecksums(t *testing.T) {
	path := filepath.Join(os.TempDir(), "checksum-"+uuid.New().String()+".pdf")
	os.WriteFile(path, testutil.MinimalTextPDF("checksum test"), 0644)
	defer os.Remove(path)

	md5, sha512, err := calculateChecksums(path)
	testutil.AssertNoError(t, err, "calculate checksums")
	testutil.AssertEqual(t, len(md5), 32, "md5 length")
	testutil.AssertEqual(t, len(sha512), 128, "sha512 length")
}

func TestConsumerFileFromPath(t *testing.T) {
	path := filepath.Join(os.TempDir(), "ffp-"+uuid.New().String()+".pdf")
	os.WriteFile(path, testutil.MinimalTextPDF("file from path"), 0644)
	defer os.Remove(path)

	file, err := FileFromPath(path)
	testutil.AssertNoError(t, err, "file from path")
	testutil.AssertEqual(t, file.Name, filepath.Base(path), "name")
	testutil.AssertEqual(t, file.MimeType, "application/pdf", "mime")
	testutil.AssertEqual(t, file.FileSize > 0, true, "size > 0")
}

func TestConsumerProcessImageFile(t *testing.T) {
	consumer, cfg, client, cleanup := setupConsumerTest(t)
	defer cleanup()

	ctx := context.Background()

	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := range 100 {
		for x := range 100 {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	pngPath := filepath.Join(cfg.Storage.ConsumptionDir, "test-image.png")
	f, err := os.Create(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	file, err := FileFromPath(pngPath)
	testutil.AssertNoError(t, err, "file from path")
	testutil.AssertEqual(t, file.MimeType, "image/png", "mime type")

	docID := uuid.New().String()
	processed, err := consumer.Process(ctx, file, docID)
	testutil.AssertNoError(t, err, "process image")

	t.Run("document record created", func(t *testing.T) {
		doc, err := client.GetDocument(ctx, docID)
		testutil.AssertNoError(t, err, "get document")
		testutil.AssertEqual(t, doc.MimeType, "image/png", "mime type")
	})

	t.Run("original stored with real extension", func(t *testing.T) {
		if processed.StorageOriginalPath == nil {
			t.Fatal("expected original path")
		}
		if !strings.HasSuffix(*processed.StorageOriginalPath, ".png") {
			t.Errorf("original should have .png extension, got: %s", *processed.StorageOriginalPath)
		}
	})

	t.Run("processed stored as pdf", func(t *testing.T) {
		if processed.StorageProcessedPath == nil {
			t.Fatal("expected processed path")
		}
		if !strings.HasSuffix(*processed.StorageProcessedPath, ".pdf") {
			t.Errorf("processed should have .pdf extension, got: %s", *processed.StorageProcessedPath)
		}
	})

	t.Run("page count is 1", func(t *testing.T) {
		doc, err := client.GetDocument(ctx, docID)
		testutil.AssertNoError(t, err, "get document")
		testutil.AssertEqual(t, int(doc.PageCount), 1, "page count")
	})
}

func TestMoveFailedFile(t *testing.T) {
	tests := []struct {
		name    string
		errType string
		subDir  string
	}{
		{name: "generic error", errType: "", subDir: "errors"},
		{name: "duplicate error", errType: "duplicate", subDir: filepath.Join("errors", "duplicated")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storageDir := t.TempDir()
			srcPath := filepath.Join(storageDir, "src.pdf")
			os.WriteFile(srcPath, []byte("content"), 0644)

			logger := utils.NewDiscardLogger()
			docID := uuid.New().String()

			MoveFailedFile(storageDir, srcPath, tt.errType, logger, &docID)

			if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
				t.Fatal("source file should no longer exist at original path")
			}

			destDir := filepath.Join(storageDir, tt.subDir)
			entries, err := os.ReadDir(destDir)
			if err != nil {
				t.Fatalf("failed to read %s: %v", destDir, err)
			}
			if len(entries) != 1 {
				t.Fatalf("expected 1 file in %s, got %d", destDir, len(entries))
			}
			if !strings.HasSuffix(entries[0].Name(), "-src.pdf") {
				t.Fatalf("expected filename ending with '-src.pdf', got %s", entries[0].Name())
			}
		})
	}
}

func TestGetFilesAndFiltering(t *testing.T) {
	t.Run("finds PDFs", func(t *testing.T) {
		cfg, cleanup := testutil.NewTestConfig(t)
		defer cleanup()
		os.WriteFile(filepath.Join(cfg.Storage.ConsumptionDir, "a.pdf"), testutil.MinimalTextPDF("a"), 0644)
		os.WriteFile(filepath.Join(cfg.Storage.ConsumptionDir, "b.pdf"), testutil.MinimalTextPDF("b"), 0644)

		files, err := GetFiles(cfg.Storage.ConsumptionDir, []string{".pdf"}, 0)
		testutil.AssertNoError(t, err, "get files")
		testutil.AssertEqual(t, len(files), 2, "found 2 PDFs")
	})

	t.Run("skips unsupported extensions", func(t *testing.T) {
		cfg, cleanup := testutil.NewTestConfig(t)
		defer cleanup()
		os.WriteFile(filepath.Join(cfg.Storage.ConsumptionDir, "notes.txt"), []byte("text"), 0644)
		os.WriteFile(filepath.Join(cfg.Storage.ConsumptionDir, "doc.pdf"), testutil.MinimalTextPDF("doc"), 0644)

		files, err := GetFiles(cfg.Storage.ConsumptionDir, []string{".pdf"}, 0)
		testutil.AssertNoError(t, err, "get files")
		testutil.AssertEqual(t, len(files), 1, "only PDF")
		testutil.AssertEqual(t, files[0].Name, "doc.pdf", "name")
	})
}

func TestMoveAndCopyFile(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "move-copy-test-*")
	defer os.RemoveAll(tmpDir)

	src := filepath.Join(tmpDir, "src.txt")
	dst := filepath.Join(tmpDir, "dst.txt")
	cpy := filepath.Join(tmpDir, "cpy.txt")
	os.WriteFile(src, []byte("content"), 0644)

	testutil.AssertNoError(t, CopyFile(src, cpy), "copy")
	data, _ := os.ReadFile(cpy)
	testutil.AssertEqual(t, string(data), "content", "copy content")

	testutil.AssertNoError(t, MoveFile(src, dst), "move")
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source should be gone after move")
	}

	// CopyFile overwrites existing destinations (O_TRUNC)
	testutil.AssertNoError(t, CopyFile(dst, cpy), "copy to existing")
	dst2 := filepath.Join(tmpDir, "dst2.txt")
	os.WriteFile(dst2, []byte("existing"), 0644)
	// MoveFile replaces existing destinations via rename(2)
	testutil.AssertNoError(t, MoveFile(dst, dst2), "move to existing")
}

func TestRemoveFileAndCleanUp(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "rm-test-*")
	defer os.RemoveAll(tmpDir)

	p := filepath.Join(tmpDir, "rm.txt")
	os.WriteFile(p, []byte("x"), 0644)
	testutil.AssertNoError(t, RemoveFile(p), "remove existing")
	testutil.AssertNoError(t, RemoveFile(p), "remove again (no-op)")
	testutil.AssertNoError(t, RemoveFile(""), "remove empty path")

	tmp2, _ := os.CreateTemp("", "clean-*")
	path2 := tmp2.Name()
	tmp2.Close()
	testutil.AssertNoError(t, CleanUp(path2), "clean up existing")
	testutil.AssertNoError(t, CleanUp("/tmp/nonexistent-cleanup-file"), "clean up non-existent")
}

func TestQuarantineFailedFiles(t *testing.T) {
	t.Run("moves files and discards enrich tasks", func(t *testing.T) {
		client := database.NewTestClient(t)
		defer client.DB().Close()
		ctx := context.Background()
		storageDir := t.TempDir()
		logger := utils.NewDiscardLogger()

		batchID := uuid.New().String()
		err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: batchID, Source: "test", Status: "processing",
		})
		testutil.AssertNoError(t, err, "create batch")

		inboxDir := filepath.Join(t.TempDir(), "inbox")
		os.MkdirAll(inboxDir, 0755)
		filePath := filepath.Join(inboxDir, "quarantine-doc.pdf")
		os.WriteFile(filePath, []byte("test content"), 0644)

		enrichTaskID := uuid.New().String()
		payload, _ := json.Marshal(map[string]any{
			"file_path":    filePath,
			"on_completed": enrichTaskID,
		})

		payloadPtr := json.RawMessage(payload)
		taskID, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID:   uuid.New().String(),
			TaskType: "consume",
			Status:   "pending",
			Payload:  &payloadPtr,
			BatchID:  sql.NullString{String: batchID, Valid: true},
		})
		testutil.AssertNoError(t, err, "create consume task")

		_, err = client.DB().ExecContext(ctx,
			"UPDATE task SET status = 'failed', error = 'Max retries exceeded (3)', completed_at = CURRENT_TIMESTAMP WHERE id = $1",
			taskID)
		testutil.AssertNoError(t, err, "quarantine task")

		enrichPayloadPtr := json.RawMessage(`{}`)
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID:   enrichTaskID,
			TaskType: "enrich",
			Status:   "waiting",
			Payload:  &enrichPayloadPtr,
			BatchID:  sql.NullString{String: batchID, Valid: true},
		})
		testutil.AssertNoError(t, err, "create enrich task")

		err = QuarantineFailedFiles(ctx, client.Queries, storageDir, logger, batchID)
		testutil.AssertNoError(t, err, "quarantine failed files")

		if _, err := os.Stat(filePath); !os.IsNotExist(err) {
			t.Fatal("file should have been moved from original path")
		}

		errorDir := filepath.Join(storageDir, "errors")
		entries, _ := os.ReadDir(errorDir)
		if len(entries) != 1 {
			t.Fatalf("expected 1 file in errors dir, got %d", len(entries))
		}

		enrichTask, err := client.Queries.GetTaskByTaskID(ctx, enrichTaskID)
		testutil.AssertNoError(t, err, "get enrich task")
		testutil.AssertEqual(t, enrichTask.Status, "discarded", "enrich task should be discarded")
	})

	t.Run("no quarantined tasks is a no-op", func(t *testing.T) {
		client := database.NewTestClient(t)
		defer client.DB().Close()
		ctx := context.Background()
		storageDir := t.TempDir()
		logger := utils.NewDiscardLogger()

		batchID := uuid.New().String()
		err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: batchID, Source: "test", Status: "processing",
		})
		testutil.AssertNoError(t, err, "create batch")

		noPayload := json.RawMessage(`{}`)
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID:   uuid.New().String(),
			TaskType: "consume",
			Status:   "pending",
			Payload:  &noPayload,
			BatchID:  sql.NullString{String: batchID, Valid: true},
		})
		testutil.AssertNoError(t, err, "create task")

		err = QuarantineFailedFiles(ctx, client.Queries, storageDir, logger, batchID)
		testutil.AssertNoError(t, err, "quarantine should succeed")

		errorDir := filepath.Join(storageDir, "errors")
		if _, err := os.Stat(errorDir); !os.IsNotExist(err) {
			t.Fatal("errors dir should not have been created")
		}
	})

	t.Run("discards enrich task without file_path", func(t *testing.T) {
		client := database.NewTestClient(t)
		defer client.DB().Close()
		ctx := context.Background()
		storageDir := t.TempDir()
		logger := utils.NewDiscardLogger()

		batchID := uuid.New().String()
		err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
			ID: batchID, Source: "test", Status: "processing",
		})
		testutil.AssertNoError(t, err, "create batch")

		enrichTaskID := uuid.New().String()
		payload, _ := json.Marshal(map[string]any{
			"on_completed": enrichTaskID,
		})

		payloadPtr := json.RawMessage(payload)
		taskID, err := client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID:   uuid.New().String(),
			TaskType: "consume",
			Status:   "pending",
			Payload:  &payloadPtr,
			BatchID:  sql.NullString{String: batchID, Valid: true},
		})
		testutil.AssertNoError(t, err, "create consume task")

		_, err = client.DB().ExecContext(ctx,
			"UPDATE task SET status = 'failed', error = 'Max retries exceeded (3)', completed_at = CURRENT_TIMESTAMP WHERE id = $1",
			taskID)
		testutil.AssertNoError(t, err, "quarantine task")

		noPayload2 := json.RawMessage(`{}`)
		_, err = client.Queries.CreateTask(ctx, database.CreateTaskParams{
			TaskID:   enrichTaskID,
			TaskType: "enrich",
			Status:   "waiting",
			Payload:  &noPayload2,
			BatchID:  sql.NullString{String: batchID, Valid: true},
		})
		testutil.AssertNoError(t, err, "create enrich task")

		err = QuarantineFailedFiles(ctx, client.Queries, storageDir, logger, batchID)
		testutil.AssertNoError(t, err, "quarantine should succeed")

		enrichTask, err := client.Queries.GetTaskByTaskID(ctx, enrichTaskID)
		testutil.AssertNoError(t, err, "get enrich task")
		testutil.AssertEqual(t, enrichTask.Status, "discarded", "enrich task should be discarded")

		errorDir := filepath.Join(storageDir, "errors")
		if _, err := os.Stat(errorDir); !os.IsNotExist(err) {
			t.Fatal("errors dir should not exist when no file_path")
		}
	})
}

// --- helpers ---

type errSentinel struct{}

func (errSentinel) Error() string { return "mock failure" }

func docCount(t *testing.T, queries *database.Queries) int64 {
	t.Helper()
	docs, err := queries.ListDocumentsWithSort(context.Background(),
		database.ListDocumentsWithSortParams{Limit: 10000, Offset: 0, SortBy: "created_at", SortOrder: "desc"})
	testutil.AssertNoError(t, err, "list docs")
	return int64(len(docs))
}
