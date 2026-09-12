package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

// addTagMergeToEnv wires a fresh TagMerge service into the test env using the
// same nonexistent-socket MatcherClient that the rest of the env uses. The
// merge path only needs the Embedder; the suggestions path will hit the
// matcher's 503 when it tries to dial.
func addTagMergeToEnv(t *testing.T, env *handlerTestEnv) *service.TagMerge {
	t.Helper()
	embedder := testutil.NewMockEmbedder()
	svc := service.NewTagMerge(env.client, env.matcherClient, embedder, env.logger)
	env.services.TagMerge = svc
	return svc
}

func TestTagMergeHandler_Merge_HappyPath(t *testing.T) {
	env := newHandlerTestEnv(t)
	addTagMergeToEnv(t, env)
	h := NewTagHandler(env.services, env.logger)
	ctx := context.Background()

	sourceID := database.MustCreateTag(t, env.client.Queries, "handler-merge-source")
	targetID := database.MustCreateTag(t, env.client.Queries, "handler-merge-target")

	docID, _ := database.CreateTestDocument(t, env.client.Queries, "hm.pdf")
	database.MustAddDocumentTag(t, env.client.Queries, docID, sourceID)

	body, _ := json.Marshal(types.MergeTagRequest{TargetID: targetID})
	w := rec()
	r := req(t, "POST", "/api/v1/tags/"+strconv.FormatInt(sourceID, 10)+"/merge", body)
	r.SetPathValue("id", strconv.FormatInt(sourceID, 10))
	h.Merge(w, r)

	testutil.AssertEqual(t, w.Code, http.StatusOK, "status")
	var resp types.MergeTagResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	testutil.AssertEqual(t, resp.ID, targetID, "response id is the target")
	testutil.AssertEqual(t, resp.Name, "handler-merge-target", "response name is the target name")
	testutil.AssertEqual(t, resp.Reassigned, int64(1), "one document re-pointed")

	if _, err := env.client.Queries.GetTag(ctx, sourceID); err == nil {
		t.Fatal("source tag should be gone after merge")
	}
}

func TestTagMergeHandler_Merge_BadRequest(t *testing.T) {
	env := newHandlerTestEnv(t)
	addTagMergeToEnv(t, env)
	h := NewTagHandler(env.services, env.logger)

	t.Run("invalid id", func(t *testing.T) {
		w := rec()
		body, _ := json.Marshal(types.MergeTagRequest{TargetID: 1})
		r := req(t, "POST", "/api/v1/tags/not-a-number/merge", body)
		r.SetPathValue("id", "not-a-number")
		h.Merge(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "invalid id → 400")
	})

	t.Run("missing target_id", func(t *testing.T) {
		sourceID := database.MustCreateTag(t, env.client.Queries, "missing-target-source")
		w := rec()
		body, _ := json.Marshal(types.MergeTagRequest{TargetID: 0})
		r := req(t, "POST", "/api/v1/tags/"+strconv.FormatInt(sourceID, 10)+"/merge", body)
		r.SetPathValue("id", strconv.FormatInt(sourceID, 10))
		h.Merge(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "target_id=0 → 400")
	})

	t.Run("same id is invalid", func(t *testing.T) {
		id := database.MustCreateTag(t, env.client.Queries, "handler-self-merge")
		w := rec()
		body, _ := json.Marshal(types.MergeTagRequest{TargetID: id})
		r := req(t, "POST", "/api/v1/tags/"+strconv.FormatInt(id, 10)+"/merge", body)
		r.SetPathValue("id", strconv.FormatInt(id, 10))
		h.Merge(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusBadRequest, "same id → 400")
	})
}

func TestTagMergeHandler_Merge_NotFound(t *testing.T) {
	env := newHandlerTestEnv(t)
	addTagMergeToEnv(t, env)
	h := NewTagHandler(env.services, env.logger)

	t.Run("missing source", func(t *testing.T) {
		targetID := database.MustCreateTag(t, env.client.Queries, "nf-target")
		w := rec()
		body, _ := json.Marshal(types.MergeTagRequest{TargetID: targetID})
		r := req(t, "POST", "/api/v1/tags/999999999/merge", body)
		r.SetPathValue("id", "999999999")
		h.Merge(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNotFound, "missing source → 404")
	})

	t.Run("missing target", func(t *testing.T) {
		sourceID := database.MustCreateTag(t, env.client.Queries, "nf-source")
		w := rec()
		body, _ := json.Marshal(types.MergeTagRequest{TargetID: 999999999})
		r := req(t, "POST", "/api/v1/tags/"+strconv.FormatInt(sourceID, 10)+"/merge", body)
		r.SetPathValue("id", strconv.FormatInt(sourceID, 10))
		h.Merge(w, r)
		testutil.AssertEqual(t, w.Code, http.StatusNotFound, "missing target → 404")
	})
}

// TestTagMergeHandler_Suggestions_503WhenMatcherDown confirms the handler
// surfaces the matcher's unavailability as 503, matching tag CRUD's contract
// (isMatcherUnavailable → matcherUnavailableResponse). The happy-path
// suggestion content is exercised in the service test.
func TestTagMergeHandler_Suggestions_503WhenMatcherDown(t *testing.T) {
	env := newHandlerTestEnv(t)
	addTagMergeToEnv(t, env)
	h := NewTagHandler(env.services, env.logger)

	w := rec()
	r := req(t, "GET", "/api/v1/tags/merge?limit=5", nil)
	h.MergeSuggestions(w, r)
	testutil.AssertEqual(t, w.Code, http.StatusServiceUnavailable, "matcher down → 503")
}
