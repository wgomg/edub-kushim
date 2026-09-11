package service

import (
	"context"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func newTestTag(t *testing.T) (*Tag, *database.Client) {
	t.Helper()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	tag, err := NewTag(client.Queries, testutil.NewTestLogger(), testutil.NewMockEmbedder())
	if err != nil {
		t.Fatalf("NewTag: %v", err)
	}
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