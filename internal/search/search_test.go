package search

import (
	"context"
	"database/sql"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

func setupSearchTest(t *testing.T) (*Engine, *database.Queries) {
	t.Helper()
	q, _ := database.NewTestQueries(t)
	engine := NewEngine(testutil.NewTestLogger(), q)
	return engine, q
}


func getID(t *testing.T, res sql.Result) int64 {
	t.Helper()
	if res == nil {
		t.Fatal("expected non-nil sql.Result")
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func insertSearchDocRaw(t *testing.T, q *database.Queries, docID, title, language string, docTypeID int64, tag string) int64 {
	t.Helper()
	res, err := q.CreateDocument(context.Background(), database.CreateDocumentParams{
		DocumentID:     docID,
		Title:          title,
		Md5Checksum:    "md5-" + docID,
		Sha512Checksum: "sha512-" + docID,
		MimeType:       "application/pdf",
		FileSize:       100,
		OriginalPath:   "/tmp/" + title,
		StoragePath:    "/tmp/storage/" + title,
		TextContent:    sql.NullString{String: "content", Valid: true},
		PageCount:      1,
		WordCount:      1,
		CharCount:      7,
		Language:       language,
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	id := getID(t, res)

	if docTypeID != 3 { // 3 is default article type from seed
		err = q.UpdateDocumentEditable(context.Background(), database.UpdateDocumentEditableParams{
			Title:          title,
			DocumentTypeID: docTypeID,
			Language:       language,
			DocumentID:     docID,
		})
		if err != nil {
			t.Fatalf("update document type: %v", err)
		}
	}
	return id
}

func insertSearchDoc(t *testing.T, q *database.Queries, title, textContent string) {
	t.Helper()

	docTypes, err := q.ListAllDocumentTypes(context.Background())
	if err != nil || len(docTypes) == 0 {
		t.Fatalf("no document types: %v", err)
	}

	docID := testutil.FormatString("doc-%d", len(textContent))
	res, err := q.CreateDocument(context.Background(), database.CreateDocumentParams{
		DocumentID:     docID,
		Title:          title,
		Md5Checksum:    testutil.FormatString("md5-%d", len(textContent)),
		Sha512Checksum: testutil.FormatString("sha512-%d", len(textContent)),
		MimeType:       "application/pdf",
		FileSize:       int64(len(textContent)),
		OriginalPath:   "/tmp/" + title,
		StoragePath:    "/tmp/storage/" + title,
		TextContent:    sql.NullString{String: textContent, Valid: true},
		PageCount:      1,
		WordCount:      int64(len(textContent) / 5),
		CharCount:      int64(len(textContent)),
		Language:       "eng",
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("get last insert id: %v", err)
	}

	// Update FTS directly
	q.UpdateDocumentFTS(context.Background(), struct {
		DocumentID int64
		Title      string
		Content    string
	}{
		DocumentID: id,
		Title:      title,
		Content:    textContent,
	})
}

func TestSearchEngine(t *testing.T) {
	engine, q := setupSearchTest(t)
	ctx := context.Background()

	insertSearchDoc(t, q, "report.pdf", "This is the Q1 financial report showing revenue growth")
	insertSearchDoc(t, q, "invoice.pdf", "Invoice for consulting services provided in Q2")
	insertSearchDoc(t, q, "notes.pdf", "Meeting notes discussing Q1 results and Q2 planning")

	t.Run("basic search", func(t *testing.T) {
		results, err := engine.Search(ctx, "financial report", 10, 0)
		testutil.AssertNoError(t, err, "search")
		if len(results) == 0 {
			t.Fatal("expected at least 1 result")
		}
	})

	t.Run("snippet returned", func(t *testing.T) {
		results, err := engine.Search(ctx, "revenue", 10, 0)
		testutil.AssertNoError(t, err, "search")
		if len(results) > 0 && results[0].Snippet == "" {
			t.Fatal("expected non-empty snippet")
		}
	})

	t.Run("no match", func(t *testing.T) {
		results, err := engine.Search(ctx, "nonexistentxyz", 10, 0)
		testutil.AssertNoError(t, err, "search")
		testutil.AssertEqual(t, len(results), 0, "empty")
	})

	t.Run("empty query", func(t *testing.T) {
		results, err := engine.Search(ctx, "", 10, 0)
		testutil.AssertNoError(t, err, "empty query")
		testutil.AssertEqual(t, len(results), 0, "empty")
	})

	t.Run("limit", func(t *testing.T) {
		results, err := engine.Search(ctx, "Q1", 1, 0)
		testutil.AssertNoError(t, err, "limited search")
		if len(results) > 1 {
			t.Fatalf("expected at most 1, got %d", len(results))
		}
	})

	t.Run("rank is non-zero for matches", func(t *testing.T) {
		results, err := engine.Search(ctx, "Q1 financial", 10, 0)
		testutil.AssertNoError(t, err, "ranked search")
		for _, r := range results {
			if r.Rank == 0 {
				t.Fatalf("expected non-zero rank, got %f", r.Rank)
			}
		}
	})
}

func TestStructuredSearch(t *testing.T) {
	engine, q := setupSearchTest(t)
	ctx := context.Background()

	insertSearchDoc(t, q, "finance.pdf", "Financial analysis and budget planning")
	insertSearchDoc(t, q, "legal.pdf", "Legal contract terms and conditions")

	t.Run("with query", func(t *testing.T) {
		results, total, err := engine.SearchStructured(ctx, Filter{
			Query: "budget planning", Limit: 10,
		})
		testutil.AssertNoError(t, err, "structured")
		if total > 0 && len(results) > 0 {
			testutil.AssertEqual(t, results[0].Title, "finance.pdf", "expected finance.pdf first")
		}
	})

	t.Run("mime_type filter", func(t *testing.T) {
		results, total, err := engine.SearchStructured(ctx, Filter{
			MimeType: "application/pdf", Limit: 10,
		})
		testutil.AssertNoError(t, err, "mime filter")
		testutil.AssertEqual(t, total, int64(2), "two pdfs")
		_ = results
	})

	t.Run("language filter", func(t *testing.T) {
		_, total, err := engine.SearchStructured(ctx, Filter{
			Language: "eng", Limit: 10,
		})
		testutil.AssertNoError(t, err, "lang filter")
		testutil.AssertEqual(t, total > 0, true, "english docs")
	})

	t.Run("pagination", func(t *testing.T) {
		results1, total, err := engine.SearchStructured(ctx, Filter{Limit: 1, Offset: 0})
		testutil.AssertNoError(t, err, "page 1")
		testutil.AssertEqual(t, len(results1), 1, "page 1 count")
		testutil.AssertEqual(t, total, int64(2), "total")

		results2, _, err := engine.SearchStructured(ctx, Filter{Limit: 1, Offset: 1})
		testutil.AssertNoError(t, err, "page 2")
		testutil.AssertEqual(t, len(results2), 1, "page 2 count")
	})

	t.Run("MissingLanguage filter", func(t *testing.T) {
		insertSearchDocRaw(t, q, "ss-und-1", "und-doc.pdf", "und", 1, "")
		insertSearchDocRaw(t, q, "ss-empty-1", "empty-lang.pdf", "", 1, "")

		results, total, err := engine.SearchStructured(ctx, Filter{
			MissingLanguage: true, Limit: 100,
		})
		testutil.AssertNoError(t, err, "search missing language")
		if total < 2 {
			t.Fatalf("expected at least 2 missing language docs, got %d", total)
		}
		titles := map[string]bool{}
		for _, r := range results {
			titles[r.Title] = true
		}
		if !titles["und-doc.pdf"] {
			t.Fatal("expected und-doc.pdf in results")
		}
		if !titles["empty-lang.pdf"] {
			t.Fatal("expected empty-lang.pdf in results")
		}
	})

	t.Run("MissingType filter", func(t *testing.T) {
		insertSearchDocRaw(t, q, "ss-ut-1", "typed-doc.pdf", "eng", 1, "")

		results, total, err := engine.SearchStructured(ctx, Filter{
			MissingType: true, Limit: 100,
		})
		testutil.AssertNoError(t, err, "search missing type")
		if total < 1 {
			t.Fatalf("expected at least 1 missing type doc, got %d", total)
		}
		titles := map[string]bool{}
		for _, r := range results {
			titles[r.Title] = true
		}
		if !titles["typed-doc.pdf"] {
			t.Fatal("expected typed-doc.pdf in results")
		}
	})

	t.Run("Untagged filter", func(t *testing.T) {
		insertSearchDocRaw(t, q, "ss-utg-1", "untagged.pdf", "eng", 3, "")
		taggedID := insertSearchDocRaw(t, q, "ss-utg-2", "tagged.pdf", "eng", 3, "")

		// Tag the tagged doc
		res, err := q.CreateTag(ctx, "ss-test-tag")
		testutil.AssertNoError(t, err, "create tag")
		tagID := getID(t, res)

		err = q.AddDocumentTag(ctx, database.AddDocumentTagParams{DocumentID: taggedID, TagID: tagID})
		testutil.AssertNoError(t, err, "tag document")

		results, total, err := engine.SearchStructured(ctx, Filter{
			Untagged: true, Limit: 100,
		})
		testutil.AssertNoError(t, err, "search untagged")
		if total < 2 {
			t.Fatalf("expected at least 2 untagged docs, got %d", total)
		}
		titles := map[string]bool{}
		for _, r := range results {
			titles[r.Title] = true
		}
		if !titles["untagged.pdf"] {
			t.Fatal("expected untagged.pdf in results")
		}
		if titles["tagged.pdf"] {
			t.Fatal("tagged.pdf should be excluded")
		}
	})

	t.Run("MissingLanguage+Untagged combined", func(t *testing.T) {
		insertSearchDocRaw(t, q, "ss-combo-1", "both-missing.pdf", "und", 1, "")

		results, total, err := engine.SearchStructured(ctx, Filter{
			MissingLanguage: true, Untagged: true, Limit: 100,
		})
		testutil.AssertNoError(t, err, "search combined")
		if total < 1 {
			t.Fatalf("expected at least 1 combined match, got %d", total)
		}
		titles := map[string]bool{}
		for _, r := range results {
			titles[r.Title] = true
		}
		if !titles["both-missing.pdf"] {
			t.Fatal("expected both-missing.pdf in results")
		}
	})
}

func TestSanitizeQuery(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"hello", `"hello"`},
		{"hello world", `"hello world"`},
		{`quo"te`, `"quo""te"`},
	}
	for _, c := range cases {
		got := sanitizeQuery(c.in)
		if got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNewEngine(t *testing.T) {
	client := database.NewTestClient(t)
	engine := NewEngine(testutil.NewTestLogger(), client.Queries)
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
}
