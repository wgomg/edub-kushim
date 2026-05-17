package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/api/handlers"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/queue"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Server struct {
	httpServer *http.Server
	logger     *utils.Logger
	addr       string
	queue      *queue.Queue
}

func NewServer(cfg config.Config, logger *utils.Logger, db *sql.DB) *Server {
	addr := fmt.Sprintf("%s:%d", cfg.Srv.Host, cfg.Srv.Port)

	mux := http.NewServeMux()

	queries := database.NewQueries(db)
	engine := search.NewEngine(logger, db)
	consumer := consumption.NewConsumer(&cfg, logger, db)

	// choose a default — one worker is sensible for OCR-heavy workloads
	workers := cfg.Consumer.Workers
	if workers < 1 {
		workers = 1
	}

	consumeHandler := consumption.NewConsumeTaskHandler(consumer)
	taskQueue := queue.New(logger, db, workers, consumeHandler)

	registerRoutes(mux, logger, queries, engine, consumer, taskQueue, &cfg)
	handler := chainMiddleware(logger, mux)

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  cfg.Srv.ReadTimeout,
		WriteTimeout: cfg.Srv.WriteTimeout,
		IdleTimeout:  cfg.Srv.IdleTimeout,
	}

	return &Server{
		httpServer: server,
		logger:     logger,
		addr:       addr,
		queue:      taskQueue,
	}
}

func registerRoutes(mux *http.ServeMux, logger *utils.Logger, queries *database.Queries, engine *search.Engine, consumer *consumption.Consumer, taskQueue *queue.Queue, cfg *config.Config) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		handlers.HealthHandler(w, r, logger)
	})

	docHandler := handlers.NewDocumentHandler(queries, logger, engine)
	mux.HandleFunc("GET /api/v1/documents", docHandler.ListDocuments)
	mux.HandleFunc("GET /api/v1/documents/{id}", docHandler.GetDocument)
	mux.HandleFunc("GET /api/v1/documents/search", docHandler.SearchDocuments)

	consumeHandler := handlers.NewConsumeHandler(consumer, taskQueue, cfg, logger)
	mux.HandleFunc("POST /api/v1/consume", consumeHandler.Consume)

	taskHandler := handlers.NewTaskHandler(queries, logger)
	mux.HandleFunc("GET /api/v1/tasks", taskHandler.ListTasks)
	mux.HandleFunc("GET /api/v1/tasks/{id}", taskHandler.GetTask)
	mux.HandleFunc("GET /api/v1/batches/{id}", taskHandler.GetBatchSummary)
}

func chainMiddleware(logger *utils.Logger, h http.Handler) http.Handler {
	return requestMiddleware(logger, parambagMiddleware(h))
}

func requestMiddleware(logger *utils.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := uuid.New().String()

		ctx := context.WithValue(r.Context(), "reqid", reqID)

		logger.Info(nil, "%s %s REQID=%s", r.Method, r.URL.Path, reqID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func parambagMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pb := utils.NewParamBag(r)
		r = utils.WithParamBag(r, pb)

		next.ServeHTTP(w, r)
	})
}

func (s *Server) Start() error {
	s.queue.Start()
	s.logger.Info(nil, "Starting HTTP server on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info(nil, "Shutting down HTTP server")

	// Stop accepting new requests first, then drain the queue
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.queue.Stop(stopCtx)

	return nil
}

func (s *Server) Addr() string {
	return s.addr
}
