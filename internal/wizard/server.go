package wizard

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/wgomg/edub-kushim/internal/api/handlers"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/task"
	taskhandlers "github.com/wgomg/edub-kushim/internal/task/handlers"
	"github.com/wgomg/edub-kushim/internal/utils"
	_ "modernc.org/sqlite"
)

type Server struct {
	httpServer *http.Server
	logger     *utils.Logger
	addr       string
	pool       *pool.Pool
	db         *sql.DB
}

func NewServer(addr string, logger *utils.Logger) *Server {
	return &Server{
		logger: logger,
		addr:   addr,
	}
}

func (s *Server) Start() error {
	configHandler := handlers.NewConfigHandler(nil, nil, s.logger, nil)
	configHandler.OnBootstrap = s.bootstrap

	mux := http.NewServeMux()
	mux.HandleFunc("GET /wizard/config", configHandler.GetConfig)
	mux.HandleFunc("PUT /wizard/config", configHandler.PutConfig)
	mux.HandleFunc("GET /wizard/config/status", configHandler.ConfigStatus)

	registerStaticRoutes(mux)

	s.httpServer = &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.logger.Info(nil, "Starting wizard HTTP server on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) bootstrap(configDir string) (*config.Config, *database.Queries, *task.Dispatcher, error) {
	cfg, err := config.Bootstrap(configDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bootstrap config: %w", err)
	}

	dsn := filepath.Join(cfg.Db.Path, cfg.Db.Name)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open database: %w", err)
	}
	s.db = db

	queries := database.NewQueries(db)
	store := task.NewStore(queries)
	registry := task.NewRegistry()
	registry.Register("config", taskhandlers.NewConfigTaskHandler(s.logger))

	dispatcher := task.NewDispatcher(s.logger, store, registry)
	runner := task.NewRunner(store, registry, s.logger)
	s.pool = pool.New(s.logger, runner, 1, 5*time.Second, "config")
	s.pool.Start(context.Background())

	return cfg, queries, dispatcher, nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}

	if s.pool != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.pool.Stop(stopCtx)
	}
	if s.db != nil {
		s.db.Close()
	}

	return nil
}

func registerStaticRoutes(mux *http.ServeMux) {
	fsys := WebFS()
	fileServer := http.FileServer(http.FS(fsys))

	mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		if path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}

		f, err := fsys.Open(path)
		if err != nil {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()

		fileServer.ServeHTTP(w, r)
	})
}
