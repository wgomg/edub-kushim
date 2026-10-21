package cli

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
	"strings"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
)

func serveHugotHandler(c *container.Container, args []string) error {
	defaultSocket := c.SocketPath()
	socketPath := defaultSocket

	fp := flags.NewFlagParser(args)
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

	logFile := c.LogFileConfig("hugot.log")
	_ = os.MkdirAll(filepath.Dir(logFile.Path), 0755)
	if err := c.GetLogger().SetLogFile(logFile); err != nil {
		c.GetLogger().Error("", "failed to open hugot log file: %v", err)
	}

	if bgFlag {
		bgArgs := []string{"hugot"}
		if socketPath != defaultSocket {
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

	hugot, err := matcher.NewHugot(c.GetLogger(), c.GetConfig().Enricher.TagMatcher, "tagmatcher")
	if err != nil {
		return fmt.Errorf("tagmatcher: %w", err)
	}
	defer hugot.Close()

	embStore := matcher.NewTagEmbeddingStore(nil)
	if err := matcher.BuildTagCache(context.Background(), client.Queries, hugot, embStore); err != nil {
		c.GetLogger().Error("", "failed to build tag cache: %v — continuing with empty cache", err)
	}
	hugot.SetStore(embStore)

	textCache := matcher.NewTextEmbeddingCache(c.GetLogger(), client.Queries, hugot.EmbeddingFingerprint())
	hugot.SetTextCache(textCache)
	if cleared, clearErr := textCache.ClearStaleGeneration(context.Background()); clearErr != nil {
		c.GetLogger().Warn("", "clear stale matcher text embeddings: %v", clearErr)
	} else if cleared {
		c.GetLogger().Info("", "cleared matcher text embeddings for a new embedding generation")
	}

	bodyCap := matcher.MaxMatchBodyBytes(c.GetConfig().Enricher.TagMatcher.ReduceTargetWords)

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc/v1/encode", handleEncode(hugot, c.GetLogger(), bodyCap))
	mux.HandleFunc("/rpc/v1/match", handleMatch(hugot, c.GetLogger(), bodyCap))
	mux.HandleFunc("/rpc/v1/rank", handleRank(hugot, c.GetLogger(), bodyCap))
	mux.HandleFunc("/rpc/v1/add-to-store", handleAddToStore(hugot, c.GetLogger(), bodyCap))
	mux.HandleFunc("/rpc/v1/remove-from-store", handleRemoveFromStore(hugot, c.GetLogger(), bodyCap))
	mux.HandleFunc("/health", handleHealth)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		var errno syscall.Errno
		if errors.As(err, &errno) && errno == syscall.EADDRINUSE {
			return fmt.Errorf("matcher already running at %s", socketPath)
		}
		return fmt.Errorf("listen unix %s: %w", socketPath, err)
	}

	notifyReady(c.GetLogger())

	writeTimeout := time.Duration(c.GetConfig().Enricher.TagMatcher.Timeout) * time.Second
	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: writeTimeout, // 0 = no deadline
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

	c.GetLogger().Info("", "listening on %s", socketPath)

	var serveError error
	select {
	case <-sigCh:
		c.GetLogger().Info("", "shutting down matcher server...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := server.Shutdown(ctx); err != nil {
			c.GetLogger().Error("", "matcher shutdown: %v", err)
		}
		cancel()
		<-serveErr
	case serveError = <-serveErr:
	}

	_ = os.Remove(socketPath)

	return serveError
}

func notifyReady(logger *log.Logger) {
	socketPath := os.Getenv("NOTIFY_SOCKET")
	if socketPath == "" {
		return
	}
	if strings.HasPrefix(socketPath, "@") {
		socketPath = "\x00" + socketPath[1:]
	}
	addr := &net.UnixAddr{Name: socketPath, Net: "unixgram"}
	conn, err := net.DialUnix("unixgram", nil, addr)
	if err != nil {
		logger.Warn("", "sd_notify connect: %v", err)
		return
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("READY=1\n")); err != nil {
		logger.Warn("", "sd_notify write: %v", err)
	}
}

func respondError(w http.ResponseWriter, logger *log.Logger, err error, status int, msg string) {
	logger.Error("", "matcher: %v", err)
	http.Error(w, msg, status)
}

func handleEncode(h *matcher.Hugot, logger *log.Logger, maxBodyBytes int) http.HandlerFunc {
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
			respondError(w, logger, err, http.StatusBadRequest, "bad request")
			return
		}
		embeddings, err := h.Encode(r.Context(), "", req.Texts)
		if err != nil {
			respondError(w, logger, err, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embeddings})
	}
}

func handleMatch(h *matcher.Hugot, logger *log.Logger, maxBodyBytes int) http.HandlerFunc {
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
			respondError(w, logger, err, http.StatusBadRequest, "bad request")
			return
		}
		matches, err := h.Match(r.Context(), req.DocID, req.Input)
		if err != nil {
			respondError(w, logger, err, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"matches": matches})
	}
}

func handleRank(h *matcher.Hugot, logger *log.Logger, maxBodyBytes int) http.HandlerFunc {
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
			respondError(w, logger, err, http.StatusBadRequest, "bad request")
			return
		}
		results, err := h.Rank(r.Context(), req.DocID, req.Queries)
		if err != nil {
			respondError(w, logger, err, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	}
}

func handleAddToStore(h *matcher.Hugot, logger *log.Logger, maxBodyBytes int) http.HandlerFunc {
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
			respondError(w, logger, err, http.StatusBadRequest, "bad request")
			return
		}
		if err := h.AddToStore(r.Context(), req.Names); err != nil {
			respondError(w, logger, err, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}
}

func handleRemoveFromStore(h *matcher.Hugot, logger *log.Logger, maxBodyBytes int) http.HandlerFunc {
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
			respondError(w, logger, err, http.StatusBadRequest, "bad request")
			return
		}
		if err := h.RemoveFromStore(r.Context(), req.Names); err != nil {
			respondError(w, logger, err, http.StatusInternalServerError, "internal error")
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
