// Package api is the REST server of edub: routing, the auth middleware
// and role gates, the config watcher driving hot rebuilds of the handler
// set, the matcher client, the config-reload task pool, and the embedded
// SPA.
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
	"github.com/wgomg/edub-kushim/internal/api/handlers"
	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/ops/configtask"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/static"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Server is the edub REST server: it assembles the handler set over the
// current configuration, serves the SPA, and rebuilds the wiring whenever
// the config watcher reports a change.
type Server struct {
	httpServer    *http.Server
	logger        *log.Logger
	addr          string
	cfg           atomic.Pointer[config.Config]
	matcherClient *matcher.MatcherClient
	services      *service.CrudServices
	tag           *curation.Tag
	tagMerge      *curation.TagMerge
	tagIdentity   *curation.TagIdentity
	configWatcher *config.Watcher
	db            *sql.DB
	client        *database.Client
	pools         struct {
		config *task.Pool
	}
}

// NewServer assembles the server. An empty session secret is generated
// in memory (sessions do not survive a restart), the matcher client dials
// the daemon socket, and the initial handler set is built.
func NewServer(cfg config.Config, logger *log.Logger, db *sql.DB) *Server {
	addr := fmt.Sprintf("%s:%d", cfg.Srv.Host, cfg.Srv.Port)

	if cfg.Srv.SessionSecret == "" {
		secret, err := auth.GenerateSessionSecret()
		if err != nil {
			logger.Fatal("generate session secret: ", err)
		}
		cfg.Srv.SessionSecret = secret
		logger.Info("", "WARNING: server.session_secret is empty — generated temporary in-memory secret (sessions lost on restart)")
	}

	matcherClient := matcher.NewMatcherClient(filepath.Join(cfg.App.ConfigDir, "kushim-hugot.sock"), matcher.MaxMatchBodyBytes(cfg.Enricher.TagMatcher.ReduceTargetWords))

	initial := new(config.Config)
	*initial = cfg
	s := &Server{
		logger:        logger,
		addr:          addr,
		matcherClient: matcherClient,
		db:            db,
		client:        database.NewClient(db),
	}
	s.cfg.Store(initial)

	s.rebuild(s.client)

	return s
}

func (s *Server) rebuild(client *database.Client) {
	cfg := s.cfg.Load()

	services := &service.CrudServices{}

	s.tag = curation.NewTag(client, s.logger, s.matcherClient)
	s.tagMerge = curation.NewTagMerge(client, s.matcherClient, s.matcherClient, s.logger)
	services.Batch = service.NewBatch(client, cfg.Consumer.Reclaim.MaxRetries)
	services.People = service.NewPeople(client.Queries, s.logger)
	services.PeopleType = service.NewPeopleType(client.Queries)
	services.DocumentType = service.NewDocumentType(client.Queries)
	services.User = service.NewUser(client.Queries)

	workStore := task.NewStore(client)
	configStore := task.NewStore(client)

	services.Orphaned = service.NewOrphaned(client.Queries, cfg, s.logger, workStore, services.Batch)
	services.Orphaned.ScanAndQuarantineAsync()

	services.Trash = service.NewTrash(client, cfg, s.logger, services.Orphaned)
	services.Trash.StartBackgroundPurge()

	services.ReEnrich = service.NewReEnrich(client.Queries, workStore, services.Batch)
	s.tagIdentity = curation.NewTagIdentity(client.Queries, workStore, services.Batch)
	services.ErroredFiles = service.NewErroredFiles(cfg, s.logger)

	registry := task.NewRegistry()
	registry.Register(types.Task.Type.Config, configtask.NewConfigTaskHandler(client.Queries, s.logger))

	dispatcher := task.NewDispatcher(workStore, registry)
	configRunner := task.NewRunner(configStore, registry, s.logger)

	s.pools.config = task.NewPool(s.logger, configRunner, 1, 5*time.Second, "config")

	onConfigSet := func(cfg *config.Config) {
		if cfg.Srv.SessionSecret == "" {
			cfg.Srv.SessionSecret = s.cfg.Load().Srv.SessionSecret
		}
		s.cfg.Store(cfg)
	}

	getConfigFn := func() *config.Config { return s.cfg.Load() }

	mux := registerRoutes(s.logger, client, dispatcher, getConfigFn, onConfigSet, services, workStore, s.tag, s.tagMerge, s.tagIdentity)
	registerStaticRoutes(mux)

	s.services = services
	s.client = client

	getSecret := func() string { return s.cfg.Load().Srv.SessionSecret }
	getAuthEnabled := func() bool { return s.cfg.Load().Srv.AuthEnabled }
	validateAPIKey := func(ctx context.Context, rawKey string) (*database.User, error) {
		u, err := services.User.ValidateAPIKey(ctx, rawKey)
		if err != nil {
			return nil, err
		}
		return &u, nil
	}
	getUserByID := func(ctx context.Context, id int64) (*database.User, error) {
		u, err := services.User.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return &u, nil
	}
	handler := chainMiddleware(s.logger, getSecret, getAuthEnabled, validateAPIKey, getUserByID, mux)

	if s.httpServer == nil {
		s.httpServer = &http.Server{
			Addr:         s.addr,
			Handler:      handler,
			ReadTimeout:  cfg.Srv.ReadTimeout,
			WriteTimeout: cfg.Srv.WriteTimeout,
			IdleTimeout:  cfg.Srv.IdleTimeout,
		}
	} else {
		s.httpServer.Handler = handler
	}
}

func (s *Server) reconnectDB(newDB *sql.DB) {
	if s.pools.config != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		s.pools.config.Stop(ctx)
		cancel()
	}
	if s.tag != nil {
		s.tag.Close()
	}
	if s.services != nil {
		if err := s.services.Close(); err != nil {
			s.logger.Error("", "close services: %v", err)
		}
	}

	s.rebuild(database.NewClient(newDB))
	s.pools.config.Start(context.Background())
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
	logger *log.Logger,
	client *database.Client,
	dispatcher *task.Dispatcher,
	getConfig func() *config.Config,
	onConfigSet func(*config.Config),
	services *service.CrudServices,
	workStore *task.Store,
	tag *curation.Tag,
	tagMerge *curation.TagMerge,
	tagIdentity *curation.TagIdentity,
) *http.ServeMux {
	mux := http.NewServeMux()
	engine := search.NewEngine(logger, client)

	cfg := getConfig()
	llmRegistry, err := llm.NewRegistry(filepath.Join(cfg.App.ConfigDir, "model_catalog.json"))
	if err != nil {
		logger.Error("", "load model catalog: %v", err)
	}
	if llmRegistry != nil {
		llmHandler := handlers.NewLlmHandler(llmRegistry)
		mux.Handle("GET /api/v1/llm/models", RequireRole([]auth.Role{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin}...)(http.HandlerFunc(llmHandler.ListModels)))
	}

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		handlers.HealthHandler(w, r, logger)
	})

	authHandler := handlers.NewAuthHandler(services.User, getConfig, logger)
	mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
	mux.HandleFunc("POST /api/v1/auth/logout", authHandler.Logout)

	docHandler := handlers.NewDocumentHandler(client, logger, engine, services, tag, getConfig)
	viewer := []auth.Role{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin}
	editor := []auth.Role{auth.RoleEditor, auth.RoleAdmin}
	admin := []auth.Role{auth.RoleAdmin}
	mux.Handle("GET /api/v1/documents", RequireRole(viewer...)(http.HandlerFunc(docHandler.ListDocuments)))
	mux.Handle("GET /api/v1/documents/{id}", RequireRole(viewer...)(http.HandlerFunc(docHandler.GetDocument)))
	mux.Handle("GET /api/v1/documents/{id}/file", RequireRole(viewer...)(http.HandlerFunc(docHandler.GetDocumentFile)))
	mux.Handle("GET /api/v1/documents/{id}/thumbnail", RequireRole(viewer...)(http.HandlerFunc(docHandler.GetDocumentThumbnail)))
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
	mux.Handle("POST /api/v1/documents/batch-type", RequireRole(editor...)(http.HandlerFunc(docHandler.BatchSetDocumentType)))
	mux.Handle("GET /api/v1/filter-languages", RequireRole(viewer...)(http.HandlerFunc(docHandler.FilterLanguages)))
	mux.Handle("GET /api/v1/supported-mime-types", RequireRole(viewer...)(http.HandlerFunc(docHandler.SupportedMimeTypes)))

	orphanedHandler := handlers.NewOrphanedHandler(services.Orphaned, logger)
	mux.Handle("GET /api/v1/orphaned", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.ListOrphaned)))
	mux.Handle("POST /api/v1/orphaned/scan", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.ScanOrphaned)))
	mux.Handle("DELETE /api/v1/orphaned/{id}", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.DeleteOrphaned)))
	mux.Handle("POST /api/v1/orphaned/{id}/restore", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.RestoreOrphaned)))
	mux.Handle("POST /api/v1/orphaned/{id}/move-to-inbox", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.MoveToInbox)))
	mux.Handle("POST /api/v1/orphaned/delete-all", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.DeleteAllOrphaned)))
	mux.Handle("POST /api/v1/orphaned/move-to-inbox-all", RequireRole(editor...)(http.HandlerFunc(orphanedHandler.MoveAllToInbox)))

	trashHandler := handlers.NewTrashHandler(logger, services.Trash, getConfig)
	mux.Handle("GET /api/v1/trash", RequireRole(viewer...)(http.HandlerFunc(trashHandler.ListTrash)))
	mux.Handle("GET /api/v1/trash/{id}", RequireRole(viewer...)(http.HandlerFunc(trashHandler.GetTrashDocument)))
	mux.Handle("POST /api/v1/trash/{id}/restore", RequireRole(editor...)(http.HandlerFunc(trashHandler.RestoreDocument)))
	mux.Handle("DELETE /api/v1/trash/{id}", RequireRole(editor...)(http.HandlerFunc(trashHandler.PermanentlyDeleteDocument)))
	mux.Handle("POST /api/v1/trash/batch-delete", RequireRole(editor...)(http.HandlerFunc(trashHandler.BatchPermanentlyDelete)))
	mux.Handle("POST /api/v1/trash/batch-restore", RequireRole(editor...)(http.HandlerFunc(trashHandler.BatchRestore)))
	mux.Handle("POST /api/v1/trash/purge", RequireRole(editor...)(http.HandlerFunc(trashHandler.PurgeExpired)))

	erroredHandler := handlers.NewErroredHandler(services.ErroredFiles, logger)
	mux.Handle("GET /api/v1/errored", RequireRole(editor...)(http.HandlerFunc(erroredHandler.ListErrored)))
	mux.Handle("GET /api/v1/errored/download", RequireRole(editor...)(http.HandlerFunc(erroredHandler.DownloadErrored)))
	mux.Handle("DELETE /api/v1/errored", RequireRole(editor...)(http.HandlerFunc(erroredHandler.DeleteErrored)))
	mux.Handle("POST /api/v1/errored/delete-all", RequireRole(editor...)(http.HandlerFunc(erroredHandler.DeleteAllErrored)))

	taskHandler := handlers.NewTaskHandler(services, client, logger, getConfig)

	tagHandler := handlers.NewTagHandler(tag, tagMerge, tagIdentity, logger)
	mux.Handle("GET /api/v1/tags", RequireRole(viewer...)(http.HandlerFunc(tagHandler.List)))
	mux.Handle("GET /api/v1/tags/deleted", RequireRole(editor...)(http.HandlerFunc(tagHandler.ListDeleted)))
	mux.Handle("GET /api/v1/tags/provisional", RequireRole(viewer...)(http.HandlerFunc(tagHandler.ListProvisional)))
	mux.Handle("GET /api/v1/tags/merge", RequireRole(viewer...)(http.HandlerFunc(tagHandler.MergeSuggestions)))
	mux.Handle("POST /api/v1/tags/merge/dismissals", RequireRole(editor...)(http.HandlerFunc(tagHandler.DismissMergePair)))
	mux.Handle("DELETE /api/v1/tags/merge/dismissals", RequireRole(editor...)(http.HandlerFunc(tagHandler.RestoreMergePair)))
	mux.Handle("GET /api/v1/tags/reconciliation/report", RequireRole(viewer...)(http.HandlerFunc(taskHandler.GetTagReconcileReport)))
	mux.Handle("POST /api/v1/tags", RequireRole(editor...)(http.HandlerFunc(tagHandler.Create)))
	mux.Handle("PUT /api/v1/tags/{id}", RequireRole(editor...)(http.HandlerFunc(tagHandler.Update)))
	mux.Handle("DELETE /api/v1/tags/{id}", RequireRole(editor...)(http.HandlerFunc(tagHandler.Delete)))
	mux.Handle("DELETE /api/v1/tags/deleted/{name}", RequireRole(editor...)(http.HandlerFunc(tagHandler.RestoreDeleted)))
	mux.Handle("POST /api/v1/tags/{id}/merge", RequireRole(editor...)(http.HandlerFunc(tagHandler.Merge)))
	mux.Handle("POST /api/v1/tags/identity/reevaluate", RequireRole(editor...)(http.HandlerFunc(tagHandler.ReevaluateIdentity)))
	mux.Handle("POST /api/v1/tags/{id}/promote", RequireRole(editor...)(http.HandlerFunc(tagHandler.PromoteTag)))

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

	configHandler := handlers.NewConfigHandler(getConfig, onConfigSet, client, logger, dispatcher, services)
	mux.HandleFunc("GET /wizard/bootstrap", configHandler.Bootstrap)
	mux.Handle("GET /wizard/config", RequireRole(admin...)(http.HandlerFunc(configHandler.GetConfig)))
	mux.Handle("PUT /wizard/config", RequireRole(admin...)(http.HandlerFunc(configHandler.PutConfig)))
	mux.Handle("GET /wizard/config/status", RequireRole(admin...)(http.HandlerFunc(configHandler.ConfigStatus)))
	mux.Handle("POST /wizard/config/retry", RequireRole(admin...)(http.HandlerFunc(configHandler.RetryFailedConfig)))

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

func chainMiddleware(logger *log.Logger, getSecret func() string, getAuthEnabled func() bool, validateAPIKey func(ctx context.Context, rawKey string) (*database.User, error), getUserByID func(ctx context.Context, id int64) (*database.User, error), h http.Handler) http.Handler {
	return requestMiddleware(logger, AuthMiddleware(parambagMiddleware(h), getSecret, getAuthEnabled, validateAPIKey, getUserByID))
}

func requestMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := uuid.New().String()

		ctx := context.WithValue(r.Context(), "reqid", reqID)

		logger.Info("", "%s %s REQID=%s", r.Method, r.URL.Path, reqID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func parambagMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pb := handlers.NewParamBag(r)
		r = handlers.WithParamBag(r, pb)

		next.ServeHTTP(w, r)
	})
}

// Start probes the matcher (tag endpoints will 503 without it), starts
// the config-reload pool and watcher, and serves until Shutdown or an
// accept error.
func (s *Server) Start() error {
	s.probeMatcher()

	s.pools.config.Start(context.Background())
	s.logger.Info("", "config pool started")

	s.configWatcher = config.NewWatcher(
		s.cfg.Load().App.ConfigDir,
		5*time.Second,
		s.onConfigReload,
		s.logger,
	)
	s.configWatcher.Start()
	s.logger.Info("", "config watcher started (5s interval)")

	s.logger.Info("", "Starting HTTP server on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) onConfigReload(cfg *config.Config) {
	if cfg.Srv.SessionSecret == "" {
		cfg.Srv.SessionSecret = s.cfg.Load().Srv.SessionSecret
	}

	oldCfg := s.cfg.Load()
	s.cfg.Store(cfg)

	if !config.DatabaseConnectionChanged(oldCfg.Db, cfg.Db) {
		return
	}

	s.logger.Info("", "DB config changed, reconnecting...")
	newDB, err := database.NewPostgresDB(config.BuildPostgresDSN(cfg.Db))
	if err != nil {
		s.logger.Error("", "failed to connect to new DB: %v — keeping previous configuration", err)
		s.cfg.Store(oldCfg)
		return
	}

	s.reconnectDB(newDB)
	if oldDB := s.db; oldDB != nil {
		_ = oldDB.Close()
	}
	s.db = newDB
	s.logger.Info("", "reconnected to new database")
}

func (s *Server) probeMatcher() {
	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.matcherClient.Health(probeCtx); err != nil {
		s.logger.Error("", "WARNING: matcher unavailable — tag CRUD will 503, enrich will fall back to LLM tags: %v", err)
		return
	}

	s.logger.Info("", "matcher reachable")
}

// Shutdown stops the watcher, drains the HTTP server and the pools under
// the caller's deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("", "Shutting down HTTP server")

	s.configWatcher.Stop()
	s.logger.Info("", "config watcher stopped")

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.pools.config.Stop(stopCtx)

	if s.tag != nil {
		s.tag.Close()
	}
	if err := s.services.Close(); err != nil {
		s.logger.Error("", "close services: %v", err)
	}

	return nil
}

// Addr returns the bound address.
func (s *Server) Addr() string {
	return s.addr
}
