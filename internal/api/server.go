package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	types "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/handlers"
	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/configtask"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/static"
	"github.com/wgomg/edub-kushim/internal/tagmatch"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Server struct {
	httpServer    *http.Server
	logger        *utils.Logger
	addr          string
	cfg           atomic.Pointer[config.Config]
	matcherClient *tagmatch.MatcherClient
	services      *types.CrudServices
	configWatcher *config.Watcher
	pools         struct {
		config *pool.Pool
	}
}

func NewServer(cfg config.Config, logger *utils.Logger, db *sql.DB) *Server {
	addr := fmt.Sprintf("%s:%d", cfg.Srv.Host, cfg.Srv.Port)

	if cfg.Srv.SessionSecret == "" {
		secret, err := auth.GenerateSessionSecret()
		if err != nil {
			logger.Fatal("generate session secret: ", err)
		}
		cfg.Srv.SessionSecret = secret
		logger.Info(nil, "WARNING: server.session_secret is empty — generated temporary in-memory secret (sessions lost on restart)")
	}

	client := database.NewClient(db)

	matcherClient := tagmatch.NewMatcherClient(filepath.Join(cfg.App.ConfigDir, "kushim-hugot.sock"), tagmatch.MaxMatchBodyBytes(cfg.Enricher.TagMatcher.ReduceTargetWords))

	initial := new(config.Config)
	*initial = cfg
	s := &Server{
		logger:        logger,
		addr:          addr,
		services:      &types.CrudServices{},
		matcherClient: matcherClient,
	}
	s.cfg.Store(initial)

	tagSvc, err := service.NewTag(client.Queries, logger, matcherClient)
	if err != nil {
		logger.Fatal("tag service: ", err)
	}
	s.services.Tag = tagSvc

	s.services.Batch = service.NewBatch(client, cfg.Consumer.Reclaim.MaxRetries)
	s.services.People = service.NewPeople(client.Queries, logger)
	s.services.PeopleType = service.NewPeopleType(client.Queries, logger)
	s.services.DocumentType = service.NewDocumentType(client.Queries, logger)
	s.services.User = service.NewUser(client.Queries)

	workStore := task.NewStore(client.Queries)
	configStore := task.NewStore(client.Queries)

	s.services.Orphaned = service.NewOrphaned(client.Queries, &cfg, logger, workStore, s.services.Batch)
	s.services.Orphaned.ScanAndQuarantineAsync()

	s.services.ReEnrich = service.NewReEnrich(client.Queries, workStore, s.services.Batch)

	s.services.ErroredFiles = service.NewErroredFiles(&cfg, logger)

	registry := task.NewRegistry()
	registry.Register("config", configtask.NewConfigTaskHandler(logger))

	dispatcher := task.NewDispatcher(logger, workStore, registry)
	configRunner := task.NewRunner(configStore, registry, logger)

	s.pools.config = pool.New(logger, configRunner, 1, 5*time.Second, "config")

	onConfigSet := func(cfg *config.Config) {
		if cfg.Srv.SessionSecret == "" {
			cfg.Srv.SessionSecret = s.cfg.Load().Srv.SessionSecret
		}
		s.cfg.Store(cfg)
	}

	getConfigFn := func() *config.Config { return s.cfg.Load() }

	mux := registerRoutes(logger, client, dispatcher, getConfigFn, onConfigSet, s.services, workStore)
	registerStaticRoutes(mux)

	getSecret := func() string { return s.cfg.Load().Srv.SessionSecret }
	getAuthEnabled := func() bool { return s.cfg.Load().Srv.AuthEnabled }
	validateAPIKey := func(ctx context.Context, rawKey string) (*database.User, error) {
		u, err := s.services.User.ValidateAPIKey(ctx, rawKey)
		if err != nil {
			return nil, err
		}
		return &u, nil
	}
	getUserByID := func(ctx context.Context, id int64) (*database.User, error) {
		u, err := s.services.User.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return &u, nil
	}
	handler := chainMiddleware(logger, getSecret, getAuthEnabled, validateAPIKey, getUserByID, mux)

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  cfg.Srv.ReadTimeout,
		WriteTimeout: cfg.Srv.WriteTimeout,
		IdleTimeout:  cfg.Srv.IdleTimeout,
	}

	return s
}

func registerStaticRoutes(mux *http.ServeMux) {
	fsys := static.WebFS()
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

func registerRoutes(
	logger *utils.Logger,
	client *database.Client,
	dispatcher *task.Dispatcher,
	getConfig func() *config.Config,
	onConfigSet func(*config.Config),
	services *types.CrudServices,
	workStore *task.Store,
) *http.ServeMux {
	mux := http.NewServeMux()
	engine := search.NewEngine(logger, client.Queries)

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		handlers.HealthHandler(w, r, logger)
	})

	authHandler := handlers.NewAuthHandler(services.User, getConfig, logger)
	mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
	mux.HandleFunc("POST /api/v1/auth/logout", authHandler.Logout)

	docHandler := handlers.NewDocumentHandler(client, logger, engine, services, getConfig)
	viewer := []auth.Role{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin}
	editor := []auth.Role{auth.RoleEditor, auth.RoleAdmin}
	admin := []auth.Role{auth.RoleAdmin}
	mux.Handle("GET /api/v1/documents", RequireRole(viewer...)(http.HandlerFunc(docHandler.ListDocuments)))
	mux.Handle("GET /api/v1/documents/{id}", RequireRole(viewer...)(http.HandlerFunc(docHandler.GetDocument)))
	mux.Handle("GET /api/v1/documents/{id}/file", RequireRole(viewer...)(http.HandlerFunc(docHandler.GetDocumentFile)))
	mux.Handle("GET /api/v1/documents/search", RequireRole(viewer...)(http.HandlerFunc(docHandler.SearchDocuments)))
	mux.Handle("POST /api/v1/documents/search", RequireRole(viewer...)(http.HandlerFunc(docHandler.SearchDocumentsStructured)))
	mux.Handle("PUT /api/v1/documents/{id}", RequireRole(editor...)(http.HandlerFunc(docHandler.UpdateDocument)))
	mux.Handle("DELETE /api/v1/documents/{id}", RequireRole(editor...)(http.HandlerFunc(docHandler.DeleteDocument)))
	mux.Handle("POST /api/v1/documents/{id}/reenrich", RequireRole(editor...)(http.HandlerFunc(docHandler.ReEnrich)))
	mux.Handle("POST /api/v1/documents/{id}/tags", RequireRole(editor...)(http.HandlerFunc(docHandler.AddDocumentTag)))
	mux.Handle("DELETE /api/v1/documents/{id}/tags", RequireRole(editor...)(http.HandlerFunc(docHandler.RemoveDocumentTag)))
	mux.Handle("POST /api/v1/documents/{id}/people", RequireRole(editor...)(http.HandlerFunc(docHandler.AddDocumentPeople)))
	mux.Handle("DELETE /api/v1/documents/{id}/people", RequireRole(editor...)(http.HandlerFunc(docHandler.RemoveDocumentPeople)))
	mux.Handle("POST /api/v1/documents/download", RequireRole(viewer...)(http.HandlerFunc(docHandler.DownloadDocuments)))
	mux.Handle("POST /api/v1/documents/batch-delete", RequireRole(editor...)(http.HandlerFunc(docHandler.BatchDeleteDocuments)))
	mux.Handle("POST /api/v1/documents/batch-tags", RequireRole(editor...)(http.HandlerFunc(docHandler.BatchAssignTags)))
	mux.Handle("GET /api/v1/filter-languages", RequireRole(viewer...)(http.HandlerFunc(docHandler.FilterLanguages)))
	mux.Handle("GET /api/v1/filter-mime-types", RequireRole(viewer...)(http.HandlerFunc(docHandler.FilterMimeTypes)))

	orphanedHandler := handlers.NewOrphanedHandler(services.Orphaned, logger)
	mux.Handle("GET /api/v1/orphaned", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.ListOrphaned)))
	mux.Handle("POST /api/v1/orphaned/scan", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.ScanOrphaned)))
	mux.Handle("DELETE /api/v1/orphaned/{id}", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.DeleteOrphaned)))
	mux.Handle("POST /api/v1/orphaned/{id}/restore", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.RestoreOrphaned)))
	mux.Handle("POST /api/v1/orphaned/{id}/move-to-inbox", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.MoveToInbox)))
	mux.Handle("POST /api/v1/orphaned/delete-all", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.DeleteAllOrphaned)))
	mux.Handle("POST /api/v1/orphaned/move-to-inbox-all", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.MoveAllToInbox)))

	erroredHandler := handlers.NewErroredHandler(services.ErroredFiles, logger)
	mux.Handle("GET /api/v1/errored", RequireRole(editor...)(http.HandlerFunc(erroredHandler.ListErrored)))
	mux.Handle("GET /api/v1/errored/download", RequireRole(editor...)(http.HandlerFunc(erroredHandler.DownloadErrored)))
	mux.Handle("DELETE /api/v1/errored", RequireRole(editor...)(http.HandlerFunc(erroredHandler.DeleteErrored)))
	mux.Handle("POST /api/v1/errored/delete-all", RequireRole(editor...)(http.HandlerFunc(erroredHandler.DeleteAllErrored)))

	tagHandler := handlers.NewTagHandler(services, logger)
	mux.Handle("GET /api/v1/tags", RequireRole(viewer...)(http.HandlerFunc(tagHandler.List)))
	mux.Handle("POST /api/v1/tags", RequireRole(editor...)(http.HandlerFunc(tagHandler.Create)))
	mux.Handle("PUT /api/v1/tags/{id}", RequireRole(editor...)(http.HandlerFunc(tagHandler.Update)))
	mux.Handle("DELETE /api/v1/tags/{id}", RequireRole(editor...)(http.HandlerFunc(tagHandler.Delete)))

	peopleHandler := handlers.NewPeopleHandler(services, logger)
	mux.Handle("GET /api/v1/people", RequireRole(viewer...)(http.HandlerFunc(peopleHandler.List)))
	mux.Handle("POST /api/v1/people", RequireRole(editor...)(http.HandlerFunc(peopleHandler.Create)))
	mux.Handle("PUT /api/v1/people/{id}", RequireRole(editor...)(http.HandlerFunc(peopleHandler.Update)))
	mux.Handle("DELETE /api/v1/people/{id}", RequireRole(editor...)(http.HandlerFunc(peopleHandler.Delete)))
	mux.Handle("GET /api/v1/people-types", RequireRole(viewer...)(http.HandlerFunc(peopleHandler.ListPeopleTypes)))
	mux.Handle("POST /api/v1/people-types", RequireRole(editor...)(http.HandlerFunc(peopleHandler.CreatePeopleType)))
	mux.Handle("PUT /api/v1/people-types/{id}", RequireRole(editor...)(http.HandlerFunc(peopleHandler.UpdatePeopleType)))
	mux.Handle("DELETE /api/v1/people-types/{id}", RequireRole(editor...)(http.HandlerFunc(peopleHandler.DeletePeopleType)))

	docTypeHandler := handlers.NewDocumentTypeHandler(services, logger)
	mux.Handle("GET /api/v1/document-types", RequireRole(viewer...)(http.HandlerFunc(docTypeHandler.List)))
	mux.Handle("POST /api/v1/document-types", RequireRole(editor...)(http.HandlerFunc(docTypeHandler.Create)))
	mux.Handle("PUT /api/v1/document-types/{id}", RequireRole(editor...)(http.HandlerFunc(docTypeHandler.Update)))
	mux.Handle("DELETE /api/v1/document-types/{id}", RequireRole(editor...)(http.HandlerFunc(docTypeHandler.Delete)))

	consumeHandler := handlers.NewConsumeHandler(getConfig, logger, workStore, client.Queries, services)
	mux.Handle("POST /api/v1/consume", RequireRole(editor...)(http.HandlerFunc(consumeHandler.Consume)))
	mux.Handle("POST /api/v1/consume/upload", RequireRole(editor...)(http.HandlerFunc(consumeHandler.Upload)))

	userHandler := handlers.NewUserHandler(services, logger)
	mux.Handle("GET /api/v1/users", RequireRole(admin...)(http.HandlerFunc(userHandler.List)))
	mux.Handle("GET /api/v1/users/{id}", RequireRole(admin...)(http.HandlerFunc(userHandler.Get)))
	mux.Handle("POST /api/v1/users", RequireRole(admin...)(http.HandlerFunc(userHandler.Create)))
	mux.Handle("PUT /api/v1/users/{id}", RequireRole(admin...)(http.HandlerFunc(userHandler.Update)))
	mux.Handle("DELETE /api/v1/users/{id}", RequireRole(admin...)(http.HandlerFunc(userHandler.Delete)))

	apiKeyHandler := handlers.NewAPIKeyHandler(services.User, logger)
	mux.Handle("POST /api/v1/users/{id}/api-key", RequireRole(admin...)(http.HandlerFunc(apiKeyHandler.GenerateKey)))
	mux.Handle("DELETE /api/v1/users/{id}/api-key", RequireRole(admin...)(http.HandlerFunc(apiKeyHandler.RevokeKey)))
	mux.Handle("PUT /api/v1/users/{id}/api-key", RequireRole(admin...)(http.HandlerFunc(apiKeyHandler.RotateKey)))
	mux.Handle("GET /api/v1/users/{id}/api-key", RequireRole(admin...)(http.HandlerFunc(apiKeyHandler.GetKeyStatus)))

	configHandler := handlers.NewConfigHandler(getConfig, onConfigSet, client.Queries, logger, dispatcher, services)
	mux.HandleFunc("GET /wizard/config", configHandler.GetConfig)
	mux.HandleFunc("PUT /wizard/config", configHandler.PutConfig)
	mux.HandleFunc("GET /wizard/config/status", configHandler.ConfigStatus)
	mux.HandleFunc("POST /wizard/config/retry", configHandler.RetryFailedConfig)

	taskHandler := handlers.NewTaskHandler(services, client.Queries, logger, getConfig)
	mux.Handle("GET /api/v1/tasks", RequireRole(viewer...)(http.HandlerFunc(taskHandler.ListTasks)))
	mux.Handle("GET /api/v1/tasks/{id}", RequireRole(viewer...)(http.HandlerFunc(taskHandler.GetTask)))
	mux.Handle("POST /api/v1/tasks/{id}/retry", RequireRole(editor...)(http.HandlerFunc(taskHandler.RetryTask)))
	mux.Handle("GET /api/v1/dashboard", RequireRole(viewer...)(http.HandlerFunc(taskHandler.GetDashboard)))
	mux.Handle("GET /api/v1/batches", RequireRole(viewer...)(http.HandlerFunc(taskHandler.ListBatches)))
	mux.Handle("GET /api/v1/batches/{id}", RequireRole(viewer...)(http.HandlerFunc(taskHandler.GetBatchSummary)))
	mux.Handle("POST /api/v1/batches/{id}/retry", RequireRole(editor...)(http.HandlerFunc(taskHandler.RetryBatch)))
	mux.Handle("POST /api/v1/batches/{id}/resume", RequireRole(editor...)(http.HandlerFunc(taskHandler.ResumeBatch)))
	mux.Handle("POST /api/v1/batches/{id}/cancel", RequireRole(editor...)(http.HandlerFunc(taskHandler.CancelBatch)))

	savedSearchHandler := handlers.NewSavedSearchHandler(client.Queries, logger)
	mux.Handle("GET /api/v1/saved-searches", RequireRole(viewer...)(http.HandlerFunc(savedSearchHandler.List)))
	mux.Handle("POST /api/v1/saved-searches", RequireRole(editor...)(http.HandlerFunc(savedSearchHandler.Create)))
	mux.Handle("DELETE /api/v1/saved-searches/{id}", RequireRole(editor...)(http.HandlerFunc(savedSearchHandler.Delete)))

	logsHandler := handlers.NewLogsHandler(getConfig, logger)
	mux.Handle("GET /api/v1/logs/{name}", RequireRole(admin...)(http.HandlerFunc(logsHandler.ListLogs)))

	// Self-service /me routes (authenticated user only, role-independent)
	mux.Handle("GET /api/v1/me", RequireRole(viewer...)(http.HandlerFunc(authHandler.MeHandler)))
	mux.Handle("POST /api/v1/me/api-key", RequireRole(viewer...)(http.HandlerFunc(apiKeyHandler.MeGenerateKey)))
	mux.Handle("DELETE /api/v1/me/api-key", RequireRole(viewer...)(http.HandlerFunc(apiKeyHandler.MeRevokeKey)))
	mux.Handle("PUT /api/v1/me/api-key", RequireRole(viewer...)(http.HandlerFunc(apiKeyHandler.MeRotateKey)))
	mux.Handle("GET /api/v1/me/api-key", RequireRole(viewer...)(http.HandlerFunc(apiKeyHandler.MeGetKeyStatus)))

	return mux
}

func chainMiddleware(logger *utils.Logger, getSecret func() string, getAuthEnabled func() bool, validateAPIKey func(ctx context.Context, rawKey string) (*database.User, error), getUserByID func(ctx context.Context, id int64) (*database.User, error), h http.Handler) http.Handler {
	return requestMiddleware(logger, AuthMiddleware(parambagMiddleware(h), getSecret, getAuthEnabled, validateAPIKey, getUserByID))
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
	s.probeMatcher()

	s.pools.config.Start(context.Background())
	s.logger.Info(nil, "config pool started")

	s.configWatcher = config.NewWatcher(
		s.cfg.Load().App.ConfigDir,
		5*time.Second,
		func(cfg *config.Config) {
			if cfg.Srv.SessionSecret == "" {
				cfg.Srv.SessionSecret = s.cfg.Load().Srv.SessionSecret
			}
			s.cfg.Store(cfg)
		},
		s.logger,
	)
	s.configWatcher.Start()
	s.logger.Info(nil, "config watcher started (5s interval)")

	s.logger.Info(nil, "Starting HTTP server on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) probeMatcher() {
	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.matcherClient.Health(probeCtx); err != nil {
		s.logger.Error(nil, "WARNING: matcher unavailable — tag CRUD will 503, enrich will fall back to LLM tags: %v", err)
		return
	}

	s.logger.Info(nil, "matcher reachable")
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info(nil, "Shutting down HTTP server")

	s.configWatcher.Stop()
	s.logger.Info(nil, "config watcher stopped")

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.pools.config.Stop(stopCtx)

	s.services.Close()

	return nil
}

func (s *Server) Addr() string {
	return s.addr
}
