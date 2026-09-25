package database

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// WithConnectTimeout appends the connect_timeout parameter to a DSN,
// returning the DSN unchanged when it is not URL-shaped.
func WithConnectTimeout(dsn string, seconds int) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	q := u.Query()
	q.Set("connect_timeout", strconv.Itoa(seconds))
	u.RawQuery = q.Encode()
	return u.String()
}

// NewPostgresDB creates the database when it does not exist, opens the
// pool with the production limits, and pings before returning.
func NewPostgresDB(dsn string) (*sql.DB, error) {
	if err := ensureDatabaseExists(dsn); err != nil {
		return nil, fmt.Errorf("ensure database exists: %w", err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return db, nil
}

func ensureDatabaseExists(targetDSN string) error {
	bootstrapDSN := replaceDBName(targetDSN, "postgres")
	db, err := sql.Open("pgx", bootstrapDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	targetDB := extractDBName(targetDSN)

	var exists bool
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", targetDB).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check database existence: %w", err)
	}
	if !exists {
		log.Printf("database %q does not exist, creating...", targetDB)
		_, err = db.Exec(fmt.Sprintf("CREATE DATABASE %s", quoteIdent(targetDB)))
		if err != nil {
			return fmt.Errorf("create database %q: %w", targetDB, err)
		}
	}
	return nil
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func extractDBName(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

func replaceDBName(dsn, newDB string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + newDB
	u.RawPath = ""
	return u.String()
}
