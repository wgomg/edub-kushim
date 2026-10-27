// Package wizard is the setup wizard server: the same config handler the API exposes, served on its own port
// before a configuration exists, with the embedded wizard SPA. Bootstrap prepares the config directory and
// skeleton config; the database is opened only when the operator submits the connection settings, so no
// database operation precedes the schema creation. Once bootstrapped, the wizard is the setup-time dispatcher
// for migrations, executing their handlers in-process while no queue daemon exists yet.
package wizard

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wgomg/edub-kushim/internal/api/handlers"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/configtask"
	"github.com/wgomg/edub-kushim/internal/ops/dispatch"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// dispatchSafetyInterval is the fallback dispatch wake when a notification is missed.
const dispatchSafetyInterval = 30 * time.Second

// inProcessDrainTimeout bounds the shutdown wait for in-process executions, mirroring the pool drain the
// setup wizard used before the dispatcher.
const inProcessDrainTimeout = 30 * time.Second

// setupConnectTimeoutSeconds bounds the bootstrap connect so an unreachable server fails the setup request
// in seconds instead of stalling it.
const setupConnectTimeoutSeconds = 10

// Server is the setup wizard's HTTP server; Start serves the SPA and the
// config endpoints until the process is stopped.
type Server struct {
	httpServer *http.Server
	logger     *log.Logger
	addr       string
	configDir  string
	db         *sql.DB

	servicesMu      sync.Mutex
	servicesStarted bool

	client         *database.Client
	batchSvc       *service.Batch
	dispatcher     *dispatch.Dispatcher
	dispatchNotify chan struct{}
	dispatchCancel context.CancelFunc
	dispatchDone   chan struct{}
}

// NewServer prepares the wizard server on addr.
func NewServer(addr string, logger *log.Logger) *Server {
	return &Server{
		logger: logger,
		addr:   addr,
	}
}

// Start assembles the wizard routes and serves until Shutdown. RunSetup refuses to start it over an existing
// configuration, so the wizard always begins from an empty environment and sets its in-memory config at the
// config-directory step.
func (s *Server) Start() error {
	var cfgPtr atomic.Pointer[config.Config]
	getConfig := func() *config.Config { return cfgPtr.Load() }
	onConfigSet := func(c *config.Config) { cfgPtr.Store(c) }

	configHandler := handlers.NewConfigHandler(getConfig, onConfigSet, nil, s.logger, nil, nil)
	configHandler.OnBootstrap = s.bootstrap
	configHandler.OnDatabaseInit = s.bootstrapServices

	mux := http.NewServeMux()
	mux.HandleFunc("GET /wizard/config", configHandler.GetConfig)
	mux.HandleFunc("PUT /wizard/config", configHandler.PutConfig)
	mux.HandleFunc("GET /wizard/config/status", configHandler.ConfigStatus)
	mux.HandleFunc("POST /wizard/config/retry", configHandler.RetryFailedConfig)
	mux.HandleFunc("POST /wizard/admin-user", configHandler.CreateAdminUser)

	handlers.RegisterStaticRoutes(mux, WebFS())

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

// bootstrap prepares the configuration environment for the wizard: it creates the directories and the
// skeleton config and returns the loaded settings. It never opens the database, so setup stays free of any
// database operation until the operator submits the connection settings.
func (s *Server) bootstrap(configDir string) (*config.Config, error) {
	s.configDir = configDir

	cfg, err := config.LoadOrBootstrap(configDir)
	if err != nil {
		return nil, fmt.Errorf("bootstrap config: %w", err)
	}
	return cfg, nil
}

// bootstrapServices opens the configured database, applies the schema and wires the setup-time dispatcher to
// it. It runs only after the database target was claimed by PrepareFreshDatabase, so adopting an existing
// database here is always adopting the setup's own. One initialization is claimed at a time: a retry that
// arrives while the schema work is in flight, or after it already finished, is refused instead of racing a
// second goose run. The context bounds the schema work; a cancelled run leaves no services bound, so a retry
// resumes it.
func (s *Server) bootstrapServices(ctx context.Context) (*database.Client, *task.Enqueuer, error) {
	s.servicesMu.Lock()
	claimed := s.servicesStarted
	s.servicesStarted = true
	s.servicesMu.Unlock()
	if claimed {
		return nil, nil, fmt.Errorf("database initialization already started")
	}

	client, enqueuer, err := s.initServices(ctx)
	if err != nil {
		s.servicesMu.Lock()
		s.servicesStarted = false
		s.servicesMu.Unlock()
		return nil, nil, err
	}
	return client, enqueuer, nil
}

// initServices performs the one-time wiring bootstrapServices guards: it loads the configuration from disk,
// connects with a bounded connect attempt, applies the schema and starts the setup dispatcher.
func (s *Server) initServices(ctx context.Context) (*database.Client, *task.Enqueuer, error) {
	cfg, err := config.Load(s.configDir)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	dsn := database.WithConnectTimeout(config.BuildPostgresDSN(cfg.Db), setupConnectTimeoutSeconds)
	db, err := database.NewPostgresDB(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}

	if err := database.InitializeSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("initialize schema: %w", err)
	}

	client := database.NewClient(db)
	registry := task.NewRegistry()
	registry.Register(types.Task.Type.MigrateDB, configtask.NewMigrateDBTaskHandler(client.Queries, s.logger))
	registry.Register(types.Task.Type.MigrateStorage, configtask.NewMigrateStorageTaskHandler(client.Queries, s.logger))

	catalog, err := task.NewCatalog(map[types.TaskType]task.TypeWiring{
		types.Task.Type.MigrateDB:      {DedupKey: task.ConstantDedupKey(configtask.DedupKeyMigrateDB)},
		types.Task.Type.MigrateStorage: {DedupKey: task.ConstantDedupKey(configtask.DedupKeyMigrateStorage)},
	})
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("task catalog: %w", err)
	}
	enqueuer := task.NewEnqueuer(client, catalog)

	maxConcurrent := cfg.Srv.MaxConcurrentBatches
	if maxConcurrent < 1 {
		maxConcurrent = 2
	}
	s.db = db
	s.client = client
	s.batchSvc = service.NewBatch(client, cfg.Consumer.Reclaim.MaxRetries)
	s.dispatchNotify = make(chan struct{}, 4)
	s.dispatcher = dispatch.NewDispatcher(catalog, s.logger, types.HostWizard, maxConcurrent, s.wakeDispatcher,
		&dispatch.InProcessRegistration{
			Types: []types.TaskType{types.Task.Type.MigrateDB, types.Task.Type.MigrateStorage},
			Run: dispatch.InProcessRun(task.BatchRunDeps{
				Logger:     s.logger,
				Catalog:    catalog,
				Registry:   registry,
				MaxRetries: int32(cfg.Consumer.Reclaim.MaxRetries),
			}),
		})
	s.startDispatch(cfg)

	return client, enqueuer, nil
}

// wakeDispatcher pokes the dispatch loop once; it is the dispatcher's backoff-expiry callback.
func (s *Server) wakeDispatcher() {
	select {
	case s.dispatchNotify <- struct{}{}:
	default:
	}
}

// startDispatch runs the setup-only dispatcher loop over the bootstrapped client: one listener for batch
// and lock notifications plus a safety poll. The wizard cannot fork kushim, so its registration covers the
// migration types in-process; the queue daemon takes over once it runs.
func (s *Server) startDispatch(cfg *config.Config) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.dispatchCancel = cancel
	s.dispatchDone = done

	go dispatch.ListenForBatchNotifications(ctx, config.BuildPostgresDSN(cfg.Db), s.dispatchNotify, s.logger)

	go func() {
		defer close(done)
		safetyTimer := time.NewTimer(dispatchSafetyInterval)
		defer safetyTimer.Stop()

		dispatchOnce := func() {
			if err := s.dispatcher.Dispatch(ctx, s.client, s.batchSvc); err != nil {
				s.logger.Error("", "wizard dispatch: %v", err)
			}
		}
		dispatchOnce()

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.dispatchNotify:
				dispatchOnce()
				if !safetyTimer.Stop() {
					select {
					case <-safetyTimer.C:
					default:
					}
				}
				safetyTimer.Reset(dispatchSafetyInterval)
			case <-safetyTimer.C:
				dispatchOnce()
				safetyTimer.Reset(dispatchSafetyInterval)
			}
		}
	}()
}

// stopDispatch stops the loop and waits for it.
func (s *Server) stopDispatch() {
	if s.dispatchCancel == nil {
		return
	}
	s.dispatchCancel()
	<-s.dispatchDone
	s.dispatchCancel = nil
	s.dispatchDone = nil
}

// drainInProcess waits, bounded, for the setup-time executions the dispatcher started to release their
// claims before the database closes under them.
func (s *Server) drainInProcess() {
	if s.dispatcher == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), inProcessDrainTimeout)
	defer cancel()
	if err := s.dispatcher.WaitInProcess(ctx); err != nil {
		s.logger.Warn("", "in-process executions did not settle before shutdown: %v", err)
	}
}

// Shutdown drains the HTTP server, stops the setup-time dispatcher, waits for its executions and closes the
// database. Safe before Start.
func (s *Server) Shutdown(ctx context.Context) error {
	s.stopDispatch()

	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			return err
		}
	}

	s.drainInProcess()

	if s.db != nil {
		_ = s.db.Close()
	}

	return nil
}
