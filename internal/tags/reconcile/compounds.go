package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/types"
)

type compoundEntry struct {
	name         string
	docCount     int64
	pendingApply bool // a split a prior run decided but did not apply: it needs applying, not judging
	split        bool
	components   []string
	reason       string
	skipReason   string
}

// buildCompoundWorklist returns the compounds still owed a disposition, pending-apply splits first and then
// by live mass, so a run cap only ever defers the lightest and least-used work.
func (r *Reconciler) buildCompoundWorklist(ctx context.Context, allTags []database.Tag, tagCounts map[string]int64, report *Report) []*compoundEntry {
	var names []string
	for _, t := range allTags {
		if tags.ClassifyShape(t.Name) != tags.ShapeCompound {
			continue
		}
		if tags.IsPeriodClass(tags.HouseForm(t.Name)) {
			report.Skips["period_store_application"]++
			continue
		}
		names = append(names, t.Name)
	}
	if len(names) == 0 {
		return nil
	}

	held, err := tags.IdentityHeldNames(ctx, r.client.Queries, names)
	if err != nil {
		r.logger.Error("", "tag reconcile: compound identity hold lookup: %v", err)
		held = map[string]string{}
	}
	compositionHeld, states, err := tags.CompositionReview(ctx, r.client.Queries, names, analyzer.TagSplitVersion)
	if err != nil {
		r.logger.Error("", "tag reconcile: compound composition lookup: %v", err)
		compositionHeld = map[string]string{}
		states = map[string]tags.CompositionState{}
	}

	entries := make([]*compoundEntry, 0, len(names))
	for _, name := range names {
		if anchor, ok := held[name]; ok {
			report.Skips["identity_hold"]++
			r.logger.Debug("", "tag reconcile: compound %q is identity-held (anchor=%q) — left for review", name, anchor)
			continue
		}
		if reason, ok := compositionHeld[name]; ok {
			report.Skips["composition_hold"]++
			r.logger.Debug("", "tag reconcile: compound %q is composition-held (reason=%s) — left for review", name, reason)
			continue
		}
		state, ok := states[tags.StoredName(name)]
		if ok && state.Decided {
			// a split may still owe an application; a keep or drop leaves the stored tag as it is
			if state.Disposition == tags.CompositionSplit {
				entries = append(entries, &compoundEntry{name: name, docCount: tagCounts[name], pendingApply: true})
			} else {
				report.Skips["decided"]++
			}
			continue
		}
		entries = append(entries, &compoundEntry{name: name, docCount: tagCounts[name]})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].pendingApply != entries[j].pendingApply {
			return entries[i].pendingApply
		}
		if entries[i].docCount != entries[j].docCount {
			return entries[i].docCount > entries[j].docCount
		}
		return entries[i].name < entries[j].name
	})
	return entries
}

// decideCompoundSplits disposes of the stored compounds through the admission gate's shared judgment
// (admission.JudgeCompounds), so a compound is judged alike whether it arrives by emission or by sweep; the
// journal row it writes is what lets a decided hold bind promotion and a decided keep be skipped later at
// emission. Only a split touches the store — a drop is recorded with its tag left standing, revocation being
// demotion's job — and without an adjudicator only the conjunction split stands, no other verdict being
// reachable.
func (r *Reconciler) decideCompoundSplits(ctx context.Context, cfg *config.Config, entries []*compoundEntry, promotedNames map[string]string, liveNames map[string]struct{}) {
	if !cfg.Enricher.TagAdjudicator.Enabled {
		componentFilter := analyzer.NewTagFilter(nil, nil, nil)
		for _, e := range entries {
			if textnorm.SplitJointTag(e.name) == nil {
				e.skipReason = "undecided_no_judge"
				continue
			}
			components := admission.ConjunctionComponents(e.name, promotedNames, liveNames, componentFilter)
			if components == nil {
				e.skipReason = "split_invalid"
				continue
			}
			e.split = true
			e.components = components
			e.reason = "deterministic"
		}
		return
	}

	candidates := make([]string, 0, len(entries))
	for _, e := range entries {
		candidates = append(candidates, e.name)
	}
	adjudicator := admission.NewAdjudicator(cfg, r.logger, r.client.Queries, r.runner)
	rows, err := adjudicator.JudgeCompounds(ctx, candidates, liveNames, promotedNames, analyzer.NewTagFilter(nil, nil, nil), 0, "")
	if err != nil {
		r.logger.Error("", "tag reconcile: compound judgment failed: %v", err)
		return
	}
	for i, row := range rows {
		if row.Disposition == tags.CompositionSplit {
			entries[i].split = true
			entries[i].components = row.Parts
			entries[i].reason = "adjudicated"
			continue
		}
		entries[i].skipReason = row.Reason
	}
	// a degraded keep is judgment still owed, not a verdict: journaling one would make the sweep's own hold
	// filter skip the compound on every later run, so an outage leaves the entry undecided, as at emission
	recorded := make([]tags.CompositionRow, 0, len(rows))
	for _, row := range rows {
		if row.Reason == tags.CompositionReasonJudgeUnavailable {
			continue
		}
		recorded = append(recorded, row)
	}
	if len(recorded) > 0 {
		if err := r.tag.RecordCompositionEvidence(ctx, recorded); err != nil {
			r.logger.Error("", "tag reconcile: record composition evidence: %v", err)
		}
	}
}

func (r *Reconciler) planCompoundSplits(entries []*compoundEntry, report *Report) {
	for _, e := range entries {
		if !e.split {
			if e.skipReason != "" {
				report.Skips[e.skipReason]++
			}
			continue
		}
		report.PlannedCompoundSplits = append(report.PlannedCompoundSplits, CompoundSplitEntry{
			Query:      e.name,
			Components: e.components,
			Reassigned: e.docCount,
			Reason:     e.reason,
		})
		r.logger.Info("", "tag reconcile (report-only): would split %q → %v (%d documents)%s",
			e.name, e.components, e.docCount, reasonSuffix(e.reason))
	}
}

func (r *Reconciler) applyCompoundSplits(ctx context.Context, cfg *config.Config, entries []*compoundEntry, report *Report, storeTags map[string]database.Tag) {
	budget := cfg.Enricher.TagReconcile.MaxRetag
	applied := false

	for i, e := range entries {
		if !e.split {
			if e.skipReason != "" {
				report.Skips[e.skipReason]++
			}
			continue
		}
		if budget <= 0 && applied {
			for _, rest := range entries[i:] {
				if rest.split {
					report.Capped++
				} else if rest.skipReason != "" {
					report.Skips[rest.skipReason]++
				}
			}
			break
		}

		reassigned, err := r.applyCompoundSplit(ctx, e, storeTags)
		if err != nil {
			r.logger.Error("", "tag reconcile: split %q failed: %v", e.name, err)
			report.Skips["split_error"]++
			continue
		}
		applied = true
		report.CompoundSplits = append(report.CompoundSplits, CompoundSplitEntry{
			Query:      e.name,
			Components: e.components,
			Reassigned: reassigned,
			Reason:     e.reason,
		})
		r.logger.Info("", "tag reconcile: split %q → %v (re-pointed %d documents)%s",
			e.name, e.components, reassigned, reasonSuffix(e.reason))
		budget -= int(reassigned)
	}
}

func (r *Reconciler) applyCompoundSplit(ctx context.Context, e *compoundEntry, storeTags map[string]database.Tag) (int64, error) {
	compound, ok := storeTags[e.name]
	if !ok {
		return 0, fmt.Errorf("tag %q no longer in store", e.name)
	}

	outputs := make([]string, 0, len(e.components))
	seen := make(map[string]bool, len(e.components))
	for _, name := range e.components {
		if name == "" || seen[name] || name == e.name {
			continue
		}
		seen[name] = true
		outputs = append(outputs, name)
	}
	if len(outputs) == 0 {
		return 0, fmt.Errorf("no valid components")
	}

	results, err := r.tag.Create(ctx, outputs)
	if err != nil {
		return 0, fmt.Errorf("create component tags: %w", err)
	}
	var componentIDs []int64
	for i, res := range results {
		switch res.Status {
		case types.Created, types.Conflict, types.Apply:
			componentIDs = append(componentIDs, res.Entity.ID)
		default:
			return 0, fmt.Errorf("create component %q: invalid result", outputs[i])
		}
	}

	tx, err := r.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	txQ := r.client.WithTx(tx)

	locked, err := txQ.GetTagForUpdate(ctx, compound.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("compound tag %q already deleted", e.name)
		}
		return 0, fmt.Errorf("lock compound tag: %w", err)
	}
	if locked.Name != e.name {
		return 0, fmt.Errorf("compound tag %q renamed to %q during run", e.name, locked.Name)
	}
	docIDs, err := txQ.GetTagDocumentIDs(ctx, compound.ID)
	if err != nil {
		return 0, fmt.Errorf("document ids for tag: %w", err)
	}
	if _, err := txQ.AddComponentTagsToTagDocuments(ctx, database.AddComponentTagsToTagDocumentsParams{
		ComponentIds: componentIDs,
		JointID:      compound.ID,
	}); err != nil {
		return 0, fmt.Errorf("add component tags: %w", err)
	}
	if len(docIDs) > 0 {
		if err := txQ.ClearOrphanTagMarksByTagIDs(ctx, componentIDs); err != nil {
			return 0, fmt.Errorf("clear component orphan tag marks: %w", err)
		}
	}
	if err := txQ.DeleteTag(ctx, compound.ID); err != nil {
		return 0, fmt.Errorf("delete compound tag: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit split: %w", err)
	}

	if err := r.tag.RemoveFromStore(ctx, []string{e.name}); err != nil {
		r.logger.Error("", "tag reconcile: split %q: remove from store: %v", e.name, err)
	}
	return int64(len(docIDs)), nil
}
