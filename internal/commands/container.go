package commands

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	types "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/configtask"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tagmatch"
	"github.com/wgomg/edub-kushim/internal/task"
	taskhandlers "github.com/wgomg/edub-kushim/internal/task/handlers"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Container struct {
	cfg        atomic.Pointer[config.Config]
	logger     *utils.Logger
	db         *sql.DB
	client     *database.Client
	engine     *search.Engine
	dispatcher *task.Dispatcher
	runner     *task.Runner
	store      *task.Store
	services   *types.CrudServices
	pools      struct {
		consume *pool.Pool
		enrich  *pool.Pool
		config  *pool.Pool
		backup  *pool.Pool
	}
}

func NewContainer(cfg *config.Config, logger *utils.Logger) *Container {
	return NewContainerWithDB(cfg, logger, nil)
}

func NewContainerWithDB(cfg *config.Config, logger *utils.Logger, db *sql.DB) *Container {
	c := &Container{
		logger: logger,
		db:     db,
	}
	c.cfg.Store(cfg)
	return c
}

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
	c.pools.consume = nil
	c.pools.enrich = nil
	c.pools.config = nil
	c.pools.backup = nil
}

func (c *Container) reconnectClient() (*database.Client, error) {
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

func (c *Container) socketPath() string {
	return filepath.Join(c.cfg.Load().App.ConfigDir, "kushim-hugot.sock")
}

func (c *Container) GetDispatcher() (*task.Dispatcher, error) {
	if c.dispatcher != nil {
		return c.dispatcher, nil
	}

	client, err := c.GetClient()
	if err != nil {
		return nil, err
	}

	matcherClient := tagmatch.NewMatcherClient(c.socketPath(), tagmatch.MaxMatchBodyBytes(c.cfg.Load().Enricher.TagMatcher.ReduceTargetWords))

	tagSvc, err := service.NewTag(client.Queries, c.logger, matcherClient)
	if err != nil {
		return nil, fmt.Errorf("tag service: %w", err)
	}

	peopleSvc := service.NewPeople(client.Queries, c.logger)
	peopleTypeSvc := service.NewPeopleType(client.Queries, c.logger)
	docTypeSvc := service.NewDocumentType(client.Queries, c.logger)

	c.services = &types.CrudServices{
		Tag: tagSvc, People: peopleSvc, PeopleType: peopleTypeSvc, DocumentType: docTypeSvc,
	}

	store := task.NewStore(client.Queries)
	c.store = store

	consumer, err := consumption.NewConsumer(c.cfg.Load(), c.logger, client)
	if err != nil {
		return nil, err
	}

	enricher, err := enrichment.NewEnricher(c.cfg.Load(), c.logger, client.Queries, c.services, matcherClient)
	if err != nil {
		return nil, err
	}

	registry := task.NewRegistry()
	registry.Register("consume", taskhandlers.NewConsumeTaskHandler(consumer, store, c.logger))
	registry.Register("enrich", taskhandlers.NewEnrichTaskHandler(enricher, client.Queries, c.logger))
	registry.Register("config", configtask.NewConfigTaskHandler(c.logger))
	registry.Register("backup", taskhandlers.NewBackupTaskHandler(c.db, client.Queries, func() *config.Config { return c.cfg.Load() }, c.logger))

	c.dispatcher = task.NewDispatcher(c.logger, store, registry)
	c.runner = task.NewRunner(store, registry, c.logger)

	return c.dispatcher, nil
}

func (c *Container) GetRunner() (*task.Runner, error) {
	if _, err := c.GetDispatcher(); err != nil {
		return nil, err
	}
	return c.runner, nil
}

func (c *Container) SetRunnerOwnerID(id string) error {
	if _, err := c.GetRunner(); err != nil {
		return err
	}
	c.store.SetOwnerID(id)
	return nil
}

func (c *Container) GetPool(taskType string) (*pool.Pool, error) {
	var pp **pool.Pool
	switch taskType {
	case "consume":
		pp = &c.pools.consume
	case "enrich":
		pp = &c.pools.enrich
	case "config":
		pp = &c.pools.config
	case "backup":
		pp = &c.pools.backup
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
		case "config":
			workers = 1
			interval = 5 * time.Second
		case "backup":
			workers = 1
			interval = 60 * time.Second
		}
		*pp = pool.New(c.logger, runner, workers, interval, taskType)
	}

	return *pp, nil
}

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
	if c.db != nil {
		c.db.Close()
	}
	if c.services != nil {
		c.services.Close()
	}
}
