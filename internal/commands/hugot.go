package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/cache"
	"github.com/wgomg/edub-kushim/internal/tagmatch"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func serveHugotHandler(c *Container, args []string) error {
	socketPath := filepath.Join(c.config.App.ConfigDir, "kushim-hugot.sock")

	fp := NewFlagParser(args)
	if fp.Help("Usage: kushim hugot [--bg] [--socket <path>]") {
		return nil
	}

	bgFlag := false
	fp.Bool("--bg", &bgFlag)

	if err := fp.String("--socket", &socketPath); err != nil {
		return err
	}
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown flag(s): %v", rest)
	}

	logFile := filepath.Join(c.config.App.ConfigDir, "logs", "hugot.log")
	os.MkdirAll(filepath.Dir(logFile), 0755)
	if err := c.logger.SetLogFile(utils.LogFileConfig{
		Path:       logFile,
		MaxSize:    c.config.App.Logging.MaxSize,
		MaxBackups: c.config.App.Logging.MaxBackups,
		MaxAge:     c.config.App.Logging.MaxAge,
		Compress:   c.config.App.Logging.Compress,
	}); err != nil {
		c.logger.Error(nil, "failed to open hugot log file: %v", err)
	}

	if bgFlag {
		bgArgs := []string{"hugot"}
		if socketPath != filepath.Join(c.config.App.ConfigDir, "kushim-hugot.sock") {
			bgArgs = append(bgArgs, "--socket", socketPath)
		}
		cmd := exec.Command(os.Args[0], bgArgs...)
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start background process: %w", err)
		}
		return nil
	}

	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket %s: %w", socketPath, err)
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	hugot, err := tagmatcher.NewHugot(c.logger, c.config.Enricher.TagMatcher, "tagmatcher")
	if err != nil {
		return fmt.Errorf("tagmatcher: %w", err)
	}
	defer hugot.Close()

	embStore := cache.NewEmbeddingStore(nil, nil)
	if err := cache.BuildTagCache(context.Background(), client.Queries, c.logger, hugot, embStore); err != nil {
		c.logger.Error(nil, "failed to build tag cache: %v — continuing with empty cache", err)
	}
	hugot.SetStore(embStore)

	bodyCap := tagmatch.MaxMatchBodyBytes(c.config.Enricher.TagMatcher.ReduceTargetWords)

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc/v1/encode", handleEncode(hugot, c.logger, bodyCap))
	mux.HandleFunc("/rpc/v1/match", handleMatch(hugot, c.logger, bodyCap))
	mux.HandleFunc("/rpc/v1/consolidate", handleConsolidate(hugot, c.logger, bodyCap))
	mux.HandleFunc("/rpc/v1/add-to-store", handleAddToStore(hugot, c.logger, bodyCap))
	mux.HandleFunc("/rpc/v1/remove-from-store", handleRemoveFromStore(hugot, c.logger, bodyCap))
	mux.HandleFunc("/health", handleHealth)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		var errno syscall.Errno
		if errors.As(err, &errno) && errno == syscall.EADDRINUSE {
			return fmt.Errorf("matcher already running at %s", socketPath)
		}
		return fmt.Errorf("listen unix %s: %w", socketPath, err)
	}

	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
		close(serveErr)
	}()

	c.logger.Info(nil, "listening on %s", socketPath)

	var serveError error
	select {
	case <-sigCh:
		c.logger.Info(nil, "shutting down matcher server...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := server.Shutdown(ctx); err != nil {
			c.logger.Error(nil, "matcher shutdown: %v", err)
		}
		cancel()
		<-serveErr
	case serveError = <-serveErr:
	}

	os.Remove(socketPath)

	return serveError
}

func respondError(w http.ResponseWriter, log *utils.Logger, err error, status int, msg string) {
	log.Error(nil, "matcher: %v", err)
	http.Error(w, msg, status)
}

func handleEncode(h *tagmatcher.Hugot, log *utils.Logger, maxBodyBytes int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, log, err, http.StatusBadRequest, "bad request")
			return
		}
		embeddings, err := h.Encode(r.Context(), nil, req.Texts)
		if err != nil {
			respondError(w, log, err, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embeddings})
	}
}

func handleMatch(h *tagmatcher.Hugot, log *utils.Logger, maxBodyBytes int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
		var req struct {
			DocID string `json:"doc_id"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, log, err, http.StatusBadRequest, "bad request")
			return
		}
		matches, err := h.Match(r.Context(), req.DocID, req.Input)
		if err != nil {
			respondError(w, log, err, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"matches": matches})
	}
}

func handleConsolidate(h *tagmatcher.Hugot, log *utils.Logger, maxBodyBytes int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
		var req struct {
			DocID   string   `json:"doc_id"`
			Queries []string `json:"queries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, log, err, http.StatusBadRequest, "bad request")
			return
		}
		results, err := h.Consolidate(r.Context(), req.DocID, req.Queries)
		if err != nil {
			respondError(w, log, err, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	}
}

func handleAddToStore(h *tagmatcher.Hugot, log *utils.Logger, maxBodyBytes int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
		var req struct {
			Names []string `json:"names"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, log, err, http.StatusBadRequest, "bad request")
			return
		}
		if err := h.AddToStore(r.Context(), req.Names); err != nil {
			respondError(w, log, err, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}

func handleRemoveFromStore(h *tagmatcher.Hugot, log *utils.Logger, maxBodyBytes int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
		var req struct {
			Names []string `json:"names"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, log, err, http.StatusBadRequest, "bad request")
			return
		}
		if err := h.RemoveFromStore(r.Context(), req.Names); err != nil {
			respondError(w, log, err, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
