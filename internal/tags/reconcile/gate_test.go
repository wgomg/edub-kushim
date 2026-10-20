package reconcile

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
)

// quietGateStore strips every source the opening probes read — verdict evidence, stored tags, the
// measurement worklists, and prior reconcile tasks — and clears the flag, so each gate test starts from
// an unambiguous quiet store regardless of what ran before it in the package.
func quietGateStore(t *testing.T, client *database.Client) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		"DELETE FROM document_tag",
		"DELETE FROM tag_alias",
		"DELETE FROM tag_verdict_event",
		"DELETE FROM tag_measurement",
		"DELETE FROM task WHERE task_type = 'tagreconcile'",
		"DELETE FROM tag",
	} {
		if _, err := client.DB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("quiet the store via %q: %v", stmt, err)
		}
	}
	if err := client.Queries.SetTagStoreSignal(ctx, database.SetTagStoreSignalParams{
		Name:  tags.SignalReconcileChanged,
		Value: tags.SignalClear,
	}); err != nil {
		t.Fatalf("clear the reconcile store-changed signal: %v", err)
	}
}

// newGateReconciler wires the pass over the quiet store with the default config rooted at a throwaway
// directory, so the bootstrap log probe reads nothing from a developer machine. The adjudicator is
// enabled because Run's grace sweeps live behind that branch; report-only stays at its default, which
// keeps every judge path clear of the tools runner.
func newGateReconciler(t *testing.T, client *database.Client) *Reconciler {
	t.Helper()
	cfg := config.DefaultConfig(t.TempDir())
	cfg.Enricher.TagAdjudicator.Enabled = true
	logger := log.NewDiscardLogger()
	return NewReconciler(func() *config.Config { return cfg }, client, curation.NewTag(client, logger, nil), nil, logger)
}

// seedCompletedReconcileTask records the completed run the zero-work gate requires behind it.
func seedCompletedReconcileTask(t *testing.T, client *database.Client) {
	t.Helper()
	if _, err := client.Queries.CreateTask(context.Background(), database.CreateTaskParams{
		TaskID:   uuid.New().String(),
		TaskType: "tagreconcile",
		Status:   "completed",
	}); err != nil {
		t.Fatalf("seed a completed reconcile task: %v", err)
	}
}

func reconcileFlagValue(t *testing.T, client *database.Client) string {
	t.Helper()
	value, err := client.Queries.GetTagStoreSignal(context.Background(), tags.SignalReconcileChanged)
	if err != nil {
		t.Fatalf("read the reconcile store-changed signal: %v", err)
	}
	return value
}

// TestRunZeroWorkOnQuietStore pins the gate's happy path: with a completed run behind it, a clear flag,
// and every work probe empty, the pass reports zero work and leaves the flag clear.
func TestRunZeroWorkOnQuietStore(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	quietGateStore(t, client)
	seedCompletedReconcileTask(t, client)

	report, err := newGateReconciler(t, client).Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !report.ZeroWork {
		t.Error("quiet store with a clear signal reported work")
	}
	if got := reconcileFlagValue(t, client); got != tags.SignalClear {
		t.Errorf("flag after a zero-work run = %q, want it left clear", got)
	}
}

// TestRunFullAfterStoreMutation pins the signal leg: a mutation re-arms the flag through the trigger,
// the next run takes the full path instead of skipping, and the completed run clears the flag again.
func TestRunFullAfterStoreMutation(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	quietGateStore(t, client)
	seedCompletedReconcileTask(t, client)
	database.MustCreateTag(t, client.Queries, "gazetteer")

	report, err := newGateReconciler(t, client).Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.ZeroWork {
		t.Error("a changed store was skipped: the mutation must re-arm the flag into a full pass")
	}
	if got := reconcileFlagValue(t, client); got != tags.SignalClear {
		t.Errorf("flag after a full run = %q, want it cleared at open", got)
	}
}

// TestRunWorksWhenProvisionalGraceElapsed pins the probe leg: time creates work a clear signal cannot
// cover, so a provisional tag past its grace window must reach the arm and be queued for re-judging.
func TestRunWorksWhenProvisionalGraceElapsed(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	quietGateStore(t, client)
	seedCompletedReconcileTask(t, client)

	tagID := database.MustCreateTag(t, client.Queries, "gazetteer")
	docID, _ := database.CreateTestDocument(t, client.Queries, "provisional gate fixture")
	database.MustAddDocumentTag(t, client.Queries, docID, tagID)
	if _, err := client.DB().ExecContext(context.Background(),
		"UPDATE tag SET created_at = now() - interval '60 days' WHERE id = $1", tagID); err != nil {
		t.Fatalf("backdate the provisional tag: %v", err)
	}
	// the backdate sits outside the trigger's semantic columns, so the store must be told it changed
	// before its last open the way a real pre-run mutation would have left it
	if err := client.Queries.SetTagStoreSignal(context.Background(), database.SetTagStoreSignalParams{
		Name:  tags.SignalReconcileChanged,
		Value: tags.SignalClear,
	}); err != nil {
		t.Fatalf("clear the reconcile store-changed signal: %v", err)
	}

	report, err := newGateReconciler(t, client).Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.ZeroWork {
		t.Error("a provisional tag past its grace window was skipped: the probe must fire")
	}
	if report.Provisional.Rejudged != 1 {
		t.Errorf("rejudged = %d, want 1: the due provisional tag must reach the arm", report.Provisional.Rejudged)
	}
}

// TestRunBootstrapsWithoutCompletedRun pins the completed-run leg: the gate must never fire before a
// first completed run exists, or a store owed its bootstrap report-only pass would never get it.
func TestRunBootstrapsWithoutCompletedRun(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	quietGateStore(t, client)

	report, err := newGateReconciler(t, client).Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.ZeroWork {
		t.Error("zero work reported with no completed run behind it: the bootstrap pass must run")
	}
	if !report.ReportOnly {
		t.Error("the first run must be report-only")
	}
}

// TestRunRearmsSignalWhenRunFails pins the failure scenario the gate depends on: the open clears the
// flag, a later read fails on a live context, and the error path must set the flag again — the next run
// cannot read the failed run as an unchanged store. The failure is injected by renaming the bootstrap
// table the run reads right after its open; the rename is restored on cleanup.
func TestRunRearmsSignalWhenRunFails(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	quietGateStore(t, client)

	ctx := context.Background()
	if err := client.Queries.SetTagStoreSignal(ctx, database.SetTagStoreSignalParams{
		Name:  tags.SignalReconcileChanged,
		Value: tags.SignalSet,
	}); err != nil {
		t.Fatalf("set the reconcile store-changed signal: %v", err)
	}
	if _, err := client.DB().ExecContext(ctx,
		"ALTER TABLE task RENAME TO task_gate_failure_probe"); err != nil {
		t.Fatalf("hide the task table from the run: %v", err)
	}
	t.Cleanup(func() {
		if _, err := client.DB().ExecContext(context.Background(),
			"ALTER TABLE task_gate_failure_probe RENAME TO task"); err != nil {
			t.Errorf("restore the task table: %v", err)
		}
	})

	if _, err := newGateReconciler(t, client).Run(ctx, nil); err == nil {
		t.Fatal("run should fail when its bootstrap read cannot reach the task table")
	}
	if got := reconcileFlagValue(t, client); got != tags.SignalSet {
		t.Errorf("flag after a failed run = %q, want it re-armed", got)
	}
}
