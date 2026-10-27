package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// connectionTimeoutSeconds bounds every setup-side connect attempt, so an unreachable server fails the
// wizard or CLI request instead of hanging it.
const connectionTimeoutSeconds = 10

// ErrDatabaseNotEmpty reports a setup target database that already holds tables. Setup only adopts a
// completely empty database, so the operator can drop it (or pick another name) and retry without risking
// existing data.
var ErrDatabaseNotEmpty = errors.New("target database is not empty")

// PrepareFreshDatabase claims the target of dsn for a fresh setup: it creates the database when it does not
// exist and verifies the target is reachable with the given credentials. An existing database is accepted
// only when it has no tables in any non-system schema; otherwise it reports ErrDatabaseNotEmpty. It does not
// create any schema — callers run InitializeSchema once the configuration is persisted.
func PrepareFreshDatabase(dsn string) error {
	target := extractDBName(dsn)
	adminDSN := WithConnectTimeout(replaceDBName(dsn, "postgres"), connectionTimeoutSeconds)

	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		return fmt.Errorf("open database server: %w", err)
	}
	defer admin.Close()

	var exists bool
	if err := admin.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", target).Scan(&exists); err != nil {
		return fmt.Errorf("check database %q exists: %w", target, err)
	}
	if !exists {
		if _, err := admin.Exec(fmt.Sprintf("CREATE DATABASE %s", quoteIdent(target))); err != nil {
			return fmt.Errorf("create database %q: %w", target, err)
		}
	}

	db, err := sql.Open("pgx", WithConnectTimeout(dsn, connectionTimeoutSeconds))
	if err != nil {
		return fmt.Errorf("open database %q: %w", target, err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("connect to database %q: %w", target, err)
	}
	if !exists {
		return nil
	}

	var tables int64
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM pg_catalog.pg_tables
		WHERE schemaname NOT LIKE 'pg\_%' AND schemaname <> 'information_schema'
	`).Scan(&tables); err != nil {
		return fmt.Errorf("count tables in database %q: %w", target, err)
	}
	if tables > 0 {
		return fmt.Errorf("%w: %q contains %d table(s) — drop it or choose another database name",
			ErrDatabaseNotEmpty, target, tables)
	}
	return nil
}

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
