package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/utils"
)

func ValidateMigrationDestination(ctx context.Context, db *sql.DB) error {
	var count int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pg_tables
		WHERE schemaname = 'public' AND tablename NOT IN ('goose_db_version', 'backup_lock')
	`).Scan(&count); err != nil {
		return fmt.Errorf("check destination tables: %w", err)
	}
	if count == 0 {
		return nil
	}
	var version int64
	err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version").Scan(&version)
	if err != nil || version == 0 {
		return fmt.Errorf("destination database contains %d existing table(s) without edub migration history — refusing to replace it", count)
	}
	return nil
}

func WaitForTaskDrain(ctx context.Context, queries *Queries, logger *utils.Logger, what string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		count, err := queries.CountProcessingTasks(ctx)
		if err != nil {
			return fmt.Errorf("count processing tasks: %w", err)
		}
		if count == 0 {
			return nil
		}

		logger.Info(nil, "%s: waiting for %d in-flight task(s) to drain", what, count)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func RewriteStoragePaths(ctx context.Context, db *sql.DB, oldDir, newDir string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin path rewrite: %w", err)
	}
	defer tx.Rollback()

	pattern := escapeLike(oldDir) + "/%"
	statements := []string{
		`UPDATE document SET storage_path = REPLACE(storage_path, $2, $3)
		 WHERE storage_path LIKE $1 OR storage_path = $2`,
		`UPDATE document SET original_path = REPLACE(original_path, $2, $3)
		 WHERE original_path LIKE $1 OR original_path = $2`,
		`UPDATE orphaned_file SET file_path = REPLACE(file_path, $2, $3)
		 WHERE file_path LIKE $1 OR file_path = $2`,
	}
	for _, q := range statements {
		if _, err := tx.ExecContext(ctx, q, pattern, oldDir, newDir); err != nil {
			return fmt.Errorf("execute path rewrite: %w", err)
		}
	}

	return tx.Commit()
}

func RewriteTaskPayloadPaths(ctx context.Context, db *sql.DB, oldConsumptionDir, newConsumptionDir string) error {
	pattern := escapeLike(oldConsumptionDir) + "/%"
	_, err := db.ExecContext(ctx, `
		UPDATE task
		SET payload = jsonb_set(
			payload,
			'{file_path}',
			to_jsonb($2 || substring(payload->>'file_path' from char_length($3) + 1))
		)
		WHERE task_type = 'consume'
		  AND payload->>'file_path' LIKE $1
	`, pattern, newConsumptionDir, oldConsumptionDir)
	if err != nil {
		return fmt.Errorf("rewrite task payload paths: %w", err)
	}
	return nil
}

// escapeLike neutralizes LIKE wildcards so the pattern only matches paths
// that actually start with oldDir, not lookalikes (e.g. /data/storage-2).
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
