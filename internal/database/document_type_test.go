package database

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func docTypeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE document_type (
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

func TestCreateDocumentType(t *testing.T) {
	db := docTypeTestDB(t)
	q := New(db)

	result, err := q.CreateDocumentType(context.Background(), "invoice")
	if err != nil {
		t.Fatalf("CreateDocumentType: %v", err)
	}
	id, _ := result.LastInsertId()
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
}

func TestCreateDocumentType_DuplicateName(t *testing.T) {
	db := docTypeTestDB(t)
	q := New(db)

	q.CreateDocumentType(context.Background(), "report")
	_, err := q.CreateDocumentType(context.Background(), "report")
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation, got nil")
	}
}

func TestGetDocumentType(t *testing.T) {
	db := docTypeTestDB(t)
	q := New(db)

	result, _ := q.CreateDocumentType(context.Background(), "letter")
	id, _ := result.LastInsertId()

	dt, err := q.GetDocumentType(context.Background(), id)
	if err != nil {
		t.Fatalf("GetDocumentType: %v", err)
	}
	if dt.Name != "letter" {
		t.Errorf("Name = %q", dt.Name)
	}
}

func TestGetDocumentType_NotFound(t *testing.T) {
	db := docTypeTestDB(t)
	q := New(db)

	_, err := q.GetDocumentType(context.Background(), 999)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestUpdateDocumentType(t *testing.T) {
	db := docTypeTestDB(t)
	q := New(db)

	result, _ := q.CreateDocumentType(context.Background(), "memo")
	id, _ := result.LastInsertId()

	err := q.UpdateDocumentType(context.Background(), UpdateDocumentTypeParams{
		Name: "memo-updated",
		ID:   id,
	})
	if err != nil {
		t.Fatalf("UpdateDocumentType: %v", err)
	}

	dt, _ := q.GetDocumentType(context.Background(), id)
	if dt.Name != "memo-updated" {
		t.Errorf("Name = %q", dt.Name)
	}
}

func TestDeleteDocumentType(t *testing.T) {
	db := docTypeTestDB(t)
	q := New(db)

	result, _ := q.CreateDocumentType(context.Background(), "draft")
	id, _ := result.LastInsertId()

	err := q.DeleteDocumentType(context.Background(), id)
	if err != nil {
		t.Fatalf("DeleteDocumentType: %v", err)
	}

	_, err = q.GetDocumentType(context.Background(), id)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestListDocumentTypes(t *testing.T) {
	db := docTypeTestDB(t)
	q := New(db)

	q.CreateDocumentType(context.Background(), "contract")
	q.CreateDocumentType(context.Background(), "receipt")

	types, err := q.ListDocumentTypes(context.Background(), ListDocumentTypesParams{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListDocumentTypes: %v", err)
	}
	if len(types) != 2 {
		t.Errorf("got %d types, want 2", len(types))
	}
}
