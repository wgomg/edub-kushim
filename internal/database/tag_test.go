package database

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func tagTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE tag (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func TestCreateTag(t *testing.T) {
	db := tagTestDB(t)
	q := New(db)

	result, err := q.CreateTag(context.Background(), "receipt")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	id, _ := result.LastInsertId()
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
}

func TestCreateTag_DuplicateName(t *testing.T) {
	db := tagTestDB(t)
	q := New(db)

	q.CreateTag(context.Background(), "duplicate")
	_, err := q.CreateTag(context.Background(), "duplicate")
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation, got nil")
	}
}

func TestGetTag(t *testing.T) {
	db := tagTestDB(t)
	q := New(db)

	result, _ := q.CreateTag(context.Background(), "work")
	id, _ := result.LastInsertId()

	tag, err := q.GetTag(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	if tag.Name != "work" {
		t.Errorf("Name = %q", tag.Name)
	}
}

func TestGetTag_NotFound(t *testing.T) {
	db := tagTestDB(t)
	q := New(db)

	_, err := q.GetTag(context.Background(), 999)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestUpdateTag(t *testing.T) {
	db := tagTestDB(t)
	q := New(db)

	result, _ := q.CreateTag(context.Background(), "old")
	id, _ := result.LastInsertId()

	err := q.UpdateTag(context.Background(), UpdateTagParams{
		Name: "new",
		ID:   id,
	})
	if err != nil {
		t.Fatalf("UpdateTag: %v", err)
	}

	tag, _ := q.GetTag(context.Background(), id)
	if tag.Name != "new" {
		t.Errorf("Name = %q, want 'new'", tag.Name)
	}
}

func TestUpdateTag_DuplicateName(t *testing.T) {
	db := tagTestDB(t)
	q := New(db)

	q.CreateTag(context.Background(), "existing")
	result, _ := q.CreateTag(context.Background(), "to-rename")
	id, _ := result.LastInsertId()

	err := q.UpdateTag(context.Background(), UpdateTagParams{
		Name: "existing",
		ID:   id,
	})
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation, got nil")
	}
}

func TestDeleteTag(t *testing.T) {
	db := tagTestDB(t)
	q := New(db)

	result, _ := q.CreateTag(context.Background(), "delete-me")
	id, _ := result.LastInsertId()

	err := q.DeleteTag(context.Background(), id)
	if err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}

	_, err = q.GetTag(context.Background(), id)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestListTags(t *testing.T) {
	db := tagTestDB(t)
	q := New(db)

	q.CreateTag(context.Background(), "urgent")
	q.CreateTag(context.Background(), "finance")

	tags, err := q.ListTags(context.Background(), ListTagsParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 2 {
		t.Errorf("got %d tags, want 2", len(tags))
	}
}

func TestListTags_Pagination(t *testing.T) {
	db := tagTestDB(t)
	q := New(db)

	q.CreateTag(context.Background(), "alpha")
	q.CreateTag(context.Background(), "beta")
	q.CreateTag(context.Background(), "gamma")

	tags, err := q.ListTags(context.Background(), ListTagsParams{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 2 {
		t.Errorf("got %d tags, want 2", len(tags))
	}
}
