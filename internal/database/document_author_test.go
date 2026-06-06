package database

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func docAuthorTestDB(t *testing.T) *sql.DB {
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
		CREATE TABLE document (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			md5_checksum TEXT NOT NULL,
			sha512_checksum TEXT UNIQUE NOT NULL,
			mime_type TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			page_count INTEGER NOT NULL DEFAULT 0,
			word_count INTEGER NOT NULL DEFAULT 0,
			char_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			modified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			document_type_id INTEGER,
			original_path TEXT NOT NULL,
			storage_path TEXT NOT NULL,
			text_content TEXT
		);
		CREATE TABLE document_author (
			document_id INTEGER NOT NULL,
			author_id INTEGER NOT NULL,
			PRIMARY KEY (document_id, author_id),
			FOREIGN KEY (document_id) REFERENCES document(id),
			FOREIGN KEY (author_id) REFERENCES author(id)
		);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

const seedAuthorName = "Test Author"

func seedAuthor(t *testing.T, q *Queries) int64 {
	t.Helper()
	result, err := q.CreateAuthor(context.Background(), seedAuthorName)
	if err != nil {
		t.Fatalf("seedAuthor: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func seedDocForAuthor(t *testing.T, q *Queries, title, md5, sha512 string) int64 {
	t.Helper()
	result, err := q.CreateDocument(context.Background(), CreateDocumentParams{
		Title:          title,
		Md5Checksum:    md5,
		Sha512Checksum: sha512,
		MimeType:       "application/pdf",
		FileSize:       100,
		PageCount:      0,
		OriginalPath:   "/" + title,
		StoragePath:    "/store/" + title,
	})
	if err != nil {
		t.Fatalf("seedDoc: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestAddDocumentAuthor(t *testing.T) {
	db := docAuthorTestDB(t)
	q := New(db)

	aid := seedAuthor(t, q)
	did := seedDocForAuthor(t, q, "doc1", "md5-a1", "sha512-a1")

	err := q.AddDocumentAuthor(context.Background(), AddDocumentAuthorParams{
		DocumentID: did,
		AuthorID:   aid,
	})
	if err != nil {
		t.Fatalf("AddDocumentAuthor: %v", err)
	}
}

func TestAddDocumentAuthor_Duplicate(t *testing.T) {
	db := docAuthorTestDB(t)
	q := New(db)

	aid := seedAuthor(t, q)
	did := seedDocForAuthor(t, q, "doc2", "md5-a2", "sha512-a2")

	q.AddDocumentAuthor(context.Background(), AddDocumentAuthorParams{DocumentID: did, AuthorID: aid})
	err := q.AddDocumentAuthor(context.Background(), AddDocumentAuthorParams{DocumentID: did, AuthorID: aid})
	if err == nil {
		t.Fatal("expected PRIMARY KEY violation, got nil")
	}
}

func TestGetDocumentAuthors(t *testing.T) {
	db := docAuthorTestDB(t)
	q := New(db)

	aid := seedAuthor(t, q)
	did := seedDocForAuthor(t, q, "doc3", "md5-a3", "sha512-a3")

	q.AddDocumentAuthor(context.Background(), AddDocumentAuthorParams{DocumentID: did, AuthorID: aid})

	authors, err := q.GetDocumentAuthors(context.Background(), did)
	if err != nil {
		t.Fatalf("GetDocumentAuthors: %v", err)
	}
	if len(authors) != 1 {
		t.Fatalf("got %d authors, want 1", len(authors))
	}
	if authors[0].Name != seedAuthorName {
		t.Errorf("Name = %q", authors[0].Name)
	}
}

func TestGetDocumentAuthors_None(t *testing.T) {
	db := docAuthorTestDB(t)
	q := New(db)

	did := seedDocForAuthor(t, q, "doc-no-authors", "md5-na", "sha512-na")

	authors, err := q.GetDocumentAuthors(context.Background(), did)
	if err != nil {
		t.Fatalf("GetDocumentAuthors: %v", err)
	}
	if len(authors) != 0 {
		t.Errorf("got %d authors, want 0", len(authors))
	}
}

func TestGetAuthorDocuments(t *testing.T) {
	db := docAuthorTestDB(t)
	q := New(db)

	aid := seedAuthor(t, q)
	d1 := seedDocForAuthor(t, q, "d1", "md5-d1", "sha512-d1")
	d2 := seedDocForAuthor(t, q, "d2", "md5-d2", "sha512-d2")

	q.AddDocumentAuthor(context.Background(), AddDocumentAuthorParams{DocumentID: d1, AuthorID: aid})
	q.AddDocumentAuthor(context.Background(), AddDocumentAuthorParams{DocumentID: d2, AuthorID: aid})

	docs, err := q.GetAuthorDocuments(context.Background(), aid)
	if err != nil {
		t.Fatalf("GetAuthorDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(docs))
	}
}

func TestRemoveDocumentAuthor(t *testing.T) {
	db := docAuthorTestDB(t)
	q := New(db)

	aid := seedAuthor(t, q)
	did := seedDocForAuthor(t, q, "remove-me", "md5-rm", "sha512-rm")

	q.AddDocumentAuthor(context.Background(), AddDocumentAuthorParams{DocumentID: did, AuthorID: aid})
	err := q.RemoveDocumentAuthor(context.Background(), RemoveDocumentAuthorParams{
		DocumentID: did,
		AuthorID:   aid,
	})
	if err != nil {
		t.Fatalf("RemoveDocumentAuthor: %v", err)
	}

	authors, _ := q.GetDocumentAuthors(context.Background(), did)
	if len(authors) != 0 {
		t.Errorf("got %d authors after remove, want 0", len(authors))
	}
}

func TestRemoveDocumentAuthor_Nonexistent(t *testing.T) {
	db := docAuthorTestDB(t)
	q := New(db)

	err := q.RemoveDocumentAuthor(context.Background(), RemoveDocumentAuthorParams{
		DocumentID: 1,
		AuthorID:   1,
	})
	if err != nil {
		t.Fatalf("RemoveDocumentAuthor on missing pair: %v", err)
	}
}

func TestClearDocumentAuthors(t *testing.T) {
	db := docAuthorTestDB(t)
	q := New(db)

	aid := seedAuthor(t, q)
	did := seedDocForAuthor(t, q, "clear", "md5-cl", "sha512-cl")

	q.AddDocumentAuthor(context.Background(), AddDocumentAuthorParams{DocumentID: did, AuthorID: aid})
	err := q.ClearDocumentAuthors(context.Background(), did)
	if err != nil {
		t.Fatalf("ClearDocumentAuthors: %v", err)
	}

	authors, _ := q.GetDocumentAuthors(context.Background(), did)
	if len(authors) != 0 {
		t.Errorf("got %d authors after clear, want 0", len(authors))
	}
}
