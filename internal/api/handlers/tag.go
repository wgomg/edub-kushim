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
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/types"
)

func isMatcherUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, matcher.ErrMatcherUnavailable)
}

func matcherUnavailableResponse(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "matcher unavailable — tag store is offline",
	})
}

// TagHandler is the tag API: CRUD, provisional promotion and its
// quality verdicts, deleted-tag records, merges, and the merge-review
// endpoints backed by curation.
type TagHandler struct {
	tag      *curation.Tag
	merges   *curation.TagMerge
	identity *curation.TagIdentity
	logger   *log.Logger
}

// NewTagHandler wires the tag handler to the curation services.
func NewTagHandler(tag *curation.Tag, merges *curation.TagMerge, identity *curation.TagIdentity, logger *log.Logger) *TagHandler {
	return &TagHandler{tag: tag, merges: merges, identity: identity, logger: logger}
}

// List serves the paged tag listing with a name-prefix search and
// document counts.
func (h *TagHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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
		writeServiceError(w, h.logger, reqID, "list tags", err)
		return
	}

	writeJSON(w, http.StatusOK, TagListResponse{Results: responses, Total: total})
}

// Create creates one tag from the JSON body.
func (h *TagHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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
		writeServiceError(w, h.logger, reqID, "create tag", err)
		return
	}

	switch results[0].Status {
	case types.Created:
		writeJSON(w, http.StatusCreated, TagResponse{ID: results[0].Entity.ID, Name: results[0].Entity.Name})
	case types.Apply, types.Alias:
		writeJSON(w, http.StatusOK, TagResponse{ID: results[0].Entity.ID, Name: results[0].Entity.Name})
	case types.Conflict:
		writeJSON(w, http.StatusConflict, map[string]any{
			"id":   results[0].Entity.ID,
			"name": results[0].Entity.Name,
		})
	default:
		http.Error(w, "Tag name is required", http.StatusBadRequest)
	}
}

// Update renames one tag.
func (h *TagHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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
		writeServiceError(w, h.logger, reqID, "update tag", err)
		return
	}

	switch results[0].Status {
	case types.Updated, types.Noop:
		writeJSON(w, http.StatusOK, TagResponse{ID: results[0].Entity.ID, Name: results[0].Entity.Name})
	case types.UpdateConflict:
		writeJSON(w, http.StatusConflict, map[string]any{
			"id":   results[0].Entity.ID,
			"name": results[0].Entity.Name,
		})
	case types.UpdateNotFound:
		http.Error(w, "Tag not found", http.StatusNotFound)
	default:
		http.Error(w, "Tag name is required", http.StatusBadRequest)
	}
}

// Delete removes a tag and blocks its name.
func (h *TagHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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
		writeServiceError(w, h.logger, reqID, "delete tag", err)
		return
	}

	switch results[0].Status {
	case types.Deleted:
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Tag not found", http.StatusNotFound)
	}
}

// ListProvisional serves the paged provisional tag list with quality
// verdicts, pending identity counts, and identity holds.
func (h *TagHandler) ListProvisional(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	filter, err := parseProvisionalFilter(pb)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := max(pb.GetInt64("offset", 0, 0, 100000), 0)

	rows, err := h.tag.ListProvisional(ctx, filter, int32(limit), int32(offset))
	if err != nil {
		writeServiceError(w, h.logger, reqID, "list provisional tags", err)
		return
	}
	total, err := h.tag.CountProvisional(ctx, filter)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "count provisional tags", err)
		return
	}
	pendingIdentityTotal, err := h.tag.CountPendingIdentity(ctx)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "count pending identity events", err)
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
			writeServiceError(w, h.logger, reqID, "provisional tag verdicts", err)
			return
		}
		verdicts := make(map[string]TagQualityVerdictInfo, len(events))
		for _, e := range events {
			info := TagQualityVerdictInfo{
				Pass:      e.Pass,
				Outcome:   e.Outcome.String,
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
			writeServiceError(w, h.logger, reqID, "provisional tag identity evidence", err)
			return
		}
		held, err := h.tag.IdentityHeldNames(ctx, names)
		if err != nil {
			writeServiceError(w, h.logger, reqID, "provisional tag identity holds", err)
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
				IdentityHold:    held[t.Name],
			}
			if v, ok := verdicts[t.Name]; ok {
				responses[i].Verdict = &v
			}
		}
	}

	writeJSON(w, http.StatusOK, ProvisionalTagListResponse{
		Results:              responses,
		Total:                total,
		PendingIdentityTotal: pendingIdentityTotal,
	})
}

// parseProvisionalFilter reads the criteria and verdict query parameters
// into the curation filter. Criteria entries are `key:state` pairs with
// state `pass`, `fail` or `absent`; unknown keys, unknown states and
// unknown verdict values are rejected instead of silently ignored.
func parseProvisionalFilter(pb *ParamBag) (curation.ProvisionalFilter, error) {
	var filter curation.ProvisionalFilter
	fields := map[string]*string{
		"subject_matter":        &filter.SubjectMatter,
		"dimension":             &filter.Dimension,
		"not_person_place_work": &filter.NotPersonPlaceWork,
		"atomic":                &filter.Atomic,
		"not_form_genre":        &filter.NotFormGenre,
		"defer":                 &filter.Defer,
	}
	states := map[string]string{"pass": "true", "fail": "false", "absent": "absent"}

	for _, entry := range pb.GetStrings("criteria") {
		key, state, ok := strings.Cut(entry, ":")
		dst, knownKey := fields[key]
		sqlState, knownState := states[state]
		if !ok || !knownKey || !knownState {
			return curation.ProvisionalFilter{}, errors.New("Invalid criteria filter")
		}
		*dst = sqlState
	}

	verdict := pb.Get("verdict", "")
	switch verdict {
	case "", "none", "admit", "defer", "reject":
		filter.Verdict = verdict
	default:
		return curation.ProvisionalFilter{}, errors.New("Invalid verdict filter")
	}
	return filter, nil
}

// PromoteTag promotes one provisional tag, recording the reason.
func (h *TagHandler) PromoteTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	_, status, err := h.tag.Promote(ctx, id, "human-approve")
	if err != nil {
		writeServiceError(w, h.logger, reqID, "promote tag", err)
		return
	}

	switch status {
	case types.PromotePromoted:
		w.WriteHeader(http.StatusNoContent)
	case types.PromoteAlreadyPromoted:
		http.Error(w, "Tag is already promoted", http.StatusConflict)
	default:
		http.Error(w, "Tag not found", http.StatusNotFound)
	}
}

// ReevaluateIdentity enqueues a fresh identity sweep for the vocabulary.
func (h *TagHandler) ReevaluateIdentity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	batchID, alreadyQueued, err := h.identity.Enqueue(ctx)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "enqueue tag identity run", err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"batch_id":       batchID,
		"already_queued": alreadyQueued,
	})
}

// Merge folds one tag into the target from the JSON body.
func (h *TagHandler) Merge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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
		writeServiceError(w, h.logger, reqID, "merge tag", err)
		return
	}

	writeJSON(w, http.StatusOK, MergeTagResponse{
		ID:         result.TargetID,
		Name:       result.TargetName,
		Reassigned: result.Reassigned,
	})
}

// ListDeleted serves the paged deleted-tag records.
func (h *TagHandler) ListDeleted(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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
		writeServiceError(w, h.logger, reqID, "list deleted tags", err)
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

	writeJSON(w, http.StatusOK, DeletedTagListResponse{Results: responses, Total: total})
}

// RestoreDeleted unblocks one deleted tag name.
func (h *TagHandler) RestoreDeleted(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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
		writeServiceError(w, h.logger, reqID, "restore deleted tag", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"name": row.Name, "kind": row.Kind})
}

// MergeSuggestions serves the paged merge-review candidates.
func (h *TagHandler) MergeSuggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

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
		writeServiceError(w, h.logger, reqID, "merge suggestions", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": suggestions})
}

// DismissMergePair records the decision not to merge a pair.
func (h *TagHandler) DismissMergePair(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	var req MergePairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.merges.DismissPair(ctx, req.TagA, req.TagB); err != nil {
		writeServiceError(w, h.logger, reqID, "dismiss merge pair", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RestoreMergePair makes a dismissed pair reviewable again.
func (h *TagHandler) RestoreMergePair(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	var req MergePairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.merges.RestorePair(ctx, req.TagA, req.TagB); err != nil {
		writeServiceError(w, h.logger, reqID, "restore merge pair", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// TagListResponse is one page of the tag listing.
type TagListResponse struct {
	Results []TagResponse `json:"results"`
	Total   int64         `json:"total"`
}

// DeletedTagResponse is one deleted-tag record as the API shows it:
// the name, whether it was blocked or merged (with the target), and
// when.
type DeletedTagResponse struct {
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	TargetName string    `json:"target_name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// DeletedTagListResponse is one page of deleted-tag records.
type DeletedTagListResponse struct {
	Results []DeletedTagResponse `json:"results"`
	Total   int64                `json:"total"`
}

// TagQualityCriteriaView is the API view of the judge's rubric answers;
// a nil answer had no signal in the stored payload.
type TagQualityCriteriaView struct {
	SubjectMatter      *bool `json:"subject_matter"`
	Dimension          *bool `json:"dimension"`
	NotPersonPlaceWork *bool `json:"not_person_place_work"`
	Atomic             *bool `json:"atomic"`
	NotFormGenre       *bool `json:"not_form_genre"`
	Defer              *bool `json:"defer"`
}

// TagQualityVerdictInfo is the latest recorded quality verdict for a
// provisional tag: the pass flag, the recorded outcome (absent on rows
// predating the outcome column), its criteria (nil when unjudged) and the
// model.
type TagQualityVerdictInfo struct {
	Pass      bool                    `json:"pass"`
	Outcome   string                  `json:"outcome,omitempty"`
	Criteria  *TagQualityCriteriaView `json:"criteria"`
	Model     string                  `json:"model,omitempty"`
	VerdictAt time.Time               `json:"verdict_at"`
}

// ProvisionalTagResponse is one provisional tag with its document
// count, creation time, latest quality verdict, pending identity
// events, and the store anchor of a refused fold holding it.
type ProvisionalTagResponse struct {
	ID              int64                  `json:"id"`
	Name            string                 `json:"name"`
	DocumentCount   int64                  `json:"document_count"`
	CreatedAt       time.Time              `json:"created_at"`
	Verdict         *TagQualityVerdictInfo `json:"verdict"`
	PendingIdentity int64                  `json:"pending_identity"`
	IdentityHold    string                 `json:"identity_hold,omitempty"`
}

// ProvisionalTagListResponse is one page of provisional tags: the
// filtered total for the queue's pager and the queue-level pending
// identity count that decides the reevaluation affordance.
type ProvisionalTagListResponse struct {
	Results              []ProvisionalTagResponse `json:"results"`
	Total                int64                    `json:"total"`
	PendingIdentityTotal int64                    `json:"pending_identity_total"`
}

// CreateTagRequest is the body of POST /api/v1/tags.
type CreateTagRequest struct {
	Name string `json:"name"`
}

// UpdateTagRequest is the body of a tag rename.
type UpdateTagRequest struct {
	Name string `json:"name"`
}

// MergeTagRequest is the body of a tag merge: the surviving tag's id.
type MergeTagRequest struct {
	TargetID int64 `json:"target_id"`
}

// MergeTagResponse reports the merge: the surviving tag and how many
// documents were reassigned.
type MergeTagResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Reassigned int64  `json:"reassigned"`
}

// MergePairRequest is the body of a pair-level merge or dismissal: the
// two tag names as typed.
type MergePairRequest struct {
	TagA string `json:"tag_a"`
	TagB string `json:"tag_b"`
}
