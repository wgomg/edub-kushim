package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
)

func isMatcherUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, matcher.ErrMatcherUnavailable)
}

func matcherUnavailableResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	json.NewEncoder(w).Encode(map[string]string{
		"error": "matcher unavailable — tag store is offline",
	})
}

type TagHandler struct {
	tag      *curation.Tag
	merges   *curation.TagMerge
	identity *curation.TagIdentity
	logger   *log.Logger
}

func NewTagHandler(tag *curation.Tag, merges *curation.TagMerge, identity *curation.TagIdentity, logger *log.Logger) *TagHandler {
	return &TagHandler{tag: tag, merges: merges, identity: identity, logger: logger}
}

func (h *TagHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	q := pb.Get("q", "")
	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 100000)

	var total int64
	var err error

	var responses []TagResponse

	if q != "" {
		result, searchErr := h.tag.SearchByNameWithDocumentCount(ctx, q, int32(limit), int32(offset))
		err = searchErr
		if err == nil {
			total, err = h.tag.CountByName(ctx, q)
		}
		if err == nil {
			responses = make([]TagResponse, len(result))
			for i, t := range result {
				responses[i] = TagResponse{ID: t.ID, Name: t.Name, DocumentCount: t.DocumentCount}
			}
		}
	} else {
		result, listErr := h.tag.ListWithDocumentCount(ctx, int32(limit), int32(offset))
		err = listErr
		if err == nil {
			total, err = h.tag.Count(ctx)
		}
		if err == nil {
			responses = make([]TagResponse, len(result))
			for i, t := range result {
				responses[i] = TagResponse{ID: t.ID, Name: t.Name, DocumentCount: t.DocumentCount}
			}
		}
	}
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list tags", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TagListResponse{Results: responses, Total: total})
}

func (h *TagHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req CreateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = stripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "Tag name is required", http.StatusBadRequest)
		return
	}

	results, err := h.tag.CreateAndUnblock(ctx, []string{req.Name})
	if err != nil {
		if isMatcherUnavailable(err) {
			matcherUnavailableResponse(w)
			return
		}
		writeServiceError(w, h.logger, &reqID, "create tag", err)
		return
	}

	switch results[0].Status {
	case service.Created:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(TagResponse{ID: results[0].Entity.ID, Name: results[0].Entity.Name})
	case service.Apply, service.Alias:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TagResponse{ID: results[0].Entity.ID, Name: results[0].Entity.Name})
	case service.Conflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"id":   results[0].Entity.ID,
			"name": results[0].Entity.Name,
		})
	default:
		http.Error(w, "Tag name is required", http.StatusBadRequest)
	}
}

func (h *TagHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req UpdateTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = stripTags(strings.TrimSpace(req.Name))
	if req.Name == "" {
		http.Error(w, "Tag name is required", http.StatusBadRequest)
		return
	}

	results, err := h.tag.Update(ctx, []curation.TagUpdatePair{{ID: id, Name: req.Name}})
	if err != nil {
		if isMatcherUnavailable(err) {
			matcherUnavailableResponse(w)
			return
		}
		writeServiceError(w, h.logger, &reqID, "update tag", err)
		return
	}

	switch results[0].Status {
	case service.Updated, service.Noop:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TagResponse{ID: results[0].Entity.ID, Name: results[0].Entity.Name})
	case service.UpdateConflict:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]any{
			"id":   results[0].Entity.ID,
			"name": results[0].Entity.Name,
		})
	case service.UpdateNotFound:
		http.Error(w, "Tag not found", http.StatusNotFound)
	default:
		http.Error(w, "Tag name is required", http.StatusBadRequest)
	}
}

func (h *TagHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	results, err := h.tag.Delete(ctx, []int64{id})
	if err != nil {
		if isMatcherUnavailable(err) {
			matcherUnavailableResponse(w)
			return
		}
		writeServiceError(w, h.logger, &reqID, "delete tag", err)
		return
	}

	switch results[0].Status {
	case service.Deleted:
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Tag not found", http.StatusNotFound)
	}
}

func (h *TagHandler) ListProvisional(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := max(pb.GetInt64("offset", 0, 0, 100000), 0)

	rows, err := h.tag.ListProvisional(ctx, int32(limit), int32(offset))
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list provisional tags", err)
		return
	}
	total, err := h.tag.CountProvisional(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "count provisional tags", err)
		return
	}

	responses := make([]ProvisionalTagResponse, len(rows))
	if len(rows) > 0 {
		names := make([]string, len(rows))
		for i, t := range rows {
			names[i] = t.Name
		}
		events, err := h.tag.LatestQualityEventsForNames(ctx, names)
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "provisional tag verdicts", err)
			return
		}
		verdicts := make(map[string]TagQualityVerdictInfo, len(events))
		for _, e := range events {
			info := TagQualityVerdictInfo{
				Pass:      e.Pass,
				Model:     e.VerdictModel.String,
				VerdictAt: e.VerdictAt,
			}
			var criteria TagQualityCriteriaView
			if len(e.Criteria) > 0 && json.Unmarshal(e.Criteria, &criteria) == nil {
				info.Criteria = &criteria
			}
			verdicts[e.TagName] = info
		}
		pending, err := h.tag.PendingIdentityCounts(ctx, names)
		if err != nil {
			writeServiceError(w, h.logger, &reqID, "provisional tag identity evidence", err)
			return
		}
		for i, t := range rows {
			responses[i] = ProvisionalTagResponse{
				ID:            t.ID,
				Name:          t.Name,
				DocumentCount: t.DocumentCount,
				// the list query coalesces created_at, so the value is always present
				CreatedAt:       t.CreatedAt.Time,
				PendingIdentity: pending[t.Name],
			}
			if v, ok := verdicts[t.Name]; ok {
				responses[i].Verdict = &v
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ProvisionalTagListResponse{Results: responses, Total: total})
}

func (h *TagHandler) PromoteTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	_, status, err := h.tag.Promote(ctx, id, "human-approve")
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "promote tag", err)
		return
	}

	switch status {
	case service.PromotePromoted:
		w.WriteHeader(http.StatusNoContent)
	case service.PromoteAlreadyPromoted:
		http.Error(w, "Tag is already promoted", http.StatusConflict)
	default:
		http.Error(w, "Tag not found", http.StatusNotFound)
	}
}

func (h *TagHandler) ReevaluateIdentity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	batchID, alreadyQueued, err := h.identity.Enqueue(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "enqueue tag identity run", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"batch_id":       batchID,
		"already_queued": alreadyQueued,
	})
}

func (h *TagHandler) Merge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req MergeTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.TargetID <= 0 {
		http.Error(w, "target_id is required", http.StatusBadRequest)
		return
	}

	result, err := h.merges.Merge(ctx, id, req.TargetID)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "merge tag", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(MergeTagResponse{
		ID:         result.TargetID,
		Name:       result.TargetName,
		Reassigned: result.Reassigned,
	})
}

func (h *TagHandler) ListDeleted(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	q := pb.Get("q", "")
	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 100000)

	rows, total, err := h.tag.ListDeleted(ctx, q, int32(limit), int32(offset))
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list deleted tags", err)
		return
	}

	responses := make([]DeletedTagResponse, len(rows))
	for i, t := range rows {
		responses[i] = DeletedTagResponse{
			Name:       t.Name,
			Kind:       t.Kind,
			TargetName: t.TargetName.String,
			CreatedAt:  t.CreatedAt,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(DeletedTagListResponse{Results: responses, Total: total})
}

func (h *TagHandler) RestoreDeleted(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		http.Error(w, "Tag name is required", http.StatusBadRequest)
		return
	}

	row, err := h.tag.RestoreDeleted(ctx, name)
	if err != nil {
		if isMatcherUnavailable(err) {
			matcherUnavailableResponse(w)
			return
		}
		writeServiceError(w, h.logger, &reqID, "restore deleted tag", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"name": row.Name, "kind": row.Kind})
}

func (h *TagHandler) MergeSuggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	limit := pb.GetInt64("limit", 20, 1, 100)
	forTag := pb.Get("for_tag", "")
	includeDismissed := pb.GetBool("include_dismissed", false)

	suggestions, err := h.merges.Suggestions(ctx, curation.SuggestionsParams{
		Limit:            int(limit),
		ForTag:           forTag,
		IncludeDismissed: includeDismissed,
	})
	if err != nil {
		if isMatcherUnavailable(err) {
			matcherUnavailableResponse(w)
			return
		}
		writeServiceError(w, h.logger, &reqID, "merge suggestions", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"results": suggestions})
}

func (h *TagHandler) DismissMergePair(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req MergePairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.merges.DismissPair(ctx, req.TagA, req.TagB); err != nil {
		writeServiceError(w, h.logger, &reqID, "dismiss merge pair", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *TagHandler) RestoreMergePair(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req MergePairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.merges.RestorePair(ctx, req.TagA, req.TagB); err != nil {
		writeServiceError(w, h.logger, &reqID, "restore merge pair", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type TagListResponse struct {
	Results []TagResponse `json:"results"`
	Total   int64         `json:"total"`
}

type DeletedTagResponse struct {
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	TargetName string    `json:"target_name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type DeletedTagListResponse struct {
	Results []DeletedTagResponse `json:"results"`
	Total   int64                `json:"total"`
}

type TagQualityCriteriaView struct {
	SubjectMatter      *bool `json:"subject_matter"`
	StandardTerm       *bool `json:"standard_term"`
	NotPersonPlaceWork *bool `json:"not_person_place_work"`
	AppropriatelyBroad *bool `json:"appropriately_broad"`
}

type TagQualityVerdictInfo struct {
	Pass      bool                    `json:"pass"`
	Criteria  *TagQualityCriteriaView `json:"criteria"`
	Model     string                  `json:"model,omitempty"`
	VerdictAt time.Time               `json:"verdict_at"`
}

type ProvisionalTagResponse struct {
	ID              int64                  `json:"id"`
	Name            string                 `json:"name"`
	DocumentCount   int64                  `json:"document_count"`
	CreatedAt       time.Time              `json:"created_at"`
	Verdict         *TagQualityVerdictInfo `json:"verdict"`
	PendingIdentity int64                  `json:"pending_identity"`
}

type ProvisionalTagListResponse struct {
	Results []ProvisionalTagResponse `json:"results"`
	Total   int64                    `json:"total"`
}

type CreateTagRequest struct {
	Name string `json:"name"`
}

type UpdateTagRequest struct {
	Name string `json:"name"`
}

type MergeTagRequest struct {
	TargetID int64 `json:"target_id"`
}

type MergeTagResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Reassigned int64  `json:"reassigned"`
}

type MergePairRequest struct {
	TagA string `json:"tag_a"`
	TagB string `json:"tag_b"`
}
