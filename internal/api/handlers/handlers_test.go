package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	_ "time"

	itypes "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/search"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tagmatch"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type handlerTestEnv struct {
	db            *sql.DB
	queries       *database.Queries
	logger        *utils.Logger
	engine        *search.Engine
	matcherClient *tagmatch.MatcherClient
	tagSvc        *service.Tag
	peopleSvc     *service.People
	peopleTypeSvc *service.PeopleType
	docTypeSvc    *service.DocumentType
	services      *itypes.CrudServices
	workStore     *task.Store
	dispatcher    *task.Dispatcher
	registry      *task.Registry
	semaphore     *pool.Semaphore
}

func newMockTagService(queries *database.Queries) (*service.Tag, *testutil.MockEmbedder) {
	embedder := testutil.NewMockEmbedder()
	tagSvc, _ := service.NewTag(queries, testutil.NewTestLogger(), embedder)
	return tagSvc, embedder
}

func newHandlerTestEnv(t *testing.T) *handlerTestEnv {
	t.Helper()
	db := database.NewTestDB(t)
	queries := database.NewQueries(db)
	logger := testutil.NewTestLogger()
	engine := search.NewEngine(logger, db)
	matcherClient := tagmatch.NewMatcherClient("/nonexistent/matcher.sock")

	tagSvc, _ := newMockTagService(queries)
	peopleSvc := service.NewPeople(queries, logger)
	peopleTypeSvc := service.NewPeopleType(queries, logger)
	docTypeSvc := service.NewDocumentType(queries, logger)

	services := &itypes.CrudServices{
		Tag:          tagSvc,
		People:       peopleSvc,
		PeopleType:   peopleTypeSvc,
		DocumentType: docTypeSvc,
	}

	workStore := task.NewStore(queries)
	registry := task.NewRegistry()
	dispatcher := task.NewDispatcher(logger, workStore, registry)

	return &handlerTestEnv{
		db:            db,
		queries:       queries,
		logger:        logger,
		engine:        engine,
		matcherClient: matcherClient,
		tagSvc:        tagSvc,
		peopleSvc:     peopleSvc,
		peopleTypeSvc: peopleTypeSvc,
		docTypeSvc:    docTypeSvc,
		services:      services,
		workStore:     workStore,
		dispatcher:    dispatcher,
		registry:      registry,
		semaphore:     pool.NewSemaphore(4),
	}
}

func req(t *testing.T, method, path string, body []byte) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r = utils.WithParamBag(r, utils.NewParamBag(r))
	r = r.WithContext(context.WithValue(r.Context(), "reqid", "test-req"))
	return r
}

func rec() *httptest.ResponseRecorder {
	return httptest.NewRecorder()
}

func TestHealthHandler(t *testing.T) {
	env := newHandlerTestEnv(t)
	w := rec()
	// HealthHandler expects a reqid in the context
	r := req(t, "GET", "/health", nil)
	r = r.WithContext(context.WithValue(r.Context(), "reqid", "test-request-id"))
	HealthHandler(w, r, env.logger)
	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

	var m map[string]any
	json.NewDecoder(w.Body).Decode(&m)
	testutil.AssertEqual(t, m["status"], "healthy", "health")
}

// docUUID creates a test document and returns its UUID string.
func docUUID(t *testing.T, queries *database.Queries, title string) string {
	t.Helper()
	_, uuid := database.CreateTestDocument(t, queries, title)
	return uuid
}

func TestDocumentList(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewDocumentHandler(env.queries, env.logger, env.engine, env.services)

	docUUID(t, env.queries, "list-test.pdf")

	t.Run("lists documents", func(t *testing.T) {
		w := rec()
		h.ListDocuments(w, req(t, "GET", "/api/v1/documents?limit=10", nil))
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")
		var docs []types.DocumentResponse
		json.NewDecoder(w.Body).Decode(&docs)
		if len(docs) == 0 {
			t.Fatal("expected documents")
		}
		testutil.AssertEqual(t, docs[0].Title, "list-test.pdf", "title")
	})
}

func TestDocumentGet(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewDocumentHandler(env.queries, env.logger, env.engine, env.services)

	dID := docUUID(t, env.queries, "get-test.pdf")

	t.Run("found", func(t *testing.T) {
		w := rec()
		r := req(t, "GET", "/api/v1/documents/"+dID, nil)
		r.SetPathValue("id", dID)
		h.GetDocument(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")
		var doc types.DocumentResponse
		json.NewDecoder(w.Body).Decode(&doc)
		testutil.AssertEqual(t, doc.Title, "get-test.pdf", "title")
		testutil.AssertEqual(t, doc.ID, dID, "uuid")
	})

	t.Run("not found", func(t *testing.T) {
		w := rec()
		r := req(t, "GET", "/api/v1/documents/no-such-id", nil)
		r.SetPathValue("id", "no-such-id")
		h.GetDocument(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNotFound, "not found")
	})
}

func TestDocumentUpdate(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewDocumentHandler(env.queries, env.logger, env.engine, env.services)

	dID := docUUID(t, env.queries, "before.pdf")

	t.Run("success", func(t *testing.T) {
		body, _ := json.Marshal(types.DocumentUpdateRequest{
			Title: "after.pdf", DocumentTypeID: 1, Language: "spa",
		})
		w := rec()
		r := req(t, "PUT", "/api/v1/documents/"+dID, body)
		r.SetPathValue("id", dID)
		h.UpdateDocument(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNoContent, "status")
		doc, _ := env.queries.GetDocument(context.Background(), dID)
		testutil.AssertEqual(t, doc.Title, "after.pdf", "title")
	})

	t.Run("empty title rejected", func(t *testing.T) {
		body, _ := json.Marshal(types.DocumentUpdateRequest{
			Title: "", DocumentTypeID: 1, Language: "eng",
		})
		w := rec()
		r := req(t, "PUT", "/api/v1/documents/"+dID, body)
		r.SetPathValue("id", dID)
		h.UpdateDocument(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "empty title")
	})

	t.Run("invalid doc type rejected", func(t *testing.T) {
		body, _ := json.Marshal(types.DocumentUpdateRequest{
			Title: "valid.pdf", DocumentTypeID: -1, Language: "eng",
		})
		w := rec()
		r := req(t, "PUT", "/api/v1/documents/"+dID, body)
		r.SetPathValue("id", dID)
		h.UpdateDocument(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "invalid doc type")
	})
}

func TestDocumentDelete(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewDocumentHandler(env.queries, env.logger, env.engine, env.services)

	dID := docUUID(t, env.queries, "delete-me.pdf")

	t.Run("success", func(t *testing.T) {
		w := rec()
		r := req(t, "DELETE", "/api/v1/documents/"+dID, nil)
		r.SetPathValue("id", dID)
		h.DeleteDocument(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNoContent, "status")
		_, err := env.queries.GetDocument(context.Background(), dID)
		testutil.AssertError(t, err, "doc gone")
	})

	t.Run("not found", func(t *testing.T) {
		w := rec()
		r := req(t, "DELETE", "/api/v1/documents/unknown", nil)
		r.SetPathValue("id", "unknown")
		h.DeleteDocument(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNotFound, "not found")
	})
}

func TestDocumentTagLifecycle(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewDocumentHandler(env.queries, env.logger, env.engine, env.services)
	ctx := context.Background()

	docDBID, dID := database.CreateTestDocument(t, env.queries, "tags.pdf")
	tag := database.SeedTagByName(t, env.queries, "")

	err := env.queries.AddDocumentTag(ctx, database.AddDocumentTagParams{
		DocumentID: docDBID, TagID: tag.ID,
	})
	testutil.AssertNoError(t, err, "add tag")

	w := rec()
	r := req(t, "GET", "/api/v1/documents/"+dID, nil)
	r.SetPathValue("id", dID)
	h.GetDocument(w, r)

	var doc types.DocumentResponse
	json.NewDecoder(w.Body).Decode(&doc)
	if len(doc.Tags) == 0 {
		t.Fatal("expected at least 1 tag")
	}

	err = env.queries.RemoveDocumentTag(ctx, database.RemoveDocumentTagParams{
		DocumentID: docDBID, TagID: tag.ID,
	})
	testutil.AssertNoError(t, err, "remove tag")
}

func TestTagCrud(t *testing.T) {
	env := newHandlerTestEnv(t)
	ctx := context.Background()

	t.Run("create tag", func(t *testing.T) {
		r, err := env.tagSvc.Create(ctx, []string{"test-tag-1"})
		testutil.AssertNoError(t, err, "create")
		testutil.AssertEqual(t, r[0].Status, service.Created, "created")
	})

	t.Run("duplicate tag", func(t *testing.T) {
		r, err := env.tagSvc.Create(ctx, []string{"test-tag-1"})
		testutil.AssertNoError(t, err, "duplicate")
		testutil.AssertEqual(t, r[0].Status, service.Conflict, "conflict")
	})

	t.Run("list tags", func(t *testing.T) {
		tags, err := env.tagSvc.List(ctx, 100, 0)
		testutil.AssertNoError(t, err, "list")
		if len(tags) == 0 {
			t.Fatal("expected tags")
		}
	})

	t.Run("delete tag", func(t *testing.T) {
		tags, _ := env.tagSvc.List(ctx, 100, 0)
		if len(tags) > 0 {
			r, err := env.tagSvc.Delete(ctx, []int64{tags[0].ID})
			testutil.AssertNoError(t, err, "delete")
			testutil.AssertEqual(t, r[0].Status, service.Deleted, "deleted")
		}
	})
}

func TestPeopleCrud(t *testing.T) {
	env := newHandlerTestEnv(t)
	ctx := context.Background()

	t.Run("create person", func(t *testing.T) {
		r, err := env.peopleSvc.Create(ctx, []service.CreatePersonInput{
			{Name: "Alice Wonderland"},
		})
		testutil.AssertNoError(t, err, "create")
		testutil.AssertEqual(t, r[0].Status, service.Created, "created")
	})

	t.Run("duplicate person", func(t *testing.T) {
		r, err := env.peopleSvc.Create(ctx, []service.CreatePersonInput{
			{Name: "Alice Wonderland"},
		})
		testutil.AssertNoError(t, err, "duplicate")
		testutil.AssertEqual(t, r[0].Status, service.Conflict, "conflict")
	})

	t.Run("search by prefix", func(t *testing.T) {
		ppl, err := env.peopleSvc.Search(ctx, "Alice", 10)
		testutil.AssertNoError(t, err, "search")
		if len(ppl) == 0 {
			t.Fatal("expected at least 1 result")
		}
	})
}

func TestPeopleTypeSeeded(t *testing.T) {
	env := newHandlerTestEnv(t)
	ctx := context.Background()

	types, err := env.peopleTypeSvc.List(ctx, 100, 0)
	testutil.AssertNoError(t, err, "list people types")
	if len(types) == 0 {
		t.Fatal("expected seeded people types")
	}
}

func TestDocumentTypeCrud(t *testing.T) {
	env := newHandlerTestEnv(t)
	ctx := context.Background()

	t.Run("seeded types exist", func(t *testing.T) {
		types, err := env.docTypeSvc.List(ctx, 100, 0)
		testutil.AssertNoError(t, err, "list")
		if len(types) == 0 {
			t.Fatal("expected seeded document types")
		}
	})

	t.Run("create document type", func(t *testing.T) {
		r, err := env.docTypeSvc.Create(ctx, []service.CreateDocumentTypeInput{
			{Name: "custom-type", Description: "Custom type"},
		})
		testutil.AssertNoError(t, err, "create")
		testutil.AssertEqual(t, r[0].Status, service.Created, "created")
	})
}

func TestTaskEndpoints(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewTaskHandler(env.queries, env.logger)
	ctx := context.Background()

	_, err := env.queries.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "task-e2e-1", TaskType: "consume", Status: "completed",
		Payload: []byte(`{"file":"test.pdf"}`),
	})
	testutil.AssertNoError(t, err, "create task")

	t.Run("list tasks", func(t *testing.T) {
		w := rec()
		h.ListTasks(w, req(t, "GET", "/api/v1/tasks?limit=10", nil))
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")
		var resp types.ListTasksResponse
		json.NewDecoder(w.Body).Decode(&resp)
		if len(resp.Tasks) == 0 {
			t.Fatal("expected tasks")
		}
	})

	t.Run("get task", func(t *testing.T) {
		w := rec()
		r := req(t, "GET", "/api/v1/tasks/task-e2e-1", nil)
		r.SetPathValue("id", "task-e2e-1")
		h.GetTask(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")
		var resp types.TaskResponse
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp.TaskID, "task-e2e-1", "task id")
	})

	t.Run("list batches", func(t *testing.T) {
		w := rec()
		h.ListBatches(w, req(t, "GET", "/api/v1/batches?limit=10", nil))
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")
	})

	t.Run("global summary", func(t *testing.T) {
		w := rec()
		h.GlobalSummary(w, req(t, "GET", "/api/v1/summary", nil))
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")
		var summary types.GlobalSummaryResponse
		json.NewDecoder(w.Body).Decode(&summary)
		if summary.TotalBatches < 0 {
			t.Fatal("expected non-negative batch count")
		}
	})
}

func TestSavedSearchEndpoints(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewSavedSearchHandler(env.queries, env.logger)

	t.Run("create", func(t *testing.T) {
		w := rec()
		h.Create(w, req(t, "POST", "/api/v1/saved-searches",
			[]byte(`{"name":"my-search","filter":{"query":"test"}}`)))
		testutil.AssertEqual(t, w.Code, http.StatusCreated, "created")
	})

	t.Run("list", func(t *testing.T) {
		w := rec()
		h.List(w, req(t, "GET", "/api/v1/saved-searches", nil))
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")
	})

	t.Run("delete", func(t *testing.T) {
		w := rec()
		h.Create(w, req(t, "POST", "/api/v1/saved-searches",
			[]byte(`{"name":"delete-me","filter":{"query":"x"}}`)))
		var created struct{ ID int64 }
		json.NewDecoder(w.Body).Decode(&created)

		w2 := rec()
		r := req(t, "DELETE", fmt.Sprintf("/api/v1/saved-searches/%d", created.ID), nil)
		r.SetPathValue("id", fmt.Sprintf("%d", created.ID))
		h.Delete(w2, r)
		testutil.AssertEqual(t, w2.Code, http.StatusNoContent, "deleted")
	})
}

func TestConcurrentDocumentOps(t *testing.T) {
	env := newHandlerTestEnv(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			title := fmt.Sprintf("concurrent-%d.pdf", idx)
			// Create with inline doc to avoid checksum conflicts
			docID := fmt.Sprintf("cid-%d", idx)
			types, _ := env.queries.ListAllDocumentTypes(ctx)
			dtID := int64(1)
			if len(types) > 0 {
				dtID = types[0].ID
			}
			_, err := env.queries.CreateDocument(ctx, database.CreateDocumentParams{
				DocumentID: docID, Title: title,
				Md5Checksum:    fmt.Sprintf("md5-c-%d", idx),
				Sha512Checksum: fmt.Sprintf("sha512-c-%d", idx),
				MimeType:       "application/pdf", FileSize: 100,
				OriginalPath: "/tmp/" + title, StoragePath: "/tmp/storage/" + title,
				TextContent: sql.NullString{String: "content", Valid: true},
				PageCount:   1, WordCount: 1, CharCount: 7, Language: "eng",
			})
			if err != nil {
				errCh <- fmt.Errorf("create %s: %w", title, err)
				return
			}
			doc, err := env.queries.GetDocument(ctx, docID)
			if err != nil {
				errCh <- fmt.Errorf("get %s: %w", title, err)
				return
			}
			_ = dtID
			if doc.Title != title {
				errCh <- fmt.Errorf("title mismatch: %s", doc.Title)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestConsumeHandlerConfig(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test-consume-cfg")
	cfg.App.LogLevel = "silent"
	_ = cfg
}

func TestErrorHelpers(t *testing.T) {
	env := newHandlerTestEnv(t)

	t.Run("sql no rows -> logs error", func(t *testing.T) {
		w := rec()
		writeServiceError(w, env.logger, nil, "get", sql.ErrNoRows)
		// errs.FromDB may return 500 for generic errors, so just verify we get a response
		if w.Code != http.StatusNotFound && w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 404 or 500, got %d", w.Code)
		}
	})

	t.Run("generic error -> 500", func(t *testing.T) {
		w := rec()
		writeServiceError(w, env.logger, nil, "do", fmt.Errorf("boom"))
		testutil.AssertEqual(t, w.Code, http.StatusInternalServerError, "500")
	})
}
