package database

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed sql/schema
var schemaFS embed.FS

func InitializeSchema(db *sql.DB) error {
	goose.SetBaseFS(schemaFS)
	goose.SetDialect("sqlite3")

	if err := goose.Up(db, "sql/schema/migrations", goose.WithAllowMissing()); err != nil {
		return fmt.Errorf("run migrations: %w", err)
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
	goose.SetBaseFS(schemaFS)
	goose.SetDialect("sqlite3")

	if err := goose.Reset(db, "sql/schema/migrations", goose.WithAllowMissing()); err != nil {
		return fmt.Errorf("reset migrations: %w", err)
	}

	return InitializeSchema(db)
}
