package reconcile

import (
	"context"
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
	name           string
	docCount       int64
	pendingApply   bool // a split a prior run decided but did not apply: it needs applying, not judging
	split          bool
	components     []string
	demote         bool // a decided drop whose influence still stands: revoke it, do not judge or split
	notify         bool // a decided drop on operator standing: keep it and report the disagreement
	standingSource string
	reason         string
	skipReason     string
}

// Pending applications and decided drops lead the worklist so a run cap only ever defers the lightest
// work; seeded values and held compounds never enter it.
func (r *Reconciler) buildCompoundWorklist(ctx context.Context, allTags []database.Tag, tagCounts map[string]int64, report *Report) []*compoundEntry {
	var names []string
	promoted := make(map[string]bool, len(allTags))
	standings := make(map[string]string, len(allTags))
	for _, t := range allTags {
		if tags.ClassifyShape(t.Name) != tags.ShapeCompound {
			continue
		}
		if tags.IsPeriodClass(tags.HouseForm(t.Name)) {
			report.Skips["period_store_application"]++
			continue
		}
		if t.Seeded {
			// only a seeded row that would otherwise reach the worklist counts, so the tally is not the
			// constant size of the operator-owned seed inventory
			report.Skips["seeded"]++
			continue
		}
		names = append(names, t.Name)
		promoted[t.Name] = t.PromotedAt.Valid
		standings[t.Name] = t.StandingSource
	}
	if len(names) == 0 {
		return nil
	}

	held, err := tags.IdentityHeldNamesAnyVersion(ctx, r.client.Queries, names)
	if err != nil {
		r.logger.Error("", "tag reconcile: compound identity hold lookup: %v", err)
		held = map[string]tags.IdentityHold{}
	}
	compositionHeld, states, err := tags.CompositionReview(ctx, r.client.Queries, names, analyzer.TagSplitVersion)
	if err != nil {
		r.logger.Error("", "tag reconcile: compound composition lookup: %v", err)
		compositionHeld = map[string]string{}
		states = map[string]tags.CompositionState{}
	}
	// a recorded operator split is the decision of record for a stored compound: it is applied with the
	// operator's parts regardless of the journal state under the current criteria version, so a version bump
	// or a later judge row can never judge the row back into a keep while emissions keep substituting
	rules, err := tags.OperatorSplitsForNames(ctx, r.client.Queries, names)
	if err != nil {
		r.logger.Error("", "tag reconcile: operator split rule lookup: %v", err)
		rules = map[string][]string{}
	}

	entries := make([]*compoundEntry, 0, len(names))
	for _, name := range names {
		if parts := rules[tags.StoredName(name)]; len(parts) > 0 {
			entries = append(entries, &compoundEntry{
				name:           name,
				docCount:       tagCounts[name],
				pendingApply:   true,
				split:          true,
				components:     parts,
				standingSource: standings[name],
				reason:         tags.CompositionReasonOperator,
			})
			continue
		}
		entry, skip := compoundEntryFor(name, promoted[name], standings[name], tagCounts[name], held, compositionHeld, states)
		if entry != nil {
			entries = append(entries, entry)
			continue
		}
		report.Skips[skip]++
		switch skip {
		case "identity_hold":
			r.logger.Debug("", "tag reconcile: compound %q is identity-held (anchor=%q) — left for review", name, held[name].Anchor)
		case "composition_hold":
			r.logger.Debug("", "tag reconcile: compound %q is composition-held (reason=%s) — left for review", name, compositionHeld[name])
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].pendingApply != entries[j].pendingApply {
			return entries[i].pendingApply
		}
		if entries[i].demote != entries[j].demote {
			return entries[i].demote
		}
		if entries[i].docCount != entries[j].docCount {
			return entries[i].docCount > entries[j].docCount
		}
		return entries[i].name < entries[j].name
	})
	return entries
}

// compoundEntryFor classifies one stored compound candidate into the work it represents: an application owed
// for a decided split, a demotion owed for a decided drop still promoted, a notification owed for a decided
// drop on operator standing, or a judgment owed otherwise. A nil entry comes with the tally key naming why
// the sweep has nothing to do. A recorded operator split rule outranks it on both paths, and for the
// journal-driven candidates that remain, the compound arm and the opening gate's probe both read membership
// through it, so the two cannot disagree about what work is due.
func compoundEntryFor(name string, promoted bool, standingSource string, docCount int64, held map[string]tags.IdentityHold,
	compositionHeld map[string]string, states map[string]tags.CompositionState) (*compoundEntry, string) {
	if _, ok := held[name]; ok {
		return nil, "identity_hold"
	}
	state, ok := states[tags.StoredName(name)]
	if ok && state.Decided && state.Disposition == tags.CompositionSplit {
		return &compoundEntry{name: name, docCount: docCount, pendingApply: true, standingSource: standingSource}, ""
	}
	if ok && state.Decided && state.Disposition == tags.CompositionDrop {
		if promoted {
			// every drop reason is a hold reason, so the held map carries the demotion reason
			if standingSource == tags.StandingOperator {
				return &compoundEntry{name: name, docCount: docCount, notify: true, standingSource: standingSource, reason: compositionHeld[name]}, ""
			}
			return &compoundEntry{name: name, docCount: docCount, demote: true, standingSource: standingSource, reason: compositionHeld[name]}, ""
		}
		return nil, "decided"
	}
	if _, ok := compositionHeld[name]; ok {
		return nil, "composition_hold"
	}
	if ok && state.Decided {
		return nil, "decided"
	}
	return &compoundEntry{name: name, docCount: docCount, standingSource: standingSource}, ""
}

// Stored compounds are judged through the same entry point the emission gate uses, so a verdict binds
// identically on either path — including the substrate reading the judgment consults. Only a split acts here;
// a decided drop belongs to demotion. An entry whose split parts a recorded operator rule already decided is
// applied, not judged, so the operator's parts win over a re-derived disposition.
func (r *Reconciler) decideCompoundSplits(ctx context.Context, cfg *config.Config, entries []*compoundEntry, promotedNames map[string]string, liveNames map[string]struct{}) {
	if !cfg.Enricher.TagAdjudicator.Enabled {
		componentFilter := analyzer.NewTagFilter(nil, nil, nil)
		for _, e := range entries {
			if e.demote || e.notify || e.split {
				// a demotion, a notification, or a decided operator rule is not judged here
				continue
			}
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

	judged := make([]*compoundEntry, 0, len(entries))
	candidates := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.demote || e.notify || e.split {
			continue
		}
		judged = append(judged, e)
		candidates = append(candidates, e.name)
	}
	if len(candidates) == 0 {
		return
	}
	adjudicator := admission.NewAdjudicator(cfg, r.logger, r.client.Queries, r.runner)
	rows, err := adjudicator.JudgeCompounds(ctx, candidates, liveNames, promotedNames, analyzer.NewTagFilter(nil, nil, nil), 0, "")
	if err != nil {
		r.logger.Error("", "tag reconcile: compound judgment failed: %v", err)
		return
	}
	for i, row := range rows {
		switch row.Disposition {
		case tags.CompositionSplit:
			judged[i].split = true
			judged[i].components = row.Parts
			judged[i].reason = "adjudicated"
		case tags.CompositionDrop:
			if judged[i].standingSource == tags.StandingOperator {
				judged[i].notify = true
				judged[i].reason = row.Reason
				continue
			}
			judged[i].demote = true
			judged[i].reason = row.Reason
		default:
			judged[i].skipReason = row.Reason
		}
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
		if e.notify {
			notifyKept(report, &report.Demotion.Notified, e.name, e.reason)
			r.logger.Info("", "tag reconcile (report-only): operator-standing compound %q disagrees with the current split criteria (reason=%s) — kept", e.name, e.reason)
			continue
		}
		if e.demote {
			report.Demotion.NoDimension++
			r.planDemotion(report, e.name, e.reason)
			continue
		}
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

	for _, e := range entries {
		switch {
		case e.notify:
			notifyKept(report, &report.Demotion.Notified, e.name, e.reason)
			r.logger.Info("", "tag reconcile: operator-standing compound %q disagrees with the current split criteria (reason=%s) — kept", e.name, e.reason)
		case e.demote:
			// demotions move no documents, so they never consume the re-tag budget
			report.Demotion.NoDimension++
			r.applyDemotion(ctx, storeTags[e.name].ID, e.name, e.reason, report)
		case !e.split:
			if e.skipReason != "" {
				report.Skips[e.skipReason]++
			}
		case budget <= 0 && applied:
			report.Capped++
		default:
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
}

// One failed revocation is counted, not fatal, so a bad row cannot abort a run.
func (r *Reconciler) applyDemotion(ctx context.Context, id int64, name, reason string, report *Report) {
	if id == 0 {
		report.Demotion.Skipped++
		return
	}
	_, status, err := r.tag.Demote(ctx, id, reason)
	if err != nil {
		r.logger.Error("", "tag reconcile: demote %q: %v", name, err)
		report.Demotion.Failed++
		return
	}
	if status != types.DemoteDemoted {
		report.Demotion.Skipped++
		return
	}
	report.Demotion.Demoted++
	report.Demoted = append(report.Demoted, DemotionEntry{Name: name, Reason: reason})
	r.logger.Info("", "tag reconcile: demoted %q (reason=%s rule_set=decision-%s)",
		name, reason, tags.DecisionRuleSetVersion)
}

// applyCompoundSplit applies one planned split through the shared store mutation the operator path
// also uses, so both leave the store in the same shape. A compound renamed since the worklist was
// built refuses the split rather than re-pointing the wrong value's documents.
func (r *Reconciler) applyCompoundSplit(ctx context.Context, e *compoundEntry, storeTags map[string]database.Tag) (int64, error) {
	compound, ok := storeTags[e.name]
	if !ok {
		return 0, fmt.Errorf("tag %q no longer in store", e.name)
	}

	outcome, err := r.tag.SplitCompound(ctx, compound.ID, e.name, e.components, false, tags.StandingAuto)
	if err != nil {
		return 0, err
	}
	switch outcome.Status {
	case types.CompositionApplied:
		return outcome.Reassigned, nil
	case types.CompositionConflict:
		return 0, fmt.Errorf("compound tag %q renamed during run", e.name)
	case types.CompositionNotFound:
		return 0, fmt.Errorf("compound tag %q already deleted", e.name)
	default:
		return 0, fmt.Errorf("split %q did not apply", e.name)
	}
}
