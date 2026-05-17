package commands

import (
	"context"
	"database/sql"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/queue"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Container struct {
	config   *config.Config
	logger   *utils.Logger
	db       *sql.DB
	consumer *consumption.Consumer
	engine   *search.Engine
	taskQ    *queue.Queue
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

func (c *Container) GetConsumer() (*consumption.Consumer, error) {
	if c.consumer == nil {
		db, err := c.GetDB()
		if err != nil {
			return nil, err
		}
		c.consumer = consumption.NewConsumer(c.config, c.logger, db)
	}
	return c.consumer, nil
}

func (c *Container) GetQueue() (*queue.Queue, error) {
	if c.taskQ == nil {
		db, err := c.GetDB()
		if err != nil {
			return nil, err
		}
		consumer, err := c.GetConsumer()
		if err != nil {
			return nil, err
		}

		workers := c.config.Consumer.Workers
		if workers < 1 {
			workers = 1
		}

		handler := consumption.NewConsumeTaskHandler(consumer)
		c.taskQ = queue.New(c.logger, db, workers, handler)
	}
	return c.taskQ, nil
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
	if c.taskQ != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.taskQ.Stop(ctx)
	}
	if c.db != nil {
		c.db.Close()
	}
}
