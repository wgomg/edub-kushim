// Package wizard is the setup wizard server: the same config handler the
// API exposes, served on its own port before a configuration exists, with
// the embedded wizard SPA. Bootstrap happens here first; the main server
// then serves the configured instance.
package wizard

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/wgomg/edub-kushim/internal/api/handlers"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/configtask"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Server is the setup wizard's HTTP server; Start serves the SPA and the
// config endpoints until the process is stopped.
type Server struct {
	httpServer *http.Server
	logger     *log.Logger
	addr       string
	pool       *task.Pool
	db         *sql.DB
}

// NewServer prepares the wizard server on addr.
func NewServer(addr string, logger *log.Logger) *Server {
	return &Server{
		logger: logger,
		addr:   addr,
	}
}

// Start assembles the wizard routes, auto-resumes an existing
// configuration if one is found, and serves until Shutdown.
func (s *Server) Start() error {
	var cfgPtr atomic.Pointer[config.Config]
	getConfig := func() *config.Config { return cfgPtr.Load() }
	onConfigSet := func(c *config.Config) { cfgPtr.Store(c) }

	configHandler := handlers.NewConfigHandler(getConfig, onConfigSet, nil, s.logger, nil, nil)
	configHandler.OnBootstrap = s.bootstrap

	if configDir, err := config.ConfigDir(); err == nil {
		if config.ConfigExists(*configDir) {
			s.logger.Info("", "existing config found at %s — auto-resuming", *configDir)
			if cfg, client, dispatcher, err := s.bootstrap(*configDir); err != nil {
				s.logger.Error("", "auto-resume bootstrap: %v", err)
			} else {
				onConfigSet(cfg)
				configHandler.SetServices(client, dispatcher)
			}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /wizard/config", configHandler.GetConfig)
	mux.HandleFunc("PUT /wizard/config", configHandler.PutConfig)
	mux.HandleFunc("GET /wizard/config/status", configHandler.ConfigStatus)
	mux.HandleFunc("POST /wizard/config/retry", configHandler.RetryFailedConfig)
	mux.HandleFunc("POST /wizard/admin-user", configHandler.CreateAdminUser)

	registerStaticRoutes(mux)

	s.httpServer = &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.logger.Info("", "Starting wizard HTTP server on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) bootstrap(configDir string) (*config.Config, *database.Client, *task.Dispatcher, error) {
	if s.db != nil {
		return nil, nil, nil, fmt.Errorf("already bootstrapped")
	}

	cfg, err := config.LoadOrBootstrap(configDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bootstrap config: %w", err)
	}

	db, err := database.NewPostgresDB(config.BuildPostgresDSN(cfg.Db))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open database: %w", err)
	}
	s.db = db

	if err := database.InitializeSchema(db); err != nil {
		return nil, nil, nil, fmt.Errorf("initialize schema: %w", err)
	}

	client := database.NewClient(db)
	store := task.NewStore(client)
	registry := task.NewRegistry()
	registry.Register(types.Task.Type.Config, configtask.NewConfigTaskHandler(client.Queries, s.logger))

	dispatcher := task.NewDispatcher(s.logger, store, registry)
	runner := task.NewRunner(store, registry, s.logger)
	s.pool = task.NewPool(s.logger, runner, 1, 5*time.Second, "config")
	s.pool.Start(context.Background())

	return cfg, client, dispatcher, nil
}

// Shutdown drains the HTTP server, stops the config pool and closes the
// database. Safe before Start.
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
		_ = s.db.Close()
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
		_ = f.Close()

		fileServer.ServeHTTP(w, r)
	})
}
