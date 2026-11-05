package reconcile

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tools"
)

// TestReask_DisabledAdjudicatorMakesNoWork pins the pass's gate: without an enabled adjudicator it returns
// an empty report without touching the database.
func TestReask_DisabledAdjudicatorMakesNoWork(t *testing.T) {
	cfg := config.DefaultConfig(t.TempDir())
	cfg.Enricher.TagAdjudicator.Enabled = false
	runner := NewReaskRunner(func() *config.Config { return cfg }, log.NewDiscardLogger(), nil, &tools.Runner{})

	report, err := runner.Reask(context.Background(), 10)
	if err != nil {
		t.Fatalf("Reask: %v", err)
	}
	if *report != (ReaskReport{}) {
		t.Errorf("report = %+v, want the zero report with the adjudicator disabled", report)
	}
}

// contains reports whether values includes want.
func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

// TestReaskEligibilityLegs pins the two selection legs of the re-ask pass against the database: the
// retention leg takes an abstention older than the window and nothing younger or decided, and the
// multi-compound leg takes a young abstention only when more than one compound carried it while it was in
// force — a compound judged before the abstention existed never counts.
func TestReaskEligibilityLegs(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	ctx := context.Background()
	for _, table := range []string{"tag_part_dimension_event", "tag_composition_event"} {
		if _, err := client.DB().ExecContext(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}
	docID := database.CreateUniqueDocument(t, client.Queries, "reask-legs")

	const staleBeforeDays = 7
	staleBefore := time.Now().Add(-staleBeforeDays * 24 * time.Hour)
	seedDefer := func(value string, age time.Duration) {
		t.Helper()
		if _, err := client.DB().ExecContext(ctx, `
			INSERT INTO tag_part_dimension_event
			    (document_id, value, criteria, dimensional, verdict_model, policy_version, verdict_at)
			VALUES ($1, $2, '{"dimensional":false,"defer":true}'::jsonb, false, 'seed-model', $3, now() - $4::interval)`,
			docID, value, analyzer.TagPartDimensionVersion, fmt.Sprintf("%d seconds", int(age.Seconds()))); err != nil {
			t.Fatalf("seed defer for %q: %v", value, err)
		}
	}
	seedComposition := func(candidate string, age time.Duration) {
		t.Helper()
		if _, err := client.DB().ExecContext(ctx, `
			INSERT INTO tag_composition_event
			    (document_id, candidate, parts, disposition, reason, criteria_version, created_at)
			VALUES ($1, $2, '[]'::jsonb, 'keep', 'split_defer', $3, now() - $4::interval)`,
			docID, candidate, analyzer.TagSplitVersion, fmt.Sprintf("%d seconds", int(age.Seconds()))); err != nil {
			t.Fatalf("seed composition row for %q: %v", candidate, err)
		}
	}
	// a decided row older than the window must stay out: the legs select abstentions only
	if _, err := client.DB().ExecContext(ctx, `
		INSERT INTO tag_part_dimension_event
		    (document_id, value, criteria, dimensional, verdict_model, policy_version, verdict_at)
		VALUES ($1, 'decidedold', '{"dimensional":true,"defer":false}'::jsonb, true, 'seed-model', $2, now() - interval '10 days')`,
		docID, analyzer.TagPartDimensionVersion); err != nil {
		t.Fatalf("seed decided row: %v", err)
	}

	seedDefer("staleword", 10*24*time.Hour)
	seedDefer("duoword", 24*time.Hour)
	seedDefer("singleword", 24*time.Hour)
	seedDefer("windowedword", 24*time.Hour)

	seedComposition("duoword history", time.Minute)
	seedComposition("duoword war", time.Minute)
	seedComposition("singleword war", time.Minute)
	// only this one postdates the abstention; the older twin must not count toward the leg
	seedComposition("windowedword after", time.Minute)
	seedComposition("windowedword before", 48*time.Hour)

	stale, err := client.Queries.ListRetentionStaleDeferredPartValues(ctx, database.ListRetentionStaleDeferredPartValuesParams{
		PolicyVersion: analyzer.TagPartDimensionVersion,
		StaleBefore:   staleBefore,
		PageLimit:     100,
	})
	if err != nil {
		t.Fatalf("ListRetentionStaleDeferredPartValues: %v", err)
	}
	if !contains(stale, "staleword") {
		t.Errorf("retention leg = %v, want the abstention older than the window", stale)
	}
	for _, out := range []string{"duoword", "singleword", "decidedold"} {
		if contains(stale, out) {
			t.Errorf("retention leg = %v, must not select %q", stale, out)
		}
	}

	young, err := client.Queries.ListYoungDeferredPartValues(ctx, database.ListYoungDeferredPartValuesParams{
		PolicyVersion:        analyzer.TagPartDimensionVersion,
		StaleBefore:          staleBefore,
		SplitCriteriaVersion: analyzer.TagSplitVersion,
		PageLimit:            100,
	})
	if err != nil {
		t.Fatalf("ListYoungDeferredPartValues: %v", err)
	}
	if !contains(young, "duoword") {
		t.Errorf("multi-compound leg = %v, want the abstention withholding two compounds", young)
	}
	for _, out := range []string{"staleword", "singleword", "windowedword"} {
		if contains(young, out) {
			t.Errorf("multi-compound leg = %v, must not select %q", young, out)
		}
	}
}
