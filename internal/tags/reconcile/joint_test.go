package reconcile

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/tools"
)

// jointSweepConfig returns the only configuration decideCompoundSplits reads: whether the adjudicator is on.
func jointSweepConfig(adjudicatorEnabled bool) *config.Config {
	return &config.Config{Enricher: config.EnricherConfig{
		TagAdjudicator: config.TagAdjudicatorConfig{Enabled: adjudicatorEnabled},
	}}
}

// entryNamed returns the worklist entry for a candidate, failing the test when the worklist dropped it.
func entryNamed(t *testing.T, entries []*compoundEntry, name string) *compoundEntry {
	t.Helper()
	for _, e := range entries {
		if e.name == name {
			return e
		}
	}
	t.Fatalf("worklist has no entry for %q", name)
	return nil
}

// TestDecideCompoundSplits_DeterministicWithoutAdjudicator covers the sweep's degraded path: with the
// adjudicator off, only explicit coordination splits — mechanically, from the matched conjunction segments —
// and a name whose segments all filter away is reported rather than silently split.
func TestDecideCompoundSplits_DeterministicWithoutAdjudicator(t *testing.T) {
	r := &Reconciler{logger: testutil.NewTestLogger()}
	entries := []*compoundEntry{
		{name: "supply and demand"},
		{name: "one two three four and five six seven eight"},
	}

	r.decideCompoundSplits(context.Background(), jointSweepConfig(false), entries, nil, nil)

	split, invalid := entries[0], entries[1]
	if !split.split {
		t.Error("an explicit conjunction must split deterministically without an adjudicator")
	}
	if want := []string{"supply", "demand"}; !reflect.DeepEqual(split.components, want) {
		t.Errorf("components = %v, want %v", split.components, want)
	}
	if split.reason != "deterministic" {
		t.Errorf("reason = %q, want deterministic", split.reason)
	}
	if split.skipReason != "" {
		t.Errorf("skipReason = %q, want empty for a decided split", split.skipReason)
	}
	if invalid.split {
		t.Error("a name with nothing left after the function-word drop must not split")
	}
	if invalid.skipReason != "split_invalid" {
		t.Errorf("skipReason = %q, want split_invalid", invalid.skipReason)
	}
}

// TestDecideCompoundSplits_DisposesThroughDecision pins the sweep's arm of the composition decision: verdicts
// read from the persisted caches dispose through the same admission judgment the emission gate uses, so a
// composition whose parts are dimensional splits and a named unit whose parts are not dimensional keeps with
// the decision reason as the report's skip reason.
func TestDecideCompoundSplits_DisposesThroughDecision(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	resetSplitVerdicts(t, client.DB())

	seedPartVerdicts(t, client.Queries, []string{"supply", "demand"}, true)
	seedPartVerdicts(t, client.Queries, []string{"rock", "roll"}, false)
	seedCompoundVerdict(t, client.Queries, "supply and demand", analyzer.TagCompoundComposition)
	seedCompoundVerdict(t, client.Queries, "rock and roll", analyzer.TagCompoundNamedUnit)

	r := &Reconciler{
		client: client,
		tag:    curation.NewTag(client, testutil.NewTestLogger(), testutil.NewMockEmbedder()),
		runner: &tools.Runner{},
		logger: testutil.NewTestLogger(),
	}
	allTags := []database.Tag{
		{ID: 1, Name: "supply and demand"},
		{ID: 2, Name: "rock and roll"},
	}
	entries := r.buildCompoundWorklist(ctx, allTags, nil, &Report{Skips: map[string]int{}})
	if len(entries) != 2 {
		t.Fatalf("worklist = %d entries, want the two compound names", len(entries))
	}

	r.decideCompoundSplits(ctx, jointSweepConfig(true), entries, nil, nil)

	split := entryNamed(t, entries, "supply and demand")
	if !split.split {
		t.Fatal("supply and demand: split = false, want it disposed as a composition split")
	}
	if want := []string{"supply", "demand"}; !reflect.DeepEqual(split.components, want) {
		t.Errorf("supply and demand: components = %v, want %v", split.components, want)
	}
	if split.reason != "adjudicated" {
		t.Errorf("supply and demand: reason = %q, want adjudicated", split.reason)
	}

	kept := entryNamed(t, entries, "rock and roll")
	if kept.split {
		t.Error("rock and roll: a named unit must not split")
	}
	if kept.skipReason != "named_unit" {
		t.Errorf("rock and roll: skipReason = %q, want the decision reason named_unit", kept.skipReason)
	}
}

// resetSplitVerdicts clears the two append-only verdict tables so this test's fixed seed keys stay
// idempotent across runs, the way the database package's resetDB clears the tables it seeds.
func resetSplitVerdicts(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"tag_part_dimension_event", "tag_compound_status_event"} {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}
}

// seedPartVerdicts writes the parts-pass verdicts the sweep reads from cache, so no judge call is needed to
// dispose the entries. The rows carry no document: this is the sweep's provenance.
func seedPartVerdicts(t *testing.T, q *database.Queries, values []string, dimensional bool) {
	t.Helper()
	ctx := context.Background()
	for _, value := range values {
		criteria := `{"dimensional":` + strconv.FormatBool(dimensional) + `,"defer":false}`
		n, err := q.InsertTagPartDimensionEvent(ctx, database.InsertTagPartDimensionEventParams{
			DocumentID:    sql.NullInt64{},
			Value:         value,
			Criteria:      []byte(criteria),
			Dimensional:   dimensional,
			VerdictModel:  sql.NullString{String: "judge-m", Valid: true},
			PolicyVersion: analyzer.TagPartDimensionVersion,
		})
		if err != nil || n != 1 {
			t.Fatalf("seed part verdict for %q: rows=%d err=%v", value, n, err)
		}
	}
}

// seedCompoundVerdict writes one compound status under the current criteria version, the row the sweep's
// compound pass reads back.
func seedCompoundVerdict(t *testing.T, q *database.Queries, candidate string, status analyzer.TagCompoundStatus) {
	t.Helper()
	n, err := q.InsertTagCompoundStatusEvent(context.Background(), database.InsertTagCompoundStatusEventParams{
		DocumentID:           sql.NullInt64{},
		Candidate:            candidate,
		Status:               string(status),
		Criteria:             []byte(`{"status":"` + string(status) + `","defer":false}`),
		VerdictModel:         sql.NullString{String: "judge-m", Valid: true},
		SplitCriteriaVersion: analyzer.TagSplitVersion,
	})
	if err != nil || n != 1 {
		t.Fatalf("seed compound verdict for %q: rows=%d err=%v", candidate, n, err)
	}
}
