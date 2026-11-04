package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// newProvisionalCompound creates a two-word tag carrying one live document, the shape an operator
// split or keep decision applies to.
func newProvisionalCompound(t *testing.T, client *database.Client, name string) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	id := database.MustCreateTag(t, client.Queries, name)
	_, err := client.DB().ExecContext(ctx, "UPDATE tag SET promoted_at = NULL WHERE id = $1", id)
	testutil.AssertNoError(t, err, "set provisional")
	docID := database.CreateUniqueDocument(t, client.Queries, name+" doc")
	database.MustAddDocumentTag(t, client.Queries, docID, id)
	return id, docID
}

// latestComposition returns the newest journal row for one candidate; a missing row fails the test.
func latestComposition(t *testing.T, client *database.Client, name string) database.LatestCompositionEventsForNamesRow {
	t.Helper()
	rows, err := client.Queries.LatestCompositionEventsForNames(context.Background(), []string{name})
	testutil.AssertNoError(t, err, "read composition journal")
	if len(rows) != 1 {
		t.Fatalf("composition rows for %q = %d, want exactly 1", name, len(rows))
	}
	return rows[0]
}

// assertNoComposition asserts that no journal row was recorded for a candidate.
func assertNoComposition(t *testing.T, client *database.Client, name string) {
	t.Helper()
	rows, err := client.Queries.LatestCompositionEventsForNames(context.Background(), []string{name})
	testutil.AssertNoError(t, err, "read composition journal")
	if len(rows) != 0 {
		t.Errorf("composition rows for %q = %d, want none", name, len(rows))
	}
}

func countDocumentTags(t *testing.T, client *database.Client, docID int64) int {
	t.Helper()
	var count int
	testutil.AssertNoError(t,
		client.DB().QueryRowContext(context.Background(),
			"SELECT count(*) FROM document_tag WHERE document_id = $1", docID).Scan(&count),
		"count document tags")
	return count
}

// TestTag_ApplyCompositionDecision pins the operator's split and keep decisions: a keep leaves the
// compound standing with its journal record, a split removes the row and moves its documents onto the
// parts, and every refusal leaves the store exactly as it was — including the part rows a refused
// decision had already created.
func TestTag_ApplyCompositionDecision(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	t.Run("keep whole leaves the row standing and journals the decision", func(t *testing.T) {
		id, _ := newProvisionalCompound(t, client, "keep whole compound")

		status, err := svc.ApplyCompositionDecision(ctx, id, "keep", nil)
		testutil.AssertNoError(t, err, "keep")
		testutil.AssertEqual(t, status, types.CompositionApplied, "status")

		if _, err := client.Queries.GetTagByName(ctx, "keep whole compound"); err != nil {
			t.Errorf("the compound must survive a keep: %v", err)
		}
		row := latestComposition(t, client, "keep whole compound")
		testutil.AssertEqual(t, row.Disposition, string(tags.CompositionKeep), "disposition")
		testutil.AssertEqual(t, row.Reason, tags.CompositionReasonOperatorKeep, "reason")
		testutil.AssertEqual(t, row.Model.String, "operator", "model")
		testutil.AssertEqual(t, row.CriteriaVersion, analyzer.TagSplitVersion, "criteria version")
	})

	t.Run("keep with parts attaches them to the same documents", func(t *testing.T) {
		id, docID := newProvisionalCompound(t, client, "keep parts compound")

		status, err := svc.ApplyCompositionDecision(ctx, id, "keep", []string{"alongside part a", "alongside part b"})
		testutil.AssertNoError(t, err, "keep with parts")
		testutil.AssertEqual(t, status, types.CompositionApplied, "status")

		for _, part := range []string{"alongside part a", "alongside part b"} {
			if _, err := client.Queries.GetTagByName(ctx, part); err != nil {
				t.Errorf("part %q must exist: %v", part, err)
			}
		}
		if got := countDocumentTags(t, client, docID); got != 3 {
			t.Errorf("document tags = %d, want the compound and both parts", got)
		}
		if _, err := client.Queries.GetTagByName(ctx, "keep parts compound"); err != nil {
			t.Errorf("the compound must survive an apply-parts keep: %v", err)
		}

		row := latestComposition(t, client, "keep parts compound")
		var parts []string
		testutil.AssertNoError(t, json.Unmarshal(row.Parts, &parts), "decode journal parts")
		testutil.AssertEqual(t, len(parts), 2, "journal parts")
	})

	t.Run("split replaces the compound with its parts", func(t *testing.T) {
		id, docID := newProvisionalCompound(t, client, "split compound value")

		status, err := svc.ApplyCompositionDecision(ctx, id, "split", []string{"split part one", "split part two"})
		testutil.AssertNoError(t, err, "split")
		testutil.AssertEqual(t, status, types.CompositionApplied, "status")

		if _, err := client.Queries.GetTagByName(ctx, "split compound value"); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("the compound row should be gone, got err %v", err)
		}
		for _, part := range []string{"split part one", "split part two"} {
			if _, err := client.Queries.GetTagByName(ctx, part); err != nil {
				t.Errorf("part %q must exist: %v", part, err)
			}
		}
		if got := countDocumentTags(t, client, docID); got != 2 {
			t.Errorf("document tags = %d, want the two parts only", got)
		}

		row := latestComposition(t, client, "split compound value")
		testutil.AssertEqual(t, row.Disposition, string(tags.CompositionSplit), "disposition")
		testutil.AssertEqual(t, row.Reason, tags.CompositionReasonOperator, "reason")
	})

	t.Run("a promoted compound is refused", func(t *testing.T) {
		id := database.MustCreateTag(t, client.Queries, "promoted compound value")
		_, err := client.DB().ExecContext(ctx, "UPDATE tag SET promoted_at = now() WHERE id = $1", id)
		testutil.AssertNoError(t, err, "set standing")

		status, err := svc.ApplyCompositionDecision(ctx, id, "split", []string{"promoted part one"})
		testutil.AssertNoError(t, err, "decision")
		testutil.AssertEqual(t, status, types.CompositionNotProvisional, "status")

		if _, err := client.Queries.GetTagByName(ctx, "promoted compound value"); err != nil {
			t.Errorf("a promoted compound must survive: %v", err)
		}
		assertNoComposition(t, client, "promoted compound value")
	})

	t.Run("a missing tag reports not found", func(t *testing.T) {
		status, err := svc.ApplyCompositionDecision(ctx, 999999, "keep", nil)
		testutil.AssertNoError(t, err, "decision")
		testutil.AssertEqual(t, status, types.CompositionNotFound, "status")
	})

	t.Run("an unknown disposition is invalid", func(t *testing.T) {
		id, _ := newProvisionalCompound(t, client, "unknown disposition compound")

		status, err := svc.ApplyCompositionDecision(ctx, id, "bogus", []string{"unknown part one"})
		testutil.AssertNoError(t, err, "decision")
		testutil.AssertEqual(t, status, types.CompositionInvalid, "status")

		if _, err := client.Queries.GetTagByName(ctx, "unknown disposition compound"); err != nil {
			t.Errorf("the compound must survive an invalid decision: %v", err)
		}
		if _, err := client.Queries.GetTagByName(ctx, "unknown part one"); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("no part may be created for an invalid decision, got err %v", err)
		}
		assertNoComposition(t, client, "unknown disposition compound")
	})

	t.Run("a split with nothing valid is invalid", func(t *testing.T) {
		id, _ := newProvisionalCompound(t, client, "empty parts compound")

		status, err := svc.ApplyCompositionDecision(ctx, id, "split", []string{"!!!"})
		testutil.AssertNoError(t, err, "decision")
		testutil.AssertEqual(t, status, types.CompositionInvalid, "status")
		assertNoComposition(t, client, "empty parts compound")
	})

	t.Run("a refused part rolls back the parts already created", func(t *testing.T) {
		id, _ := newProvisionalCompound(t, client, "rollback compound value")
		testutil.AssertNoError(t,
			client.Queries.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
				Name: "blocked rollback part", Kind: DeletedTagKindBlocked,
			}),
			"block the second part")

		// the fresh name comes first so it is created before the refusal is seen
		status, err := svc.ApplyCompositionDecision(ctx, id,
			"split", []string{"fresh rollback part", "blocked rollback part"})
		testutil.AssertNoError(t, err, "decision")
		testutil.AssertEqual(t, status, types.CompositionInvalid, "status")

		if _, err := client.Queries.GetTagByName(ctx, "fresh rollback part"); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("the created part must be rolled back, got err %v", err)
		}
		if _, err := client.Queries.GetTagByName(ctx, "rollback compound value"); err != nil {
			t.Errorf("the compound must survive a refused decision: %v", err)
		}
		assertNoComposition(t, client, "rollback compound value")
	})
}

// TestTag_Split pins the lifecycle-wide operator split: a promoted compound is replaced by its parts
// and the decision is journaled, a provisional one splits the same way, and a seeded or missing tag is
// refused without touching the store.
func TestTag_Split(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	t.Run("a promoted compound is replaced by its parts and journaled", func(t *testing.T) {
		id := database.MustCreateTag(t, client.Queries, "lifecycle split compound")
		_, err := client.DB().ExecContext(ctx, "UPDATE tag SET promoted_at = now() WHERE id = $1", id)
		testutil.AssertNoError(t, err, "set standing")
		docID := database.CreateUniqueDocument(t, client.Queries, "lifecycle split doc")
		database.MustAddDocumentTag(t, client.Queries, docID, id)

		status, err := svc.Split(ctx, "lifecycle split compound", []string{"lifecycle part one", "lifecycle part two"})
		testutil.AssertNoError(t, err, "split")
		testutil.AssertEqual(t, status, types.CompositionApplied, "status")

		if _, err := client.Queries.GetTagByName(ctx, "lifecycle split compound"); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("the compound row should be gone, got err %v", err)
		}
		if got := countDocumentTags(t, client, docID); got != 2 {
			t.Errorf("document tags = %d, want the two parts only", got)
		}
		row := latestComposition(t, client, "lifecycle split compound")
		testutil.AssertEqual(t, row.Disposition, string(tags.CompositionSplit), "disposition")
		testutil.AssertEqual(t, row.Reason, tags.CompositionReasonOperator, "reason")
	})

	t.Run("a provisional compound splits too", func(t *testing.T) {
		newProvisionalCompound(t, client, "lifecycle provisional compound")

		status, err := svc.Split(ctx, "lifecycle provisional compound", []string{"lifecycle provisional part"})
		testutil.AssertNoError(t, err, "split")
		testutil.AssertEqual(t, status, types.CompositionApplied, "status")
	})

	t.Run("a seeded value is refused", func(t *testing.T) {
		id := database.MustCreateTag(t, client.Queries, "lifecycle seeded compound")
		_, err := client.DB().ExecContext(ctx, "UPDATE tag SET seeded = true WHERE id = $1", id)
		testutil.AssertNoError(t, err, "mark seeded")

		status, err := svc.Split(ctx, "lifecycle seeded compound", []string{"lifecycle seeded part"})
		testutil.AssertNoError(t, err, "split")
		testutil.AssertEqual(t, status, types.CompositionSeeded, "status")

		if _, err := client.Queries.GetTagByName(ctx, "lifecycle seeded compound"); err != nil {
			t.Errorf("a seeded value must survive: %v", err)
		}
		if _, err := client.Queries.GetTagByName(ctx, "lifecycle seeded part"); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("no part may be created for a refused split, got err %v", err)
		}
	})

	t.Run("a missing tag reports not found", func(t *testing.T) {
		status, err := svc.Split(ctx, "lifecycle missing compound", []string{"lifecycle missing part"})
		testutil.AssertNoError(t, err, "split")
		testutil.AssertEqual(t, status, types.CompositionNotFound, "status")
	})
}
