package database

import (
	"context"
	"database/sql"
)

// Client pairs the generated Queries with the handle they run over, plus
// the transaction opener the services use.
type Client struct {
	*Queries
	db *sql.DB
}

// NewClient builds the client over the pool.
func NewClient(db *sql.DB) *Client {
	return &Client{
		Queries: New(db),
		db:      db,
	}
}

// BeginTx opens a transaction on the underlying pool.
func (c *Client) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return c.db.BeginTx(ctx, opts)
}

// DB returns the underlying pool.
func (c *Client) DB() *sql.DB {
	return c.db
}
