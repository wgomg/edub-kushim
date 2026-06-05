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

	seed, err := schemaFS.ReadFile("sql/schema/seed-tags.sql")
	if err != nil {
		return fmt.Errorf("read embedded seed: %w", err)
	}
	if _, err := db.Exec(string(seed)); err != nil {
		return fmt.Errorf("seed tags: %w", err)
	}

	return nil
}
