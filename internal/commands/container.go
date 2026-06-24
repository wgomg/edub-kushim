package commands

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/configtask"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/documenttypes"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/people"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/tagmatch/rpc"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/task"
	taskhandlers "github.com/wgomg/edub-kushim/internal/task/handlers"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Container struct {
	config     *config.Config
	logger     *utils.Logger
	db         *sql.DB
	engine     *search.Engine
	dispatcher *task.Dispatcher
	runner     *task.Runner
	store      *task.Store
	services   *types.CrudServices
	pools      struct {
		consume *pool.Pool
		enrich  *pool.Pool
		config  *pool.Pool
	}
}

func NewContainer(cfg *config.Config, logger *utils.Logger) *Container {
	return NewContainerWithDB(cfg, logger, nil)
}

func NewContainerWithDB(cfg *config.Config, logger *utils.Logger, db *sql.DB) *Container {
	return &Container{
		config: cfg,
		logger: logger,
		db:     db,
	}
}

func (c *Container) GetDB() (*sql.DB, error) {
	if c.db == nil {
		db, err := database.NewSQLiteDB(c.config.Db.Path, c.config.Db.Name)
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

func (c *Container) socketPath() string {
	return filepath.Join(c.config.App.ConfigDir, "kushim-matcher.sock")
}

func (c *Container) GetDispatcher() (*task.Dispatcher, error) {
	if c.dispatcher != nil {
		return c.dispatcher, nil
	}

	db, err := c.GetDB()
	if err != nil {
		return nil, err
	}

	queries := database.NewQueries(db)

	matcherClient := rpc.NewMatcherClient(c.socketPath())

	tagSvc, err := tags.NewTagService(queries, c.logger, matcherClient)
	if err != nil {
		return nil, fmt.Errorf("tag service: %w", err)
	}

	peopleSvc := people.NewPeopleService(database.NewQueries(db), c.logger)
	peopleTypeSvc := people.NewPeopleTypeService(database.NewQueries(db), c.logger)
	docTypeSvc := documenttypes.NewDocumentTypeService(database.NewQueries(db), c.logger)

	c.services = &types.CrudServices{
		Tag: tagSvc, People: peopleSvc, PeopleType: peopleTypeSvc, DocumentType: docTypeSvc,
	}

	store := task.NewStore(database.NewQueries(db))
	c.store = store

	consumer, err := consumption.NewConsumer(c.config, c.logger, db)
	if err != nil {
		return nil, err
	}

	enricher, err := enrichment.NewEnricher(c.config, c.logger, db, c.services, matcherClient)
	if err != nil {
		return nil, err
	}

	registry := task.NewRegistry()
	registry.Register("consume", taskhandlers.NewConsumeTaskHandler(consumer, store, c.logger))
	registry.Register("enrich", taskhandlers.NewEnrichTaskHandler(enricher, c.logger))
	registry.Register("config", configtask.NewConfigTaskHandler(c.logger))

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
			workers = max(c.config.Consumer.Workers, 1)
			interval = 2 * time.Second
		case "enrich":
			workers = max(c.config.Enricher.Workers, 1)
			interval = 5 * time.Second
		case "config":
			workers = 1
			interval = 5 * time.Second
		}
		*pp = pool.New(c.logger, runner, workers, interval, taskType)
	}

	return *pp, nil
}

func (c *Container) GetSearchEngine() (*search.Engine, error) {
	if c.engine == nil {
		db, err := c.GetDB()
		if err != nil {
			return nil, err
		}
		c.engine = search.NewEngine(c.logger, db)
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
	if c.db != nil {
		c.db.Close()
	}
	if c.services != nil {
		c.services.Close()
	}
}
