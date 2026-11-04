package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// maxCompositionParts bounds one operator split or keep: every part becomes a tag row, an embedding,
// and a component-document fan-out, so the request-side limit keeps one call proportional to a
// realistic decomposition.
const maxCompositionParts = 64

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

// TagHandler is the tag API: CRUD, provisional promotion and its quality verdicts, deleted-tag records,
// the identity reevaluation trigger, the completeness audit trigger and coverage review surface, the
// operator's fold and composition decisions, retirement, and on-demand re-judging, backed by curation.
type TagHandler struct {
	tag      *curation.Tag
	identity *curation.TagIdentity
	audit    *curation.TagAudit
	rejudge  *curation.TagRejudge
	logger   *log.Logger
}

// NewTagHandler wires the tag handler to the curation services.
func NewTagHandler(tag *curation.Tag, identity *curation.TagIdentity, audit *curation.TagAudit, rejudge *curation.TagRejudge, logger *log.Logger) *TagHandler {
	return &TagHandler{tag: tag, identity: identity, audit: audit, rejudge: rejudge, logger: logger}
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
				responses[i] = TagResponse{ID: t.ID, Name: t.Name, DocumentCount: t.DocumentCount, Promoted: t.PromotedAt.Valid, Seeded: t.Seeded, Compound: tags.ClassifyShape(t.Name) == tags.ShapeCompound, CreatedAt: tagCreatedAt(t.CreatedAt)}
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
				responses[i] = TagResponse{ID: t.ID, Name: t.Name, DocumentCount: t.DocumentCount, Promoted: t.PromotedAt.Valid, Seeded: t.Seeded, Compound: tags.ClassifyShape(t.Name) == tags.ShapeCompound, CreatedAt: tagCreatedAt(t.CreatedAt)}
			}
		}
	}
	if err != nil {
		writeServiceError(w, h.logger, reqID, "list tags", err)
		return
	}

	writeJSON(w, http.StatusOK, TagListResponse{Results: responses, Total: total})
}

// tagCreatedAt returns the recording time of a tag row, or nil when the column carries no value: the
// tag table allows a null created_at, and legacy rows must surface as absent rather than as the zero
// time.
func tagCreatedAt(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	created := v.Time
	return &created
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

// ListProvisional serves the paged provisional tag list with its quality verdicts, pending identity
// counts and identity holds, plus the review-state filter and, when requested, the optional per-state
// facet counts kept under the same name and rubric constraints.
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
	total, stateFacets, err := h.tag.CountProvisional(ctx, filter, pb.GetBool("facets", false))
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
		compositionHeld, err := h.tag.CompositionHeldNames(ctx, names)
		if err != nil {
			writeServiceError(w, h.logger, reqID, "provisional tag composition holds", err)
			return
		}
		compositionEvents, err := h.tag.LatestCompositionEventsForNames(ctx, names)
		if err != nil {
			writeServiceError(w, h.logger, reqID, "provisional tag composition events", err)
			return
		}
		compositionByCandidate := make(map[string]compositionView, len(compositionEvents))
		for _, e := range compositionEvents {
			var parts []string
			if len(e.Parts) > 0 {
				if err := json.Unmarshal(e.Parts, &parts); err != nil {
					h.logger.Warn("", "provisional tag composition parts for %q: %v", e.Candidate, err)
					parts = nil
				}
			}
			compositionByCandidate[e.Candidate] = compositionView{
				Reason: e.Reason,
				Action: tags.CompositionActionFor(tags.CompositionDisposition(e.Disposition), e.Reason),
				Parts:  parts,
			}
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
				CompositionHold: compositionHeld[t.Name],
				DemotedReason:   t.DemotedReason.String,
				Compound:        tags.ClassifyShape(t.Name) == tags.ShapeCompound,
			}
			if composition, ok := compositionByCandidate[t.Name]; ok {
				responses[i].CompositionReason = composition.Reason
				responses[i].CompositionAction = composition.Action
				responses[i].CompositionParts = composition.Parts
			}
			if t.DemotedAt.Valid {
				demotedAt := t.DemotedAt.Time
				responses[i].DemotedAt = &demotedAt
			}
			if t.AcceptedAt.Valid && t.AcceptedAt.Time.After(t.FreshVerdictAt) {
				acceptedAt := t.AcceptedAt.Time
				responses[i].AcceptedAt = &acceptedAt
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
		StateValues:          curation.ProvisionalStateValues(),
		StateFacets:          stateFacets,
	})
}

// parseProvisionalFilter reads the name, criteria, verdict, review-state and sort query parameters into
// the curation filter. The name is an ILIKE prefix, like the deleted-tag listing; the criteria entries
// are `key:state` pairs carrying `pass`, `fail` or `absent`; the review-state entries are the values
// from curation.ProvisionalStateValues, with duplicates dropped; and sort is `newest` (the default)
// or `oldest`. Unknown keys, unknown criteria states, unknown verdict values, unknown review states
// and unknown sort values are rejected instead of silently ignored.
func parseProvisionalFilter(pb *ParamBag) (curation.ProvisionalFilter, error) {
	filter := curation.ProvisionalFilter{NamePrefix: pb.Get("q", "")}
	fields := map[string]*string{
		"subject_matter": &filter.SubjectMatter,
		"dimension":      &filter.Dimension,
		"not_person":     &filter.NotPerson,
		"atomic":         &filter.Atomic,
		"not_form_genre": &filter.NotFormGenre,
		"defer":          &filter.Defer,
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

	switch pb.Get("sort", "") {
	case "", "newest":
		filter.OldestFirst = false
	case "oldest":
		filter.OldestFirst = true
	default:
		return curation.ProvisionalFilter{}, errors.New("Invalid sort filter")
	}

	known := curation.ProvisionalStateValues()
	knownStates := make(map[string]struct{}, len(known))
	for _, state := range known {
		knownStates[state] = struct{}{}
	}
	seen := make(map[string]struct{})
	for _, state := range pb.GetStrings("state") {
		if _, ok := knownStates[state]; !ok {
			return curation.ProvisionalFilter{}, errors.New("Invalid state filter")
		}
		if _, dup := seen[state]; dup {
			continue
		}
		seen[state] = struct{}{}
		filter.States = append(filter.States, state)
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

// DemoteTag withdraws a tag's standing at an operator's request.
func (h *TagHandler) DemoteTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	_, status, err := h.tag.Demote(ctx, id, tags.DemotionOperator)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "demote tag", err)
		return
	}

	switch status {
	case types.DemoteDemoted:
		w.WriteHeader(http.StatusNoContent)
	case types.DemoteSeeded:
		http.Error(w, "Seeded values cannot be demoted", http.StatusConflict)
	case types.DemoteNotPromoted:
		http.Error(w, "Tag is not promoted", http.StatusConflict)
	default:
		http.Error(w, "Tag not found", http.StatusNotFound)
	}
}

// FoldTag folds one tag name onto another at an operator's request, the manual counterpart to an identity
// fold: the alias name's documents move onto the canonical value and the alias name leaves the store.
// Seeded values cannot be folded away, since the startup seeder would re-create them.
func (h *TagHandler) FoldTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	var req FoldTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	alias := stripTags(strings.TrimSpace(req.Alias))
	canonical := stripTags(strings.TrimSpace(req.Canonical))
	if alias == "" || canonical == "" {
		http.Error(w, "Both names are required", http.StatusBadRequest)
		return
	}

	status, _, err := h.tag.Fold(ctx, alias, canonical)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "fold tag", err)
		return
	}

	switch status {
	case types.FoldApplied:
		w.WriteHeader(http.StatusNoContent)
	case types.FoldNotFound:
		http.Error(w, "Tag not found", http.StatusNotFound)
	case types.FoldSeeded:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Seeded values cannot be folded"})
	default:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "The target is gone or already an alias"})
	}
}

// CompositionDecision applies an operator's split or keep decision over one provisional compound.
func (h *TagHandler) CompositionDecision(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req CompositionDecisionRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Parts) > maxCompositionParts {
		http.Error(w, "Too many components", http.StatusBadRequest)
		return
	}

	status, err := h.tag.ApplyCompositionDecision(ctx, id, strings.TrimSpace(req.Disposition), req.Parts)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "apply composition decision", err)
		return
	}

	switch status {
	case types.CompositionApplied:
		w.WriteHeader(http.StatusNoContent)
	case types.CompositionNotFound:
		http.Error(w, "Tag not found", http.StatusNotFound)
	case types.CompositionNotProvisional:
		http.Error(w, "Tag is already promoted", http.StatusConflict)
	case types.CompositionConflict:
		http.Error(w, "Tag changed while the decision was applied", http.StatusConflict)
	default:
		http.Error(w, "Invalid composition decision", http.StatusBadRequest)
	}
}

// SplitTag replaces the named live tag with the parts an editor names: the parts are created, the tag's
// documents are re-pointed onto them, and the tag is deleted. Seeded values are refused because the
// startup seeder would re-create them; promoted values are split in place.
func (h *TagHandler) SplitTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	var req SplitTagRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	name := stripTags(strings.TrimSpace(req.Name))
	if name == "" {
		http.Error(w, "Tag name is required", http.StatusBadRequest)
		return
	}
	if len(req.Parts) > maxCompositionParts {
		http.Error(w, "Too many components", http.StatusBadRequest)
		return
	}

	status, err := h.tag.Split(ctx, name, req.Parts)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "split tag", err)
		return
	}

	switch status {
	case types.CompositionApplied:
		w.WriteHeader(http.StatusNoContent)
	case types.CompositionNotFound:
		http.Error(w, "Tag not found", http.StatusNotFound)
	case types.CompositionSeeded:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Seeded values cannot be split"})
	case types.CompositionConflict:
		http.Error(w, "Tag changed while the split was applied", http.StatusConflict)
	default:
		http.Error(w, "Invalid split decision", http.StatusBadRequest)
	}
}

// RetireTag removes one provisional tag without blocklisting its name.
func (h *TagHandler) RetireTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	status, err := h.tag.Retire(ctx, id)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "retire tag", err)
		return
	}

	switch status {
	case types.RetireRetired:
		w.WriteHeader(http.StatusNoContent)
	case types.RetireNotFound:
		http.Error(w, "Tag not found", http.StatusNotFound)
	default:
		http.Error(w, "Use demote for a promoted tag", http.StatusConflict)
	}
}

// AcceptTag waives one provisional tag's review deferral so the ordinary promotion floor decides it.
func (h *TagHandler) AcceptTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	status, err := h.tag.Accept(ctx, id)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "accept tag", err)
		return
	}

	switch status {
	case types.AcceptApplied:
		w.WriteHeader(http.StatusNoContent)
	case types.AcceptNotFound:
		http.Error(w, "Tag not found", http.StatusNotFound)
	case types.AcceptNotProvisional:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Tag is already promoted"})
	case types.AcceptAlready:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Tag is already accepted"})
	case types.AcceptNotDeferred:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Tag is not deferred to review"})
	default:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Another review concern still holds this tag"})
	}
}

// SeparateTag records the operator's decision that one identity-held provisional tag names its own value,
// clearing the hold so the ordinary review and promotion rules decide it.
func (h *TagHandler) SeparateTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	status, err := h.tag.Separate(ctx, id)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "separate tag", err)
		return
	}

	switch status {
	case types.SeparateApplied:
		w.WriteHeader(http.StatusNoContent)
	case types.SeparateNotFound:
		http.Error(w, "Tag not found", http.StatusNotFound)
	case types.SeparateNotProvisional:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Tag is already promoted"})
	default:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "No identity hold to separate from"})
	}
}

// RejudgeTag enqueues one on-demand re-judge of a tag's quality verdict or its composition verdict.
func (h *TagHandler) RejudgeTag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req RejudgeTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	scope := tags.RejudgeScope(strings.TrimSpace(req.Scope))
	if !scope.Valid() {
		http.Error(w, "Invalid re-judge scope", http.StatusBadRequest)
		return
	}

	tag, err := h.tag.Get(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "Tag not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, reqID, "load tag for re-judge", err)
		return
	}
	switch scope {
	case tags.RejudgeScopeComposition:
		if tags.ClassifyShape(tag.Name) != tags.ShapeCompound {
			http.Error(w, "Tag is not a compound", http.StatusConflict)
			return
		}
	case tags.RejudgeScopeQuality:
		verdicts, err := h.tag.LatestQualityEventsForNames(ctx, []string{tag.Name})
		if err != nil {
			writeServiceError(w, h.logger, reqID, "tag re-judge verdict", err)
			return
		}
		if len(verdicts) > 0 {
			http.Error(w, "Tag already carries a current verdict", http.StatusConflict)
			return
		}
	}

	spec, err := task.NewBatchSpec(types.Batch.Origin.API, "")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	batchID, alreadyQueued, err := h.rejudge.Enqueue(ctx, spec, id, scope)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "enqueue tag rejudge", err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"batch_id":       batchID,
		"already_queued": alreadyQueued,
	})
}

// ReevaluateIdentity enqueues a fresh identity sweep for the vocabulary.
func (h *TagHandler) ReevaluateIdentity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	spec, err := task.NewBatchSpec(types.Batch.Origin.API, "")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	batchID, alreadyQueued, err := h.identity.Enqueue(ctx, spec)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "enqueue tag identity run", err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"batch_id":       batchID,
		"already_queued": alreadyQueued,
	})
}

// ReevaluateAudit enqueues a fresh completeness audit for the vocabulary.
func (h *TagHandler) ReevaluateAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	spec, err := task.NewBatchSpec(types.Batch.Origin.API, "")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	batchID, alreadyQueued, err := h.audit.Enqueue(ctx, spec)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "enqueue tag audit run", err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"batch_id":       batchID,
		"already_queued": alreadyQueued,
	})
}

// ListSiblings serves the latest measurement run's near-contained tag pairs for review. Containment carries
// no automatic action, so the endpoint is read-only: nothing merges, holds, or demotes from these readings.
func (h *TagHandler) ListSiblings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 100000)

	snapshot, err := h.tag.ListSiblings(ctx)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "list sibling readings", err)
		return
	}

	readings := snapshot.Readings
	total := int64(len(readings))
	start := min(int(offset), len(readings))
	end := min(start+int(limit), len(readings))
	page := readings[start:end]

	responses := make([]SiblingReadingResponse, len(page))
	for i, s := range page {
		responses[i] = SiblingReadingResponse{
			A:           s.A,
			B:           s.B,
			Containment: s.Containment,
			MassA:       s.MassA,
			MassB:       s.MassB,
			Gray:        s.Gray,
		}
	}

	writeJSON(w, http.StatusOK, SiblingListResponse{
		RunID:      snapshot.RunID,
		SnapshotAt: snapshot.SnapshotAt,
		Absent:     snapshot.Absent,
		Results:    responses,
		Total:      total,
	})
}

// ListAuditFlags serves the paged open coverage flags.
func (h *TagHandler) ListAuditFlags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 100000)

	flags, total, err := h.audit.ListFlags(ctx, int32(limit), int32(offset))
	if err != nil {
		writeServiceError(w, h.logger, reqID, "list coverage flags", err)
		return
	}

	responses := make([]CoverageFlagResponse, len(flags))
	for i, f := range flags {
		responses[i] = CoverageFlagResponse{
			ID:            f.ID,
			DocumentID:    f.DocumentID,
			DocumentUUID:  f.DocumentUUID,
			DocumentTitle: f.DocumentTitle,
			TagName:       f.TagName,
			Class:         f.Class,
			Source:        f.Source,
			Provisional:   f.Provisional,
			CreatedAt:     f.CreatedAt,
		}
	}

	writeJSON(w, http.StatusOK, CoverageFlagListResponse{Results: responses, Total: total})
}

// ApplyAuditFlag confirms a coverage flag, attaching the dimension to the document.
func (h *TagHandler) ApplyAuditFlag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	var req CoverageFlagActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.audit.ApplyFlag(ctx, req.DocumentID, req.TagName); err != nil {
		writeServiceError(w, h.logger, reqID, "apply coverage flag", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DismissAuditFlag records the operator's not-applies outcome for a coverage flag.
func (h *TagHandler) DismissAuditFlag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	var req CoverageFlagActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.audit.DismissFlag(ctx, req.DocumentID, req.TagName); err != nil {
		writeServiceError(w, h.logger, reqID, "dismiss coverage flag", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CreateAuditValue registers a missing flagged dimension through the normal tag-creation path: the value
// is born provisionally with pending identity evidence, and the fill lands on a later audit pass.
func (h *TagHandler) CreateAuditValue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	var req CreateAuditValueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	name := stripTags(strings.TrimSpace(req.TagName))
	if name == "" {
		http.Error(w, "Tag name is required", http.StatusBadRequest)
		return
	}

	if _, err := h.tag.CreateAndUnblock(ctx, []string{name}); err != nil {
		if isMatcherUnavailable(err) {
			matcherUnavailableResponse(w)
			return
		}
		writeServiceError(w, h.logger, reqID, "create audited value", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
			Name:      t.Name,
			Kind:      t.Kind,
			DeletedAt: t.DeletedAt,
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

// TagListResponse is one page of the tag listing.
type TagListResponse struct {
	Results []TagResponse `json:"results"`
	Total   int64         `json:"total"`
}

// DeletedTagResponse is one deleted-tag record as the API shows it: the name, its kind (an operator
// block), and when.
type DeletedTagResponse struct {
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	DeletedAt time.Time `json:"deleted_at"`
}

// DeletedTagListResponse is one page of deleted-tag records.
type DeletedTagListResponse struct {
	Results []DeletedTagResponse `json:"results"`
	Total   int64                `json:"total"`
}

// TagQualityCriteriaView is the API view of the judge's rubric answers;
// a nil answer had no signal in the stored payload.
type TagQualityCriteriaView struct {
	SubjectMatter *bool `json:"subject_matter"`
	Dimension     *bool `json:"dimension"`
	NotPerson     *bool `json:"not_person"`
	Atomic        *bool `json:"atomic"`
	NotFormGenre  *bool `json:"not_form_genre"`
	Defer         *bool `json:"defer"`
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

// ProvisionalTagResponse describes a tag awaiting review, with its latest verdict and what keeps it
// provisional. AcceptedAt is set only while an operator acceptance still waives the recorded deferral,
// so a value whose fresh verdict has since re-decided it reads as deferred again.
type ProvisionalTagResponse struct {
	ID                int64                  `json:"id"`
	Name              string                 `json:"name"`
	DocumentCount     int64                  `json:"document_count"`
	CreatedAt         time.Time              `json:"created_at"`
	Verdict           *TagQualityVerdictInfo `json:"verdict"`
	PendingIdentity   int64                  `json:"pending_identity"`
	IdentityHold      string                 `json:"identity_hold,omitempty"`
	CompositionHold   string                 `json:"composition_hold,omitempty"`
	CompositionReason string                 `json:"composition_reason,omitempty"`
	CompositionAction tags.CompositionAction `json:"composition_action,omitempty"`
	CompositionParts  []string               `json:"composition_parts,omitempty"`
	DemotedAt         *time.Time             `json:"demoted_at,omitempty"`
	DemotedReason     string                 `json:"demoted_reason,omitempty"`
	AcceptedAt        *time.Time             `json:"accepted_at,omitempty"`
	Compound          bool                   `json:"compound,omitempty"`
}

// compositionView is one candidate's latest composition journal row as the queue reads it: the
// recorded reason, the action class derived from it, and the parts a split or an apply-alongside keep
// carries.
type compositionView struct {
	Reason string
	Action tags.CompositionAction
	Parts  []string
}

// ProvisionalTagListResponse is one page of provisional tags: the filtered total for the review queue's
// pager, the queue-level pending identity count that decides the reevaluation affordance, and also the
// canonical review-state values the filter accepts, plus the optional per-state facet counts that the
// review menu reads.
type ProvisionalTagListResponse struct {
	Results              []ProvisionalTagResponse `json:"results"`
	Total                int64                    `json:"total"`
	PendingIdentityTotal int64                    `json:"pending_identity_total"`
	StateValues          []string                 `json:"state_values"`
	StateFacets          map[string]int64         `json:"state_facets,omitempty"`
}

// CreateTagRequest is the body of POST /api/v1/tags.
type CreateTagRequest struct {
	Name string `json:"name"`
}

// FoldTagRequest is the body of POST /api/v1/tags/fold: the name that folds away and the canonical
// value it folds onto.
type FoldTagRequest struct {
	Alias     string `json:"alias"`
	Canonical string `json:"canonical"`
}

// CompositionDecisionRequest is the body of POST /api/v1/tags/{id}/composition: the disposition and,
// for a split or an apply-alongside keep, the parts.
type CompositionDecisionRequest struct {
	Disposition string   `json:"disposition"`
	Parts       []string `json:"parts"`
}

// SplitTagRequest is the body of POST /api/v1/tags/split: the tag to replace and the components that
// replace it.
type SplitTagRequest struct {
	Name  string   `json:"name"`
	Parts []string `json:"parts"`
}

// RejudgeTagRequest is the body of POST /api/v1/tags/{id}/rejudge: which judge to re-run.
type RejudgeTagRequest struct {
	Scope string `json:"scope"`
}

// CoverageFlagResponse is one open coverage flag as the API shows it.
type CoverageFlagResponse struct {
	ID            int64     `json:"id"`
	DocumentID    int64     `json:"document_id"`
	DocumentUUID  string    `json:"document_uuid"`
	DocumentTitle string    `json:"document_title"`
	TagName       string    `json:"tag_name"`
	Class         string    `json:"class"`
	Source        string    `json:"source"`
	Provisional   bool      `json:"provisional"`
	CreatedAt     time.Time `json:"created_at"`
}

// CoverageFlagListResponse is one page of open coverage flags.
type CoverageFlagListResponse struct {
	Results []CoverageFlagResponse `json:"results"`
	Total   int64                  `json:"total"`
}

// SiblingReadingResponse is one near-contained tag pair, A holding nearly all of B's documents: containment
// is the fraction of B's live documents that A also holds, and the masses are those live document counts.
type SiblingReadingResponse struct {
	A           string  `json:"a"`
	B           string  `json:"b"`
	Containment float64 `json:"containment"`
	MassA       int64   `json:"mass_a"`
	MassB       int64   `json:"mass_b"`
	Gray        bool    `json:"gray"`
}

// SiblingListResponse is the latest measurement run's containment readings with the run identity they came
// from; Absent marks the zero-data state where no measurement run exists yet.
type SiblingListResponse struct {
	RunID      string                   `json:"run_id,omitempty"`
	SnapshotAt time.Time                `json:"snapshot_at,omitzero"`
	Absent     bool                     `json:"absent"`
	Results    []SiblingReadingResponse `json:"results"`
	Total      int64                    `json:"total"`
}

// CoverageFlagActionRequest is the body of the coverage flag apply and dismiss actions.
type CoverageFlagActionRequest struct {
	DocumentID int64  `json:"document_id"`
	TagName    string `json:"tag_name"`
}

// CreateAuditValueRequest is the body of the coverage create-value action: it names the missing dimension to
// register and carries no document, since the value is a vocabulary addition.
type CreateAuditValueRequest struct {
	TagName string `json:"tag_name"`
}

// UpdateTagRequest is the body of a tag rename.
type UpdateTagRequest struct {
	Name string `json:"name"`
}
