package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"time"

	"github.com/pressly/goose/v3"
)

// SchemaFS embeds the migration and seeder files applied at startup.
//
//go:embed sql/schema
var SchemaFS embed.FS

// InitializeSchema applies the embedded migrations (allowing missing files so a parallel checkout can lag)
// and runs the idempotent seeders. The context bounds the whole sequence, so a caller with a request
// deadline (the setup wizard) aborts instead of outliving its response; migrations are transactional per
// file, so a cancelled run resumes where it stopped on the next attempt.
func InitializeSchema(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(SchemaFS)
	goose.SetDialect("postgres")

	if err := goose.UpContext(ctx, db, "sql/schema/migrations", goose.WithAllowMissing()); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	seeders := []string{"tags", "tag-aliases", "document-types", "people-types"}
	for _, seed := range seeders {
		seeder, err := SchemaFS.ReadFile(fmt.Sprintf("sql/schema/seed-%s.sql", seed))
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", seed, err)
		}
		if _, err := db.ExecContext(ctx, string(seeder)); err != nil {
			return fmt.Errorf("seed %s: %w", seed, err)
		}
	}

	backfillCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := BackfillProcessedSizes(backfillCtx, db); err != nil {
		slog.Default().Error("backfill processed sizes", "error", err)
	}

	hashCtx, hashCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer hashCancel()
	if err := BackfillTextHash(hashCtx, db); err != nil {
		slog.Default().Error("backfill text hash", "error", err)
	}

	return nil
}

// ResetDatabase drops and re-creates the database through a maintenance connection, for the setup CLI's
// --reset-database path.
func ResetDatabase(db *sql.DB) error {
	goose.SetBaseFS(SchemaFS)
	goose.SetDialect("postgres")

	if err := goose.Reset(db, "sql/schema/migrations", goose.WithAllowMissing()); err != nil {
		return fmt.Errorf("reset migrations: %w", err)
	}

	return InitializeSchema(context.Background(), db)
}
