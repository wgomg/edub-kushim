package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func newOrphanedHandler(t *testing.T) (*OrphanedHandler, *config.Config) {
	t.Helper()
	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	t.Cleanup(func() { client.DB().Close() })

	logger := testutil.NewTestLogger()
	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	cfg.Storage.StorageDir = filepath.Join(configDir, "storage")
	cfg.Storage.ConsumptionDir = filepath.Join(configDir, "inbox")
	os.MkdirAll(filepath.Join(cfg.Storage.StorageDir, "originals"), 0755)
	os.MkdirAll(filepath.Join(cfg.Storage.StorageDir, "processed"), 0755)
	os.MkdirAll(cfg.Storage.ConsumptionDir, 0755)

	mock := &mockTaskCreator{}
	svc := service.NewOrphaned(client.Queries, cfg, logger, mock, mock)
	h := NewOrphanedHandler(svc, logger)
	return h, cfg
}

type mockTaskCreator struct{}

func (m *mockTaskCreator) CreateTask(_ context.Context, _, _ string, _ json.RawMessage, _, _, _ string) (string, error) {
	return "", nil
}

func (m *mockTaskCreator) Create(_ context.Context, _, _, _ string) error {
	return nil
}

func createOrphanedPDF(t *testing.T, storageDir, sourceDir, filename string) string {
	t.Helper()
	dir := filepath.Join(storageDir, sourceDir)
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, filename)
	testutil.CreateTestPDF(t, path, "test")
	past := time.Now().Add(-1 * time.Minute)
	os.Chtimes(path, past, past)
	return path
}

func TestOrphanedHandler_ListOrphaned_Empty(t *testing.T) {
	h, _ := newOrphanedHandler(t)
	w := rec()
	r := req(t, "GET", "/api/v1/documents/orphaned", nil)
	h.ListOrphaned(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var result []map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	testutil.AssertEqual(t, len(result), 0, "empty list")
}

func TestOrphanedHandler_ListOrphaned_WithData(t *testing.T) {
	h, cfg := newOrphanedHandler(t)

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	createOrphanedPDF(t, cfg.Storage.StorageDir, "originals", uuid+".pdf")

	ctx := req(t, "POST", "/api/v1/documents/orphaned/scan", nil)
	h.ScanOrphaned(rec(), ctx)

	w := rec()
	r := req(t, "GET", "/api/v1/documents/orphaned", nil)
	h.ListOrphaned(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var result []map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	testutil.AssertEqual(t, len(result), 1, "one orphaned file")
	testutil.AssertEqual(t, result[0]["document_key"], uuid, "document key")
	testutil.AssertEqual(t, result[0]["document_key_type"], "uuid", "key type")
}

func TestOrphanedHandler_ScanOrphaned(t *testing.T) {
	h, cfg := newOrphanedHandler(t)

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	createOrphanedPDF(t, cfg.Storage.StorageDir, "originals", uuid+".pdf")

	w := rec()
	r := req(t, "POST", "/api/v1/documents/orphaned/scan", nil)
	h.ScanOrphaned(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	testutil.AssertEqual(t, result["quarantined"], float64(1), "quarantined count")
}

func TestOrphanedHandler_DeleteOrphaned(t *testing.T) {
	h, cfg := newOrphanedHandler(t)

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	createOrphanedPDF(t, cfg.Storage.StorageDir, "originals", uuid+".pdf")
	scanCtx := req(t, "POST", "/api/v1/documents/orphaned/scan", nil)
	h.ScanOrphaned(rec(), scanCtx)

	listW := rec()
	h.ListOrphaned(listW, req(t, "GET", "/api/v1/documents/orphaned", nil))
	var files []map[string]any
	json.NewDecoder(listW.Body).Decode(&files)
	id := fmt.Sprintf("%.0f", files[0]["id"].(float64))

	w := rec()
	r := req(t, "DELETE", "/api/v1/documents/orphaned/file/"+id, nil)
	r.SetPathValue("id", id)
	h.DeleteOrphaned(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusNoContent, "status")
}

func TestOrphanedHandler_DeleteOrphaned_InvalidID(t *testing.T) {
	h, _ := newOrphanedHandler(t)

	w := rec()
	r := req(t, "DELETE", "/api/v1/documents/orphaned/file/abc", nil)
	r.SetPathValue("id", "abc")
	h.DeleteOrphaned(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "status")
}

func TestOrphanedHandler_RestoreOrphaned(t *testing.T) {
	h, cfg := newOrphanedHandler(t)

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	createOrphanedPDF(t, cfg.Storage.StorageDir, "originals", uuid+".pdf")
	scanCtx := req(t, "POST", "/api/v1/documents/orphaned/scan", nil)
	h.ScanOrphaned(rec(), scanCtx)

	listW := rec()
	h.ListOrphaned(listW, req(t, "GET", "/api/v1/documents/orphaned", nil))
	var files []map[string]any
	json.NewDecoder(listW.Body).Decode(&files)
	id := fmt.Sprintf("%.0f", files[0]["id"].(float64))

	w := rec()
	r := req(t, "POST", "/api/v1/documents/orphaned/"+id+"/restore", nil)
	r.SetPathValue("id", id)
	h.RestoreOrphaned(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusAccepted, "status")
}

func TestOrphanedHandler_RestoreOrphaned_InvalidID(t *testing.T) {
	h, _ := newOrphanedHandler(t)

	w := rec()
	r := req(t, "POST", "/api/v1/documents/orphaned/abc/restore", nil)
	r.SetPathValue("id", "abc")
	h.RestoreOrphaned(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "status")
}

func TestOrphanedHandler_MoveToInbox(t *testing.T) {
	h, cfg := newOrphanedHandler(t)

	uuid := "550e8400-e29b-41d4-a716-446655440000"
	createOrphanedPDF(t, cfg.Storage.StorageDir, "originals", uuid+".pdf")
	scanCtx := req(t, "POST", "/api/v1/documents/orphaned/scan", nil)
	h.ScanOrphaned(rec(), scanCtx)

	listW := rec()
	h.ListOrphaned(listW, req(t, "GET", "/api/v1/documents/orphaned", nil))
	var files []map[string]any
	json.NewDecoder(listW.Body).Decode(&files)
	id := fmt.Sprintf("%.0f", files[0]["id"].(float64))

	w := rec()
	r := req(t, "POST", "/api/v1/documents/orphaned/"+id+"/move-to-inbox", nil)
	r.SetPathValue("id", id)
	h.MoveToInbox(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusAccepted, "status")
}

func TestOrphanedHandler_MoveToInbox_InvalidID(t *testing.T) {
	h, _ := newOrphanedHandler(t)

	w := rec()
	r := req(t, "POST", "/api/v1/documents/orphaned/abc/move-to-inbox", nil)
	r.SetPathValue("id", "abc")
	h.MoveToInbox(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "status")
}

func TestOrphanedHandler_DeleteAllOrphaned(t *testing.T) {
	h, cfg := newOrphanedHandler(t)

	createOrphanedPDF(t, cfg.Storage.StorageDir, "originals", "550e8400-e29b-41d4-a716-446655440000.pdf")
	createOrphanedPDF(t, cfg.Storage.StorageDir, "processed", "6ba7b810-9dad-11d1-80b4-00c04fd430c8.pdf")
	scanCtx := req(t, "POST", "/api/v1/documents/orphaned/scan", nil)
	h.ScanOrphaned(rec(), scanCtx)

	w := rec()
	r := req(t, "POST", "/api/v1/documents/orphaned/delete-all", nil)
	h.DeleteAllOrphaned(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	testutil.AssertEqual(t, result["deleted"], float64(2), "deleted count")
}

func TestOrphanedHandler_MoveAllToInbox(t *testing.T) {
	h, cfg := newOrphanedHandler(t)

	createOrphanedPDF(t, cfg.Storage.StorageDir, "originals", "550e8400-e29b-41d4-a716-446655440000.pdf")
	createOrphanedPDF(t, cfg.Storage.StorageDir, "processed", "6ba7b810-9dad-11d1-80b4-00c04fd430c8.pdf")
	scanCtx := req(t, "POST", "/api/v1/documents/orphaned/scan", nil)
	h.ScanOrphaned(rec(), scanCtx)

	w := rec()
	r := req(t, "POST", "/api/v1/documents/orphaned/move-to-inbox-all", nil)
	h.MoveAllToInbox(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	testutil.AssertEqual(t, result["moved"], float64(2), "moved count")
}

func TestMapOrphanedFile(t *testing.T) {
	f := database.OrphanedFile{
		ID:              1,
		DocumentKey:     "550e8400-e29b-41d4-a716-446655440000",
		DocumentKeyType: "uuid",
		FilePath:        "/tmp/orphaned/550e8400-e29b-41d4-a716-446655440000.pdf",
		OriginalPath:    "originals/550e8400-e29b-41d4-a716-446655440000.pdf",
		SourceDir:       "originals",
		FileSize:        1024,
		MimeType:        "application/pdf",
		Status:          "pending",
	}

	m := mapOrphanedFile(f)
	testutil.AssertEqual(t, m["id"], int64(1), "id")
	testutil.AssertEqual(t, m["document_key"], "550e8400-e29b-41d4-a716-446655440000", "key")
	testutil.AssertEqual(t, m["source_dir"], "originals", "source dir")
	testutil.AssertEqual(t, m["file_size"], int64(1024), "file size")
	testutil.AssertEqual(t, m["mime_type"], "application/pdf", "mime type")
	testutil.AssertEqual(t, m["status"], "pending", "status")

	_, hasDetected := m["detected_at"]
	testutil.AssertEqual(t, hasDetected, false, "no detected_at when null")
}

func TestNewOrphanedHandler(t *testing.T) {
	h, _ := newOrphanedHandler(t)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.logger == nil {
		t.Fatal("expected non-nil logger")
	}
	if h.svc == nil {
		t.Fatal("expected non-nil service")
	}
}
