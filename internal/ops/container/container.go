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
	cfg        atomic.Pointer[config.Config]
	logger     *log.Logger
	db         *sql.DB
	client     *database.Client
	engine     *search.Engine
	dispatcher *task.Dispatcher
	runner     *task.Runner
	store      *task.Store
	services   *service.CrudServices
	tag        *curation.Tag
	merges     *curation.TagMerge
	pools      struct {
		consume       *task.Pool
		enrich        *task.Pool
		thumbnail     *task.Pool
		thumbbackfill *task.Pool
		config        *task.Pool
		backup        *task.Pool
		mirror        *task.Pool
		tagreconcile  *task.Pool
		tagmeasure    *task.Pool
		tagidentity   *task.Pool
	}
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
	if _, err := c.GetDispatcher(); err != nil {
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

// InvalidateDB drops the cached connection so the next GetClient
// reconnects with the current configuration.
func (c *Container) InvalidateDB() {
	if c.db != nil {
		c.db.Close()
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
	c.pools.consume = nil
	c.pools.enrich = nil
	c.pools.thumbnail = nil
	c.pools.thumbbackfill = nil
	c.pools.config = nil
	c.pools.backup = nil
	c.pools.mirror = nil
	c.pools.tagreconcile = nil
	c.pools.tagmeasure = nil
	c.pools.tagidentity = nil
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

// GetDispatcher lazily builds the task dispatcher with the handler
// registry; the registry is assembled once and includes every task type
// the binary handles.
func (c *Container) GetDispatcher() (*task.Dispatcher, error) {
	if c.dispatcher != nil {
		return c.dispatcher, nil
	}

	client, err := c.GetClient()
	if err != nil {
		return nil, err
	}

	matcherClient := matcher.NewMatcherClient(c.SocketPath(), matcher.MaxMatchBodyBytes(c.cfg.Load().Enricher.TagMatcher.ReduceTargetWords))

	c.tag = curation.NewTag(client, c.logger, matcherClient)
	c.merges = curation.NewTagMerge(client, matcherClient, matcherClient, c.logger)
	peopleSvc := service.NewPeople(client.Queries, c.logger)
	peopleTypeSvc := service.NewPeopleType(client.Queries, c.logger)
	docTypeSvc := service.NewDocumentType(client.Queries, c.logger)

	c.services = &service.CrudServices{
		People: peopleSvc, PeopleType: peopleTypeSvc, DocumentType: docTypeSvc,
	}

	store := task.NewStore(client)
	c.store = store

	consumer, err := consumption.NewConsumer(c.cfg.Load(), c.logger, client)
	if err != nil {
		return nil, err
	}

	enricher, err := enrichment.NewEnricher(c.cfg.Load(), c.logger, client, c.tag, matcherClient)
	if err != nil {
		return nil, err
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
			tools.NewRunnerWithMatcher(c.logger, c.cfg.Load(), []string{"textreducer", "contentanalyzer", "tagmatcher"}, matcherClient),
			c.logger,
		),
		client.Queries,
		c.logger,
	))
	registry.Register(types.Task.Type.TagMeasure, tags.NewTagMeasureTaskHandler(client.Queries, c.logger))
	registry.Register(types.Task.Type.TagIdentity, admission.NewTagIdentityTaskHandler(client.Queries, c.logger))

	c.dispatcher = task.NewDispatcher(c.logger, store, registry)
	c.runner = task.NewRunner(store, registry, c.logger)

	return c.dispatcher, nil
}

// GetRunner lazily builds the claim runner over the registry.
func (c *Container) GetRunner() (*task.Runner, error) {
	if _, err := c.GetDispatcher(); err != nil {
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
func (c *Container) GetPool(taskType string) (*task.Pool, error) {
	var pp **task.Pool
	switch taskType {
	case "consume":
		pp = &c.pools.consume
	case "enrich":
		pp = &c.pools.enrich
	case "thumbnail":
		pp = &c.pools.thumbnail
	case "thumbbackfill":
		pp = &c.pools.thumbbackfill
	case "config":
		pp = &c.pools.config
	case "backup":
		pp = &c.pools.backup
	case "mirror":
		pp = &c.pools.mirror
	case "tagreconcile":
		pp = &c.pools.tagreconcile
	case "tagmeasure":
		pp = &c.pools.tagmeasure
	case "tagidentity":
		pp = &c.pools.tagidentity
	default:
		return nil, fmt.Errorf("unknown pool task type: %q", taskType)
	}

	if *pp == nil {
		runner, err := c.GetRunner()
		if err != nil {
			return nil, err
		}
		var workers int
		var interval time.Duration
		switch taskType {
		case "consume":
			workers = max(c.cfg.Load().Consumer.Workers, 1)
			interval = 2 * time.Second
		case "enrich":
			workers = max(c.cfg.Load().Enricher.Workers, 1)
			interval = 5 * time.Second
		case "thumbnail":
			workers = max(c.cfg.Load().Consumer.Thumbnail.Workers, 1)
			interval = 2 * time.Second
		case "thumbbackfill":
			workers = 1
			interval = 2 * time.Second
		case "config":
			workers = 1
			interval = 5 * time.Second
		case "backup":
			workers = 1
			interval = 60 * time.Second
		case "mirror":
			workers = 1
			interval = 60 * time.Second
		case "tagreconcile":
			workers = 1
			interval = 60 * time.Second
		case "tagmeasure":
			workers = 1
			interval = 5 * time.Second
		case "tagidentity":
			workers = 1
			interval = 5 * time.Second
		}
		*pp = task.NewPool(c.logger, runner, workers, interval, taskType)
	}

	return *pp, nil
}

// GetSearchEngine lazily builds the search engine.
func (c *Container) GetSearchEngine() (*search.Engine, error) {
	if c.engine == nil {
		client, err := c.GetClient()
		if err != nil {
			return nil, err
		}
		c.engine = search.NewEngine(c.logger, client.Queries)
	}
	return c.engine, nil
}

// Close releases the matcher embedder and any open database handle.
func (c *Container) Close() {
	if c.pools.consume != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pools.consume.Stop(ctx)
	}
	if c.pools.enrich != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pools.enrich.Stop(ctx)
	}
	if c.pools.thumbnail != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pools.thumbnail.Stop(ctx)
	}
	if c.pools.thumbbackfill != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pools.thumbbackfill.Stop(ctx)
	}
	if c.pools.config != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pools.config.Stop(ctx)
	}
	if c.pools.backup != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pools.backup.Stop(ctx)
	}
	if c.pools.mirror != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pools.mirror.Stop(ctx)
	}
	if c.pools.tagreconcile != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pools.tagreconcile.Stop(ctx)
	}
	if c.pools.tagmeasure != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pools.tagmeasure.Stop(ctx)
	}
	if c.pools.tagidentity != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pools.tagidentity.Stop(ctx)
	}
	if c.db != nil {
		c.db.Close()
	}
	if c.tag != nil {
		c.tag.Close()
	}
	if c.services != nil {
		c.services.Close()
	}
}
