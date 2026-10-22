package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// ValidateMigrationDestination refuses a destination database that holds
// tables without this application's migration history.
func ValidateMigrationDestination(ctx context.Context, db *sql.DB) error {
	var count int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pg_tables
		WHERE schemaname = 'public' AND tablename <> ALL($1::text[])
	`, pq.Array(appManagedTables)).Scan(&count); err != nil {
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

// WaitForTaskDrain polls until no processing task of the given task types
// remains, reporting the remaining count on onWait; the context ends the
// wait with an error. The caller passes the task types whose declared waits
// include a lock it holds.
func WaitForTaskDrain(ctx context.Context, queries *Queries, logger *log.Logger, what string, drainTypes []types.TaskType, onWait ...func(count int64)) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		count, err := queries.CountProcessingTasks(ctx, drainTypes)
		if err != nil {
			return fmt.Errorf("count processing tasks: %w", err)
		}
		if count == 0 {
			return nil
		}

		logger.Info("", "%s: waiting for %d in-flight task(s) to drain", what, count)
		if len(onWait) > 0 && onWait[0] != nil {
			onWait[0](count)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// StartLockHeartbeat refreshes the heartbeat timestamps of the named locks
// held under token on the given interval until the context ends; it returns
// a stop function that ends the refresh early. A NULL token refreshes the
// manual holder's locks, whose time window is the only staleness backstop.
func StartLockHeartbeat(ctx context.Context, queries *Queries, logger *log.Logger, interval time.Duration, names []types.LockName, token uuid.NullUUID) func() {
	lockNames := make([]string, len(names))
	for i, name := range names {
		lockNames[i] = string(name)
	}

	stopCh := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := queries.TouchLocksForToken(ctx, TouchLocksForTokenParams{
					LockNames:  lockNames,
					OwnerToken: token,
				}); err != nil {
					logger.Error("", "lock heartbeat: touch: %v", err)
				}
			}
		}
	}()
	return func() { stopOnce.Do(func() { close(stopCh) }) }
}

// RewriteStoragePaths rewrites stored path columns from an old storage
// root to the new one, across the document and document-tag tables; both
// paths must be absolute.
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

// RewriteTaskPayloadPaths rewrites the consumption directory inside
// pending tasks' JSON payloads, so tasks queued for the old layout
// survive a migration.
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
