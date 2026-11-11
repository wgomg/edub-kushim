package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// RejudgeRequest is the payload of one on-demand re-judge task: the stored tag and which judge to
// re-run.
type RejudgeRequest struct {
	TagID int64  `json:"tag_id"`
	Scope string `json:"scope"`
}

// RejudgeReport is the stored result of one re-judge: what was judged and how it resolved, whether the
// fresh decision promoted the value at the recurrence floor, and whether an identity or composition hold
// kept it for review instead.
type RejudgeReport struct {
	TagID    int64  `json:"tag_id"`
	Name     string `json:"name"`
	Scope    string `json:"scope"`
	Outcome  string `json:"outcome,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Promoted bool   `json:"promoted,omitempty"`
	Held     bool   `json:"held,omitempty"`
}

// RejudgeTaskHandler runs one on-demand re-judge as a blocking task; the report it returns is stored
// as the task result. It runs under the maintenance lock the caller holds, like the sweeps, because
// the re-judge writes evidence the passes read.
type RejudgeTaskHandler struct {
	reconciler *Reconciler
}

// NewRejudgeTaskHandler wires the handler to the reconciler.
func NewRejudgeTaskHandler(reconciler *Reconciler) *RejudgeTaskHandler {
	return &RejudgeTaskHandler{reconciler: reconciler}
}

// Handle decodes the re-judge payload, refreshes the requested judge for the named tag, and returns
// the serialized report.
func (h *RejudgeTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	var req RejudgeRequest
	if len(t.Payload) > 0 {
		if err := json.Unmarshal(t.Payload, &req); err != nil {
			return nil, fmt.Errorf("tag rejudge: decode payload: %w", err)
		}
	}
	if req.TagID <= 0 || !tags.RejudgeScope(req.Scope).Valid() {
		return nil, fmt.Errorf("tag rejudge: payload names tag %d with scope %q", req.TagID, req.Scope)
	}

	report, err := h.reconciler.Rejudge(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("tag rejudge: %w", err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal tag rejudge report: %w", err)
	}
	h.reconciler.logger.Info("", "tag rejudge: %s %q resolved %s (%s)",
		report.Scope, report.Name, report.Outcome, report.Reason)
	return raw, nil
}

// Rejudge re-runs one judge over one stored tag: the quality scope re-judges the value from its first
// live document through the same path the reconciliation sweep uses, and the composition scope
// re-judges the compound and journals the fresh row. A missing tag, an unconfigured judge, and a tag
// with no live document text are errors; a degraded composition verdict is journaled as the sweep
// does, leaving the compound undecided for the next run.
func (r *Reconciler) Rejudge(ctx context.Context, req RejudgeRequest) (RejudgeReport, error) {
	cfg := r.getConfig()
	tag, err := r.client.Queries.GetTag(ctx, req.TagID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RejudgeReport{}, fmt.Errorf("tag %d not found", req.TagID)
		}
		return RejudgeReport{}, fmt.Errorf("get tag %d: %w", req.TagID, err)
	}

	report := RejudgeReport{TagID: tag.ID, Name: tag.Name, Scope: req.Scope}
	switch tags.RejudgeScope(req.Scope) {
	case tags.RejudgeScopeQuality:
		if !r.runner.HasTagQualityJudge() {
			return RejudgeReport{}, errors.New("tag quality judge is not configured")
		}
		decision, _, err := r.rejudgeStoredName(ctx, cfg, tag.ID, tag.Name)
		if err != nil {
			if errors.Is(err, errNoJudgeText) {
				return RejudgeReport{}, fmt.Errorf("tag %q has no live document text", tag.Name)
			}
			return RejudgeReport{}, err
		}
		decision = tags.WithAcceptance(decision, tag.AcceptedAt.Time)
		report.Outcome = decision.Outcome.String()
		report.Reason = decision.Reason

		liveCounts, err := tags.NewStore(r.client.Queries).LiveMass(ctx, []int64{tag.ID})
		if err != nil {
			return RejudgeReport{}, fmt.Errorf("count live mass for tag %q: %w", tag.Name, err)
		}
		held, err := tags.IdentityHeldNames(ctx, r.client.Queries, []string{tag.Name})
		if err != nil {
			return RejudgeReport{}, fmt.Errorf("check identity holds for tag %q: %w", tag.Name, err)
		}
		compositionHeld, err := tags.CompositionHeldNames(ctx, r.client.Queries, []string{tag.Name}, analyzer.TagSplitVersion)
		if err != nil {
			return RejudgeReport{}, fmt.Errorf("check composition holds for tag %q: %w", tag.Name, err)
		}
		floor := int64(cfg.Enricher.TagReconcile.RecurrenceFloor)
		if _, identityHeld := held[tag.Name]; identityHeld {
			report.Held = true
			r.logger.Info("", "tag reconcile: rejudged %q is identity-held — left for review", tag.Name)
		} else if compositionReason, compositionHeld := compositionHeld[tag.Name]; compositionHeld {
			report.Held = true
			r.logger.Info("", "tag reconcile: rejudged %q is composition-held (reason=%s) — left for review",
				tag.Name, compositionReason)
		} else if tags.DecidePromotion(decision.Outcome, liveCounts[tag.ID], floor) {
			reason := tags.PromoteReasonRejudge
			if decision.Reason == tags.ReasonOperatorAccept {
				reason = tags.PromoteReasonRejudgeAccepted
			}
			if _, status, err := r.tag.Promote(ctx, tag.ID, reason); err != nil {
				return RejudgeReport{}, fmt.Errorf("promote re-judged tag %q: %w", tag.Name, err)
			} else if status == types.PromotePromoted {
				report.Promoted = true
			}
		}
	case tags.RejudgeScopeComposition:
		if !cfg.Enricher.TagAdjudicator.Enabled {
			return RejudgeReport{}, errors.New("tag adjudicator is not configured")
		}
		row, err := r.rejudgeComposition(ctx, cfg, tag)
		if err != nil {
			return RejudgeReport{}, err
		}
		report.Outcome = string(row.Disposition)
		report.Reason = row.Reason
	default:
		return RejudgeReport{}, fmt.Errorf("unknown re-judge scope %q", req.Scope)
	}
	return report, nil
}

// rejudgeComposition judges one compound through the same entry point the sweep and the emission gate
// use, then journals the row so the review surface reads it. A degraded keep is left unjournaled,
// exactly as the sweep leaves it, so the compound stays undecided for a later run.
func (r *Reconciler) rejudgeComposition(ctx context.Context, cfg *config.Config, tag database.Tag) (tags.CompositionRow, error) {
	promotedNames, err := r.client.Queries.ListPromotedTagNames(ctx)
	if err != nil {
		return tags.CompositionRow{}, fmt.Errorf("list promoted tag names: %w", err)
	}
	promotedView := tags.StoreNames(promotedNames)

	// the judge receives the store's full live key set: its membership lookups only touch the compound's
	// own segments, but the substrate must match the sweep's and the emission gate's
	storeTagKeys, err := r.client.ListStoreTagKeys(ctx)
	if err != nil {
		return tags.CompositionRow{}, fmt.Errorf("list store tag keys: %w", err)
	}
	liveNames := make(map[string]struct{}, len(storeTagKeys))
	for _, key := range storeTagKeys {
		liveNames[tags.Key(key)] = struct{}{}
	}

	adjudicator := admission.NewAdjudicator(cfg, r.logger, r.client.Queries, r.runner)
	rows, err := adjudicator.JudgeCompounds(ctx, []string{tag.Name}, liveNames, promotedView,
		analyzer.NewTagFilter(nil, nil, nil), 0, "")
	if err != nil {
		return tags.CompositionRow{}, err
	}
	if len(rows) != 1 {
		return tags.CompositionRow{}, fmt.Errorf("judge returned %d rows for %q", len(rows), tag.Name)
	}
	row := rows[0]
	if row.Reason != tags.CompositionReasonJudgeUnavailable {
		if err := r.tag.RecordCompositionEvidence(ctx, rows); err != nil {
			return tags.CompositionRow{}, err
		}
	}
	return row, nil
}
