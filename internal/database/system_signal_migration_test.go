package database

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

// TestMigrationSystemSignalCarriesState pins migration 00041's upgrade contract: a maintenance lock
// held and a sweep counter advanced before the upgrade keep their values in system_signal, so a process
// running while the schema upgrades stays the lock holder and the completed audit sweep does not reset
// to a fresh one.
func TestMigrationSystemSignalCarriesState(t *testing.T) {
	ctx := context.Background()
	base := TestDSN(t)
	name := "edub_test_" + testPackageDir() + "_migration"
	release := acquireTestDB(base, name)
	defer release()

	db, err := NewPostgresDB(replaceDBName(base, name))
	if err != nil {
		t.Fatalf("open a fresh schema database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close schema database: %v", err)
		}
	}()

	goose.SetBaseFS(SchemaFS)
	goose.SetDialect("postgres")
	if err := goose.UpTo(db, "sql/schema/migrations", 40); err != nil {
		t.Fatalf("apply migrations up to 00040: %v", err)
	}

	token := uuid.New()
	if _, err := db.ExecContext(ctx,
		"UPDATE backup_lock SET running = true, started_at = NOW(), owner_token = $1 WHERE id = 1",
		token); err != nil {
		t.Fatalf("hold the pre-migration maintenance lock: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE tag_audit_sweep SET sweep_count = 3 WHERE id = 1"); err != nil {
		t.Fatalf("advance the pre-migration sweep counter: %v", err)
	}

	if err := goose.Up(db, "sql/schema/migrations"); err != nil {
		t.Fatalf("apply 00041: %v", err)
	}

	var value string
	var owner uuid.NullUUID
	var started time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT value, (data->>'owner_token')::uuid, (data->>'started_at')::timestamptz
		FROM system_signal WHERE name = 'maintenance_lock'`).Scan(&value, &owner, &started); err != nil {
		t.Fatalf("read the migrated maintenance lock: %v", err)
	}
	if value != "true" {
		t.Errorf("migrated lock value = %q, want \"true\": a held lock stays held across the migration", value)
	}
	if !owner.Valid || owner.UUID != token {
		t.Errorf("migrated lock owner = %+v, want %s", owner, token)
	}
	if started.IsZero() || time.Since(started) > time.Hour {
		t.Errorf("migrated lock started_at = %v, want the holder's fresh timestamp", started)
	}

	var held bool
	if err := db.QueryRowContext(ctx, "SELECT is_maintenance_lock_held()").Scan(&held); err != nil {
		t.Fatalf("check the lock gate after the migration: %v", err)
	}
	if !held {
		t.Error("is_maintenance_lock_held() = false, want it to see the carried lock")
	}

	var sweep string
	if err := db.QueryRowContext(ctx, `
		SELECT value FROM system_signal WHERE name = 'tag_audit_sweep'`).Scan(&sweep); err != nil {
		t.Fatalf("read the migrated sweep counter: %v", err)
	}
	if sweep != "3" {
		t.Errorf("migrated sweep value = %q, want 3: a completed sweep does not reset", sweep)
	}

	var legacy int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename IN ('backup_lock', 'tag_audit_sweep')`).Scan(&legacy); err != nil {
		t.Fatalf("check the replaced singleton tables: %v", err)
	}
	if legacy != 0 {
		t.Errorf("legacy singleton tables = %d, want none: 00041 replaces them with system_signal", legacy)
	}
}
