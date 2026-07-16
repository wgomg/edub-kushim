package database

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

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
		db.Close()
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
