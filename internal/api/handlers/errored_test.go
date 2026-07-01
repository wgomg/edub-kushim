package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func newErroredHandler(t *testing.T) (*ErroredHandler, *config.Config) {
	t.Helper()
	configDir := t.TempDir()
	cfg := config.DefaultConfig(configDir)
	cfg.Storage.StorageDir = filepath.Join(configDir, "storage")
	os.MkdirAll(cfg.Storage.StorageDir, 0755)

	logger := testutil.NewTestLogger()
	svc := service.NewErroredFiles(cfg, logger)
	h := NewErroredHandler(svc, logger)
	return h, cfg
}

func createErroredTestFile(t *testing.T, dir, name string) {
	t.Helper()
	os.MkdirAll(dir, 0755)
	testutil.CreateTestPDF(t, filepath.Join(dir, name), "error content")
}

func TestErroredHandler_ListErrored_Empty(t *testing.T) {
	h, _ := newErroredHandler(t)
	w := rec()
	r := req(t, "GET", "/api/v1/errored", nil)
	h.ListErrored(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var result []map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	testutil.AssertEqual(t, len(result), 0, "empty list")
}

func TestErroredHandler_ListErrored_WithData(t *testing.T) {
	h, cfg := newErroredHandler(t)

	errorsDir := filepath.Join(cfg.Storage.StorageDir, "errors")
	createErroredTestFile(t, errorsDir, "err1.pdf")

	w := rec()
	r := req(t, "GET", "/api/v1/errored", nil)
	h.ListErrored(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var result []map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	testutil.AssertEqual(t, len(result), 1, "one errored file")
	testutil.AssertEqual(t, result[0]["name"], "err1.pdf", "name")
	testutil.AssertEqual(t, result[0]["subdir"], "errors", "subdir")
}

func TestErroredHandler_DownloadErrored_MissingParams(t *testing.T) {
	h, _ := newErroredHandler(t)

	w := rec()
	r := req(t, "GET", "/api/v1/errored/download", nil)
	h.DownloadErrored(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "missing both params")

	w = rec()
	r = req(t, "GET", "/api/v1/errored/download?subdir=errors", nil)
	h.DownloadErrored(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "missing file param")
}

func TestErroredHandler_DeleteErrored_MissingParams(t *testing.T) {
	h, _ := newErroredHandler(t)

	w := rec()
	r := req(t, "DELETE", "/api/v1/errored", nil)
	h.DeleteErrored(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "missing both params")
}

func TestErroredHandler_DeleteErrored_Success(t *testing.T) {
	h, cfg := newErroredHandler(t)

	errorsDir := filepath.Join(cfg.Storage.StorageDir, "errors")
	createErroredTestFile(t, errorsDir, "delete-me.pdf")

	w := rec()
	r := req(t, "DELETE", "/api/v1/errored?subdir=errors&file=delete-me.pdf", nil)
	h.DeleteErrored(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusNoContent, "deleted")
}

func TestErroredHandler_DeleteErrored_NotFound(t *testing.T) {
	h, _ := newErroredHandler(t)

	w := rec()
	r := req(t, "DELETE", "/api/v1/errored?subdir=errors&file=nonexistent.pdf", nil)
	h.DeleteErrored(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusInternalServerError, "not found")
}

func TestErroredHandler_DeleteAllErrored(t *testing.T) {
	h, cfg := newErroredHandler(t)

	errorsDir := filepath.Join(cfg.Storage.StorageDir, "errors")
	dupesDir := filepath.Join(errorsDir, "duplicated")
	createErroredTestFile(t, errorsDir, "err1.pdf")
	createErroredTestFile(t, dupesDir, "dup1.pdf")

	w := rec()
	r := req(t, "POST", "/api/v1/errored/delete-all", nil)
	h.DeleteAllErrored(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)
	testutil.AssertEqual(t, result["deleted"], float64(2), "deleted count")
}

func TestNewErroredHandler(t *testing.T) {
	h, _ := newErroredHandler(t)
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
