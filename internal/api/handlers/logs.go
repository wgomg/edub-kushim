package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

var logLineStart = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\s+(ERROR|FATAL|DEBUG|WARN|INFO)\s*:`)

const maxLogLineBytes = 1024 * 1024

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

	br := bufio.NewReaderSize(reader, 64*1024)

	var allLines []string
	truncated := 0
	for {
		chunk, err := br.ReadBytes('\n')
		if len(chunk) > 0 {
			line := chunk
			if len(line) > 0 && line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if len(line) > maxLogLineBytes {
				truncated += len(line) - maxLogLineBytes
				line = fmt.Appendf(line[:maxLogLineBytes], "\n... [%d bytes truncated]", len(line)-maxLogLineBytes)
			}
			allLines = append(allLines, string(line))
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			h.logger.Error(&reqID, "read log file %s: %v", logPath, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to read log"})
			return
		}
	}

	var merged []string
	for _, line := range allLines {
		if logLineStart.MatchString(line) || len(merged) == 0 {
			merged = append(merged, line)
		} else {
			merged[len(merged)-1] += "\n" + line
		}
	}

	for i := range merged {
		if len(merged[i]) > maxLogLineBytes {
			extra := len(merged[i]) - maxLogLineBytes
			merged[i] = merged[i][:maxLogLineBytes] + fmt.Sprintf("\n... [%d bytes truncated]", extra)
			truncated += extra
		}
	}

	if truncated > 0 {
		h.logger.Debug(&reqID, "log file %s: %d bytes truncated", logPath, truncated)
	}

	if partialFirstLine && len(merged) > 0 {
		merged = merged[1:]
	}

	start := int(math.Max(0, float64(len(merged)-lines)))
	result := merged[start:]

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"lines": result})
}
