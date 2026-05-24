package database

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func docTagTestDB(t *testing.T) *sql.DB {
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
		CREATE TABLE document (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			md5_checksum TEXT NOT NULL,
			sha512_checksum TEXT UNIQUE NOT NULL,
			mime_type TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			modified_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			document_type_id INTEGER,
			original_path TEXT NOT NULL,
			storage_path TEXT NOT NULL,
			text_content TEXT
		);
		CREATE TABLE document_tag (
			document_id INTEGER NOT NULL,
			tag_id INTEGER NOT NULL,
			PRIMARY KEY (document_id, tag_id),
			FOREIGN KEY (document_id) REFERENCES document(id),
			FOREIGN KEY (tag_id) REFERENCES tag(id)
		);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

const seedTagName = "Test Tag"

func seedTag(t *testing.T, q *Queries) int64 {
	t.Helper()
	result, err := q.CreateTag(context.Background(), seedTagName)
	if err != nil {
		t.Fatalf("seedTag: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func seedDocForTag(t *testing.T, q *Queries, title, md5, sha512 string) int64 {
	t.Helper()
	result, err := q.CreateDocument(context.Background(), CreateDocumentParams{
		Title:          title,
		Md5Checksum:    md5,
		Sha512Checksum: sha512,
		MimeType:       "application/pdf",
		FileSize:       100,
		OriginalPath:   "/" + title,
		StoragePath:    "/store/" + title,
	})
	if err != nil {
		t.Fatalf("seedDoc: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestAddDocumentTag(t *testing.T) {
	db := docTagTestDB(t)
	q := New(db)

	tid := seedTag(t, q)
	did := seedDocForTag(t, q, "doc1", "md5-dt1", "sha512-dt1")

	err := q.AddDocumentTag(context.Background(), AddDocumentTagParams{
		DocumentID: did,
		TagID:      tid,
	})
	if err != nil {
		t.Fatalf("AddDocumentTag: %v", err)
	}
}

func TestAddDocumentTag_Duplicate(t *testing.T) {
	db := docTagTestDB(t)
	q := New(db)

	tid := seedTag(t, q)
	did := seedDocForTag(t, q, "doc2", "md5-dt2", "sha512-dt2")

	q.AddDocumentTag(context.Background(), AddDocumentTagParams{DocumentID: did, TagID: tid})
	err := q.AddDocumentTag(context.Background(), AddDocumentTagParams{DocumentID: did, TagID: tid})
	if err == nil {
		t.Fatal("expected PRIMARY KEY violation, got nil")
	}
}

func TestGetDocumentTags(t *testing.T) {
	db := docTagTestDB(t)
	q := New(db)

	tid := seedTag(t, q)
	did := seedDocForTag(t, q, "doc3", "md5-dt3", "sha512-dt3")

	q.AddDocumentTag(context.Background(), AddDocumentTagParams{DocumentID: did, TagID: tid})

	tags, err := q.GetDocumentTags(context.Background(), did)
	if err != nil {
		t.Fatalf("GetDocumentTags: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("got %d tags, want 1", len(tags))
	}
	if tags[0].Name != seedTagName {
		t.Errorf("Name = %q", tags[0].Name)
	}
}

func TestGetDocumentTags_None(t *testing.T) {
	db := docTagTestDB(t)
	q := New(db)

	did := seedDocForTag(t, q, "doc-no-tags", "md5-nt", "sha512-nt")

	tags, err := q.GetDocumentTags(context.Background(), did)
	if err != nil {
		t.Fatalf("GetDocumentTags: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("got %d tags, want 0", len(tags))
	}
}

func TestGetTagDocuments(t *testing.T) {
	db := docTagTestDB(t)
	q := New(db)

	tid := seedTag(t, q)
	d1 := seedDocForTag(t, q, "d1", "md5-gtd1", "sha512-gtd1")
	d2 := seedDocForTag(t, q, "d2", "md5-gtd2", "sha512-gtd2")

	q.AddDocumentTag(context.Background(), AddDocumentTagParams{DocumentID: d1, TagID: tid})
	q.AddDocumentTag(context.Background(), AddDocumentTagParams{DocumentID: d2, TagID: tid})

	docs, err := q.GetTagDocuments(context.Background(), tid)
	if err != nil {
		t.Fatalf("GetTagDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(docs))
	}
}

func TestRemoveDocumentTag(t *testing.T) {
	db := docTagTestDB(t)
	q := New(db)

	tid := seedTag(t, q)
	did := seedDocForTag(t, q, "remove-tag", "md5-rt", "sha512-rt")

	q.AddDocumentTag(context.Background(), AddDocumentTagParams{DocumentID: did, TagID: tid})
	err := q.RemoveDocumentTag(context.Background(), RemoveDocumentTagParams{
		DocumentID: did,
		TagID:      tid,
	})
	if err != nil {
		t.Fatalf("RemoveDocumentTag: %v", err)
	}

	tags, _ := q.GetDocumentTags(context.Background(), did)
	if len(tags) != 0 {
		t.Errorf("got %d tags after remove, want 0", len(tags))
	}
}

func TestRemoveDocumentTag_Nonexistent(t *testing.T) {
	db := docTagTestDB(t)
	q := New(db)

	err := q.RemoveDocumentTag(context.Background(), RemoveDocumentTagParams{
		DocumentID: 1,
		TagID:      1,
	})
	if err != nil {
		t.Fatalf("RemoveDocumentTag on missing pair: %v", err)
	}
}

func TestClearDocumentTags(t *testing.T) {
	db := docTagTestDB(t)
	q := New(db)

	tid := seedTag(t, q)
	did := seedDocForTag(t, q, "clear-tags", "md5-ct", "sha512-ct")

	q.AddDocumentTag(context.Background(), AddDocumentTagParams{DocumentID: did, TagID: tid})
	err := q.ClearDocumentTags(context.Background(), did)
	if err != nil {
		t.Fatalf("ClearDocumentTags: %v", err)
	}

	tags, _ := q.GetDocumentTags(context.Background(), did)
	if len(tags) != 0 {
		t.Errorf("got %d tags after clear, want 0", len(tags))
	}
}
