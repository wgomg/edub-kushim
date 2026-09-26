// Package container is the composition root of the kushim process: it
// owns the dynamic configuration, the database connection and reconnect
// protocol, the task dispatcher and runner with the handler registry, the
// pools per task type, and the lazily built services. Everything the CLI
// verbs and the queue consume reads from here.
package container

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documents/consumption"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/ops/configtask"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tags/reconcile"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Container is the composition root: one per process, holding the
// dynamic config pointer and the lazily constructed dependencies.
type Container struct {
	cfg            atomic.Pointer[config.Config]
	logger         *log.Logger
	db             *sql.DB
	client         *database.Client
	engine         *search.Engine
	dispatcher     *task.Dispatcher
	runner         *task.Runner
	store          *task.Store
	services       *service.CrudServices
	tag            *curation.Tag
	merges         *curation.TagMerge
	matcherClient  *matcher.MatcherClient
	tagToolsRunner *tools.Runner
	pools          map[types.TaskType]*task.Pool
}

type poolSpec struct {
	workers  func(*config.Config) int
	interval time.Duration
}

func singleWorker(*config.Config) int { return 1 }

var poolSpecs = map[types.TaskType]poolSpec{
	types.Task.Type.Consume: {
		workers:  func(cfg *config.Config) int { return max(cfg.Consumer.Workers, 1) },
		interval: 2 * time.Second,
	},
	types.Task.Type.Enrich: {
		workers:  func(cfg *config.Config) int { return max(cfg.Enricher.Workers, 1) },
		interval: 5 * time.Second,
	},
	types.Task.Type.Thumbnail: {
		workers:  func(cfg *config.Config) int { return max(cfg.Consumer.Thumbnail.Workers, 1) },
		interval: 2 * time.Second,
	},
	types.Task.Type.ThumbBackfill: {workers: singleWorker, interval: 2 * time.Second},
	types.Task.Type.Config:        {workers: singleWorker, interval: 5 * time.Second},
	types.Task.Type.Backup:        {workers: singleWorker, interval: 60 * time.Second},
	types.Task.Type.Mirror:        {workers: singleWorker, interval: 60 * time.Second},
	types.Task.Type.TagReconcile:  {workers: singleWorker, interval: 60 * time.Second},
	types.Task.Type.TagMeasure:    {workers: singleWorker, interval: 5 * time.Second},
	types.Task.Type.TagIdentity:   {workers: singleWorker, interval: 5 * time.Second},
}

// NewContainer builds a container without a database; the DB attaches
// lazily through GetClient.
func NewContainer(cfg *config.Config, logger *log.Logger) *Container {
	return NewContainerWithDB(cfg, logger, nil)
}

// NewContainerWithDB builds the container over an existing database
// connection.
func NewContainerWithDB(cfg *config.Config, logger *log.Logger, db *sql.DB) *Container {
	c := &Container{
		logger: logger,
		db:     db,
		pools:  make(map[types.TaskType]*task.Pool),
	}
	c.cfg.Store(cfg)
	return c
}

// GetConfig returns the current configuration snapshot.
func (c *Container) GetConfig() *config.Config {
	return c.cfg.Load()
}

// SetConfig swaps the configuration snapshot; new lazy services read it.
func (c *Container) SetConfig(cfg *config.Config) {
	c.cfg.Store(cfg)
}

// GetLogger returns the process logger.
func (c *Container) GetLogger() *log.Logger {
	return c.logger
}

// GetTagService lazily builds the curation tag service, embedding
// through the matcher client; errors when the config lacks a model.
func (c *Container) GetTagService() (*curation.Tag, error) {
	if err := c.ensureDispatcher(); err != nil {
		return nil, err
	}
	return c.tag, nil
}

// GetDB returns the database handle, connecting on first use. The handle
// stays valid across config reloads that keep the same connection.
func (c *Container) GetDB() (*sql.DB, error) {
	if c.db == nil {
		dsn := config.BuildPostgresDSN(c.cfg.Load().Db)
		db, err := database.NewPostgresDB(dsn)
		if err != nil {
			return nil, err
		}
		if err := database.InitializeSchema(db); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize schema: %w", err)
		}
		c.db = db
	}
	return c.db, nil
}

// GetClient returns the query client over the database, building it on
// first use.
func (c *Container) GetClient() (*database.Client, error) {
	if c.client != nil {
		return c.client, nil
	}
	db, err := c.GetDB()
	if err != nil {
		return nil, err
	}
	c.client = database.NewClient(db)
	return c.client, nil
}

// InvalidateDB drops every cached dependency so the next access rebuilds
// from the current configuration.
func (c *Container) InvalidateDB() {
	if c.db != nil {
		_ = c.db.Close()
		c.db = nil
	}
	c.client = nil
	c.dispatcher = nil
	c.runner = nil
	c.engine = nil
	c.store = nil
	c.services = nil
	c.tag = nil
	c.merges = nil
	c.matcherClient = nil
	c.tagToolsRunner = nil
	clear(c.pools)
}

// ReconnectClient closes the current connection and reopens it from the
// config, returning the fresh client.
func (c *Container) ReconnectClient() (*database.Client, error) {
	newDB, err := database.NewPostgresDB(config.BuildPostgresDSN(c.cfg.Load().Db))
	if err != nil {
		return nil, err
	}
	if err := database.InitializeSchema(newDB); err != nil {
		newDB.Close()
		return nil, err
	}
	c.InvalidateDB()
	c.db = newDB
	return c.GetClient()
}

// SocketPath returns the matcher daemon's Unix-socket path under the
// config directory.
func (c *Container) SocketPath() string {
	return filepath.Join(c.cfg.Load().App.ConfigDir, "kushim-hugot.sock")
}

// GetMatcherClient returns the matcher RPC client, with request bodies
// capped at the configured reduce-target size.
func (c *Container) GetMatcherClient() *matcher.MatcherClient {
	if c.matcherClient == nil {
		c.matcherClient = matcher.NewMatcherClient(c.SocketPath(), matcher.MaxMatchBodyBytes(c.cfg.Load().Enricher.TagMatcher.ReduceTargetWords))
	}
	return c.matcherClient
}

// GetTagToolsRunner returns the text reducer, content analyzer, and tag
// matcher tools over the matcher client.
func (c *Container) GetTagToolsRunner() *tools.Runner {
	if c.tagToolsRunner == nil {
		c.tagToolsRunner = tools.NewRunnerWithMatcher(c.logger, c.cfg.Load(), []string{"textreducer", "contentanalyzer", "tagmatcher"}, c.GetMatcherClient())
	}
	return c.tagToolsRunner
}

// LogFileConfig returns the file-log configuration of the named log file
// under the config directory's logs directory.
func (c *Container) LogFileConfig(name string) log.LogFileConfig {
	cfg := c.cfg.Load()
	return log.LogFileConfig{
		Path:       filepath.Join(cfg.App.ConfigDir, "logs", name),
		MaxSize:    cfg.App.Logging.MaxSize,
		MaxBackups: cfg.App.Logging.MaxBackups,
		MaxAge:     cfg.App.Logging.MaxAge,
		Compress:   cfg.App.Logging.Compress,
	}
}

// The dispatcher, runner, store, and tag services are built together so
// GetDispatcher, GetRunner, and GetTagService work in any call order.
func (c *Container) ensureDispatcher() error {
	if c.dispatcher != nil {
		return nil
	}

	client, err := c.GetClient()
	if err != nil {
		return err
	}

	matcherClient := c.GetMatcherClient()

	c.tag = curation.NewTag(client, c.logger, matcherClient)
	c.merges = curation.NewTagMerge(client, matcherClient, matcherClient, c.logger)
	peopleSvc := service.NewPeople(client.Queries, c.logger)
	peopleTypeSvc := service.NewPeopleType(client.Queries)
	docTypeSvc := service.NewDocumentType(client.Queries)

	c.services = &service.CrudServices{
		People: peopleSvc, PeopleType: peopleTypeSvc, DocumentType: docTypeSvc,
	}

	store := task.NewStore(client)
	c.store = store

	consumer, err := consumption.NewConsumer(c.cfg.Load(), c.logger, client)
	if err != nil {
		return err
	}

	enricher, err := enrichment.NewEnricher(c.cfg.Load(), c.logger, client, c.tag, matcherClient)
	if err != nil {
		return err
	}

	registry := task.NewRegistry()
	registry.Register(types.Task.Type.Consume, consumption.NewConsumeTaskHandler(consumer, store, client.Queries, c.logger))
	registry.Register(types.Task.Type.Enrich, enrichment.NewEnrichTaskHandler(enricher, client.Queries, c.logger))
	thumbnailHandler := consumption.NewThumbnailTaskHandler(tools.NewRunner(c.logger, c.cfg.Load(), []string{"thumbnail"}), client.Queries, c.logger, func() *config.Config { return c.cfg.Load() })
	registry.Register(types.Task.Type.Thumbnail, thumbnailHandler)
	registry.Register(types.Task.Type.ThumbBackfill, consumption.NewThumbnailBackfillTaskHandler(thumbnailHandler, client.Queries, c.logger))
	registry.Register(types.Task.Type.Config, configtask.NewConfigTaskHandler(client.Queries, c.logger))
	registry.Register(types.Task.Type.Backup, backup.NewBackupTaskHandler(c.db, client.Queries, func() *config.Config { return c.cfg.Load() }, c.logger))
	registry.Register(types.Task.Type.Mirror, backup.NewMirrorTaskHandler(client.Queries, func() *config.Config { return c.cfg.Load() }, c.logger))
	registry.Register(types.Task.Type.TagReconcile, reconcile.NewTagReconcileTaskHandler(
		reconcile.NewReconciler(
			func() *config.Config { return c.cfg.Load() },
			client,
			c.tag,
			c.merges,
			c.GetTagToolsRunner(),
			c.logger,
		),
		client.Queries,
		c.logger,
	))
	registry.Register(types.Task.Type.TagMeasure, tags.NewTagMeasureTaskHandler(client.Queries, c.logger))
	registry.Register(types.Task.Type.TagIdentity, admission.NewTagIdentityTaskHandler(client.Queries, c.logger))

	c.dispatcher = task.NewDispatcher(store, registry)
	c.runner = task.NewRunner(store, registry, c.logger)

	return nil
}

// GetDispatcher lazily builds the task dispatcher with the handler
// registry.
func (c *Container) GetDispatcher() (*task.Dispatcher, error) {
	if err := c.ensureDispatcher(); err != nil {
		return nil, err
	}
	return c.dispatcher, nil
}

// GetRunner lazily builds the claim runner over the registry.
func (c *Container) GetRunner() (*task.Runner, error) {
	if err := c.ensureDispatcher(); err != nil {
		return nil, err
	}
	return c.runner, nil
}

// SetRunnerOwnerID records the owner identity on the task store for
// claims and batch ownership rows.
func (c *Container) SetRunnerOwnerID(id string) error {
	if _, err := c.GetRunner(); err != nil {
		return err
	}
	c.store.SetOwnerID(id)
	return nil
}

// GetPool builds (once) the task pool of the given type with the
// configured worker count and interval.
func (c *Container) GetPool(taskType types.TaskType) (*task.Pool, error) {
	if pool := c.pools[taskType]; pool != nil {
		return pool, nil
	}

	spec, ok := poolSpecs[taskType]
	if !ok {
		return nil, fmt.Errorf("unknown pool task type: %q", taskType)
	}

	runner, err := c.GetRunner()
	if err != nil {
		return nil, err
	}

	pool := task.NewPool(c.logger, runner, spec.workers(c.cfg.Load()), spec.interval, taskType)
	c.pools[taskType] = pool
	return pool, nil
}

// GetSearchEngine lazily builds the search engine.
func (c *Container) GetSearchEngine() (*search.Engine, error) {
	if c.engine == nil {
		client, err := c.GetClient()
		if err != nil {
			return nil, err
		}
		c.engine = search.NewEngine(c.logger, client)
	}
	return c.engine, nil
}

// Close stops every built task pool and releases the embedder, services,
// and the database handle.
func (c *Container) Close() {
	for _, pool := range c.pools {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		pool.Stop(ctx)
		cancel()
	}
	if c.db != nil {
		_ = c.db.Close()
	}
	if c.tag != nil {
		c.tag.Close()
	}
	if c.services != nil {
		if err := c.services.Close(); err != nil {
			c.logger.Error("", "close services: %v", err)
		}
	}
}
