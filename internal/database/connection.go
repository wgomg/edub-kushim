package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/wgomg/edub-kushim/internal/config"
	_ "modernc.org/sqlite"
)

func NewSQLiteDB(cfg config.DatabaseConfig) (*sql.DB, error) {
	if err := os.MkdirAll(cfg.Path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	dbPath := filepath.Join(cfg.Path, cfg.Name)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		log.Printf("Warning: failed to enable WAL mode: %v", err)
	}

	if _, err := db.Exec("PRAGMA synchronous = NORMAL"); err != nil {
		log.Printf("Warning: failed to set synchronous mode: %v", err)
	}

	if err := createSchema(db); err != nil {
		return nil, fmt.Errorf("failed to create database schema: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	for _, seeder := range cfg.Seeders {
		if err := seedSchema(db, seeder); err != nil {
			return nil, fmt.Errorf("failed to seed %q: %w", seeder, err)
		}
	}

	return db, nil
}

func NewQueries(db *sql.DB) *Queries {
	return New(db)
}

func createSchema(db *sql.DB) error {
	// TODO: replace this check by proper migration when ready
	var tableExists bool
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='document'").
		Scan(&tableExists)
	if err == nil && tableExists {
		return nil
	}

	schemaPath := "sql/schema.sql"
	schemaSQL, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to read schema file %s: %w", schemaPath, err)
	}

	_, err = db.Exec(string(schemaSQL))
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	return nil
}

func seedSchema(db *sql.DB, seedType string) error {
	switch seedType {
	case "tags":
		path := fmt.Sprintf("sql/seed-%s.sql", seedType)
		sql, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read seed file %s: %w", path, err)
		}
		_, err = db.Exec(string(sql))
		if err != nil {
			return fmt.Errorf("failed to execute seed %q: %w", seedType, err)
		}
	default:
		return fmt.Errorf("unknown seed type: %s", seedType)
	}

	return nil
}
