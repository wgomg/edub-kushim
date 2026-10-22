package database

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrationOriginScheduleDown pins the rollback of 00048 and 00047: the pre-00047 code must see
// the batch.source column and config-typed migration tasks again, and re-applying the migrations must
// re-key them to the new task types.
func TestMigrationOriginScheduleDown(t *testing.T) {
	db := NewTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO batch (id, origin, status) VALUES ('roll-b', 'setup', 'queued')`); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO task (task_id, task_type, status, batch_id, payload, dedup_key)
		 VALUES ('roll-t', 'migrate-db', 'pending', 'roll-b', '{"config_dir":"/tmp"}', 'migrate-db')`); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM task WHERE task_id = 'roll-t'`)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM batch WHERE id = 'roll-b'`)
	})

	columnExists := func(t *testing.T, column string) bool {
		t.Helper()
		var exists bool
		if err := db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'batch' AND column_name = $1)`,
			column).Scan(&exists); err != nil {
			t.Fatalf("check batch.%s: %v", column, err)
		}
		return exists
	}
	taskRow := func(t *testing.T) (taskType, dedupKey, op string) {
		t.Helper()
		if err := db.QueryRowContext(ctx,
			`SELECT task_type, dedup_key, COALESCE(payload->>'op', '') FROM task WHERE task_id = 'roll-t'`).
			Scan(&taskType, &dedupKey, &op); err != nil {
			t.Fatalf("read task: %v", err)
		}
		return taskType, dedupKey, op
	}

	goose.SetBaseFS(SchemaFS)
	goose.SetDialect("postgres")
	if err := goose.DownTo(db, "sql/schema/migrations", 46); err != nil {
		t.Fatalf("down to 46 (00048 and 00047 must revert): %v", err)
	}

	if columnExists(t, "origin") {
		t.Error("batch.origin still exists after the down migrations")
	}
	var source string
	if err := db.QueryRowContext(ctx, `SELECT source FROM batch WHERE id = 'roll-b'`).Scan(&source); err != nil {
		t.Fatalf("read reverted batch: %v", err)
	}
	if source != "config" {
		t.Errorf("reverted batch source = %q, want the lossy 'config' mapping", source)
	}
	taskType, dedupKey, op := taskRow(t)
	if taskType != "config" || dedupKey != "config:migrate-db" || op != "migrate-db" {
		t.Errorf("reverted task = (%s, %s, %s), want (config, config:migrate-db, migrate-db)", taskType, dedupKey, op)
	}

	if err := goose.Up(db, "sql/schema/migrations"); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !columnExists(t, "origin") {
		t.Error("batch.origin missing after re-applying the up migrations")
	}
	taskType, dedupKey, _ = taskRow(t)
	if taskType != "migrate-db" || dedupKey != "migrate-db" {
		t.Errorf("re-keyed task = (%s, %s), want (migrate-db, migrate-db)", taskType, dedupKey)
	}
}
