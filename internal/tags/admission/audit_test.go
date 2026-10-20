package admission

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
)

// auditFixture clears the audit's document store and pins the derivation gate's input: one text-less
// document carrying a compound tag whose segments are not store values, so pending coverage is
// derivable while the sampled document skips its audit before the tools runner is ever reached.
func auditFixture(t *testing.T, client *database.Client) (docID, tagID int64) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		"DELETE FROM document_tag",
		"DELETE FROM tag_coverage_event",
		"DELETE FROM document_people",
		"DELETE FROM document",
	} {
		if _, err := client.DB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("clear the audit store via %q: %v", stmt, err)
		}
	}

	docID, err := client.Queries.CreateDocument(ctx, database.CreateDocumentParams{
		DocumentID:     uuid.New().String(),
		Title:          "gate fixture",
		Md5Checksum:    "gate-fixture-md5",
		Sha512Checksum: "gate-fixture-sha512",
		OriginalType:   "application/pdf",
		FileSize:       1,
		PageCount:      1,
		WordCount:      1,
		CharCount:      1,
		Language:       "eng",
		OriginalPath:   "/tmp/gate-fixture.pdf",
		StoragePath:    "/tmp/gate-fixture.pdf",
	})
	if err != nil {
		t.Fatalf("create the fixture document: %v", err)
	}
	tagID = database.MustCreateTag(t, client.Queries, "zqwal gorplix")
	database.MustAddDocumentTag(t, client.Queries, docID, tagID)
	return docID, tagID
}

// TestRunDerivesThenSkipsOnClearSignal pins the audit's gate against its store: a changed store derives
// the durable pending-coverage rows, and the following run over the same store skips the store-wide
// read — the rows stand untouched — while the sweep still samples.
func TestRunDerivesThenSkipsOnClearSignal(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	docID, tagID := auditFixture(t, client)
	logger := log.NewDiscardLogger()
	cfg := config.DefaultConfig(t.TempDir())
	runner := NewAuditRunner(func() *config.Config { return cfg }, logger, client, nil)
	ctx := context.Background()

	pendingRows := func() int {
		t.Helper()
		var count int
		err := client.DB().QueryRowContext(ctx,
			"SELECT COUNT(*) FROM tag_coverage_event WHERE document_id = $1", docID).Scan(&count)
		if err != nil {
			t.Fatalf("count pending coverage rows: %v", err)
		}
		return count
	}
	flagValue := func() string {
		t.Helper()
		value, err := client.Queries.GetTagStoreSignal(ctx, tags.SignalAuditChanged)
		if err != nil {
			t.Fatalf("read the audit store-changed signal: %v", err)
		}
		return value
	}

	first, err := runner.Run(ctx, uuid.New())
	if err != nil {
		t.Fatalf("run on a changed store: %v", err)
	}
	if first.PendingCoverage == 0 {
		t.Error("changed store derived no pending coverage")
	}
	if rows := pendingRows(); rows != first.PendingCoverage {
		t.Errorf("pending coverage rows = %d, report says %d", rows, first.PendingCoverage)
	}
	if got := flagValue(); got != tags.SignalClear {
		t.Errorf("flag after the deriving run = %q, want it cleared at open", got)
	}

	second, err := runner.Run(ctx, uuid.New())
	if err != nil {
		t.Fatalf("run on an unchanged store: %v", err)
	}
	if second.PendingCoverage != 0 {
		t.Errorf("clear signal reported pending coverage %d, want the derivation skipped", second.PendingCoverage)
	}
	if rows := pendingRows(); rows != first.PendingCoverage {
		t.Errorf("pending coverage rows = %d after the skipped run, want the durable %d to stand",
			rows, first.PendingCoverage)
	}
	if second.Sampled == 0 {
		t.Error("the sweep did not sample while the derivation was skipped")
	}
	if second.ZeroWork {
		t.Error("a run that sampled reported zero work")
	}
	if got := flagValue(); got != tags.SignalClear {
		t.Errorf("flag after the skipped run = %q, want it left clear", got)
	}

	runOne, runTwo := first.RunID, second.RunID
	t.Cleanup(func() {
		cleanup := func(stmt string, arg any) {
			t.Helper()
			if _, err := client.DB().ExecContext(context.Background(), stmt, arg); err != nil {
				t.Errorf("clean up the audit fixture: %v", err)
			}
		}
		cleanup("DELETE FROM tag_coverage_event WHERE document_id = $1", docID)
		cleanup("DELETE FROM document_tag WHERE document_id = $1", docID)
		cleanup("DELETE FROM document WHERE id = $1", docID)
		cleanup("DELETE FROM tag_measurement WHERE run_id = $1", runOne)
		cleanup("DELETE FROM tag_measurement WHERE run_id = $1", runTwo)
		cleanup("DELETE FROM tag WHERE id = $1", tagID)
	})
}

// TestRunRearmsSignalWhenRunFails pins the failure contract the gate depends on: the open clears the
// flag, a later read fails on a live context, and the error path must set the flag again — otherwise a
// pre-failure mutation's derivation would stay skipped forever. The failure is injected by renaming the
// store-view table the run reads right after its open; the rename is restored on cleanup.
func TestRunRearmsSignalWhenRunFails(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	logger := log.NewDiscardLogger()
	cfg := config.DefaultConfig(t.TempDir())
	runner := NewAuditRunner(func() *config.Config { return cfg }, logger, client, nil)

	ctx := context.Background()
	if err := client.Queries.SetTagStoreSignal(ctx, database.SetTagStoreSignalParams{
		Name:  tags.SignalAuditChanged,
		Value: tags.SignalSet,
	}); err != nil {
		t.Fatalf("set the audit store-changed signal: %v", err)
	}

	if _, err := client.DB().ExecContext(ctx, "ALTER TABLE tag RENAME TO tag_gate_failure_probe"); err != nil {
		t.Fatalf("hide the store-view table from the run: %v", err)
	}
	t.Cleanup(func() {
		if _, err := client.DB().ExecContext(context.Background(),
			"ALTER TABLE tag_gate_failure_probe RENAME TO tag"); err != nil {
			t.Errorf("restore the store-view table: %v", err)
		}
	})

	if _, err := runner.Run(ctx, uuid.New()); err == nil {
		t.Fatal("run should fail when its store view cannot reach the tag table")
	}
	value, err := client.Queries.GetTagStoreSignal(ctx, tags.SignalAuditChanged)
	if err != nil {
		t.Fatalf("read the audit store-changed signal: %v", err)
	}
	if value != tags.SignalSet {
		t.Errorf("flag after a failed run = %q, want it re-armed", value)
	}
}
