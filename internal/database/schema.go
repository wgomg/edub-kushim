package database

import (
	"database/sql"
	"embed"
	"fmt"
)

//go:embed sql/schema
var schemaFS embed.FS

func InitializeSchema(db *sql.DB) error {
	schema, err := schemaFS.ReadFile("sql/schema/schema.sql")
	if err != nil {
		return fmt.Errorf("read embedded schema: %w", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	seeders := []string{"tags", "document-types", "people-types"}
	for _, seed := range seeders {
		seeder, err := schemaFS.ReadFile(fmt.Sprintf("sql/schema/seed-%s.sql", seed))
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", seed, err)
		}
		if _, err := db.Exec(string(seeder)); err != nil {
			return fmt.Errorf("seed %s: %w", seed, err)
		}
	}

	return nil
}

func ResetDatabase(db *sql.DB) error {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan table name: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate tables: %w", err)
	}

	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable foreign keys: %w", err)
	}

	for _, t := range tables {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + t); err != nil {
			return fmt.Errorf("drop table %s: %w", t, err)
		}
	}

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("re-enable foreign keys: %w", err)
	}

	return InitializeSchema(db)
}
