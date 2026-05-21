package commands

import (
	"context"
	"database/sql"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Container struct {
	config *config.Config
	logger *utils.Logger
	db     *sql.DB
	engine *search.Engine
	pool   *pool.Pool
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

func (c *Container) GetPool() (*pool.Pool, error) {
	if c.pool == nil {
		db, err := c.GetDB()
		if err != nil {
			return nil, err
		}
		dispatcher, err := task.NewDispatcher(c.config, c.logger, db)
		if err != nil {
			return nil, err
		}
		workers := c.config.Consumer.Workers
		if workers < 1 {
			workers = 1
		}
		c.pool = pool.New(c.logger, dispatcher, workers, 2*time.Second)
	}
	return c.pool, nil
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
	if c.pool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.pool.Stop(ctx)
	}
	if c.db != nil {
		c.db.Close()
	}
}
