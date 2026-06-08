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
