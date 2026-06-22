package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	types "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/handlers"
	"github.com/wgomg/edub-kushim/internal/cache"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documenttypes"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/people"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/static"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/task"
	taskhandlers "github.com/wgomg/edub-kushim/internal/task/handlers"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	heartbeatInterval = 5 * time.Second
)

type Server struct {
	httpServer *http.Server
	logger     *utils.Logger
	addr       string
	pools      struct {
		consume *pool.Pool
		enrich  *pool.Pool
		config  *pool.Pool
	}
	owner     *task.Owner
	ownerID   string
	heartbeat *task.Heartbeat
	services  *types.CrudServices
}

func NewServer(cfg config.Config, logger *utils.Logger, db *sql.DB) *Server {
	addr := fmt.Sprintf("%s:%d", cfg.Srv.Host, cfg.Srv.Port)

	mux := http.NewServeMux()

	queries := database.NewQueries(db)
	engine := search.NewEngine(logger, db)

	tagCache, err := cache.BuildTagCache(context.Background(), db, logger, cfg.Enricher.TagMatcher)
	if err != nil {
		logger.Error(nil, "failed to build tag cache: %v — continuing with empty cache", err)
		tagCache = cache.New()
		tagCache.Set("tags", cache.NewEmbeddingStore(nil, nil))
	}

	storeIf, _ := tagCache.Get("tags")
	embStore := storeIf.(*cache.EmbeddingStore)

	s := &Server{
		logger:   logger,
		addr:     addr,
		services: &types.CrudServices{},
	}
	ownerID := uuid.New().String()

	workStore := task.NewStore(queries)
	workStore.SetOwnerID(ownerID)
	configStore := task.NewStore(queries)

	s.services.Tag, err = tags.NewTagService(queries, embStore, logger, cfg.Enricher.TagMatcher)
	if err != nil {
		logger.Fatal("tag service: ", err)
	}

	s.services.People = people.NewPeopleService(queries, logger)
	s.services.PeopleType = people.NewPeopleTypeService(queries, logger)
	s.services.DocumentType = documenttypes.NewDocumentTypeService(queries, logger)

	consumer, err := consumption.NewConsumer(&cfg, logger, db)
	if err != nil {
		logger.Fatal("consumer: ", err)
	}

	enricher, err := enrichment.NewEnricher(&cfg, logger, db, s.services)
	if err != nil {
		logger.Fatal("enricher: ", err)
	}

	registry := task.NewRegistry()
	registry.Register("consume", taskhandlers.NewConsumeTaskHandler(consumer, workStore, logger))
	registry.Register("enrich", taskhandlers.NewEnrichTaskHandler(enricher))
	registry.Register("config", taskhandlers.NewConfigTaskHandler(logger))

	dispatcher := task.NewDispatcher(logger, workStore, registry)
	workRunner := task.NewRunner(workStore, registry, logger)
	configRunner := task.NewRunner(configStore, registry, logger)

	consumeWorkers := max(cfg.Consumer.Workers, 1)
	enrichWorkers := max(cfg.Enricher.Workers, 1)

	consumePool := pool.New(logger, workRunner, consumeWorkers, 2*time.Second, "consume")
	enrichPool := pool.New(logger, workRunner, enrichWorkers, 5*time.Second, "enrich")
	configPool := pool.New(logger, configRunner, 1, 5*time.Second, "config")

	s.owner = task.NewOwner(queries, ownerID, os.Getpid(), logger)
	s.heartbeat = task.NewHeartbeat(s.owner, heartbeatInterval, logger)
	s.ownerID = ownerID
	s.pools.consume = consumePool
	s.pools.enrich = enrichPool
	s.pools.config = configPool

	registerRoutes(mux, logger, queries, engine, dispatcher, &cfg, s.owner, s.services)
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

func registerRoutes(mux *http.ServeMux, logger *utils.Logger, queries *database.Queries, engine *search.Engine, dispatcher *task.Dispatcher, cfg *config.Config, owner *task.Owner, services *types.CrudServices) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		handlers.HealthHandler(w, r, logger)
	})

	docHandler := handlers.NewDocumentHandler(queries, logger, engine, services)
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

	consumeHandler := handlers.NewConsumeHandler(cfg, logger, dispatcher, queries, owner)
	mux.HandleFunc("POST /api/v1/consume", consumeHandler.Consume)

	configHandler := handlers.NewConfigHandler(cfg, queries, logger, dispatcher)
	mux.HandleFunc("GET /wizard/config", configHandler.GetConfig)
	mux.HandleFunc("PUT /wizard/config", configHandler.PutConfig)
	mux.HandleFunc("GET /wizard/config/status", configHandler.ConfigStatus)
	mux.HandleFunc("POST /wizard/config/retry", configHandler.RetryFailedConfig)

	taskHandler := handlers.NewTaskHandler(queries, logger, owner)
	mux.HandleFunc("GET /api/v1/tasks", taskHandler.ListTasks)
	mux.HandleFunc("GET /api/v1/tasks/{id}", taskHandler.GetTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/retry", taskHandler.RetryTask)
	mux.HandleFunc("GET /api/v1/batches", taskHandler.ListBatches)
	mux.HandleFunc("GET /api/v1/batches/{id}", taskHandler.GetBatchSummary)
	mux.HandleFunc("POST /api/v1/batches/{id}/retry", taskHandler.RetryBatch)
	mux.HandleFunc("GET /api/v1/summary", taskHandler.GlobalSummary)
	mux.HandleFunc("POST /api/v1/batches/{id}/adopt", taskHandler.AdoptBatch)

	savedSearchHandler := handlers.NewSavedSearchHandler(queries, logger)
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
	if err := s.owner.CleanupCompleted(context.Background()); err != nil {
		s.logger.Error(nil, "cleanup completed batches: %v", err)
	}

	s.pools.consume.Start(context.Background())
	s.pools.enrich.Start(context.Background())
	s.pools.config.Start(context.Background())

	s.heartbeat.Start(context.Background())
	s.logger.Info(nil, "worker pools started (owner %s pid %d)", s.ownerID, os.Getpid())

	s.logger.Info(nil, "Starting HTTP server on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info(nil, "Shutting down HTTP server")

	s.heartbeat.Stop()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.pools.consume.Stop(stopCtx)
	s.pools.enrich.Stop(stopCtx)
	s.pools.config.Stop(stopCtx)

	s.services.Close()

	return nil
}

func (s *Server) Addr() string {
	return s.addr
}
