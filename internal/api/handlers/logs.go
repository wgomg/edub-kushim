package handlers

import (
	"bufio"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

var allowedLogNames = map[string]bool{
	"kushim": true,
	"edub":   true,
	"hugot":  true,
	"queue":  true,
}

type LogsHandler struct {
	getConfig func() *config.Config
	logger    *utils.Logger
}

func NewLogsHandler(getConfig func() *config.Config, logger *utils.Logger) *LogsHandler {
	return &LogsHandler{getConfig: getConfig, logger: logger}
}

func (h *LogsHandler) ListLogs(w http.ResponseWriter, r *http.Request) {
	reqID := r.Context().Value("reqid").(string)
	name := r.PathValue("name")
	if !allowedLogNames[name] {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "log not found"})
		return
	}

	lines := 500
	if l := r.URL.Query().Get("lines"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			lines = max(min(n, 5000), 100)
		}
	}

	logPath := filepath.Join(h.getConfig().App.ConfigDir, "logs", name+".log")

	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "log not found"})
			return
		}
		h.logger.Error(&reqID, "open log file %s: %v", logPath, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to read log"})
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		h.logger.Error(&reqID, "stat log file %s: %v", logPath, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to read log"})
		return
	}

	const maxRead = 2 * 1024 * 1024
	var reader io.Reader
	partialFirstLine := false

	if stat.Size() > maxRead {
		offset := stat.Size() - maxRead
		f.Seek(offset, io.SeekStart)
		reader = io.LimitReader(f, maxRead)
		partialFirstLine = true
	} else {
		reader = f
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var allLines []string
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		h.logger.Error(&reqID, "scan log file %s: %v", logPath, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to read log"})
		return
	}

	if partialFirstLine && len(allLines) > 0 {
		allLines = allLines[1:]
	}

	start := int(math.Max(0, float64(len(allLines)-lines)))
	result := allLines[start:]

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"lines": result})
}
