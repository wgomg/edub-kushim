package commands

import (
	"database/sql"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/consumption"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Container struct {
	config   *config.Config
	logger   *utils.Logger
	db       *sql.DB
	consumer *consumption.Consumer
}

func NewContainer(cfg *config.Config, logger *utils.Logger) *Container {
	return &Container{
		config: cfg,
		logger: logger,
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

func (c *Container) Close() {
	if c.db != nil {
		c.db.Close()
	}
}
