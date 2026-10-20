package database

import (
	"context"
	"database/sql"
	"errors"
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

// TestMigrationStoreChangeRecordNarrowsToThreeFlags pins the 00044 → 00045 boundary of the store-change
// record: 00044 seeds its four flags and its trigger upserts all four, and after 00045 the identity flag
// is gone while the same mutation sets exactly the three remaining flags.
func TestMigrationStoreChangeRecordNarrowsToThreeFlags(t *testing.T) {
	ctx := context.Background()
	base := TestDSN(t)
	name := "edub_test_" + testPackageDir() + "_store_signal"
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

	flagValue := func(flag string) (string, bool) {
		t.Helper()
		var value string
		err := db.QueryRowContext(ctx, "SELECT value FROM system_signal WHERE name = $1", flag).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false
		}
		if err != nil {
			t.Fatalf("read flag %s: %v", flag, err)
		}
		return value, true
	}
	clearFlags := func(flags ...string) {
		t.Helper()
		for _, flag := range flags {
			if _, err := db.ExecContext(ctx, "UPDATE system_signal SET value = 'false' WHERE name = $1", flag); err != nil {
				t.Fatalf("clear flag %s: %v", flag, err)
			}
		}
	}
	mutateAndRequire := func(tagName string, flags ...string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, "INSERT INTO tag (name) VALUES ($1)", tagName); err != nil {
			t.Fatalf("insert %q under the trigger: %v", tagName, err)
		}
		for _, flag := range flags {
			if value, ok := flagValue(flag); !ok || value != "true" {
				t.Errorf("flag %s = (%q, present=%v) after the mutation, want set to true",
					flag, value, ok)
			}
		}
	}

	goose.SetBaseFS(SchemaFS)
	goose.SetDialect("postgres")
	if err := goose.UpTo(db, "sql/schema/migrations", 44); err != nil {
		t.Fatalf("apply migrations up to 00044: %v", err)
	}

	all := []string{"tag_measure_changed", "tag_reconcile_changed", "tag_identity_changed", "tag_audit_changed"}
	for _, flag := range all {
		if _, ok := flagValue(flag); !ok {
			t.Fatalf("flag %s missing after 00044: the record must seed it", flag)
		}
	}
	clearFlags(all...)
	mutateAndRequire("pre 00045 probe", all...)

	if err := goose.Up(db, "sql/schema/migrations"); err != nil {
		t.Fatalf("apply 00045: %v", err)
	}
	if _, ok := flagValue("tag_identity_changed"); ok {
		t.Error("tag_identity_changed survived 00045: the migration must delete the row")
	}
	remaining := []string{"tag_measure_changed", "tag_reconcile_changed", "tag_audit_changed"}
	clearFlags(remaining...)
	mutateAndRequire("post 00045 probe", remaining...)
	if _, ok := flagValue("tag_identity_changed"); ok {
		t.Error("the narrowed trigger re-seeded tag_identity_changed")
	}
}
