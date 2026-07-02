package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func newLogsHandler(t *testing.T) (*LogsHandler, string) {
	t.Helper()
	configDir := t.TempDir()
	os.MkdirAll(filepath.Join(configDir, "logs"), 0755)
	logger := testutil.NewTestLogger()
	h := NewLogsHandler(func() *config.Config {
		return config.DefaultConfig(configDir)
	}, logger)
	return h, configDir
}

func writeLogFile(t *testing.T, configDir, name string, lines []string) {
	t.Helper()
	logPath := filepath.Join(configDir, "logs", name+".log")
	var content string
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatalf("write log file: %v", err)
	}
}

func TestLogsHandler_ListLogs_InvalidName(t *testing.T) {
	h, _ := newLogsHandler(t)
	for _, name := range []string{"../../etc/passwd", "nonexistent", "kushim.log", ""} {
		t.Run(name, func(t *testing.T) {
			w := rec()
			r := req(t, "GET", "/api/v1/logs/"+name, nil)
			r.SetPathValue("name", name)
			h.ListLogs(w, r)
			testutil.AssertEqual(t, w.Code, http.StatusNotFound, "status")
		})
	}
}

func TestLogsHandler_ListLogs_FileNotFound(t *testing.T) {
	h, _ := newLogsHandler(t)
	w := rec()
	r := req(t, "GET", "/api/v1/logs/kushim", nil)
	r.SetPathValue("name", "kushim")
	h.ListLogs(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusNotFound, "status")

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	testutil.AssertEqual(t, body["error"], "log not found", "error message")
}

func TestLogsHandler_ListLogs_Success(t *testing.T) {
	h, configDir := newLogsHandler(t)
	writeLogFile(t, configDir, "kushim", []string{
		"2026/01/01 10:00:00 INFO  : started",
		"2026/01/01 10:00:01 ERROR : something failed",
		"2026/01/01 10:00:02 INFO  : done",
	})

	w := rec()
	r := req(t, "GET", "/api/v1/logs/kushim", nil)
	r.SetPathValue("name", "kushim")
	h.ListLogs(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	lines := body["lines"].([]any)
	testutil.AssertEqual(t, len(lines), 3, "line count")
	testutil.AssertEqual(t, lines[0], "2026/01/01 10:00:00 INFO  : started", "first line")
	testutil.AssertEqual(t, lines[2], "2026/01/01 10:00:02 INFO  : done", "last line")
}

func TestLogsHandler_ListLogs_LinesClamping(t *testing.T) {
	h, configDir := newLogsHandler(t)

	var logLines []string
	for i := range 200 {
		logLines = append(logLines, fmt.Sprintf("2026/01/01 10:00:00 INFO  : line %d", i))
	}
	writeLogFile(t, configDir, "edub", logLines)

	w := rec()
	r := req(t, "GET", "/api/v1/logs/edub", nil)
	r.SetPathValue("name", "edub")
	h.ListLogs(w, r)
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	testutil.AssertEqual(t, len(body["lines"].([]any)), 200, "default returns all")

	w = rec()
	r = req(t, "GET", "/api/v1/logs/edub?lines=5", nil)
	r.SetPathValue("name", "edub")
	h.ListLogs(w, r)
	json.NewDecoder(w.Body).Decode(&body)
	testutil.AssertEqual(t, len(body["lines"].([]any)), 100, "below floor → 100")

	w = rec()
	r = req(t, "GET", "/api/v1/logs/edub?lines=150", nil)
	r.SetPathValue("name", "edub")
	h.ListLogs(w, r)
	json.NewDecoder(w.Body).Decode(&body)
	testutil.AssertEqual(t, len(body["lines"].([]any)), 150, "exact 150")

	w = rec()
	r = req(t, "GET", "/api/v1/logs/edub?lines=10000", nil)
	r.SetPathValue("name", "edub")
	h.ListLogs(w, r)
	json.NewDecoder(w.Body).Decode(&body)
	testutil.AssertEqual(t, len(body["lines"].([]any)), 200, "above ceiling → 500, file has 200")
}

func TestLogsHandler_ListLogs_LargeFileTail(t *testing.T) {
	h, configDir := newLogsHandler(t)

	var logLines []string
	for i := range 50000 {
		logLines = append(logLines, fmt.Sprintf("2026/01/01 10:00:00 INFO  : line %06d: %s", i, strings.Repeat("x", 50)))
	}
	writeLogFile(t, configDir, "queue", logLines)

	w := rec()
	r := req(t, "GET", "/api/v1/logs/queue?lines=500", nil)
	r.SetPathValue("name", "queue")
	h.ListLogs(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	lines := body["lines"].([]any)
	testutil.AssertEqual(t, len(lines), 500, "last 500 lines from large file")
	lastLine := lines[499].(string)
	testutil.AssertEqual(t, strings.HasPrefix(lastLine, "2026/01/01 10:00:00 INFO  : line 049999:"), true, "last line from end")
}

func TestLogsHandler_ListLogs_EmptyFile(t *testing.T) {
	h, configDir := newLogsHandler(t)
	writeLogFile(t, configDir, "hugot", []string{})

	w := rec()
	r := req(t, "GET", "/api/v1/logs/hugot", nil)
	r.SetPathValue("name", "hugot")
	h.ListLogs(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var body struct {
		Lines []string `json:"lines"`
	}
	json.NewDecoder(w.Body).Decode(&body)
	testutil.AssertEqual(t, len(body.Lines), 0, "empty file")
}
