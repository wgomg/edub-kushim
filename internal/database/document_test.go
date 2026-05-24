package database

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

func docTestDB(t *testing.T) *sql.DB {
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
			text_content TEXT,
			FOREIGN KEY (document_type_id) REFERENCES document_type(id)
		);
		CREATE INDEX idx_document_md5 ON document(md5_checksum);
	`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func insertDoc(t *testing.T, q *Queries, overrides map[string]any) int64 {
	t.Helper()
	title := "test"
	md5 := "md5-default"
	sha512 := "sha512-default"
	mime := "application/pdf"
	var size int64 = 100
	origPath := "/orig"
	storePath := "/store"
	var text sql.NullString

	if v, ok := overrides["title"]; ok {
		title = v.(string)
	}
	if v, ok := overrides["md5"]; ok {
		md5 = v.(string)
	}
	if v, ok := overrides["sha512"]; ok {
		sha512 = v.(string)
	}
	if v, ok := overrides["mime"]; ok {
		mime = v.(string)
	}
	if v, ok := overrides["size"]; ok {
		size = v.(int64)
	}
	if v, ok := overrides["orig_path"]; ok {
		origPath = v.(string)
	}
	if v, ok := overrides["store_path"]; ok {
		storePath = v.(string)
	}
	if v, ok := overrides["text"]; ok {
		text = sql.NullString{String: v.(string), Valid: true}
	}

	result, err := q.CreateDocument(context.Background(), CreateDocumentParams{
		Title:          title,
		Md5Checksum:    md5,
		Sha512Checksum: sha512,
		MimeType:       mime,
		FileSize:       size,
		OriginalPath:   origPath,
		StoragePath:    storePath,
		TextContent:    text,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func TestCreateDocument(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	id := insertDoc(t, q, map[string]any{"title": "createdoc-test"})

	doc, err := q.GetDocument(context.Background(), id)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if doc.Title != "createdoc-test" {
		t.Errorf("Title = %q, want %q", doc.Title, "createdoc-test")
	}
}

func TestCreateDocument_UniqueSHA512(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	insertDoc(t, q, map[string]any{"sha512": "dup-sha"})
	_, err := q.CreateDocument(context.Background(), CreateDocumentParams{
		Title:          "dup",
		Md5Checksum:    "other-md5",
		Sha512Checksum: "dup-sha",
		MimeType:       "text/plain",
		FileSize:       1,
		OriginalPath:   "/o",
		StoragePath:    "/s",
	})
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation on sha512, got nil")
	}
}

func TestGetDocument(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	id := insertDoc(t, q, map[string]any{
		"title":      "get-test",
		"md5":        "md5-get",
		"sha512":     "sha512-get",
		"mime":       "image/png",
		"size":       int64(2048),
		"text":       "hello",
		"orig_path":  "/in/test.png",
		"store_path": "/out/test.png",
	})

	doc, err := q.GetDocument(context.Background(), id)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}

	if doc.Title != "get-test" {
		t.Errorf("Title = %q", doc.Title)
	}
	if doc.Md5Checksum != "md5-get" {
		t.Errorf("Md5Checksum = %q", doc.Md5Checksum)
	}
	if doc.Sha512Checksum != "sha512-get" {
		t.Errorf("Sha512Checksum = %q", doc.Sha512Checksum)
	}
	if doc.MimeType != "image/png" {
		t.Errorf("MimeType = %q", doc.MimeType)
	}
	if doc.FileSize != 2048 {
		t.Errorf("FileSize = %d", doc.FileSize)
	}
	if !doc.TextContent.Valid || doc.TextContent.String != "hello" {
		t.Errorf("TextContent = %v", doc.TextContent)
	}
}

func TestGetDocument_NotFound(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	_, err := q.GetDocument(context.Background(), 999)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestDeleteDocument(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	id := insertDoc(t, q, nil)
	err := q.DeleteDocument(context.Background(), id)
	if err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}

	_, err = q.GetDocument(context.Background(), id)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestGetDocumentByMD5Checksum(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	insertDoc(t, q, map[string]any{"md5": "find-me", "sha512": "s1"})
	insertDoc(t, q, map[string]any{"md5": "other", "sha512": "s2"})

	rows, err := q.GetDocumentByMD5Checksum(context.Background(), "find-me")
	if err != nil {
		t.Fatalf("GetDocumentByMD5Checksum: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Sha512Checksum != "s1" {
		t.Errorf("Sha512Checksum = %q", rows[0].Sha512Checksum)
	}
}

func TestGetDocumentByMD5Checksum_NoMatch(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	rows, err := q.GetDocumentByMD5Checksum(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("GetDocumentByMD5Checksum: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestGetDocumentBySHA512Checksum(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	insertDoc(t, q, map[string]any{"sha512": "target-sha", "md5": "m1"})
	insertDoc(t, q, map[string]any{"sha512": "other-sha", "md5": "m2"})

	doc, err := q.GetDocumentBySHA512Checksum(context.Background(), "target-sha")
	if err != nil {
		t.Fatalf("GetDocumentBySHA512Checksum: %v", err)
	}
	if doc.Md5Checksum != "m1" {
		t.Errorf("Md5Checksum = %q", doc.Md5Checksum)
	}
}

func TestGetDocumentBySHA512Checksum_NotFound(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	_, err := q.GetDocumentBySHA512Checksum(context.Background(), "nonexistent")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestGetDocumentWithDetails(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	_, err := db.Exec("INSERT INTO document_type (id, name) VALUES (1, 'invoice')")
	if err != nil {
		t.Fatal(err)
	}

	id := insertDoc(t, q, map[string]any{"title": "with-type"})
	_, err = db.Exec("UPDATE document SET document_type_id = 1 WHERE id = ?", id)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := q.GetDocumentWithDetails(context.Background(), id)
	if err != nil {
		t.Fatalf("GetDocumentWithDetails: %v", err)
	}
	if doc.DocumentTypeName.String != "invoice" {
		t.Errorf("DocumentTypeName = %q, want %q", doc.DocumentTypeName.String, "invoice")
	}
	if doc.Title != "with-type" {
		t.Errorf("Title = %q", doc.Title)
	}
}

func TestGetDocumentWithDetails_NoType(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	id := insertDoc(t, q, map[string]any{"title": "no-type"})

	doc, err := q.GetDocumentWithDetails(context.Background(), id)
	if err != nil {
		t.Fatalf("GetDocumentWithDetails: %v", err)
	}
	if doc.DocumentTypeName.Valid {
		t.Errorf("expected nil DocumentTypeName, got %v", doc.DocumentTypeName.String)
	}
}

func TestListDocuments(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	for i := range 5 {
		title := fmt.Sprintf("doc-%d", i)
		insertDoc(t, q, map[string]any{
			"title":  title,
			"md5":    fmt.Sprintf("md5-%d", i),
			"sha512": fmt.Sprintf("sha512-%d", i),
		})
	}

	rows, err := q.ListDocuments(context.Background(), ListDocumentsParams{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("got %d rows, want 3", len(rows))
	}
}

func TestListDocuments_Offset(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	for i := range 5 {
		insertDoc(t, q, map[string]any{
			"title":  fmt.Sprintf("doc-%d", i),
			"md5":    fmt.Sprintf("md5-%d", i),
			"sha512": fmt.Sprintf("sha512-%d", i),
		})
	}

	rows, err := q.ListDocuments(context.Background(), ListDocumentsParams{Limit: 10, Offset: 3})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
}

func TestSearchDocumentsByTitle(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	insertDoc(t, q, map[string]any{
		"title":  "invoice march 2024",
		"md5":    "m1",
		"sha512": "s1",
	})
	insertDoc(t, q, map[string]any{
		"title":  "report january",
		"md5":    "m2",
		"sha512": "s2",
	})

	rows, err := q.SearchDocumentsByTitle(context.Background(), SearchDocumentsByTitleParams{
		Title:  "%invoice%",
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("SearchDocumentsByTitle: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Title != "invoice march 2024" {
		t.Errorf("Title = %q", rows[0].Title)
	}
}

func TestSearchDocumentsByTitle_NoMatch(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	insertDoc(t, q, map[string]any{"title": "report", "md5": "m1", "sha512": "s1"})

	rows, err := q.SearchDocumentsByTitle(context.Background(), SearchDocumentsByTitleParams{
		Title:  "%nonexistent%",
		Limit:  10,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("SearchDocumentsByTitle: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestUpdateDocumentPaths(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	id := insertDoc(t, q, nil)

	err := q.UpdateDocumentPaths(context.Background(), UpdateDocumentPathsParams{
		OriginalPath: "/new/orig",
		StoragePath:  "/new/store",
		ID:           id,
	})
	if err != nil {
		t.Fatalf("UpdateDocumentPaths: %v", err)
	}

	doc, err := q.GetDocument(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if doc.OriginalPath != "/new/orig" {
		t.Errorf("OriginalPath = %q", doc.OriginalPath)
	}
	if doc.StoragePath != "/new/store" {
		t.Errorf("StoragePath = %q", doc.StoragePath)
	}
}

func TestUpdateDocumentText(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	id := insertDoc(t, q, nil)

	err := q.UpdateDocumentText(context.Background(), UpdateDocumentTextParams{
		TextContent: sql.NullString{String: "updated text", Valid: true},
		ID:          id,
	})
	if err != nil {
		t.Fatalf("UpdateDocumentText: %v", err)
	}

	doc, err := q.GetDocument(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.TextContent.Valid || doc.TextContent.String != "updated text" {
		t.Errorf("TextContent = %v", doc.TextContent)
	}
}

func TestUpdateDocumentPathsWithText(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	id := insertDoc(t, q, nil)

	err := q.UpdateDocumentPathsWithText(context.Background(), UpdateDocumentPathsWithTextParams{
		OriginalPath: "/new/orig",
		StoragePath:  "/new/store",
		TextContent:  sql.NullString{String: "with text", Valid: true},
		ID:           id,
	})
	if err != nil {
		t.Fatalf("UpdateDocumentPathsWithText: %v", err)
	}

	doc, err := q.GetDocument(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if doc.OriginalPath != "/new/orig" {
		t.Errorf("OriginalPath = %q", doc.OriginalPath)
	}
	if doc.StoragePath != "/new/store" {
		t.Errorf("StoragePath = %q", doc.StoragePath)
	}
	if !doc.TextContent.Valid || doc.TextContent.String != "with text" {
		t.Errorf("TextContent = %v", doc.TextContent)
	}
}

func TestUpdateDocumentWithText(t *testing.T) {
	db := docTestDB(t)
	q := New(db)

	id := insertDoc(t, q, nil)

	err := q.UpdateDocumentWithText(context.Background(), UpdateDocumentWithTextParams{
		OriginalPath: "/new/orig",
		StoragePath:  "/new/store",
		TextContent:  sql.NullString{String: "via with text", Valid: true},
		ID:           id,
	})
	if err != nil {
		t.Fatalf("UpdateDocumentWithText: %v", err)
	}

	doc, err := q.GetDocument(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if doc.OriginalPath != "/new/orig" {
		t.Errorf("OriginalPath = %q", doc.OriginalPath)
	}
	if doc.StoragePath != "/new/store" {
		t.Errorf("StoragePath = %q", doc.StoragePath)
	}
	if !doc.TextContent.Valid || doc.TextContent.String != "via with text" {
		t.Errorf("TextContent = %v", doc.TextContent)
	}
}
