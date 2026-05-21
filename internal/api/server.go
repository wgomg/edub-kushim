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
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Server struct {
	httpServer *http.Server
	logger     *utils.Logger
	addr       string
	pool       *pool.Pool
}

func NewServer(cfg config.Config, logger *utils.Logger, db *sql.DB) *Server {
	addr := fmt.Sprintf("%s:%d", cfg.Srv.Host, cfg.Srv.Port)

	mux := http.NewServeMux()

	queries := database.NewQueries(db)
	engine := search.NewEngine(logger, db)

	dispatcher, err := task.NewDispatcher(&cfg, logger, db)
	if err != nil {
		logger.Fatal("dispatcher: ", err)
	}

	workers := cfg.Consumer.Workers
	if workers < 1 {
		workers = 1
	}

	p := pool.New(logger, dispatcher, workers, 2*time.Second)

	registerRoutes(mux, logger, queries, engine, dispatcher, &cfg)

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
		pool:       p,
	}
}

func registerRoutes(mux *http.ServeMux, logger *utils.Logger, queries *database.Queries, engine *search.Engine, dispatcher *task.Dispatcher, cfg *config.Config) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		handlers.HealthHandler(w, r, logger)
	})

	docHandler := handlers.NewDocumentHandler(queries, logger, engine)
	mux.HandleFunc("GET /api/v1/documents", docHandler.ListDocuments)
	mux.HandleFunc("GET /api/v1/documents/{id}", docHandler.GetDocument)
	mux.HandleFunc("GET /api/v1/documents/search", docHandler.SearchDocuments)

	consumeHandler := handlers.NewConsumeHandler(cfg, logger, dispatcher)
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
	s.pool.Start()
	s.logger.Info(nil, "Starting HTTP server on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info(nil, "Shutting down HTTP server")

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.pool.Stop(stopCtx)

	return nil
}

func (s *Server) Addr() string {
	return s.addr
}
