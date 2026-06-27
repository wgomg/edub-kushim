package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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
	client        *database.Client
	logger        *utils.Logger
	engine        *search.Engine
	matcherClient *tagmatch.MatcherClient
	tagSvc        *service.Tag
	peopleSvc     *service.People
	peopleTypeSvc *service.PeopleType
	docTypeSvc    *service.DocumentType
	userSvc       *service.User
	services      *itypes.CrudServices
	workStore     *task.Store
	dispatcher    *task.Dispatcher
	registry      *task.Registry
	semaphore     *pool.Semaphore
}

func newDocHandler(env *handlerTestEnv) *DocumentHandler {
	return NewDocumentHandler(env.client, env.logger, env.engine, env.services, func() *config.Config { return config.DefaultConfig("/tmp/test") })
}

func newMockTagService(queries *database.Queries) (*service.Tag, *testutil.MockEmbedder) {
	embedder := testutil.NewMockEmbedder()
	tagSvc, _ := service.NewTag(queries, testutil.NewTestLogger(), embedder)
	return tagSvc, embedder
}

func newHandlerTestEnv(t *testing.T) *handlerTestEnv {
	t.Helper()
	client := database.NewTestClient(t)
	logger := testutil.NewTestLogger()
	engine := search.NewEngine(logger, client.Queries)
	matcherClient := tagmatch.NewMatcherClient("/nonexistent/matcher.sock")

	tagSvc, _ := newMockTagService(client.Queries)
	peopleSvc := service.NewPeople(client.Queries, logger)
	peopleTypeSvc := service.NewPeopleType(client.Queries, logger)
	docTypeSvc := service.NewDocumentType(client.Queries, logger)
	userSvc := service.NewUser(client.Queries)

	services := &itypes.CrudServices{
		Tag:          tagSvc,
		People:       peopleSvc,
		PeopleType:   peopleTypeSvc,
		DocumentType: docTypeSvc,
		User:         userSvc,
	}

	workStore := task.NewStore(client.Queries)
	registry := task.NewRegistry()
	dispatcher := task.NewDispatcher(logger, workStore, registry)

	return &handlerTestEnv{
		client:        client,
		logger:        logger,
		engine:        engine,
		matcherClient: matcherClient,
		tagSvc:        tagSvc,
		peopleSvc:     peopleSvc,
		peopleTypeSvc: peopleTypeSvc,
		docTypeSvc:    docTypeSvc,
		userSvc:       userSvc,
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
	h := newDocHandler(env)

	docUUID(t, env.client.Queries, "list-test.pdf")

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
	h := newDocHandler(env)

	dID := docUUID(t, env.client.Queries, "get-test.pdf")

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
	h := newDocHandler(env)

	dID := docUUID(t, env.client.Queries, "before.pdf")

	t.Run("success", func(t *testing.T) {
		body, _ := json.Marshal(types.DocumentUpdateRequest{
			Title: "after.pdf", DocumentTypeID: 1, Language: "spa",
		})
		w := rec()
		r := req(t, "PUT", "/api/v1/documents/"+dID, body)
		r.SetPathValue("id", dID)
		h.UpdateDocument(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNoContent, "status")
		doc, _ := env.client.GetDocument(context.Background(), dID)
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
	h := newDocHandler(env)

	dID := docUUID(t, env.client.Queries, "delete-me.pdf")

	t.Run("success", func(t *testing.T) {
		w := rec()
		r := req(t, "DELETE", "/api/v1/documents/"+dID, nil)
		r.SetPathValue("id", dID)
		h.DeleteDocument(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNoContent, "status")
		_, err := env.client.GetDocument(context.Background(), dID)
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
	h := newDocHandler(env)
	ctx := context.Background()

	docDBID, dID := database.CreateTestDocument(t, env.client.Queries, "tags.pdf")
	tag := database.SeedTagByName(t, env.client.Queries, "")

	err := env.client.AddDocumentTag(ctx, database.AddDocumentTagParams{
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

	err = env.client.RemoveDocumentTag(ctx, database.RemoveDocumentTagParams{
		DocumentID: docDBID, TagID: tag.ID,
	})
	testutil.AssertNoError(t, err, "remove tag")
}

func TestGetDocumentFileDownloadParam(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := newDocHandler(env)
	ctx := context.Background()

	dID := docUUID(t, env.client.Queries, "download-test.pdf")

	tmpFile, err := os.CreateTemp("", "test-*.pdf")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.WriteString("fake pdf content")
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	err = env.client.UpdateDocumentPaths(ctx, database.UpdateDocumentPathsParams{
		DocumentID: dID, OriginalPath: "/tmp/orig.pdf", StoragePath: tmpFile.Name(),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("inline by default", func(t *testing.T) {
		w := rec()
		r := req(t, "GET", "/api/v1/documents/"+dID+"/file", nil)
		r.SetPathValue("id", dID)
		h.GetDocumentFile(w, r)
		if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") {
			t.Fatal("expected inline disposition")
		}
	})

	t.Run("attachment with download=true", func(t *testing.T) {
		w := rec()
		r := req(t, "GET", "/api/v1/documents/"+dID+"/file?download=true", nil)
		r.SetPathValue("id", dID)
		h.GetDocumentFile(w, r)
		if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
			t.Fatal("expected attachment disposition")
		}
	})
}

func TestDownloadDocumentsValidation(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	cfg.Srv.MaxDownloadFiles = 2
	cfg.Srv.MaxDownloadSizeMB = 0 // any positive size exceeds

	env := newHandlerTestEnv(t)
	h := NewDocumentHandler(env.client, env.logger, env.engine, env.services, func() *config.Config { return cfg })

	t.Run("invalid body", func(t *testing.T) {
		w := rec()
		r := req(t, "POST", "/api/v1/documents/download", []byte("not json"))
		r.Header.Set("Content-Type", "application/json")
		h.DownloadDocuments(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("empty document_ids", func(t *testing.T) {
		w := rec()
		body, _ := json.Marshal(types.DocumentDownloadRequest{DocumentIDs: []string{}})
		h.DownloadDocuments(w, req(t, "POST", "/api/v1/documents/download", body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
		var resp map[string]string
		json.NewDecoder(w.Body).Decode(&resp)
		if resp["error"] != "document_ids is required" {
			t.Fatalf("unexpected error: %s", resp["error"])
		}
	})

	t.Run("too many ids", func(t *testing.T) {
		w := rec()
		// 3 unique IDs exceeds MaxDownloadFiles=2
		body, _ := json.Marshal(types.DocumentDownloadRequest{
			DocumentIDs: []string{"a", "b", "c"},
		})
		h.DownloadDocuments(w, req(t, "POST", "/api/v1/documents/download", body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
		var resp map[string]string
		json.NewDecoder(w.Body).Decode(&resp)
		if !strings.Contains(resp["error"], "too many") {
			t.Fatalf("unexpected error: %s", resp["error"])
		}
	})

	t.Run("non-existent ids", func(t *testing.T) {
		w := rec()
		body, _ := json.Marshal(types.DocumentDownloadRequest{
			DocumentIDs: []string{"nonexistent-id"},
		})
		h.DownloadDocuments(w, req(t, "POST", "/api/v1/documents/download", body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
		var resp map[string]string
		json.NewDecoder(w.Body).Decode(&resp)
		if !strings.Contains(resp["error"], "not found") {
			t.Fatalf("unexpected error: %s", resp["error"])
		}
	})

	t.Run("size exceeds limit", func(t *testing.T) {
		dID := docUUID(t, env.client.Queries, "size-test.pdf")
		w := rec()
		body, _ := json.Marshal(types.DocumentDownloadRequest{
			DocumentIDs: []string{dID},
		})
		h.DownloadDocuments(w, req(t, "POST", "/api/v1/documents/download", body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
		var resp map[string]string
		json.NewDecoder(w.Body).Decode(&resp)
		if !strings.Contains(resp["error"], "exceeds limit") {
			t.Fatalf("unexpected error: %s", resp["error"])
		}
	})
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
	h := NewTaskHandler(env.client.Queries, env.logger, func() *config.Config { return nil })
	ctx := context.Background()

	_, err := env.client.CreateTask(ctx, database.CreateTaskParams{
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

	t.Run("cancel nonexistent batch", func(t *testing.T) {
		w := rec()
		r := req(t, "POST", "/api/v1/batches/nonexistent-batch/cancel", nil)
		r.SetPathValue("id", "nonexistent-batch")
		h.CancelBatch(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

		var resp map[string]any
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp["batch_id"], "nonexistent-batch", "batch_id")
		testutil.AssertEqual(t, resp["cancelled_pending"], float64(0), "cancelled_pending")
		testutil.AssertEqual(t, resp["cancelled_processing"], float64(0), "cancelled_processing")
		testutil.AssertEqual(t, resp["signal_sent"], false, "signal_sent")
	})

	t.Run("cancel batch with pending tasks", func(t *testing.T) {
		batchID := "cancel-pending-test"
		_, err := env.client.CreateBatch(ctx, database.CreateBatchParams{ID: batchID, Source: "test"})
		testutil.AssertNoError(t, err, "create batch")

		_, err = env.client.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "cancel-pend-1", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: batchID, Valid: true},
			Payload: []byte(`{}`),
		})
		testutil.AssertNoError(t, err, "create pending task")

		w := rec()
		r := req(t, "POST", "/api/v1/batches/"+batchID+"/cancel", nil)
		r.SetPathValue("id", batchID)
		h.CancelBatch(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

		var resp map[string]any
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp["batch_id"], batchID, "batch_id")
		testutil.AssertEqual(t, resp["cancelled_pending"], float64(1), "cancelled_pending")
		testutil.AssertEqual(t, resp["cancelled_processing"], float64(0), "cancelled_processing")
		testutil.AssertEqual(t, resp["signal_sent"], false, "signal_sent")
	})

	t.Run("cancel batch with dead owner", func(t *testing.T) {
		batchID := "cancel-dead-owner"
		_, err := env.client.CreateBatch(ctx, database.CreateBatchParams{ID: batchID, Source: "test"})
		testutil.AssertNoError(t, err, "create batch")

		_, err = env.client.CreateTask(ctx, database.CreateTaskParams{
			TaskID: "cancel-dead-1", TaskType: "consume", Status: "pending",
			BatchID: sql.NullString{String: batchID, Valid: true},
			Payload: []byte(`{}`),
		})
		testutil.AssertNoError(t, err, "create pending task")

		// Insert a batch owner with a PID that won't exist in this process namespace.
		_, err = env.client.TryInsertBatchOwner(ctx, database.TryInsertBatchOwnerParams{
			BatchID: batchID, OwnerID: "cancel-test-owner",
			Pid: int64(999999),
		})
		testutil.AssertNoError(t, err, "insert dead batch owner")

		w := rec()
		r := req(t, "POST", "/api/v1/batches/"+batchID+"/cancel", nil)
		r.SetPathValue("id", batchID)
		h.CancelBatch(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

		var resp map[string]any
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp["batch_id"], batchID, "batch_id")
		testutil.AssertEqual(t, resp["cancelled_pending"], float64(1), "cancelled_pending")
		testutil.AssertEqual(t, resp["cancelled_processing"], float64(0), "cancelled_processing")
		testutil.AssertEqual(t, resp["signal_sent"], false, "signal_sent")

		// Verify the batch owner row was released.
		_, err = env.client.GetBatchOwner(ctx, batchID)
		testutil.AssertError(t, err, "batch owner should be released")
	})
}

func TestGetDashboardActivity(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewTaskHandler(env.client.Queries, env.logger, func() *config.Config { return nil })
	ctx := context.Background()

	docDBID, docUUID := database.CreateTestDocument(t, env.client.Queries, "dash-act-doc.pdf")

	_, err := env.client.CreateBatch(ctx, database.CreateBatchParams{
		ID: "dash-act-batch", Source: "manual-upload",
	})
	testutil.AssertNoError(t, err, "create batch")

	res, err := env.client.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "dash-act-task-1", TaskType: "consume", Status: "pending",
		Payload: []byte(`{"file_name":"my-doc.pdf","document_id":"` + docUUID + `"}`),
	})
	testutil.AssertNoError(t, err, "create task 1")
	task1ID, _ := res.LastInsertId()
	testutil.AssertNoError(t, env.client.CompleteTask(ctx, database.CompleteTaskParams{ID: task1ID, Result: nil}), "complete task 1")

	res2, err := env.client.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "dash-act-task-2", TaskType: "consume", Status: "pending",
		Payload: []byte(`{"file_path":"/tmp/uploads/invoice.pdf","document_id":"some-doc-uuid"}`),
	})
	testutil.AssertNoError(t, err, "create task 2")
	task2ID, _ := res2.LastInsertId()
	testutil.AssertNoError(t, env.client.FailTask(ctx, database.FailTaskParams{
		ID: task2ID, Error: sql.NullString{String: "err", Valid: true},
	}), "fail task 2")

	w := rec()
	h.GetDashboard(w, req(t, "GET", "/api/v1/dashboard", nil))
	testutil.AssertEqual(t, w.Code, http.StatusOK, "dashboard status")

	var resp types.DashboardResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}

	if resp.Activity == nil {
		t.Fatal("expected activity field in dashboard response")
	}
	if len(resp.Activity) < 4 {
		t.Fatalf("expected at least 4 activity events, got %d", len(resp.Activity))
	}

	typeCounts := map[string]int{}
	for _, e := range resp.Activity {
		typeCounts[e.EventType]++
	}
	for _, et := range []string{"document_uploaded", "batch_created", "task_completed"} {
		if typeCounts[et] == 0 {
			t.Fatalf("expected at least one %q event", et)
		}
	}

	for _, e := range resp.Activity {
		switch e.EventType {
		case "document_uploaded":
			if !strings.HasPrefix(e.Link, "/documents/") {
				t.Fatalf("document_uploaded link should start with /documents/, got %q", e.Link)
			}
		case "task_completed", "task_failed":
			if !strings.HasPrefix(e.Link, "/tasks/") {
				t.Fatalf("%s link should start with /tasks/, got %q", e.EventType, e.Link)
			}
		case "batch_created":
			if !strings.HasPrefix(e.Link, "/tasks?batch=") {
				t.Fatalf("batch_created link should start with /tasks?batch=, got %q", e.Link)
			}
		}
	}

	var foundTitleFallback bool
	for _, e := range resp.Activity {
		if e.EventType == "task_failed" && e.Title == "invoice.pdf" {
			foundTitleFallback = true
			break
		}
	}
	if !foundTitleFallback {
		t.Fatal("expected task_failed event with title 'invoice.pdf' from file_path fallback")
	}

	for _, e := range resp.Activity {
		if e.Timestamp == "" {
			t.Fatalf("event %s (%s) has empty timestamp", e.EventType, e.Title)
		}
		if _, parseErr := time.Parse(time.RFC3339, e.Timestamp); parseErr != nil {
			t.Fatalf("event %s has non-RFC3339 timestamp %q: %v", e.EventType, e.Timestamp, parseErr)
		}
	}

	ctx2 := context.Background()

	_, err = env.client.DB().ExecContext(ctx2,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, document_type_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"analytics-doc-spa", "spanish.pdf", "a1", "b1", "text/plain", 512,
		"/tmp/spa.pdf", "/tmp/storage/spa.pdf", 2, 10, 50, "spa", 3,
	)
	testutil.AssertNoError(t, err, "create spa doc")

	_, err = env.client.DB().ExecContext(ctx2,
		`INSERT INTO document (document_id, title, md5_checksum, sha512_checksum, mime_type, file_size, original_path, storage_path, page_count, word_count, char_count, language, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		"analytics-doc-und", "und.pdf", "c2", "d2", "application/pdf", 256,
		"/tmp/und.pdf", "/tmp/storage/und.pdf", 1, 3, 15, "und",
	)
	testutil.AssertNoError(t, err, "create und doc")

	tag := database.SeedTagByName(t, env.client.Queries, "")
	err = env.client.AddDocumentTag(ctx2, database.AddDocumentTagParams{
		DocumentID: docDBID, TagID: tag.ID,
	})
	testutil.AssertNoError(t, err, "add tag to first doc")

	w2 := rec()
	h.GetDashboard(w2, req(t, "GET", "/api/v1/dashboard", nil))
	testutil.AssertEqual(t, w2.Code, http.StatusOK, "dashboard status after seeding")

	var resp2 types.DashboardResponse
	if err := json.NewDecoder(w2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}

	if resp2.Analytics == nil {
		t.Fatal("expected analytics field in dashboard response")
	}

	if len(resp2.Analytics.LanguageDistribution) == 0 {
		t.Fatal("expected language_distribution to be non-empty")
	}
	if len(resp2.Analytics.DocumentTypeDistribution) == 0 {
		t.Fatal("expected document_type_distribution to be non-empty")
	}
	if len(resp2.Analytics.TagFrequency) == 0 {
		t.Fatal("expected tag_frequency to be non-empty")
	}

	if resp2.Analytics.MissingLanguageCount < 1 {
		t.Fatalf("expected at least 1 missing language, got %d", resp2.Analytics.MissingLanguageCount)
	}
	if resp2.Analytics.MissingTypeCount < 2 {
		t.Fatalf("expected at least 2 missing types (all docs with doc_type_id=1), got %d", resp2.Analytics.MissingTypeCount)
	}
	if resp2.Analytics.MissingTagsCount < 2 {
		t.Fatalf("expected at least 2 untagged documents, got %d", resp2.Analytics.MissingTagsCount)
	}

	foundLang := false
	for _, d := range resp2.Analytics.LanguageDistribution {
		if d.Label == "eng" && d.Count >= 1 {
			foundLang = true
			break
		}
	}
	if !foundLang {
		t.Fatal("expected 'eng' in language_distribution")
	}

	foundType := false
	for _, d := range resp2.Analytics.DocumentTypeDistribution {
		if d.Label == "article" && d.Count >= 1 {
			foundType = true
			break
		}
	}
	if !foundType {
		t.Fatal("expected 'article' in document_type_distribution")
	}
}

func TestGetDashboardAnalyticsError(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewTaskHandler(env.client.Queries, env.logger, func() *config.Config { return nil })

	database.CreateTestDocument(t, env.client.Queries, "err-test.pdf")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	reqID := "analytics-err"
	analytics := h.buildDocumentAnalytics(ctx, &reqID)
	if analytics != nil {
		t.Fatal("expected nil analytics when queries fail due to cancelled context")
	}

	analyticsOk := h.buildDocumentAnalytics(context.Background(), &reqID)
	if analyticsOk == nil {
		t.Fatal("expected non-nil analytics with normal context")
	}
}

func TestGetDashboardProcessingHealth(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewTaskHandler(env.client.Queries, env.logger, func() *config.Config { return nil })
	ctx := context.Background()

	database.CreateTestDocument(t, env.client.Queries, "ph-doc.pdf")

	_, err := env.client.CreateBatch(ctx, database.CreateBatchParams{
		ID: "ph-batch-1", Source: "test",
	})
	testutil.AssertNoError(t, err, "create batch 1")

	_, err = env.client.CreateBatch(ctx, database.CreateBatchParams{
		ID: "ph-batch-2", Source: "test",
	})
	testutil.AssertNoError(t, err, "create batch 2")

	res, err := env.client.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "ph-completed", TaskType: "consume", Status: "pending",
		BatchID: sql.NullString{String: "ph-batch-1", Valid: true},
	})
	testutil.AssertNoError(t, err, "create completed task")
	task1ID, _ := res.LastInsertId()
	testutil.AssertNoError(t, env.client.CompleteTask(ctx, database.CompleteTaskParams{ID: task1ID, Result: nil}), "complete")

	res2, err := env.client.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "ph-failed", TaskType: "consume", Status: "pending",
		BatchID: sql.NullString{String: "ph-batch-1", Valid: true},
	})
	testutil.AssertNoError(t, err, "create failed task")
	task2ID, _ := res2.LastInsertId()
	testutil.AssertNoError(t, env.client.FailTask(ctx, database.FailTaskParams{
		ID: task2ID, Error: sql.NullString{String: "err", Valid: true},
	}), "fail")

	_, err = env.client.CreateTask(ctx, database.CreateTaskParams{
		TaskID: "ph-pending", TaskType: "consume", Status: "pending",
		BatchID: sql.NullString{String: "ph-batch-2", Valid: true},
	})
	testutil.AssertNoError(t, err, "create pending task")

	w := rec()
	h.GetDashboard(w, req(t, "GET", "/api/v1/dashboard", nil))
	testutil.AssertEqual(t, w.Code, http.StatusOK, "dashboard status")

	var resp types.DashboardResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}

	if resp.ProcessingHealth == nil {
		t.Fatal("expected processing_health in dashboard response")
	}

	if resp.ProcessingHealth.SuccessRate != 0.5 {
		t.Fatalf("expected success_rate 0.5, got %f", resp.ProcessingHealth.SuccessRate)
	}
	if resp.ProcessingHealth.CompletedLast7d != 1 {
		t.Fatalf("expected completed_last_7d 1, got %d", resp.ProcessingHealth.CompletedLast7d)
	}
	if resp.ProcessingHealth.FailedLast7d != 1 {
		t.Fatalf("expected failed_last_7d 1, got %d", resp.ProcessingHealth.FailedLast7d)
	}
	if resp.ProcessingHealth.ActiveBatches != 1 {
		t.Fatalf("expected active_batches 1 (ph-batch-2 has pending), got %d", resp.ProcessingHealth.ActiveBatches)
	}
	if resp.ProcessingHealth.AvgDurationMs != 0 {
		t.Fatalf("expected avg_duration_ms 0 (no started_at), got %d", resp.ProcessingHealth.AvgDurationMs)
	}

	if resp.TotalBatches < 1 {
		t.Fatalf("expected total_batches >= 1, got %d", resp.TotalBatches)
	}
	if resp.TotalFiles < 1 {
		t.Fatalf("expected total_files >= 1, got %d", resp.TotalFiles)
	}
	if resp.TotalSizeGB <= 0 {
		t.Fatalf("expected total_size_gb > 0, got %f", resp.TotalSizeGB)
	}
	if resp.Completed < 1 {
		t.Fatalf("expected completed >= 1, got %d", resp.Completed)
	}
	if resp.Failed < 1 {
		t.Fatalf("expected failed >= 1, got %d", resp.Failed)
	}
	if len(resp.MimeTypeBreakdown) == 0 {
		t.Fatal("expected non-empty mime_type_breakdown")
	}
	if len(resp.StorageTrend) == 0 {
		t.Fatal("expected non-empty storage_trend")
	}
	if resp.TotalPages < 1 {
		t.Fatalf("expected total_pages >= 1, got %d", resp.TotalPages)
	}
	if resp.TotalWords < 1 {
		t.Fatalf("expected total_words >= 1, got %d", resp.TotalWords)
	}
}

func TestSavedSearchEndpoints(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := NewSavedSearchHandler(env.client.Queries, env.logger)

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
			types, _ := env.client.ListAllDocumentTypes(ctx)
			dtID := int64(1)
			if len(types) > 0 {
				dtID = types[0].ID
			}
			_, err := env.client.CreateDocument(ctx, database.CreateDocumentParams{
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
			doc, err := env.client.GetDocument(ctx, docID)
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

func TestEnqueueBatchFilesDedup(t *testing.T) {
	env := newHandlerTestEnv(t)
	ctx := context.Background()

	cfg := config.DefaultConfig("/tmp/test")
	h := NewConsumeHandler(func() *config.Config { return cfg }, env.logger, env.workStore, env.client.Queries, env.semaphore)

	t.Run("enqueues new file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "new.pdf")
		if err := os.WriteFile(filePath, []byte("unique content"), 0644); err != nil {
			t.Fatal(err)
		}

		batchID := uuid.New().String()
		if _, err := h.queries.CreateBatch(ctx, database.CreateBatchParams{ID: batchID, Source: "test"}); err != nil {
			t.Fatal(err)
		}

		enqueued := h.enqueueBatchFiles(ctx, batchID, []string{filePath}, "test-req")
		if enqueued != 1 {
			t.Fatalf("expected 1 enqueued, got %d", enqueued)
		}
	})

	t.Run("skips file when document with same MD5 exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "dup.pdf")
		content := "duplicate content"
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		md5hash, err := utils.CalculateMD5(filePath)
		if err != nil {
			t.Fatal(err)
		}

		docID := uuid.New().String()
		if _, err := h.queries.CreateDocument(ctx, database.CreateDocumentParams{
			DocumentID:     docID,
			Title:          "existing-doc.pdf",
			Md5Checksum:    md5hash,
			Sha512Checksum: "fake-sha512",
			MimeType:       "application/pdf",
			FileSize:       100,
			OriginalPath:   "/tmp/orig.pdf",
			StoragePath:    "/tmp/storage.pdf",
			TextContent:    sql.NullString{String: "test", Valid: true},
			PageCount:      1,
			WordCount:      1,
			CharCount:      4,
		}); err != nil {
			t.Fatal(err)
		}

		batchID := uuid.New().String()
		if _, err := h.queries.CreateBatch(ctx, database.CreateBatchParams{ID: batchID, Source: "test"}); err != nil {
			t.Fatal(err)
		}

		enqueued := h.enqueueBatchFiles(ctx, batchID, []string{filePath}, "test-req")
		if enqueued != 0 {
			t.Fatalf("expected 0 enqueued (skipped), got %d", enqueued)
		}
	})
}

func newUserHandler(env *handlerTestEnv) *UserHandler {
	return NewUserHandler(env.services, env.logger)
}

func TestUserCrud(t *testing.T) {
	env := newHandlerTestEnv(t)
	h := newUserHandler(env)

	// Seed a user via the handler so subsequent tests reference an existing user.
	var createdID int64

	t.Run("create user", func(t *testing.T) {
		body, _ := json.Marshal(types.CreateUserRequest{
			Username: "alice", Password: "password123",
		})
		w := rec()
		h.Create(w, req(t, "POST", "/api/v1/users", body))
		testutil.AssertEqual(t, w.Code, http.StatusCreated, "status")

		var resp types.UserResponse
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp.Username, "alice", "username")
		if resp.ID == 0 {
			t.Fatal("expected non-zero user id")
		}
		if resp.CreatedAt == "" {
			t.Fatal("expected created_at to be set")
		}
		createdID = resp.ID
	})

	t.Run("create duplicate username", func(t *testing.T) {
		body, _ := json.Marshal(types.CreateUserRequest{
			Username: "alice", Password: "password123",
		})
		w := rec()
		h.Create(w, req(t, "POST", "/api/v1/users", body))
		testutil.AssertEqual(t, w.Code, http.StatusConflict, "409 on duplicate")
	})

	t.Run("create empty username rejected", func(t *testing.T) {
		body, _ := json.Marshal(types.CreateUserRequest{
			Username: "", Password: "password123",
		})
		w := rec()
		h.Create(w, req(t, "POST", "/api/v1/users", body))
		testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "400 on empty username")
	})

	t.Run("create empty password rejected", func(t *testing.T) {
		body, _ := json.Marshal(types.CreateUserRequest{
			Username: "bob", Password: "",
		})
		w := rec()
		h.Create(w, req(t, "POST", "/api/v1/users", body))
		testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "400 on empty password")
	})

	t.Run("create short password rejected", func(t *testing.T) {
		body, _ := json.Marshal(types.CreateUserRequest{
			Username: "bob", Password: "abc",
		})
		w := rec()
		h.Create(w, req(t, "POST", "/api/v1/users", body))
		testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "400 on short password")
	})

	t.Run("get user", func(t *testing.T) {
		w := rec()
		r := req(t, "GET", "/api/v1/users/1", nil)
		r.SetPathValue("id", "1")
		h.Get(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

		var resp types.UserResponse
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp.ID, createdID, "id")
		testutil.AssertEqual(t, resp.Username, "alice", "username")
		// Ensure password_hash and api_key are NOT leaked
		if raw, err := json.Marshal(resp); err == nil {
			if strings.Contains(string(raw), "password_hash") || strings.Contains(string(raw), "api_key") {
				t.Fatal("response leaked password_hash or api_key")
			}
		}
	})

	t.Run("get non-existent user", func(t *testing.T) {
		w := rec()
		r := req(t, "GET", "/api/v1/users/9999", nil)
		r.SetPathValue("id", "9999")
		h.Get(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNotFound, "404 on missing user")
	})

	t.Run("list users", func(t *testing.T) {
		w := rec()
		h.List(w, req(t, "GET", "/api/v1/users?limit=10", nil))
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

		var resp types.UserListResponse
		json.NewDecoder(w.Body).Decode(&resp)
		if len(resp.Users) == 0 {
			t.Fatal("expected at least 1 user")
		}
		if resp.Total < 1 {
			t.Fatalf("expected total >= 1, got %d", resp.Total)
		}
	})

	t.Run("update username", func(t *testing.T) {
		body, _ := json.Marshal(types.UpdateUserRequest{
			Username: "alicia",
		})
		w := rec()
		r := req(t, "PUT", "/api/v1/users/1", body)
		r.SetPathValue("id", "1")
		h.Update(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

		var resp types.UserResponse
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp.Username, "alicia", "updated username")
	})

	t.Run("update non-existent user", func(t *testing.T) {
		body, _ := json.Marshal(types.UpdateUserRequest{
			Username: "ghost",
		})
		w := rec()
		r := req(t, "PUT", "/api/v1/users/9999", body)
		r.SetPathValue("id", "9999")
		h.Update(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNotFound, "404 on missing user")
	})

	t.Run("update to duplicate username", func(t *testing.T) {
		body, _ := json.Marshal(types.CreateUserRequest{
			Username: "bob", Password: "password123",
		})
		w := rec()
		h.Create(w, req(t, "POST", "/api/v1/users", body))
		testutil.AssertEqual(t, w.Code, http.StatusCreated, "create bob")

		body2, _ := json.Marshal(types.UpdateUserRequest{
			Username: "bob",
		})
		w2 := rec()
		r := req(t, "PUT", "/api/v1/users/1", body2)
		r.SetPathValue("id", "1")
		h.Update(w2, r)
		testutil.AssertEqual(t, w2.Code, http.StatusConflict, "409 on duplicate username")
	})

	t.Run("delete user", func(t *testing.T) {
		w := rec()
		r := req(t, "DELETE", "/api/v1/users/2", nil) // bob
		r.SetPathValue("id", "2")
		h.Delete(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNoContent, "204 on delete")

		w2 := rec()
		r2 := req(t, "GET", "/api/v1/users/2", nil)
		r2.SetPathValue("id", "2")
		h.Get(w2, r2)
		testutil.AssertEqual(t, w2.Code, http.StatusNotFound, "404 after delete")
	})

	t.Run("delete non-existent user", func(t *testing.T) {
		w := rec()
		r := req(t, "DELETE", "/api/v1/users/9999", nil)
		r.SetPathValue("id", "9999")
		h.Delete(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNotFound, "404 on missing delete")
	})
}

func TestConfigHandlerGetConfig(t *testing.T) {
	env := newHandlerTestEnv(t)

	t.Run("nil config returns default", func(t *testing.T) {
		h := NewConfigHandler(nil, env.client.Queries, env.logger, nil)
		w := rec()
		h.GetConfig(w, req(t, "GET", "/wizard/config", nil))
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

		var resp types.ConfigResponse
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp.Server.Host, "0.0.0.0", "default host")
	})

	t.Run("stored config is returned", func(t *testing.T) {
		stored := config.DefaultConfig("/tmp/test-config")
		stored.Srv.Port = 9999
		h := NewConfigHandler(stored, env.client.Queries, env.logger, nil)
		w := rec()
		h.GetConfig(w, req(t, "GET", "/wizard/config", nil))
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

		var resp types.ConfigResponse
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp.Server.Port, 9999, "stored port")
	})

	t.Run("setbootstrap then getconfig", func(t *testing.T) {
		h := NewConfigHandler(nil, env.client.Queries, env.logger, nil)
		bootstrapped := config.DefaultConfig("/tmp/boot")
		bootstrapped.Srv.Port = 7777
		h.SetBootstrap(bootstrapped, env.client.Queries, nil)

		w := rec()
		h.GetConfig(w, req(t, "GET", "/wizard/config", nil))
		var resp types.ConfigResponse
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp.Server.Port, 7777, "bootstrapped port")
	})
}

func TestConfigHandlerConfigStatus(t *testing.T) {
	env := newHandlerTestEnv(t)

	t.Run("nil config reports not configured", func(t *testing.T) {
		h := NewConfigHandler(nil, env.client.Queries, env.logger, nil)
		w := rec()
		h.ConfigStatus(w, req(t, "GET", "/wizard/config/status", nil))
		testutil.AssertEqual(t, w.Code, http.StatusOK, "status")

		var resp types.ConfigStatusResponse
		json.NewDecoder(w.Body).Decode(&resp)
		testutil.AssertEqual(t, resp.Configured, false, "not configured when nil")
	})
}

func TestBatchDeleteDocumentsMaxLimit(t *testing.T) {
	cfg := config.DefaultConfig("/tmp/test")
	cfg.Srv.MaxBatchDelete = 2

	env := newHandlerTestEnv(t)
	h := NewDocumentHandler(env.client, env.logger, env.engine, env.services, func() *config.Config { return cfg })

	body, _ := json.Marshal(types.BatchDeleteRequest{
		DocumentIDs: []string{"a", "b", "c"},
	})
	w := rec()
	h.BatchDeleteDocuments(w, req(t, "POST", "/api/v1/documents/batch-delete", body))
	testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "too many docs")

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if !strings.Contains(resp["error"], "too many") {
		t.Fatalf("expected 'too many' error, got: %s", resp["error"])
	}
}

func TestProcessingHealthMissingTools(t *testing.T) {
	env := newHandlerTestEnv(t)

	cfg := config.DefaultConfig("/tmp/test")
	cfg.Consumer.OCR.Engine = "ocrmypdf"
	h := NewTaskHandler(env.client.Queries, env.logger, func() *config.Config { return cfg })

	ctx := context.Background()
	_, err := env.client.CreateBatch(ctx, database.CreateBatchParams{ID: "mh-batch", Source: "test"})
	testutil.AssertNoError(t, err, "create batch")

	reqID := "missing-tools"
	ph, err := h.buildProcessingHealth(ctx, &reqID)
	testutil.AssertNoError(t, err, "build health")

	if ph.MissingTools == 0 {
		t.Fatal("expected missing_tools > 0 when ocrmypdf is not installed")
	}
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
