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

type jointEntry struct {
	name       string
	segments   []string
	docCount   int64
	split      bool
	components []string
	reason     string
	skipReason string
}

func (r *Reconciler) buildJointWorklist(allTags []database.Tag, tagCounts map[string]int64) []*jointEntry {
	var entries []*jointEntry
	for _, t := range allTags {
		segments := textnorm.SplitJointTag(t.Name)
		if len(segments) < 2 {
			continue
		}
		entries = append(entries, &jointEntry{
			name:     t.Name,
			segments: segments,
			docCount: tagCounts[t.Name],
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return entries
}

// decideJointSplits disposes of the stored joint tags. The retrospective arm of the emission gate's
// decision: it judges each joint name's compound status and its fold-resolved content parts through the same
// persisted caches, folds both into tags.CompositionFeatures, and disposes through tags.DecideComposition. A
// CompositionSplit runs the existing component validation and feeds planJointSplits/applyJointSplits; every
// other disposition records the decision reason as its skip reason and leaves the stored tag in place — the
// sweep never deletes a tag. Without an adjudicator the deterministic conjunction split stands unchanged.
// promotedNames is the promoted tag names' Key→name view the parts fold and the reconstructibility signal
// read.
func (r *Reconciler) decideJointSplits(ctx context.Context, cfg *config.Config, entries []*jointEntry, promotedNames map[string]string) {
	componentFilter := analyzer.NewTagFilter(nil, nil, nil)
	validate := func(components []string) []string {
		valid := analyzer.SplitComponents(components, componentFilter)
		if len(valid) < 2 || len(valid) > cfg.Enricher.ContentAnalyzer.MaxTags {
			return nil
		}
		return valid
	}

	if !cfg.Enricher.TagAdjudicator.Enabled {
		for _, e := range entries {
			components := validate(e.segments)
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

	liveNames := tags.PromotedKeys(promotedNames)
	contentByEntry := make(map[*jointEntry][]string, len(entries))
	singlesByEntry := make(map[*jointEntry][]string, len(entries))
	var foldKeys []admission.PhraseFoldKey
	for _, e := range entries {
		// the same time-gated, fold-resolved singles the emission gate builds, so the fan covers the same
		// words on both sides and the prospective and retrospective readings agree
		content, singles := admission.ResolveContentSingles(e.name, promotedNames, liveNames)
		foldKeys = append(foldKeys, admission.PhraseFoldKeys(content, singles, tags.HouseForm(e.name), liveNames)...)
		contentByEntry[e] = content
		singlesByEntry[e] = singles
	}

	store := admission.NewVerdictStore(r.client.Queries, r.runner, r.logger, cfg.ResolvedAdjudicatorLlm().Model, tags.PromotedKeys(promotedNames))
	foldVerdicts := map[admission.PhraseFoldKey]admission.PhraseFoldVerdict{}
	if len(foldKeys) > 0 {
		// residual phrase-context folds, the same rule and cache the emission gate uses, so a fold the gate
		// confirmed reads the same in the sweep and neither side asks the judge a different question
		verdicts, ferr := store.PhraseFolds(ctx, foldKeys, 0, "")
		if ferr != nil {
			r.logger.Error("", "tag reconcile: phrase fold verdicts: %v", ferr)
		}
		foldVerdicts = verdicts
	}
	fans := make(map[*jointEntry]admission.Fan, len(entries))
	seedFans := make([]admission.Fan, 0, len(entries))
	for _, e := range entries {
		content, singles := contentByEntry[e], singlesByEntry[e]
		singles, deferred := admission.ApplyPhraseFolds(content, singles, tags.HouseForm(e.name), foldVerdicts, liveNames)
		fan := admission.BuildFan(content, singles, liveNames).WithDeferred(deferred)
		fans[e] = fan
		seedFans = append(seedFans, fan)
	}

	seenValues := make(map[string]bool)
	var values []string
	for _, e := range entries {
		for _, seg := range tags.DeriveSegments(e.name) {
			resolved := fans[e].Resolved[seg]
			if resolved == "" || seenValues[resolved] {
				continue
			}
			seenValues[resolved] = true
			values = append(values, resolved)
		}
	}

	partVerdicts, err := store.Parts(ctx, values, 0, "")
	if err != nil {
		r.logger.Error("", "tag reconcile: part verdicts: %v", err)
	}
	admission.SeedStoreValues(partVerdicts, seedFans, liveNames)
	candidates := make([]string, 0, len(entries))
	for _, e := range entries {
		candidates = append(candidates, e.name)
	}
	compoundVerdicts, err := store.Compounds(ctx, candidates, 0, "")
	if err != nil {
		r.logger.Error("", "tag reconcile: compound verdicts: %v", err)
	}

	for _, e := range entries {
		fan := fans[e]
		parts, coverComplete := admission.CoverParts(fan, liveNames, partVerdicts)
		features := admission.CompositionFeatures(parts, coverComplete, fan.DeferredSet(), partVerdicts, compoundVerdicts[e.name], promotedNames)
		decision := tags.DecideComposition(features)
		if decision.Disposition != tags.CompositionSplit {
			e.skipReason = decision.Reason
			continue
		}
		components := validate(parts)
		if components == nil {
			e.skipReason = "split_ambiguous"
			continue
		}
		e.split = true
		e.components = components
		e.reason = "adjudicated"
	}
}

func (r *Reconciler) planJointSplits(entries []*jointEntry, report *Report) {
	for _, e := range entries {
		if !e.split {
			if e.skipReason != "" {
				report.Skips[e.skipReason]++
			}
			continue
		}
		report.PlannedJointSplits = append(report.PlannedJointSplits, JointSplitEntry{
			Query:      e.name,
			Components: e.components,
			Reassigned: e.docCount,
			Reason:     e.reason,
		})
		r.logger.Info("", "tag reconcile (report-only): would split %q → %v (%d documents)%s",
			e.name, e.components, e.docCount, reasonSuffix(e.reason))
	}
}

func (r *Reconciler) applyJointSplits(ctx context.Context, cfg *config.Config, entries []*jointEntry, report *Report, storeTags map[string]database.Tag) {
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

		reassigned, err := r.applyJointSplit(ctx, e, storeTags)
		if err != nil {
			r.logger.Error("", "tag reconcile: split %q failed: %v", e.name, err)
			report.Skips["split_error"]++
			continue
		}
		applied = true
		report.JointSplits = append(report.JointSplits, JointSplitEntry{
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

func (r *Reconciler) applyJointSplit(ctx context.Context, e *jointEntry, storeTags map[string]database.Tag) (int64, error) {
	joint, ok := storeTags[e.name]
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

	locked, err := txQ.GetTagForUpdate(ctx, joint.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("joint tag %q already deleted", e.name)
		}
		return 0, fmt.Errorf("lock joint tag: %w", err)
	}
	if locked.Name != e.name {
		return 0, fmt.Errorf("joint tag %q renamed to %q during run", e.name, locked.Name)
	}
	docIDs, err := txQ.GetTagDocumentIDs(ctx, joint.ID)
	if err != nil {
		return 0, fmt.Errorf("document ids for tag: %w", err)
	}
	if _, err := txQ.AddComponentTagsToTagDocuments(ctx, database.AddComponentTagsToTagDocumentsParams{
		ComponentIds: componentIDs,
		JointID:      joint.ID,
	}); err != nil {
		return 0, fmt.Errorf("add component tags: %w", err)
	}
	if len(docIDs) > 0 {
		if err := txQ.ClearOrphanTagMarksByTagIDs(ctx, componentIDs); err != nil {
			return 0, fmt.Errorf("clear component orphan tag marks: %w", err)
		}
	}
	if err := txQ.DeleteTag(ctx, joint.ID); err != nil {
		return 0, fmt.Errorf("delete joint tag: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit split: %w", err)
	}

	if err := r.tag.RemoveFromStore(ctx, []string{e.name}); err != nil {
		r.logger.Error("", "tag reconcile: split %q: remove from store: %v", e.name, err)
	}
	return int64(len(docIDs)), nil
}
