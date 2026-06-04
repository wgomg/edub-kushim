package commands

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/cache"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Container struct {
	config     *config.Config
	logger     *utils.Logger
	db         *sql.DB
	engine     *search.Engine
	cache      *cache.Cache
	dispatcher *task.Dispatcher
	pools      struct {
		consume *pool.Pool
		enrich  *pool.Pool
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
		db, err := database.NewSQLiteDB(c.config.Db)
		if err != nil {
			return nil, err
		}
		c.db = db
	}
	return c.db, nil
}

func (c *Container) GetCache() (*cache.Cache, error) {
	db, err := c.GetDB()
	if err != nil {
		return nil, err
	}

	if c.cache == nil {
		tagCache, err := cache.BuildTagCache(context.Background(), db, c.logger, c.config.Enricher.TagMatcher)
		if err != nil {
			c.logger.Error(nil, "failed to build tag cache: %v — continuing with empty cache", err)
			c.cache = cache.New()
		} else {
			c.cache = tagCache
		}
	}
	return c.cache, nil
}

func (c *Container) GetDispatcher() (*task.Dispatcher, error) {
	db, err := c.GetDB()
	if err != nil {
		return nil, err
	}

	cache, err := c.GetCache()
	if err != nil {
		return nil, err
	}

	if c.dispatcher == nil {
		dispatcher, err := task.NewDispatcher(c.config, c.logger, db, cache)
		if err != nil {
			return nil, err
		}
		c.dispatcher = dispatcher
	}

	return c.dispatcher, nil
}

func (c *Container) GetPool(taskType string) (*pool.Pool, error) {
	var pp **pool.Pool
	switch taskType {
	case "consume":
		pp = &c.pools.consume
	case "enrich":
		pp = &c.pools.enrich
	default:
		return nil, fmt.Errorf("unknown pool task type: %q", taskType)
	}

	if *pp == nil {
		dispatcher, err := c.GetDispatcher()
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
		}
		*pp = pool.New(c.logger, dispatcher, workers, interval, taskType)
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
	if c.db != nil {
		c.db.Close()
	}
}
