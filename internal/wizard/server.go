package wizard

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/wgomg/edub-kushim/internal/api/handlers"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/configtask"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/task"
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
	var cfgPtr atomic.Pointer[config.Config]
	getConfig := func() *config.Config { return cfgPtr.Load() }
	onConfigSet := func(c *config.Config) { cfgPtr.Store(c) }

	configHandler := handlers.NewConfigHandler(getConfig, onConfigSet, nil, s.logger, nil, nil)
	configHandler.OnBootstrap = s.bootstrap

	if configDir, err := utils.ConfigDir(); err == nil {
		if config.ConfigExists(*configDir) {
			s.logger.Info(nil, "existing config found at %s — auto-resuming", *configDir)
			if cfg, client, dispatcher, err := s.bootstrap(*configDir); err != nil {
				s.logger.Error(nil, "auto-resume bootstrap: %v", err)
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

	s.logger.Info(nil, "Starting wizard HTTP server on %s", s.addr)
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

	dsn := filepath.Join(cfg.Db.Path, cfg.Db.Name)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open database: %w", err)
	}
	s.db = db

	client := database.NewClient(db)
	store := task.NewStore(client.Queries)
	registry := task.NewRegistry()
	registry.Register("config", configtask.NewConfigTaskHandler(s.logger))

	dispatcher := task.NewDispatcher(s.logger, store, registry)
	runner := task.NewRunner(store, registry, s.logger)
	s.pool = pool.New(s.logger, runner, 1, 5*time.Second, "config")
	s.pool.Start(context.Background())

	return cfg, client, dispatcher, nil
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
