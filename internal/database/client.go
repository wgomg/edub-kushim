package database

import (
	"context"
	"database/sql"
)

type Client struct {
	*Queries
	db *sql.DB
}

func NewClient(db *sql.DB) *Client {
	return &Client{
		Queries: New(db),
		db:      db,
	}
}

func (c *Client) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return c.db.BeginTx(ctx, opts)
}

func (c *Client) DB() *sql.DB {
	return c.db
}
