package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
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
		testutil.AssertEqual(t, results[0].Status, types.Conflict, "conflict against seeded c++")
		testutil.AssertEqual(t, results[0].Entity.Name, "c++", "conflict entity is seeded canonical name")
	})

	t.Run("update with equivalent form is a noop", func(t *testing.T) {
		// Create a fresh tag, then rename it to a form that normalizes to the
		// same stored name. Update must return Noop without touching the
		// embedding store (prevents store churn on equivalent renames).
		createResults, err := svc.Create(ctx, []string{"Machine Learning"})
		testutil.AssertNoError(t, err, "create")
		testutil.AssertEqual(t, createResults[0].Status, types.Created, "created")
		id := createResults[0].Entity.ID

		updateResults, err := svc.Update(ctx, []TagUpdatePair{
			{ID: id, Name: "machine learning"},
		})
		testutil.AssertNoError(t, err, "update")
		testutil.AssertEqual(t, updateResults[0].Status, types.Noop, "noop on equivalent rename")
	})
}

func TestTag_CreatePlannedTx_Rollback(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	tx, err := client.BeginTx(ctx, nil)
	testutil.AssertNoError(t, err, "begin tx")
	defer tx.Rollback()
	txQ := client.WithTx(tx)

	plan, err := svc.buildPlan(ctx, txQ, []string{"tx-test-1", "tx-test-2"}, false)
	testutil.AssertNoError(t, err, "build plan")
	results, newNames, err := svc.CreatePlannedTx(ctx, txQ, plan, false)
	testutil.AssertNoError(t, err, "create planned tx")
	testutil.AssertEqual(t, len(results), 2, "two results")
	testutil.AssertEqual(t, len(newNames), 2, "two new names")

	for i, r := range results {
		testutil.AssertEqual(t, r.Status, types.Created, "result status")
		if r.Entity.ID == 0 {
			t.Errorf("result[%d] has zero ID", i)
		}
	}

	// Rollback: the tags must not be visible to a new query outside the tx.
	tx.Rollback()
	results2, err := svc.Create(ctx, []string{"tx-test-1"})
	testutil.AssertNoError(t, err, "re-create after rollback")
	// After rollback, the tags don't exist, so Create should create them fresh.
	if results2[0].Status != types.Created {
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
		testutil.AssertEqual(t, r.Status, types.Created, "result status")
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

func TestTag_CreatePlannedTx_Quarantined_LeavesProvisional(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	tx, err := client.BeginTx(ctx, nil)
	testutil.AssertNoError(t, err, "begin tx")
	defer tx.Rollback()
	txQ := client.WithTx(tx)

	plan, err := svc.buildPlan(ctx, txQ, []string{"quarantined-tag"}, false)
	testutil.AssertNoError(t, err, "build plan")
	results, newNames, err := svc.CreatePlannedTx(ctx, txQ, plan, true)
	testutil.AssertNoError(t, err, "create planned tx quarantined")
	testutil.AssertEqual(t, len(results), 1, "one result")
	testutil.AssertEqual(t, len(newNames), 1, "one new name")
	testutil.AssertEqual(t, results[0].Status, types.Created, "created")

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
	embedder, ok := svc.embedder.(*testutil.MockEmbedder)
	if !ok {
		t.Fatalf("unexpected embedder type %T", svc.embedder)
	}

	// Create a promoted tag (via Create, which promotes).
	createResults, err := svc.Create(ctx, []string{"update-reembed-src"})
	testutil.AssertNoError(t, err, "create promoted tag")
	testutil.AssertEqual(t, createResults[0].Status, types.Created, "created")
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
	testutil.AssertEqual(t, updateResults[0].Status, types.Updated, "updated")
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

	// Create alone must refuse a blocklisted name and keep the blocklist row:
	// the automatic paths rely on it to keep guarding a deleted name.
	results, err := svc.Create(ctx, []string{"recreate me"})
	testutil.AssertNoError(t, err, "create")
	testutil.AssertEqual(t, results[0].Status, types.Invalid, "blocklisted name is not registered")
	if _, err := client.Queries.GetDeletedTag(ctx, "recreate me"); err != nil {
		t.Fatalf("Create must not clear the blocklist row: %v", err)
	}

	// CreateAndUnblock is the operator path: re-creating the name unblocks it.
	results, err = svc.CreateAndUnblock(ctx, []string{"Recreate Me"})
	testutil.AssertNoError(t, err, "create and unblock")
	testutil.AssertEqual(t, results[0].Status, types.Created, "created by the unblock path")
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
	rows, err := svc.ListProvisional(ctx, ProvisionalFilter{}, 50, 0)
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
	count, err := svc.CountProvisional(ctx, ProvisionalFilter{})
	testutil.AssertNoError(t, err, "count provisional")
	if count < 1 {
		t.Errorf("CountProvisional = %d, want >= 1", count)
	}
}

// insertProvisionalVerdict records the judge's rubric answer for a
// provisional tag under the current policy version; an empty outcome
// leaves the row without one, matching the legacy rows the verdict
// filter must still classify.
func insertProvisionalVerdict(t *testing.T, client *database.Client, docID int64, tagName, criteria string, pass bool, outcome string) {
	t.Helper()
	_, err := client.Queries.InsertTagQualityEvent(context.Background(), database.InsertTagQualityEventParams{
		DocumentID:    docID,
		TagName:       tagName,
		Criteria:      json.RawMessage(criteria),
		Pass:          pass,
		Outcome:       sql.NullString{String: outcome, Valid: outcome != ""},
		VerdictModel:  sql.NullString{String: "test-judge", Valid: true},
		PolicyVersion: analyzer.TagQualityVersion,
	})
	testutil.AssertNoError(t, err, "insert quality event")
}

func TestTag_ListProvisionalFilters(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	// All fixtures hang off one document: linking it makes each tag part
	// of the provisional queue, and an empty criteria skips the verdict
	// insert so the tag stays unjudged.
	docID := database.CreateUniqueDocument(t, client.Queries, "prov-filter-doc")

	seed := func(name, criteria string, pass bool, outcome string) int64 {
		id := database.MustCreateTag(t, client.Queries, name)
		_, err := client.DB().ExecContext(ctx, "UPDATE tag SET promoted_at = NULL WHERE id = $1", id)
		testutil.AssertNoError(t, err, "set provisional")
		database.MustAddDocumentTag(t, client.Queries, docID, id)
		if criteria != "" {
			row, err := client.Queries.GetTag(ctx, id)
			testutil.AssertNoError(t, err, "read fixture tag")
			insertProvisionalVerdict(t, client, docID, row.Name, criteria, pass, outcome)
		}
		return id
	}

	admitID := seed("prov-filter-admit",
		`{"subject_matter":true,"dimension":true,"not_person":true,"atomic":true,"not_form_genre":true,"defer":false}`,
		true, "admit")
	failID := seed("prov-filter-fail", `{"atomic":false}`, false, "")
	deferredID := seed("prov-filter-deferred", `{"defer":true}`, false, "")
	rejectID := seed("prov-filter-reject", `{"atomic":false}`, false, "reject")
	unjudgedID := seed("prov-filter-unjudged", "", false, "")

	// Assertions stay scoped to this test's fixture ids and the limit
	// keeps the whole filtered set on one page: the package database is
	// shared with the other tests, so global counts are not stable.
	cases := []struct {
		name   string
		filter ProvisionalFilter
		want   []int64
		drop   []int64
	}{
		{
			name:   "unfiltered queue holds every fixture",
			filter: ProvisionalFilter{},
			want:   []int64{admitID, failID, deferredID, rejectID, unjudgedID},
		},
		{
			name:   "criterion fail matches only the failing verdict rows",
			filter: ProvisionalFilter{Atomic: "false"},
			want:   []int64{failID, rejectID},
			drop:   []int64{admitID, deferredID, unjudgedID},
		},
		{
			name:   "criterion absent covers missing keys and unjudged tags",
			filter: ProvisionalFilter{SubjectMatter: "absent"},
			want:   []int64{failID, deferredID, rejectID, unjudgedID},
			drop:   []int64{admitID},
		},
		{
			name:   "criterion pass matches the rubric answer",
			filter: ProvisionalFilter{Defer: "true"},
			want:   []int64{deferredID},
			drop:   []int64{admitID, failID, rejectID, unjudgedID},
		},
		{
			name:   "verdict none selects the tags without a verdict row",
			filter: ProvisionalFilter{Verdict: "none"},
			want:   []int64{unjudgedID},
			drop:   []int64{admitID, failID, deferredID, rejectID},
		},
		{
			name:   "verdict admit follows the pass flag",
			filter: ProvisionalFilter{Verdict: "admit"},
			want:   []int64{admitID},
			drop:   []int64{failID, deferredID, rejectID, unjudgedID},
		},
		{
			name:   "verdict defer derives from the rubric on legacy rows",
			filter: ProvisionalFilter{Verdict: "defer"},
			want:   []int64{deferredID},
			drop:   []int64{admitID, failID, rejectID, unjudgedID},
		},
		{
			name:   "verdict reject joins derived and recorded outcomes",
			filter: ProvisionalFilter{Verdict: "reject"},
			want:   []int64{failID, rejectID},
			drop:   []int64{admitID, deferredID, unjudgedID},
		},
		{
			name:   "criteria and verdict combine with AND",
			filter: ProvisionalFilter{Atomic: "false", Verdict: "reject"},
			want:   []int64{failID, rejectID},
			drop:   []int64{admitID, deferredID, unjudgedID},
		},
		{
			name:   "disagreeing criteria and verdict yield nothing",
			filter: ProvisionalFilter{Atomic: "false", Verdict: "none"},
			drop:   []int64{admitID, failID, deferredID, rejectID, unjudgedID},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := svc.ListProvisional(ctx, tc.filter, 500, 0)
			testutil.AssertNoError(t, err, "list provisional")
			count, err := svc.CountProvisional(ctx, tc.filter)
			testutil.AssertNoError(t, err, "count provisional")
			if count != int64(len(rows)) {
				t.Errorf("filtered count = %d, len(rows) = %d, they must agree", count, len(rows))
			}

			present := make(map[int64]bool, len(rows))
			for _, row := range rows {
				present[row.ID] = true
			}
			for _, id := range tc.want {
				if !present[id] {
					t.Errorf("filter %+v: fixture %d missing from the list", tc.filter, id)
				}
			}
			for _, id := range tc.drop {
				if present[id] {
					t.Errorf("filter %+v: fixture %d must be excluded from the list", tc.filter, id)
				}
			}
		})
	}
}

func TestTag_LatestQualityEventsForNames(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	tagID := database.MustCreateTag(t, client.Queries, "quality-events-tag")
	docID := database.CreateUniqueDocument(t, client.Queries, "quality-test-doc")
	database.MustAddDocumentTag(t, client.Queries, docID, tagID)

	// Insert a quality event.
	criteria := []byte(`{"subject_matter":true,"dimension":true,"not_person":true,"atomic":true,"not_form_genre":true,"defer":false}`)
	_, err := client.Queries.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docID,
		TagName:       "quality-events-tag",
		Criteria:      criteria,
		Pass:          true,
		VerdictModel:  sql.NullString{String: "test-model", Valid: true},
		PolicyVersion: analyzer.TagQualityVersion,
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
		testutil.AssertEqual(t, status, types.PromotePromoted, "promoted status")

		// Re-fetch to verify the update persisted.
		tag, err = svc.Get(ctx, tagID)
		testutil.AssertNoError(t, err, "re-fetch after promote")
		if !tag.PromotedAt.Valid {
			t.Error("tag should be promoted now")
		}
	})

	t.Run("already promoted", func(t *testing.T) {
		// a base-form name is not flagged by M2 and promotes at birth
		results, err := svc.Create(ctx, []string{"promote-tag"})
		testutil.AssertNoError(t, err, "create")
		testutil.AssertEqual(t, results[0].Status, types.Created, "created")
		tagID := results[0].Entity.ID

		_, status, err := svc.Promote(ctx, tagID, "test-reason")
		testutil.AssertNoError(t, err, "promote again")
		testutil.AssertEqual(t, status, types.PromoteAlreadyPromoted, "already promoted")
	})

	t.Run("not found", func(t *testing.T) {
		_, status, err := svc.Promote(ctx, 999999, "test-reason")
		testutil.AssertNoError(t, err, "not found")
		testutil.AssertEqual(t, status, types.PromoteNotFound, "not found status")
	})
}

func TestTag_Demote(t *testing.T) {
	ctx := context.Background()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	embedder := testutil.NewMockEmbedder()
	svc := NewTag(client, testutil.NewTestLogger(), embedder)

	t.Run("demotion revokes the store copy and records why", func(t *testing.T) {
		results, err := svc.Create(ctx, []string{"demote-standings"})
		testutil.AssertNoError(t, err, "create")
		tagID := results[0].Entity.ID
		name := results[0].Entity.Name
		_, err = client.DB().ExecContext(ctx, "UPDATE tag SET promoted_at = now() WHERE id = $1", tagID)
		testutil.AssertNoError(t, err, "set standing")
		testutil.AssertNoError(t, embedder.AddToStore(ctx, []string{name}), "seed the store copy")

		_, status, err := svc.Demote(ctx, tagID, "mass_decay")
		testutil.AssertNoError(t, err, "demote")
		testutil.AssertEqual(t, status, types.DemoteDemoted, "demoted status")

		if embedder.Has(name) {
			t.Error("the demoted name must leave the embedding store")
		}
		tag, err := svc.Get(ctx, tagID)
		testutil.AssertNoError(t, err, "re-fetch")
		if tag.PromotedAt.Valid {
			t.Error("the value should have lost its standing")
		}
		if !tag.DemotedAt.Valid {
			t.Error("the revocation should be recorded on the row")
		}
		testutil.AssertEqual(t, tag.DemotedReason.String, "mass_decay", "the row names the evidence")
	})

	t.Run("promoting again restores standing and clears the record", func(t *testing.T) {
		results, err := svc.Create(ctx, []string{"demote-restored"})
		testutil.AssertNoError(t, err, "create")
		tagID := results[0].Entity.ID
		name := results[0].Entity.Name
		_, err = client.DB().ExecContext(ctx, "UPDATE tag SET promoted_at = now() WHERE id = $1", tagID)
		testutil.AssertNoError(t, err, "set standing")

		_, status, err := svc.Demote(ctx, tagID, "mass_decay")
		testutil.AssertNoError(t, err, "demote")
		testutil.AssertEqual(t, status, types.DemoteDemoted, "demoted status")

		_, promoteStatus, err := svc.Promote(ctx, tagID, "operator")
		testutil.AssertNoError(t, err, "promote")
		testutil.AssertEqual(t, promoteStatus, types.PromotePromoted, "re-admission")

		tag, err := svc.Get(ctx, tagID)
		testutil.AssertNoError(t, err, "re-fetch")
		if !tag.PromotedAt.Valid {
			t.Error("the value is promoted again")
		}
		if tag.DemotedAt.Valid || tag.DemotedReason.Valid {
			t.Error("re-admission must clear the demotion record")
		}
		if !embedder.Has(name) {
			t.Error("the restored value returns to the embedding store")
		}
	})

	t.Run("seeded values are refused", func(t *testing.T) {
		tagID := database.MustCreateTag(t, client.Queries, "demote-seeded")
		_, err := client.DB().ExecContext(ctx,
			"UPDATE tag SET promoted_at = now(), seeded = true WHERE id = $1", tagID)
		testutil.AssertNoError(t, err, "set standing and seeded")

		_, status, err := svc.Demote(ctx, tagID, "operator")
		testutil.AssertNoError(t, err, "demote seeded")
		testutil.AssertEqual(t, status, types.DemoteSeeded, "seeded status")

		tag, err := svc.Get(ctx, tagID)
		testutil.AssertNoError(t, err, "re-fetch")
		if !tag.PromotedAt.Valid {
			t.Error("an operator-owned value keeps its standing")
		}
		if tag.DemotedAt.Valid {
			t.Error("a refused demotion leaves no record")
		}
	})

	t.Run("not promoted", func(t *testing.T) {
		tagID := database.MustCreateTag(t, client.Queries, "demote-pending")
		_, err := client.DB().ExecContext(ctx, "UPDATE tag SET promoted_at = NULL WHERE id = $1", tagID)
		testutil.AssertNoError(t, err, "set provisional")

		_, status, err := svc.Demote(ctx, tagID, "operator")
		testutil.AssertNoError(t, err, "demote provisional")
		testutil.AssertEqual(t, status, types.DemoteNotPromoted, "not promoted status")

		tag, err := svc.Get(ctx, tagID)
		testutil.AssertNoError(t, err, "re-fetch")
		if tag.DemotedAt.Valid {
			t.Error("a provisional value has no standing to revoke")
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, status, err := svc.Demote(ctx, 999999, "operator")
		testutil.AssertNoError(t, err, "not found")
		testutil.AssertEqual(t, status, types.DemoteNotFound, "not found status")
	})
}

func TestTag_ApplyAliasPlan(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	canonicalID := database.MustCreateTag(t, client.Queries, "alias canonical tag")
	loserID := database.MustCreateTag(t, client.Queries, "alias loser tag")

	onlyLoser := database.CreateUniqueDocument(t, client.Queries, "alias-doc-1")
	both := database.CreateUniqueDocument(t, client.Queries, "alias-doc-2")
	database.MustAddDocumentTag(t, client.Queries, onlyLoser, loserID)
	database.MustAddDocumentTag(t, client.Queries, both, loserID)
	database.MustAddDocumentTag(t, client.Queries, both, canonicalID)

	// the loser owns an alias: it must retarget onto the canonical, no chains
	_, err := client.Queries.InsertTagAlias(ctx, database.InsertTagAliasParams{
		AliasName:      "old loser alias",
		CanonicalTagID: loserID,
		RuleSetVersion: tags.IdentityRuleSetVersion,
	})
	testutil.AssertNoError(t, err, "seed alias on the loser")

	outcomes, err := svc.ApplyAliasPlan(ctx, []tags.AliasAction{
		{AliasName: "alias loser tag", Canonical: "alias canonical tag", RemoveExistingTag: true},
		{AliasName: "never a row", Canonical: "alias canonical tag"},
	})
	testutil.AssertNoError(t, err, "apply alias plan")
	testutil.AssertEqual(t, len(outcomes), 2, "one outcome per action")
	testutil.AssertEqual(t, outcomes[0].Applied, true, "the loser collapse applies")
	// only the loser-only document moves; the dual-tagged one already carries the canonical
	testutil.AssertEqual(t, outcomes[0].Reassigned, int64(1), "the plan counts the re-pointed documents")
	testutil.AssertEqual(t, outcomes[1].Applied, true, "the alias-only action applies")
	testutil.AssertEqual(t, outcomes[1].Reassigned, int64(0), "an alias-only action moves no documents")

	if _, err := client.Queries.GetTagByName(ctx, "alias loser tag"); err == nil {
		t.Error("loser tag row must be removed")
	}

	canonicalDocs, err := client.Queries.GetTagDocumentIDs(ctx, canonicalID)
	testutil.AssertNoError(t, err, "canonical assignments")
	testutil.AssertEqual(t, len(canonicalDocs), 2, "both documents re-point onto the canonical")
	loserDocs, err := client.Queries.GetTagDocumentIDs(ctx, loserID)
	testutil.AssertNoError(t, err, "loser assignments")
	testutil.AssertEqual(t, len(loserDocs), 0, "no assignment keeps pointing at the loser")

	alias, err := client.Queries.GetTagAliasByName(ctx, "alias loser tag")
	testutil.AssertNoError(t, err, "loser name becomes an alias")
	testutil.AssertEqual(t, alias.CanonicalTagID, canonicalID, "alias target")
	testutil.AssertEqual(t, alias.RuleSetVersion, tags.IdentityRuleSetVersion, "alias rule-set version")

	oldAlias, err := client.Queries.GetTagAliasByName(ctx, "old loser alias")
	testutil.AssertNoError(t, err, "the loser's own alias survives")
	testutil.AssertEqual(t, oldAlias.CanonicalTagID, canonicalID, "retargeted onto the canonical")

	neverAlias, err := client.Queries.GetTagAliasByName(ctx, "never a row")
	testutil.AssertNoError(t, err, "a never-registered name becomes an alias directly")
	testutil.AssertEqual(t, neverAlias.CanonicalTagID, canonicalID, "alias target")
	if _, err := client.Queries.GetTagByName(ctx, "never a row"); err == nil {
		t.Error("an alias name must not register a tag row")
	}

	t.Run("registration consults the alias", func(t *testing.T) {
		results, err := svc.Create(ctx, []string{"alias loser tag"})
		testutil.AssertNoError(t, err, "create through alias")
		testutil.AssertEqual(t, len(results), 1, "one result")
		testutil.AssertEqual(t, results[0].Status, types.Apply, "apply through alias")
		testutil.AssertEqual(t, results[0].Entity.ID, canonicalID, "canonical entity")
	})

	t.Run("blocked names are never aliased", func(t *testing.T) {
		testutil.AssertNoError(t, client.Queries.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
			Name: "blocked loser",
			Kind: DeletedTagKindBlocked,
		}), "seed blocked tag")

		outcomes, err := svc.ApplyAliasPlan(ctx, []tags.AliasAction{
			{AliasName: "blocked loser", Canonical: "alias canonical tag"},
		})
		testutil.AssertNoError(t, err, "apply blocked alias")
		testutil.AssertEqual(t, outcomes[0].Applied, false, "a blocklisted name reports the action as not applied")

		if _, err := client.Queries.GetTagAliasByName(ctx, "blocked loser"); err == nil {
			t.Error("a blocklisted name must never be aliased")
		}
	})
}

func TestTag_CreatePlannedTx_StoresNormalizedName(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	tx, err := client.BeginTx(ctx, nil)
	testutil.AssertNoError(t, err, "begin tx")
	defer tx.Rollback()
	txQ := client.WithTx(tx)

	plan := tags.Plan{Registrations: []tags.Registration{{
		Input:       "Boundary Topic",
		Name:        "Boundary Topic",
		Disposition: tags.DispositionRegister,
	}}}
	results, _, err := svc.CreatePlannedTx(ctx, txQ, plan, false)
	testutil.AssertNoError(t, err, "create planned")
	testutil.AssertEqual(t, len(results), 1, "one result")
	testutil.AssertEqual(t, results[0].Status, types.Created, "created")
	testutil.AssertEqual(t, results[0].Entity.Name, "boundary topic", "entity carries the normalized name")

	if _, err := txQ.GetTagByName(ctx, "boundary topic"); err != nil {
		t.Errorf("stored row under the normalized name: %v", err)
	}
}

func TestTag_CreatePlannedTx_WritesCompositionEvidence(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()
	docID := database.CreateUniqueDocument(t, client.Queries, "composition-evidence")

	tx, err := client.BeginTx(ctx, nil)
	testutil.AssertNoError(t, err, "begin tx")
	defer tx.Rollback()
	txQ := client.WithTx(tx)

	// the row carries a parts reading when one exists, and none when it does not, so the nullable column
	// must distinguish a computed false from an absent reading rather than collapsing both to null
	reconstructible := true
	notReconstructible := false

	plan := tags.Plan{Composition: []tags.CompositionRow{
		{
			DocumentID:      docID,
			Candidate:       "  Russian History ",
			Parts:           []string{"Russia", "History"},
			Disposition:     tags.CompositionSplit,
			Reason:          tags.CompositionReasonJudge,
			CriteriaVersion: analyzer.TagSplitVersion,
			Model:           "judge-model",
			Reconstructible: &reconstructible,
		},
		{
			DocumentID:      docID,
			Candidate:       "Cold War",
			Disposition:     tags.CompositionKeep,
			Reason:          tags.CompositionReasonJudge,
			CriteriaVersion: analyzer.TagSplitVersion,
		},
		{
			DocumentID:      docID,
			Candidate:       "Social Ecology",
			Disposition:     tags.CompositionDrop,
			Reason:          tags.CompositionReasonPartRejected,
			CriteriaVersion: analyzer.TagSplitVersion,
			Reconstructible: &notReconstructible,
		},
	}}
	_, _, err = svc.CreatePlannedTx(ctx, txQ, plan, false)
	testutil.AssertNoError(t, err, "create planned tx")
	testutil.AssertNoError(t, tx.Commit(), "commit composition evidence")

	rows, err := client.DB().QueryContext(ctx,
		`SELECT candidate, parts::text, disposition, reason, criteria_version, model, reconstructible
		 FROM tag_composition_event WHERE document_id = $1 ORDER BY id`, docID)
	testutil.AssertNoError(t, err, "read composition evidence")
	defer rows.Close()

	type evidence struct {
		candidate, parts, disposition, reason, criteriaVersion string
		model                                                  sql.NullString
		reconstructible                                        sql.NullBool
	}
	var got []evidence
	for rows.Next() {
		var e evidence
		testutil.AssertNoError(t, rows.Scan(&e.candidate, &e.parts, &e.disposition, &e.reason, &e.criteriaVersion, &e.model, &e.reconstructible), "scan composition evidence")
		got = append(got, e)
	}
	testutil.AssertNoError(t, rows.Err(), "iterate composition evidence")
	testutil.AssertEqual(t, len(got), 3, "one row per verdict")

	// a NULL parts column would fail the scan: keep and drop rows encode
	// the empty array instead
	testutil.AssertEqual(t, got[0].candidate, "russian history", "candidate normalized at the write boundary")
	testutil.AssertEqual(t, got[0].disposition, tags.CompositionSplit, "split disposition")
	testutil.AssertEqual(t, got[0].reason, tags.CompositionReasonJudge, "split reason")
	testutil.AssertEqual(t, got[0].criteriaVersion, analyzer.TagSplitVersion, "criteria version")
	testutil.AssertEqual(t, got[0].model.Valid && got[0].model.String == "judge-model", true, "fresh verdict carries its model")
	if !slices.Equal(decodeParts(t, got[0].parts), []string{"russia", "history"}) {
		t.Errorf("split parts = %v, want [russia history]", decodeParts(t, got[0].parts))
	}

	testutil.AssertEqual(t, got[1].parts, "[]", "keep row encodes empty parts")
	testutil.AssertEqual(t, got[1].disposition, tags.CompositionKeep, "keep disposition")
	testutil.AssertEqual(t, got[1].model.Valid, false, "keep without a fresh judge carries no model")

	testutil.AssertEqual(t, got[2].parts, "[]", "drop row encodes empty parts")
	testutil.AssertEqual(t, got[2].disposition, tags.CompositionDrop, "part-rejected drop disposition")
	testutil.AssertEqual(t, got[2].reason, tags.CompositionReasonPartRejected, "drop reason")
	testutil.AssertEqual(t, got[2].model.Valid, false, "drop without a fresh judge carries no model")

	testutil.AssertEqual(t, got[0].reconstructible.Valid && got[0].reconstructible.Bool, true, "split row carries the reconstructibility signal")
	testutil.AssertEqual(t, got[1].reconstructible.Valid, false, "a row without a parts reading keeps the column null")
	testutil.AssertEqual(t, got[2].reconstructible.Valid && got[2].reconstructible.Bool, false, "a computed false stays false rather than becoming null")
}

// TestTag_CreatePlannedTx_HousesCompositionCandidate pins the store-name namespace for the
// composition journal: an emitted compound whose house form differs from its normalized form
// (roman numerals, ordinal words) must be persisted under the house form, so the promotion hold
// and the review surface — which look a compound up by its store tag name — can match it.
func TestTag_CreatePlannedTx_HousesCompositionCandidate(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()
	docID := database.CreateUniqueDocument(t, client.Queries, "composition-house")

	tx, err := client.BeginTx(ctx, nil)
	testutil.AssertNoError(t, err, "begin tx")
	defer tx.Rollback()
	txQ := client.WithTx(tx)

	plan := tags.Plan{Composition: []tags.CompositionRow{{
		DocumentID:      docID,
		Candidate:       "World War II",
		Disposition:     tags.CompositionKeep,
		Reason:          tags.CompositionReasonNamedUnitReconstructible,
		CriteriaVersion: analyzer.TagSplitVersion,
	}}}
	_, _, err = svc.CreatePlannedTx(ctx, txQ, plan, false)
	testutil.AssertNoError(t, err, "create planned tx")
	testutil.AssertNoError(t, tx.Commit(), "commit composition evidence")

	var candidate string
	testutil.AssertNoError(t,
		client.DB().QueryRowContext(ctx,
			`SELECT candidate FROM tag_composition_event WHERE document_id = $1`, docID).Scan(&candidate),
		"read composition candidate")
	testutil.AssertEqual(t, candidate, "world war 2", "candidate keyed by the store house form")

	held, err := tags.CompositionHeldNames(ctx, client.Queries, []string{"world war 2"}, analyzer.TagSplitVersion)
	testutil.AssertNoError(t, err, "composition held names")
	testutil.AssertEqual(t, held["world war 2"], tags.CompositionReasonNamedUnitReconstructible,
		"the promotion hold matches the compound by its store tag name")
}

func decodeParts(t *testing.T, raw string) []string {
	t.Helper()
	var parts []string
	if err := json.Unmarshal([]byte(raw), &parts); err != nil {
		t.Fatalf("decode parts %q: %v", raw, err)
	}
	return parts
}

func TestTag_ApplyAliasPlan_StoresNormalizedName(t *testing.T) {
	svc, client := newTestTag(t)
	ctx := context.Background()

	canonicalID := database.MustCreateTag(t, client.Queries, "boundary canonical")

	if _, err := svc.ApplyAliasPlan(ctx, []tags.AliasAction{
		{AliasName: "Boundary Loser", Canonical: "Boundary Canonical"},
	}); err != nil {
		t.Fatalf("apply alias plan: %v", err)
	}

	alias, err := client.Queries.GetTagAliasByName(ctx, "boundary loser")
	testutil.AssertNoError(t, err, "alias stored under the normalized name")
	testutil.AssertEqual(t, alias.CanonicalTagID, canonicalID, "alias target")
}
