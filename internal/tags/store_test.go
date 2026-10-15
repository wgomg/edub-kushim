package tags

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
)

func TestReconstructibilityBatch(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	q := client.Queries

	compoundID := database.MustCreateTag(t, q, "world war 2")
	partID := database.MustCreateTag(t, q, "railway timetable")
	both := database.CreateUniqueDocument(t, q, "compound and part")
	partOnly := database.CreateUniqueDocument(t, q, "part only")
	database.MustAddDocumentTag(t, q, both, compoundID)
	database.MustAddDocumentTag(t, q, both, partID)
	database.MustAddDocumentTag(t, q, partOnly, partID)

	store := NewStore(q)

	t.Run("measures a held compound over its live parts", func(t *testing.T) {
		readings, err := store.ReconstructibilityBatch(ctx,
			[]string{"world war 2"}, [][]string{{"railway timetable"}})
		if err != nil {
			t.Fatalf("ReconstructibilityBatch: %v", err)
		}
		got, ok := readings["world war 2"]
		if !ok {
			t.Fatalf("readings = %v, want an entry for world war 2", readings)
		}
		if got.CompoundMass != 1 || got.PartsIntersection != 2 || got.Both != 1 {
			t.Errorf("counts = {mass:%d intersection:%d both:%d}, want {1 2 1}",
				got.CompoundMass, got.PartsIntersection, got.Both)
		}
		if got.State != StateSufficient {
			t.Errorf("State = %q, want %q", got.State, StateSufficient)
		}
	})

	t.Run("the house form of a judged spelling resolves to the stored compound", func(t *testing.T) {
		readings, err := store.ReconstructibilityBatch(ctx,
			[]string{"world war ii", "world war 2"},
			[][]string{{"railway timetable"}, {"railway timetable"}})
		if err != nil {
			t.Fatalf("ReconstructibilityBatch: %v", err)
		}
		viaHouseForm, ok := readings["world war ii"]
		if !ok {
			t.Fatalf("readings = %v, want the judged spelling to resolve", readings)
		}
		viaStoreName := readings["world war 2"]
		if viaHouseForm.PartsIntersection != viaStoreName.PartsIntersection ||
			viaHouseForm.CompoundMass != viaStoreName.CompoundMass {
			t.Errorf("one store compound measured two ways: house form %+v, store name %+v",
				viaHouseForm, viaStoreName)
		}
	})

	t.Run("names the store does not hold leave no reading", func(t *testing.T) {
		readings, err := store.ReconstructibilityBatch(ctx,
			[]string{"utterly new compound", "world war 2"},
			[][]string{{"railway timetable"}, {"missing part"}})
		if err != nil {
			t.Fatalf("ReconstructibilityBatch: %v", err)
		}
		if _, ok := readings["utterly new compound"]; ok {
			t.Errorf("unknown compound got a reading: %+v", readings["utterly new compound"])
		}
		if _, ok := readings["world war 2"]; ok {
			t.Errorf("a cover whose part is not in the store got a reading: %+v", readings["world war 2"])
		}
	})
}

func TestSiblingWorklist(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	if _, err := client.DB().ExecContext(ctx, "DELETE FROM tag_measurement"); err != nil {
		t.Fatalf("clear measurement journal: %v", err)
	}
	store := NewStore(client.Queries)

	absent, err := store.SiblingWorklist(ctx)
	if err != nil {
		t.Fatalf("SiblingWorklist on an empty journal: %v", err)
	}
	if !absent.Absent {
		t.Error("Absent = false on a store with no measurement run")
	}

	runID := uuid.New()
	err = store.InsertRecords(ctx, runID, time.Now(), RuleSetVersion, []Record{
		{
			Operator: OperatorOverlap,
			Operands: []string{"broad topic", "narrow topic"},
			Numbers: map[string]any{
				NumberJaccard:      0.8,
				NumberContainmentA: 0.8,
				NumberContainmentB: 1.0,
				NumberMassA:        int64(5),
				NumberMassB:        int64(4),
			},
			State: StateSufficient,
		},
		reconstructRecord("steam engine", 0.05, 0.05, StateSufficient),
	})
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}

	snap, err := store.SiblingWorklist(ctx)
	if err != nil {
		t.Fatalf("SiblingWorklist: %v", err)
	}
	if snap.Absent {
		t.Fatal("Absent = true after inserting a run")
	}
	if snap.RunID != runID {
		t.Errorf("RunID = %v, want %v", snap.RunID, runID)
	}
	if len(snap.Worklists.Siblings) != 1 {
		t.Fatalf("Siblings = %v, want 1 pair", snap.Worklists.Siblings)
	}
	sibling := snap.Worklists.Siblings[0]
	if sibling.A != "broad topic" || sibling.B != "narrow topic" {
		t.Errorf("Sibling = {%s ⊇ %s}, want {broad topic ⊇ narrow topic}", sibling.A, sibling.B)
	}
	if len(snap.Worklists.Twins) != 0 {
		t.Errorf("Twins = %v, want none: the sibling read keeps only the containment worklist", snap.Worklists.Twins)
	}
	if len(snap.Worklists.CompoundSplits) != 0 {
		t.Errorf("CompoundSplits = %v, want none: only the overlap operator is decoded", snap.Worklists.CompoundSplits)
	}
}
