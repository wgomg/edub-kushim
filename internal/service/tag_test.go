package service

import (
	"context"
	"database/sql"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func newTestTag(t *testing.T) (*Tag, *database.Client) {
	t.Helper()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	tag := NewTag(client, testutil.NewTestLogger(), testutil.NewMockEmbedder())
	return tag, client
}

func TestTag_NormalizeAtSave(t *testing.T) {
	svc, _ := newTestTag(t)
	ctx := context.Background()

	t.Run("create with symbols conflicts with seeded canonical", func(t *testing.T) {
		// seed-tags.sql seeds "c++"; submitting "C++ " (uppercase + trailing
		// space) must normalize to "c++" and surface as a conflict, proving
		// Create normalizes at the save chokepoint.
		results, err := svc.Create(ctx, []string{"C++ "})
		testutil.AssertNoError(t, err, "create")
		testutil.AssertEqual(t, len(results), 1, "one result")
		testutil.AssertEqual(t, results[0].Status, Conflict, "conflict against seeded c++")
		testutil.AssertEqual(t, results[0].Entity.Name, "c++", "conflict entity is seeded canonical name")
	})

	t.Run("update with equivalent form is a noop", func(t *testing.T) {
		// Create a fresh tag, then rename it to a form that normalizes to the
		// same stored name. Update must return Noop without touching the
		// embedding store (prevents store churn on equivalent renames).
		createResults, err := svc.Create(ctx, []string{"Machine Learning"})
		testutil.AssertNoError(t, err, "create")
		testutil.AssertEqual(t, createResults[0].Status, Created, "created")
		id := createResults[0].Entity.ID

		updateResults, err := svc.Update(ctx, []TagUpdatePair{
			{ID: id, Name: "machine learning"},
		})
		testutil.AssertNoError(t, err, "update")
		testutil.AssertEqual(t, updateResults[0].Status, Noop, "noop on equivalent rename")
	})
}

func TestTag_CreateTx(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	tx, err := client.BeginTx(ctx, nil)
	testutil.AssertNoError(t, err, "begin tx")
	defer tx.Rollback()
	txQ := client.WithTx(tx)

	results, newNames, err := svc.CreateTx(ctx, txQ, []string{"tx-test-1", "tx-test-2"}, false)
	testutil.AssertNoError(t, err, "create tx")
	testutil.AssertEqual(t, len(results), 2, "two results")
	testutil.AssertEqual(t, len(newNames), 2, "two new names")

	for i, r := range results {
		testutil.AssertEqual(t, r.Status, Created, "result status")
		if r.Entity.ID == 0 {
			t.Errorf("result[%d] has zero ID", i)
		}
	}

	// Rollback: the tags must not be visible to a new query outside the tx.
	tx.Rollback()
	results2, err := svc.Create(ctx, []string{"tx-test-1"})
	testutil.AssertNoError(t, err, "re-create after rollback")
	// After rollback, the tags don't exist, so Create should create them fresh.
	if results2[0].Status != Created {
		t.Errorf("status after rollback = %v, want Created", results2[0].Status)
	}
}

func TestTag_AddToStore(t *testing.T) {
	svc, _ := newTestTag(t)
	ctx := context.Background()
	// Should not panic on empty list.
	svc.AddToStore(ctx, nil)
	svc.AddToStore(ctx, []string{})
}

func TestTag_RestoreDeleted_Blocked(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	// Seed a blocked deleted-tag entry with a name containing characters
	// that NormalizeTag transforms (hyphens → spaces).
	const tagDeleted = "restore-test-blocked"
	const tagNormalized = "restore test blocked"
	testutil.AssertNoError(t, client.Queries.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
		Name: tagDeleted,
		Kind: "blocked",
	}), "seed blocked tag")

	// Restore should re-create the tag and delete the blocklist row.
	row, err := svc.RestoreDeleted(ctx, tagDeleted)
	testutil.AssertNoError(t, err, "restore blocked")
	testutil.AssertEqual(t, row.Name, tagDeleted, "restored name")
	testutil.AssertEqual(t, row.Kind, "blocked", "restored kind")

	// Blocklist entry must be gone (keyed by original name).
	_, err = client.Queries.GetDeletedTag(ctx, tagDeleted)
	testutil.AssertError(t, err, "blocklist row removed")

	// Tag must exist in the tag table under the normalized name.
	tag, err := client.Queries.GetTagByName(ctx, tagNormalized)
	testutil.AssertNoError(t, err, "tag exists after restore")
	testutil.AssertEqual(t, tag.Name, tagNormalized, "tag name")
}

func TestTag_RestoreDeleted_Merged(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	// Seed a merged deleted-tag entry.
	testutil.AssertNoError(t, client.Queries.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
		Name:       "restore-test-merged",
		Kind:       "merged",
		TargetName: sql.NullString{String: "some-target", Valid: true},
	}), "seed merged tag")

	// Restore merged: un-map only, no re-create.
	row, err := svc.RestoreDeleted(ctx, "restore-test-merged")
	testutil.AssertNoError(t, err, "restore merged")
	testutil.AssertEqual(t, row.Name, "restore-test-merged", "restored name")
	testutil.AssertEqual(t, row.Kind, "merged", "restored kind")

	// Blocklist entry must be gone.
	_, err = client.Queries.GetDeletedTag(ctx, "restore-test-merged")
	testutil.AssertError(t, err, "blocklist row removed")

	// Tag must NOT exist in the tag table (merged restore doesn't create).
	_, err = client.Queries.GetTagByName(ctx, "restore-test-merged")
	testutil.AssertError(t, err, "tag must not exist for merged restore")
}

func TestTag_RestoreDeleted_NotFound(t *testing.T) {
	svc, _ := newTestTag(t)
	ctx := context.Background()

	_, err := svc.RestoreDeleted(ctx, "nonexistent-tag")
	testutil.AssertError(t, err, "not found")
}

func TestTag_Create_PromotesBirth(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	results, err := svc.Create(ctx, []string{"promote-test-1", "promote-test-2"})
	testutil.AssertNoError(t, err, "create")
	testutil.AssertEqual(t, len(results), 2, "two results")
	for _, r := range results {
		testutil.AssertEqual(t, r.Status, Created, "result status")
	}

	tag1, err := client.Queries.GetTagByName(ctx, "promote test 1")
	testutil.AssertNoError(t, err, "get tag 1")
	if !tag1.PromotedAt.Valid {
		t.Error("tag created via Create must have promoted_at set")
	}

	tag2, err := client.Queries.GetTagByName(ctx, "promote test 2")
	testutil.AssertNoError(t, err, "get tag 2")
	if !tag2.PromotedAt.Valid {
		t.Error("tag created via Create must have promoted_at set")
	}
}

// TestTag_Create_MidBatchFailureRollsBackWholeBatch pins Create's
// batch-atomic contract: one failing insert must roll back every tag of the
// batch, not just the failing one. Name content cannot make CreateTag fail
// (NormalizeTag strips disallowed characters; duplicates surface as
// conflicts), and Queries is not mockable, so the failure is injected at the
// DB seam with a CHECK constraint.
func TestTag_Create_MidBatchFailureRollsBackWholeBatch(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	_, err := client.DB().ExecContext(ctx,
		`ALTER TABLE tag ADD CONSTRAINT tag_rejects_poison CHECK (name <> 'poison tag')`)
	testutil.AssertNoError(t, err, "add fault-injection constraint")
	t.Cleanup(func() {
		_, _ = client.DB().ExecContext(context.Background(),
			`ALTER TABLE tag DROP CONSTRAINT IF EXISTS tag_rejects_poison`)
	})

	_, err = svc.Create(ctx, []string{"atomic rollback probe", "poison tag"})
	testutil.AssertError(t, err, "create must surface the failing insert")

	_, err = client.Queries.GetTagByName(ctx, "atomic rollback probe")
	testutil.AssertError(t, err, "earlier insert in the failed batch must be rolled back")
}

func TestTag_CreateTx_Quarantined_LeavesProvisional(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	tx, err := client.BeginTx(ctx, nil)
	testutil.AssertNoError(t, err, "begin tx")
	defer tx.Rollback()
	txQ := client.WithTx(tx)

	results, newNames, err := svc.CreateTx(ctx, txQ, []string{"quarantined-tag"}, true)
	testutil.AssertNoError(t, err, "create tx quarantined")
	testutil.AssertEqual(t, len(results), 1, "one result")
	testutil.AssertEqual(t, len(newNames), 1, "one new name")
	testutil.AssertEqual(t, results[0].Status, Created, "created")

	// The result entity for a quarantined birth must not have promoted_at set.
	if results[0].Entity.PromotedAt.Valid {
		t.Error("quarantined birth must have promoted_at NULL")
	}

	// Verify within the transaction (before rollback).
	tag, err := txQ.GetTagByName(ctx, "quarantined tag")
	testutil.AssertNoError(t, err, "get quarantined tag in tx")
	if tag.PromotedAt.Valid {
		t.Error("quarantined tag in DB must have promoted_at NULL")
	}
}

func TestTag_Update_OnlyReembedsPromotedTags(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()
	embedder := svc.embedder.(*testutil.MockEmbedder)

	// Create a promoted tag (via Create, which promotes).
	createResults, err := svc.Create(ctx, []string{"update-reembed-src"})
	testutil.AssertNoError(t, err, "create promoted tag")
	testutil.AssertEqual(t, createResults[0].Status, Created, "created")
	promotedID := createResults[0].Entity.ID

	// Verify the tag is promoted in the DB.
	tag, err := client.Queries.GetTag(ctx, promotedID)
	testutil.AssertNoError(t, err, "get created tag")
	if !tag.PromotedAt.Valid {
		t.Fatalf("created tag must be promoted, got promoted_at=%v", tag.PromotedAt)
	}

	// Rename the promoted tag — the new normalized name should enter the store.
	updateResults, err := svc.Update(ctx, []TagUpdatePair{
		{ID: promotedID, Name: "update-reembed-dst"},
	})
	testutil.AssertNoError(t, err, "update promoted tag")
	testutil.AssertEqual(t, updateResults[0].Status, Updated, "updated")
	if !embedder.Has("update reembed dst") {
		t.Error("renamed promoted tag must be added to the embedder store")
	}
}

func TestTag_CreateAndUnblock(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	testutil.AssertNoError(t, client.Queries.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
		Name: "recreate me",
		Kind: DeletedTagKindBlocked,
	}), "seed blocked tag")

	// Create alone must keep the blocklist row: the automatic paths rely on it
	// to keep guarding a deleted name.
	results, err := svc.Create(ctx, []string{"recreate me"})
	testutil.AssertNoError(t, err, "create")
	testutil.AssertEqual(t, results[0].Status, Created, "created")
	if _, err := client.Queries.GetDeletedTag(ctx, "recreate me"); err != nil {
		t.Fatalf("Create must not clear the blocklist row: %v", err)
	}

	// CreateAndUnblock is the operator path: re-creating the name unblocks it.
	results, err = svc.CreateAndUnblock(ctx, []string{"Recreate Me"})
	testutil.AssertNoError(t, err, "create and unblock")
	testutil.AssertEqual(t, results[0].Status, Conflict, "conflict on existing tag")
	if _, err := client.Queries.GetDeletedTag(ctx, "recreate me"); err == nil {
		t.Fatal("CreateAndUnblock must clear the blocklist row")
	}
}

func TestTag_ListProvisional(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	// Create a tag and set promoted_at = NULL to make it provisional.
	tagID := database.MustCreateTag(t, client.Queries, "list-prov-tag")
	_, err := client.DB().ExecContext(ctx, "UPDATE tag SET promoted_at = NULL WHERE id = $1", tagID)
	testutil.AssertNoError(t, err, "set provisional")

	// Create a document and link it to the tag.
	docID := database.CreateUniqueDocument(t, client.Queries, "prov-doc")
	database.MustAddDocumentTag(t, client.Queries, docID, tagID)

	// ListProvisional must return the provisional tag.
	rows, err := svc.ListProvisional(ctx, 50, 0)
	testutil.AssertNoError(t, err, "list provisional")
	if len(rows) == 0 {
		t.Fatal("expected at least one provisional tag")
	}
	found := false
	for _, row := range rows {
		if row.ID == tagID {
			found = true
			if row.DocumentCount != 1 {
				t.Errorf("DocumentCount = %d, want 1", row.DocumentCount)
			}
		}
	}
	if !found {
		t.Errorf("tag %d not found in provisional list", tagID)
	}

	// CountProvisional must match.
	count, err := svc.CountProvisional(ctx)
	testutil.AssertNoError(t, err, "count provisional")
	if count < 1 {
		t.Errorf("CountProvisional = %d, want >= 1", count)
	}
}

func TestTag_LatestQualityEventsForNames(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	tagID := database.MustCreateTag(t, client.Queries, "quality-events-tag")
	docID := database.CreateUniqueDocument(t, client.Queries, "quality-test-doc")
	database.MustAddDocumentTag(t, client.Queries, docID, tagID)

	// Insert a quality event.
	criteria := []byte(`{"subject_matter":true,"standard_term":true,"not_person_place_work":true,"appropriately_broad":true}`)
	_, err := client.Queries.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docID,
		TagName:       "quality-events-tag",
		Criteria:      criteria,
		Pass:          true,
		VerdictModel:  sql.NullString{String: "test-model", Valid: true},
		PolicyVersion: "v1",
	})
	testutil.AssertNoError(t, err, "insert quality event")

	events, err := svc.LatestQualityEventsForNames(ctx, []string{"quality-events-tag"})
	testutil.AssertNoError(t, err, "latest events")
	testutil.AssertEqual(t, len(events), 1, "one event")
	testutil.AssertEqual(t, events[0].Pass, true, "pass")
	testutil.AssertEqual(t, events[0].VerdictModel.String, "test-model", "model")
	testutil.AssertEqual(t, events[0].TagName, "quality-events-tag", "tag name")

	// Non-existent name returns nothing.
	events, err = svc.LatestQualityEventsForNames(ctx, []string{"nonexistent"})
	testutil.AssertNoError(t, err, "nonexistent")
	testutil.AssertEqual(t, len(events), 0, "no events")
}

func TestTag_Promote(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	t.Run("promote a provisional tag", func(t *testing.T) {
		// Create a tag and set promoted_at = NULL to make it provisional.
		tagID := database.MustCreateTag(t, client.Queries, "promote-me-tag")
		_, err := client.DB().ExecContext(ctx, "UPDATE tag SET promoted_at = NULL WHERE id = $1", tagID)
		testutil.AssertNoError(t, err, "set provisional")

		tag, err := svc.Get(ctx, tagID)
		testutil.AssertNoError(t, err, "get tag")
		if tag.PromotedAt.Valid {
			t.Fatal("tag should not be promoted yet")
		}

		tag, status, err := svc.Promote(ctx, tagID, "test-reason")
		testutil.AssertNoError(t, err, "promote")
		testutil.AssertEqual(t, status, PromotePromoted, "promoted status")

		// Re-fetch to verify the update persisted.
		tag, err = svc.Get(ctx, tagID)
		testutil.AssertNoError(t, err, "re-fetch after promote")
		if !tag.PromotedAt.Valid {
			t.Error("tag should be promoted now")
		}
	})

	t.Run("already promoted", func(t *testing.T) {
		results, err := svc.Create(ctx, []string{"already-promoted-tag"})
		testutil.AssertNoError(t, err, "create")
		testutil.AssertEqual(t, results[0].Status, Created, "created")
		tagID := results[0].Entity.ID

		_, status, err := svc.Promote(ctx, tagID, "test-reason")
		testutil.AssertNoError(t, err, "promote again")
		testutil.AssertEqual(t, status, PromoteAlreadyPromoted, "already promoted")
	})

	t.Run("not found", func(t *testing.T) {
		_, status, err := svc.Promote(ctx, 999999, "test-reason")
		testutil.AssertNoError(t, err, "not found")
		testutil.AssertEqual(t, status, PromoteNotFound, "not found status")
	})
}
