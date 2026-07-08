package service

import (
	"context"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func newTestPeople(t *testing.T) (*People, *database.Client) {
	t.Helper()
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	return NewPeople(client.Queries, testutil.NewTestLogger()), client
}

func TestPeople_Create(t *testing.T) {
	svc, _ := newTestPeople(t)
	ctx := context.Background()

	t.Run("creates person with normalized name", func(t *testing.T) {
		results, err := svc.Create(ctx, []CreatePersonInput{
			{Name: "Alice"},
		})
		testutil.AssertNoError(t, err, "create")
		testutil.AssertEqual(t, len(results), 1, "one result")
		testutil.AssertEqual(t, results[0].Status, Created, "created")
		testutil.AssertEqual(t, results[0].Entity.Name, "Alice", "name")
		testutil.AssertEqual(t, results[0].Entity.NormalizedName, "alice", "normalized name")
	})

	t.Run("exact name conflict", func(t *testing.T) {
		results, err := svc.Create(ctx, []CreatePersonInput{
			{Name: "Bob"},
		})
		testutil.AssertNoError(t, err, "create first")
		testutil.AssertEqual(t, results[0].Status, Created, "created first")

		results, err = svc.Create(ctx, []CreatePersonInput{
			{Name: "Bob"},
		})
		testutil.AssertNoError(t, err, "create duplicate")
		testutil.AssertEqual(t, results[0].Status, Conflict, "conflict on exact name")
		testutil.AssertEqual(t, results[0].Entity.Name, "Bob", "conflict entity name")
	})

	t.Run("normalized name conflict", func(t *testing.T) {
		results, err := svc.Create(ctx, []CreatePersonInput{
			{Name: "Carla"},
		})
		testutil.AssertNoError(t, err, "create first")
		testutil.AssertEqual(t, results[0].Status, Created, "created first")

		results, err = svc.Create(ctx, []CreatePersonInput{
			{Name: "carla"},
		})
		testutil.AssertNoError(t, err, "create case-different")
		testutil.AssertEqual(t, results[0].Status, Conflict, "conflict on normalized name")
	})

	t.Run("accent variant conflicts", func(t *testing.T) {
		results, err := svc.Create(ctx, []CreatePersonInput{
			{Name: "José"},
		})
		testutil.AssertNoError(t, err, "create accented")
		testutil.AssertEqual(t, results[0].Status, Created, "created accented")

		results, err = svc.Create(ctx, []CreatePersonInput{
			{Name: "Jose"},
		})
		testutil.AssertNoError(t, err, "create plain")
		testutil.AssertEqual(t, results[0].Status, Conflict, "conflict on accent variant")
	})

	t.Run("empty name is invalid", func(t *testing.T) {
		results, err := svc.Create(ctx, []CreatePersonInput{
			{Name: "  "},
		})
		testutil.AssertNoError(t, err, "create empty")
		testutil.AssertEqual(t, results[0].Status, Invalid, "invalid for whitespace-only")
	})

	t.Run("creates result entity includes normalizedName", func(t *testing.T) {
		results, err := svc.Create(ctx, []CreatePersonInput{
			{Name: "Émile"},
		})
		testutil.AssertNoError(t, err, "create")
		testutil.AssertEqual(t, results[0].Entity.NormalizedName, "emile", "normalizedName in entity")
	})
}

func TestPeople_Update(t *testing.T) {
	svc, _ := newTestPeople(t)
	ctx := context.Background()

	// Seed two people for update tests
	results, _ := svc.Create(ctx, []CreatePersonInput{
		{Name: "David"},
		{Name: "Eve"},
	})
	davidID := results[0].Entity.ID
	eveID := results[1].Entity.ID

	t.Run("updates name and normalizedName", func(t *testing.T) {
		updateResults, err := svc.Update(ctx, []PeopleUpdatePair{
			{ID: davidID, Name: "David Jr"},
		})
		testutil.AssertNoError(t, err, "update")
		testutil.AssertEqual(t, updateResults[0].Status, Updated, "updated")
		testutil.AssertEqual(t, updateResults[0].Entity.Name, "David Jr", "new name")
		testutil.AssertEqual(t, updateResults[0].Entity.NormalizedName, "david jr", "new normalizedName")
	})

	t.Run("noop when nothing changed", func(t *testing.T) {
		updateResults, err := svc.Update(ctx, []PeopleUpdatePair{
			{ID: eveID, Name: "Eve"},
		})
		testutil.AssertNoError(t, err, "update same")
		testutil.AssertEqual(t, updateResults[0].Status, Noop, "noop")
	})

	t.Run("update conflict on exact name", func(t *testing.T) {
		updateResults, err := svc.Update(ctx, []PeopleUpdatePair{
			{ID: eveID, Name: "David Jr"},
		})
		testutil.AssertNoError(t, err, "update conflict")
		testutil.AssertEqual(t, updateResults[0].Status, UpdateConflict, "update conflict")
	})

	t.Run("update conflict on normalized name", func(t *testing.T) {
		updateResults, err := svc.Update(ctx, []PeopleUpdatePair{
			{ID: eveID, Name: "david jr"},
		})
		testutil.AssertNoError(t, err, "update normalized conflict")
		testutil.AssertEqual(t, updateResults[0].Status, UpdateConflict, "update conflict on normalized")
	})

	t.Run("not found for missing id", func(t *testing.T) {
		updateResults, err := svc.Update(ctx, []PeopleUpdatePair{
			{ID: 999999, Name: "Ghost"},
		})
		testutil.AssertNoError(t, err, "update missing")
		testutil.AssertEqual(t, updateResults[0].Status, UpdateNotFound, "not found")
	})

	t.Run("empty name is invalid", func(t *testing.T) {
		updateResults, err := svc.Update(ctx, []PeopleUpdatePair{
			{ID: eveID, Name: "  "},
		})
		testutil.AssertNoError(t, err, "update empty")
		testutil.AssertEqual(t, updateResults[0].Status, UpdateInvalid, "invalid")
	})

	t.Run("accent variant update conflict", func(t *testing.T) {
		// David Jr normalizes to "david jr", creating "Dávid Jr" should conflict
		updateResults, err := svc.Update(ctx, []PeopleUpdatePair{
			{ID: eveID, Name: "Dávid Jr"},
		})
		testutil.AssertNoError(t, err, "update accent conflict")
		testutil.AssertEqual(t, updateResults[0].Status, UpdateConflict, "update conflict on accent variant")
	})
}

func TestPeople_Delete(t *testing.T) {
	svc, _ := newTestPeople(t)
	ctx := context.Background()

	results, _ := svc.Create(ctx, []CreatePersonInput{
		{Name: "Frank"},
	})
	frankID := results[0].Entity.ID

	t.Run("deletes existing person", func(t *testing.T) {
		deleteResults, err := svc.Delete(ctx, []int64{frankID})
		testutil.AssertNoError(t, err, "delete")
		testutil.AssertEqual(t, deleteResults[0].Status, Deleted, "deleted")
	})

	t.Run("not found for missing id", func(t *testing.T) {
		deleteResults, err := svc.Delete(ctx, []int64{999999})
		testutil.AssertNoError(t, err, "delete missing")
		testutil.AssertEqual(t, deleteResults[0].Status, DeleteNotFound, "not found")
	})
}

func TestNormalizeForDB_roundtrip(t *testing.T) {
	name := "María-José O'Brien"
	normalized := utils.NormalizeForDB(name)
	expected := "maria jose obrien"
	if normalized != expected {
		t.Errorf("NormalizeForDB(%q) = %q, want %q", name, normalized, expected)
	}
}
