package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	types "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/handlers"
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
	matcherClient *tagmatch.MatcherClient
	services      *types.CrudServices
	pools         struct {
		config *pool.Pool
	}
}

func NewServer(cfg config.Config, logger *utils.Logger, db *sql.DB) *Server {
	addr := fmt.Sprintf("%s:%d", cfg.Srv.Host, cfg.Srv.Port)

	mux := http.NewServeMux()

	client := database.NewClient(db)
	engine := search.NewEngine(logger, client.Queries)

	matcherClient := tagmatch.NewMatcherClient(filepath.Join(cfg.App.ConfigDir, "kushim-hugot.sock"))

	s := &Server{
		logger:        logger,
		addr:          addr,
		services:      &types.CrudServices{},
		matcherClient: matcherClient,
	}

	tagSvc, err := service.NewTag(client.Queries, logger, matcherClient)
	if err != nil {
		logger.Fatal("tag service: ", err)
	}
	s.services.Tag = tagSvc

	s.services.People = service.NewPeople(client.Queries, logger)
	s.services.PeopleType = service.NewPeopleType(client.Queries, logger)
	s.services.DocumentType = service.NewDocumentType(client.Queries, logger)
	s.services.User = service.NewUser(client.Queries)

	workStore := task.NewStore(client.Queries)
	configStore := task.NewStore(client.Queries)

	registry := task.NewRegistry()
	registry.Register("config", configtask.NewConfigTaskHandler(logger))

	dispatcher := task.NewDispatcher(logger, workStore, registry)
	configRunner := task.NewRunner(configStore, registry, logger)

	s.pools.config = pool.New(logger, configRunner, 1, 5*time.Second, "config")

	semaphore := pool.NewSemaphore(max(cfg.Srv.MaxConcurrentBatches, 2))

	registerRoutes(mux, logger, client, engine, dispatcher, &cfg, s.services, semaphore, workStore)
	registerStaticRoutes(mux)

	handler := chainMiddleware(logger, mux)

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

func registerRoutes(mux *http.ServeMux, logger *utils.Logger, client *database.Client, engine *search.Engine, dispatcher *task.Dispatcher, cfg *config.Config, services *types.CrudServices, semaphore *pool.Semaphore, workStore *task.Store) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		handlers.HealthHandler(w, r, logger)
	})

	docHandler := handlers.NewDocumentHandler(client, logger, engine, services, cfg)
	mux.HandleFunc("GET /api/v1/documents", docHandler.ListDocuments)
	mux.HandleFunc("GET /api/v1/documents/{id}", docHandler.GetDocument)
	mux.HandleFunc("GET /api/v1/documents/{id}/file", docHandler.GetDocumentFile)
	mux.HandleFunc("GET /api/v1/documents/search", docHandler.SearchDocuments)
	mux.HandleFunc("POST /api/v1/documents/search", docHandler.SearchDocumentsStructured)
	mux.HandleFunc("PUT /api/v1/documents/{id}", docHandler.UpdateDocument)
	mux.HandleFunc("DELETE /api/v1/documents/{id}", docHandler.DeleteDocument)
	mux.HandleFunc("POST /api/v1/documents/{id}/tags", docHandler.AddDocumentTag)
	mux.HandleFunc("DELETE /api/v1/documents/{id}/tags", docHandler.RemoveDocumentTag)
	mux.HandleFunc("POST /api/v1/documents/{id}/people", docHandler.AddDocumentPeople)
	mux.HandleFunc("DELETE /api/v1/documents/{id}/people", docHandler.RemoveDocumentPeople)
	mux.HandleFunc("POST /api/v1/documents/download", docHandler.DownloadDocuments)
	mux.HandleFunc("POST /api/v1/documents/batch-delete", docHandler.BatchDeleteDocuments)
	mux.HandleFunc("POST /api/v1/documents/batch-tags", docHandler.BatchAssignTags)

	tagHandler := handlers.NewTagHandler(services, logger)
	mux.HandleFunc("GET /api/v1/tags", tagHandler.List)
	mux.HandleFunc("POST /api/v1/tags", tagHandler.Create)
	mux.HandleFunc("PUT /api/v1/tags/{id}", tagHandler.Update)
	mux.HandleFunc("DELETE /api/v1/tags/{id}", tagHandler.Delete)

	peopleHandler := handlers.NewPeopleHandler(services, logger)
	mux.HandleFunc("GET /api/v1/people", peopleHandler.List)
	mux.HandleFunc("POST /api/v1/people", peopleHandler.Create)
	mux.HandleFunc("PUT /api/v1/people/{id}", peopleHandler.Update)
	mux.HandleFunc("DELETE /api/v1/people/{id}", peopleHandler.Delete)
	mux.HandleFunc("GET /api/v1/people-types", peopleHandler.ListPeopleTypes)
	mux.HandleFunc("POST /api/v1/people-types", peopleHandler.CreatePeopleType)
	mux.HandleFunc("PUT /api/v1/people-types/{id}", peopleHandler.UpdatePeopleType)
	mux.HandleFunc("DELETE /api/v1/people-types/{id}", peopleHandler.DeletePeopleType)

	docTypeHandler := handlers.NewDocumentTypeHandler(services, logger)
	mux.HandleFunc("GET /api/v1/document-types", docTypeHandler.List)
	mux.HandleFunc("POST /api/v1/document-types", docTypeHandler.Create)
	mux.HandleFunc("PUT /api/v1/document-types/{id}", docTypeHandler.Update)
	mux.HandleFunc("DELETE /api/v1/document-types/{id}", docTypeHandler.Delete)

	consumeHandler := handlers.NewConsumeHandler(cfg, logger, workStore, client.Queries, semaphore)
	mux.HandleFunc("POST /api/v1/consume", consumeHandler.Consume)
	mux.HandleFunc("POST /api/v1/consume/upload", consumeHandler.Upload)

	userHandler := handlers.NewUserHandler(services, logger)
	mux.HandleFunc("GET /api/v1/users", userHandler.List)
	mux.HandleFunc("GET /api/v1/users/{id}", userHandler.Get)
	mux.HandleFunc("POST /api/v1/users", userHandler.Create)
	mux.HandleFunc("PUT /api/v1/users/{id}", userHandler.Update)
	mux.HandleFunc("DELETE /api/v1/users/{id}", userHandler.Delete)

	configHandler := handlers.NewConfigHandler(cfg, client.Queries, logger, dispatcher)
	mux.HandleFunc("GET /wizard/config", configHandler.GetConfig)
	mux.HandleFunc("PUT /wizard/config", configHandler.PutConfig)
	mux.HandleFunc("GET /wizard/config/status", configHandler.ConfigStatus)
	mux.HandleFunc("POST /wizard/config/retry", configHandler.RetryFailedConfig)

	taskHandler := handlers.NewTaskHandler(client.Queries, logger, cfg)
	mux.HandleFunc("GET /api/v1/tasks", taskHandler.ListTasks)
	mux.HandleFunc("GET /api/v1/tasks/{id}", taskHandler.GetTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/retry", taskHandler.RetryTask)
	mux.HandleFunc("GET /api/v1/dashboard", taskHandler.GetDashboard)
	mux.HandleFunc("GET /api/v1/batches", taskHandler.ListBatches)
	mux.HandleFunc("GET /api/v1/batches/{id}", taskHandler.GetBatchSummary)
	mux.HandleFunc("POST /api/v1/batches/{id}/retry", taskHandler.RetryBatch)
	mux.HandleFunc("POST /api/v1/batches/{id}/resume", taskHandler.ResumeBatch)
	mux.HandleFunc("POST /api/v1/batches/{id}/cancel", taskHandler.CancelBatch)

	savedSearchHandler := handlers.NewSavedSearchHandler(client.Queries, logger)
	mux.HandleFunc("GET /api/v1/saved-searches", savedSearchHandler.List)
	mux.HandleFunc("POST /api/v1/saved-searches", savedSearchHandler.Create)
	mux.HandleFunc("DELETE /api/v1/saved-searches/{id}", savedSearchHandler.Delete)
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
	s.probeMatcher()

	s.pools.config.Start(context.Background())
	s.logger.Info(nil, "config pool started")

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
