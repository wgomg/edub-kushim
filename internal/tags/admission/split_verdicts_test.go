package admission

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tools"
)

// newTestVerdictStore wires a store over a fresh test database, with the judge runner handing results back
// through adj. The returned document is a real row for the FK the emission path stamps.
func newTestVerdictStore(t *testing.T, adj *mockAdjudicator) (*VerdictStore, *database.Queries, int64) {
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

	return NewVerdictStore(q, runner, logger, "test-model"), q, database.CreateUniqueDocument(t, q, "verdict-store")
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
}
