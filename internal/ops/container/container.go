// Package container gives one kushim CLI invocation a single lazily built dependency graph, so every
// command shares the same connection, clients and caches, nothing is constructed before a command needs
// it, and a database settings change reconnects and drops everything derived from the old connection in
// one place.
package container

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tools"
)

// Container is the CLI-side holder of the dynamic config pointer and the lazily built shared services.
type Container struct {
	cfg            atomic.Pointer[config.Config]
	logger         *log.Logger
	db             *sql.DB
	client         *database.Client
	engine         *search.Engine
	tag            *curation.Tag
	matcherClient  *matcher.MatcherClient
	tagToolsRunner *tools.Runner
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

// GetTagService lazily builds the curation tag service over the client, logger and matcher client; it errors
// only when the database is unreachable.
func (c *Container) GetTagService() (*curation.Tag, error) {
	if c.tag != nil {
		return c.tag, nil
	}
	client, err := c.GetClient()
	if err != nil {
		return nil, err
	}
	c.tag = curation.NewTag(client, c.logger, c.GetMatcherClient())
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
		if err := database.InitializeSchema(context.Background(), db); err != nil {
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
	c.engine = nil
	c.tag = nil
	c.matcherClient = nil
	c.tagToolsRunner = nil
}

// ReconnectClient closes the current connection and reopens it from the
// config, returning the fresh client.
func (c *Container) ReconnectClient() (*database.Client, error) {
	newDB, err := database.NewPostgresDB(config.BuildPostgresDSN(c.cfg.Load().Db))
	if err != nil {
		return nil, err
	}
	if err := database.InitializeSchema(context.Background(), newDB); err != nil {
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

// Close releases the embedder and the database handle.
func (c *Container) Close() {
	if c.db != nil {
		_ = c.db.Close()
	}
	if c.tag != nil {
		c.tag.Close()
	}
}
