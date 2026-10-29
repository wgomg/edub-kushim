package database

import (
	"context"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

// TestMigrationAuditRegisterCarryAndDeletedAtRename pins migrations 00042 and 00043's carry contract: a
// register row under the old ledger column takes the live sweep and a deleted-tag row keeps its instant.
func TestMigrationAuditRegisterCarryAndDeletedAtRename(t *testing.T) {
	ctx := context.Background()
	base := TestDSN(t)
	name := "edub_test_" + testPackageDir() + "_auditcarry"
	release := acquireTestDB(base, name)
	defer release()

	db, err := NewPostgresDB(replaceDBName(base, name), DefaultMaxOpenConns)
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
	if err := goose.UpTo(db, "sql/schema/migrations", 41); err != nil {
		t.Fatalf("apply migrations up to 00041: %v", err)
	}

	// the migration-replay schema carries no seed data and document creation needs the default type row
	seed, err := SchemaFS.ReadFile("sql/schema/seed-document-types.sql")
	if err != nil {
		t.Fatalf("read the document type seed: %v", err)
	}
	if _, err := db.ExecContext(ctx, string(seed)); err != nil {
		t.Fatalf("seed document types: %v", err)
	}

	// advance the sweep so the carry is observable: a stamp of 3 proves the row took the live signal and
	// not its pre-migration audit_count (7) nor the seed default (0)
	if _, err := db.ExecContext(ctx,
		`UPDATE system_signal SET value = '3' WHERE name = 'tag_audit_sweep'`); err != nil {
		t.Fatalf("advance the sweep counter: %v", err)
	}

	q := New(db)
	doc := CreateUniqueDocument(t, q, "audit register carry")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO tag_audit_register (document_id, audit_count) VALUES ($1, 7)`, doc); err != nil {
		t.Fatalf("seed the ledger row: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO deleted_tag (name, kind, created_at) VALUES ('zqauditcarry', 'blocked', NOW())`); err != nil {
		t.Fatalf("seed the deleted-tag row: %v", err)
	}
	var blockedAt time.Time
	if err := db.QueryRowContext(ctx,
		`SELECT created_at FROM deleted_tag WHERE name = 'zqauditcarry'`).Scan(&blockedAt); err != nil {
		t.Fatalf("read the pre-migration instant: %v", err)
	}

	if err := goose.Up(db, "sql/schema/migrations"); err != nil {
		t.Fatalf("apply 00042 and 00043: %v", err)
	}

	var stamped int64
	if err := db.QueryRowContext(ctx,
		`SELECT last_audited_sweep FROM tag_audit_register WHERE document_id = $1`, doc).Scan(&stamped); err != nil {
		t.Fatalf("read the carried register row: %v", err)
	}
	if stamped != 3 {
		t.Errorf("carried stamp = %d, want 3: the row takes the current sweep, not its ledger value", stamped)
	}

	var deletedAt time.Time
	if err := db.QueryRowContext(ctx,
		`SELECT deleted_at FROM deleted_tag WHERE name = 'zqauditcarry'`).Scan(&deletedAt); err != nil {
		t.Fatalf("read the renamed column: %v", err)
	}
	if !deletedAt.Equal(blockedAt) {
		t.Errorf("deleted_at = %v, want the pre-migration instant %v: the rename must not touch values",
			deletedAt, blockedAt)
	}
}
