package database

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func authorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE author (
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

func TestCreateAuthor(t *testing.T) {
	db := authorTestDB(t)
	q := New(db)

	result, err := q.CreateAuthor(context.Background(), "John Doe")
	if err != nil {
		t.Fatalf("CreateAuthor: %v", err)
	}
	id, _ := result.LastInsertId()
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
}

func TestCreateAuthor_DuplicateName(t *testing.T) {
	db := authorTestDB(t)
	q := New(db)

	q.CreateAuthor(context.Background(), "Jane Doe")
	_, err := q.CreateAuthor(context.Background(), "Jane Doe")
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation, got nil")
	}
}

func TestGetAuthor(t *testing.T) {
	db := authorTestDB(t)
	q := New(db)

	result, _ := q.CreateAuthor(context.Background(), "Author Name")
	id, _ := result.LastInsertId()

	author, err := q.GetAuthor(context.Background(), id)
	if err != nil {
		t.Fatalf("GetAuthor: %v", err)
	}
	if author.Name != "Author Name" {
		t.Errorf("Name = %q", author.Name)
	}
}

func TestGetAuthor_NotFound(t *testing.T) {
	db := authorTestDB(t)
	q := New(db)

	_, err := q.GetAuthor(context.Background(), 999)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestUpdateAuthor(t *testing.T) {
	db := authorTestDB(t)
	q := New(db)

	result, _ := q.CreateAuthor(context.Background(), "Old Name")
	id, _ := result.LastInsertId()

	err := q.UpdateAuthor(context.Background(), UpdateAuthorParams{
		Name: "New Name",
		ID:   id,
	})
	if err != nil {
		t.Fatalf("UpdateAuthor: %v", err)
	}

	author, _ := q.GetAuthor(context.Background(), id)
	if author.Name != "New Name" {
		t.Errorf("Name = %q, want 'New Name'", author.Name)
	}
}

func TestDeleteAuthor(t *testing.T) {
	db := authorTestDB(t)
	q := New(db)

	result, _ := q.CreateAuthor(context.Background(), "To Delete")
	id, _ := result.LastInsertId()

	err := q.DeleteAuthor(context.Background(), id)
	if err != nil {
		t.Fatalf("DeleteAuthor: %v", err)
	}

	_, err = q.GetAuthor(context.Background(), id)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestListAuthors(t *testing.T) {
	db := authorTestDB(t)
	q := New(db)

	q.CreateAuthor(context.Background(), "Alice")
	q.CreateAuthor(context.Background(), "Bob")

	authors, err := q.ListAuthors(context.Background(), ListAuthorsParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuthors: %v", err)
	}
	if len(authors) != 2 {
		t.Errorf("got %d authors, want 2", len(authors))
	}
}

func TestListAuthors_Pagination(t *testing.T) {
	db := authorTestDB(t)
	q := New(db)

	q.CreateAuthor(context.Background(), "A")
	q.CreateAuthor(context.Background(), "B")
	q.CreateAuthor(context.Background(), "C")

	authors, err := q.ListAuthors(context.Background(), ListAuthorsParams{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuthors: %v", err)
	}
	if len(authors) != 2 {
		t.Errorf("got %d authors, want 2", len(authors))
	}
}
