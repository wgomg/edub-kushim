package admission

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tools"
)

// newTestVerdictStore wires a store over a fresh test database, with the judge runner handing results back
// through adj. The returned document is a real row for the FK the emission path stamps.
func newTestVerdictStore(t *testing.T, adj *mockAdjudicator) (*VerdictStore, *database.Queries, int64) {
	return newTestVerdictStoreWithPromoted(t, adj, nil)
}

// newTestVerdictStoreWithPromoted is newTestVerdictStore with the promoted-value set the false-verdict
// guard reads; a nil set means no value counts as promoted.
func newTestVerdictStoreWithPromoted(t *testing.T, adj *mockAdjudicator, promoted map[string]struct{}) (*VerdictStore, *database.Queries, int64) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	q, db := database.NewTestQueries(t)
	t.Cleanup(func() { db.Close() })

	logger := log.NewDiscardLogger()
	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{
			TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0},
		},
	})
	setUnexportedField(t, runner, "tagAdjudicator", adj)

	return NewVerdictStore(q, runner, logger, "test-model", promoted), q, database.CreateUniqueDocument(t, q, "verdict-store")
}

// TestWriteCompoundVerdict_ConflictComparesReading pins the conflict reread's comparison: the stored row and
// the fresh sample are both read on the judge's reading, so two workers that abstained the same way agree,
// while only a genuinely different reading sets Disagreement. Comparing the row status instead — where an
// abstention is normalized to the defer status — would report a disagreement that never happened and route
// the candidate to review under the wrong reason.
func TestWriteCompoundVerdict_ConflictComparesReading(t *testing.T) {
	store, q, docID := newTestVerdictStore(t, &mockAdjudicator{})
	ctx := context.Background()

	tests := []struct {
		name             string
		stored           string
		storedStatus     string
		answer           analyzer.TagCompoundResult
		wantDisagreement bool
		wantStatus       analyzer.TagCompoundStatus
		wantDeferred     bool
	}{
		{
			name:         "the same abstention on both sides agrees",
			stored:       `{"status":"composition","defer":true}`,
			storedStatus: "defer",
			answer:       analyzer.TagCompoundResult{Status: analyzer.TagCompoundComposition, Defer: true},
			wantStatus:   analyzer.TagCompoundComposition,
			wantDeferred: true,
		},
		{
			name:             "a stored reading that differs disagrees",
			stored:           `{"status":"named_unit","defer":false}`,
			storedStatus:     "named_unit",
			answer:           analyzer.TagCompoundResult{Status: analyzer.TagCompoundComposition, Defer: false},
			wantDisagreement: true,
			wantStatus:       analyzer.TagCompoundNamedUnit,
			wantDeferred:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := fmt.Sprintf("%s candidate for %d", tt.storedStatus, docID)
			seed := database.InsertTagCompoundStatusEventParams{
				DocumentID:           sql.NullInt64{Int64: docID, Valid: true},
				Candidate:            candidate,
				Status:               tt.storedStatus,
				Criteria:             []byte(tt.stored),
				VerdictModel:         sql.NullString{String: "judge-m", Valid: true},
				SplitCriteriaVersion: analyzer.TagSplitVersion,
			}
			if n, err := q.InsertTagCompoundStatusEvent(ctx, seed); err != nil || n != 1 {
				t.Fatalf("seed the first writer's row: rows=%d err=%v", n, err)
			}

			got := store.writeCompoundVerdict(ctx, docID, candidate, tt.answer, "")

			if got.Disagreement != tt.wantDisagreement {
				t.Errorf("Disagreement = %t, want %t", got.Disagreement, tt.wantDisagreement)
			}
			if !got.Cached {
				t.Error("a conflict must hand back the first writer's stored verdict")
			}
			if got.Status != tt.wantStatus || got.Deferred != tt.wantDeferred {
				t.Errorf("stored verdict = %s/%t, want %s/%t", got.Status, got.Deferred, tt.wantStatus, tt.wantDeferred)
			}
		})
	}
}

// TestVerdictStore_SalvageAppliesPresentAndDefersMissing pins the incomplete-reply contract the split passes
// share: the entries the judge did return are persisted and applied, while an omitted index stays out of the
// result so the fold reads it as judge_unavailable. A partial reply is never retried — its omissions are
// systematic at a fixed output budget.
func TestVerdictStore_SalvageAppliesPresentAndDefersMissing(t *testing.T) {
	adj := &mockAdjudicator{
		partFn: func(_ context.Context, values []string) ([]analyzer.TagPartResult, error) {
			return []analyzer.TagPartResult{{Index: 0, Dimensional: true}, {Index: 1}},
				&analyzer.IncompleteReplyError{Missing: []int{1}}
		},
		compoundFn: func(_ context.Context, candidates []string) ([]analyzer.TagCompoundResult, error) {
			return []analyzer.TagCompoundResult{{Index: 0, Status: analyzer.TagCompoundComposition}, {Index: 1}},
				&analyzer.IncompleteReplyError{Missing: []int{1}}
		},
		phraseFoldFn: func(_ context.Context, candidates []analyzer.TagPhraseFoldCandidate) ([]analyzer.TagPhraseFoldResult, error) {
			return []analyzer.TagPhraseFoldResult{{Index: 0, Fold: true}, {Index: 1}},
				&analyzer.IncompleteReplyError{Missing: []int{1}}
		},
	}
	store, q, docID := newTestVerdictStore(t, adj)
	ctx := context.Background()

	t.Run("parts", func(t *testing.T) {
		present := fmt.Sprintf("salvaged part %d", docID)
		missing := fmt.Sprintf("omitted part %d", docID)

		verdicts, err := store.Parts(ctx, []string{present, missing}, docID, "")
		if err != nil {
			t.Fatalf("Parts: %v", err)
		}
		if _, ok := verdicts[present]; !ok {
			t.Errorf("the returned entry %q was not applied", present)
		}
		if _, ok := verdicts[missing]; ok {
			t.Errorf("the omitted entry %q was applied instead of deferred", missing)
		}
		if adj.partCall != 1 {
			t.Errorf("judge calls = %d, want 1 — an incomplete reply is never retried", adj.partCall)
		}

		rows, err := q.LatestTagPartVerdictsForValues(ctx, database.LatestTagPartVerdictsForValuesParams{
			PolicyVersion: analyzer.TagPartDimensionVersion,
			PartValues:    []string{present, missing},
		})
		if err != nil {
			t.Fatalf("read back the salvaged verdicts: %v", err)
		}
		if len(rows) != 1 || rows[0].Value != present {
			t.Errorf("persisted part rows = %v, want only the salvaged entry", rows)
		}
	})

	t.Run("compounds", func(t *testing.T) {
		present := fmt.Sprintf("salvaged candidate %d", docID)
		missing := fmt.Sprintf("omitted candidate %d", docID)

		verdicts, err := store.Compounds(ctx, []string{present, missing}, docID, "")
		if err != nil {
			t.Fatalf("Compounds: %v", err)
		}
		if v, ok := verdicts[present]; !ok || v.Status != analyzer.TagCompoundComposition {
			t.Errorf("the returned entry %q = %+v, want the judged composition", present, v)
		}
		if _, ok := verdicts[missing]; ok {
			t.Errorf("the omitted entry %q was applied instead of deferred", missing)
		}
		if adj.compoundCall != 1 {
			t.Errorf("judge calls = %d, want 1 — an incomplete reply is never retried", adj.compoundCall)
		}

		rows, err := q.LatestTagCompoundStatusForCandidates(ctx, database.LatestTagCompoundStatusForCandidatesParams{
			SplitCriteriaVersion: analyzer.TagSplitVersion,
			Candidates:           []string{present, missing},
		})
		if err != nil {
			t.Fatalf("read back the salvaged verdicts: %v", err)
		}
		if len(rows) != 1 || rows[0].Candidate != present {
			t.Errorf("persisted compound rows = %v, want only the salvaged entry", rows)
		}
	})

	t.Run("phrase folds", func(t *testing.T) {
		phrase := fmt.Sprintf("civil war %d", docID)
		present := PhraseFoldKey{Phrase: phrase, Part: "civil", Anchor: "state"}
		missing := PhraseFoldKey{Phrase: phrase, Part: "civil", Anchor: "nation"}

		verdicts, err := store.PhraseFolds(ctx, []PhraseFoldKey{present, missing}, docID, "")
		if err != nil {
			t.Fatalf("PhraseFolds: %v", err)
		}
		if v, ok := verdicts[present]; !ok || !v.Fold || v.Deferred {
			t.Errorf("the returned key = %+v, want the judged fold", v)
		}
		if _, ok := verdicts[missing]; ok {
			t.Errorf("the omitted key was applied instead of deferred: %+v", verdicts[missing])
		}
		if adj.phraseFoldCall != 1 {
			t.Errorf("judge calls = %d, want 1 — an incomplete reply is never retried", adj.phraseFoldCall)
		}

		rows, err := q.LatestTagPhraseFoldVerdictsForPhrases(ctx, database.LatestTagPhraseFoldVerdictsForPhrasesParams{
			CriteriaVersion: analyzer.TagPhraseFoldVersion,
			Phrases:         []string{phrase},
		})
		if err != nil {
			t.Fatalf("read back the salvaged verdicts: %v", err)
		}
		if len(rows) != 1 || rows[0].Part != "civil" || rows[0].Anchor != "state" || !rows[0].Fold {
			t.Errorf("persisted phrase-fold rows = %v, want only the salvaged fold", rows)
		}

		// the salvaged answer is the cache: re-reading the judged key asks the judge nothing again
		reread, err := store.PhraseFolds(ctx, []PhraseFoldKey{present}, docID, "")
		if err != nil {
			t.Fatalf("PhraseFolds re-read: %v", err)
		}
		if adj.phraseFoldCall != 1 {
			t.Errorf("judge calls after the re-read = %d, want 1 — the cache answers the salvaged key", adj.phraseFoldCall)
		}
		if v, ok := reread[present]; !ok || !v.Fold {
			t.Errorf("re-read key = %+v, want the cached fold", v)
		}
	})
}

// TestVerdictStore_PartFalseGuard pins the destructive-direction guard on a fresh part verdict:
// a false answer on a value the store already holds promoted is never persisted as a plain false —
// the store signal wins and the verdict defers to review without a second sample — and any other fresh
// false must be corroborated by a second sample before it is cached, deferring when the samples disagree.
func TestVerdictStore_PartFalseGuard(t *testing.T) {
	ctx := context.Background()

	t.Run("a false verdict on a promoted value defers without a second sample", func(t *testing.T) {
		adj := &mockAdjudicator{partFn: func(_ context.Context, values []string) ([]analyzer.TagPartResult, error) {
			results := make([]analyzer.TagPartResult, len(values))
			for i := range values {
				results[i] = analyzer.TagPartResult{Index: i, Dimensional: false}
			}
			return results, nil
		}}
		promoted := map[string]struct{}{tags.Key("economy"): {}}
		store, q, docID := newTestVerdictStoreWithPromoted(t, adj, promoted)

		verdicts, err := store.Parts(ctx, []string{"economy"}, docID, "")
		if err != nil {
			t.Fatalf("Parts: %v", err)
		}
		v, ok := verdicts["economy"]
		if !ok {
			t.Fatal("the promoted value needs a verdict")
		}
		if v.Dimensional || !v.Deferred {
			t.Errorf("verdict = dimensional:%t defer:%t, want a defer the store signal set", v.Dimensional, v.Deferred)
		}
		if adj.partCall != 1 {
			t.Errorf("judge calls = %d, want 1 — a promoted value's false is never re-sampled", adj.partCall)
		}

		rows, err := q.LatestTagPartVerdictsForValues(ctx, database.LatestTagPartVerdictsForValuesParams{
			PolicyVersion: analyzer.TagPartDimensionVersion,
			PartValues:    []string{"economy"},
		})
		if err != nil {
			t.Fatalf("read back the verdict: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("persisted part rows = %d, want 1", len(rows))
		}
		var criteria analyzer.TagPartCriteria
		if err := json.Unmarshal(rows[0].Criteria, &criteria); err != nil {
			t.Fatalf("persisted criteria %q: %v", rows[0].Criteria, err)
		}
		if criteria.Defer == nil || !*criteria.Defer {
			t.Errorf("persisted criteria = %s, want the defer the store signal set", rows[0].Criteria)
		}
		if criteria.Dimensional == nil || *criteria.Dimensional {
			t.Errorf("persisted criteria = %s, want a false the store signal converted", rows[0].Criteria)
		}
	})

	t.Run("a false the second sample contradicts defers", func(t *testing.T) {
		call := 0
		adj := &mockAdjudicator{partFn: func(_ context.Context, values []string) ([]analyzer.TagPartResult, error) {
			call++
			results := make([]analyzer.TagPartResult, len(values))
			for i := range values {
				results[i] = analyzer.TagPartResult{Index: i, Dimensional: call > 1}
			}
			return results, nil
		}}
		store, _, docID := newTestVerdictStore(t, adj)

		verdicts, err := store.Parts(ctx, []string{"entropy budget"}, docID, "")
		if err != nil {
			t.Fatalf("Parts: %v", err)
		}
		v, ok := verdicts["entropy budget"]
		if !ok {
			t.Fatal("the value needs a verdict")
		}
		if v.Dimensional || !v.Deferred {
			t.Errorf("verdict = dimensional:%t defer:%t, want a defer from the disagreement", v.Dimensional, v.Deferred)
		}
		if adj.partCall != 2 {
			t.Errorf("judge calls = %d, want 2 — the fresh false took one corroborating sample", adj.partCall)
		}
	})

	t.Run("a corroborated false persists as false", func(t *testing.T) {
		adj := &mockAdjudicator{partFn: func(_ context.Context, values []string) ([]analyzer.TagPartResult, error) {
			results := make([]analyzer.TagPartResult, len(values))
			for i := range values {
				results[i] = analyzer.TagPartResult{Index: i, Dimensional: false}
			}
			return results, nil
		}}
		store, _, docID := newTestVerdictStore(t, adj)

		verdicts, err := store.Parts(ctx, []string{"eviction notice"}, docID, "")
		if err != nil {
			t.Fatalf("Parts: %v", err)
		}
		v, ok := verdicts["eviction notice"]
		if !ok {
			t.Fatal("the value needs a verdict")
		}
		if v.Dimensional || v.Deferred {
			t.Errorf("verdict = dimensional:%t defer:%t, want a corroborated false", v.Dimensional, v.Deferred)
		}
		if adj.partCall != 2 {
			t.Errorf("judge calls = %d, want 2 — the fresh false took one corroborating sample", adj.partCall)
		}
	})
}
