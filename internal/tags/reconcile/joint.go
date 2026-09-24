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
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/textnorm"
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

func (r *Reconciler) decideJointSplits(ctx context.Context, cfg *config.Config, entries []*jointEntry) {
	componentFilter := analyzer.NewTagFilter(nil, nil, "", nil)
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

	for i := 0; i < len(entries); i += adjudicateBatchSize {
		end := min(i+adjudicateBatchSize, len(entries))
		tags := make([]string, 0, end-i)
		for _, e := range entries[i:end] {
			tags = append(tags, e.name)
		}
		results, err := r.runner.AdjudicateTagSplits(ctx, tags)
		if err != nil {
			r.logger.Error(nil, "tag reconcile: tag split adjudication batch failed: %v", err)
			for _, e := range entries[i:end] {
				e.skipReason = "split_adjudication_error"
			}
			continue
		}
		if len(results) != len(tags) {
			for _, e := range entries[i:end] {
				e.skipReason = "split_adjudication_error"
			}
			continue
		}
		for j, res := range results {
			e := entries[i:end][j]
			switch res.Verdict {
			case analyzer.TagSplitSplit:
				components := validate(res.Components)
				if components == nil {
					e.skipReason = "split_ambiguous"
					continue
				}
				e.split = true
				e.components = components
				e.reason = "adjudicated"
			case analyzer.TagSplitKeep:
				e.skipReason = "split_kept"
			default:
				e.skipReason = "split_ambiguous"
			}
		}
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
		r.logger.Info(nil, "tag reconcile (report-only): would split %q → %v (%d documents)%s",
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
			r.logger.Error(nil, "tag reconcile: split %q failed: %v", e.name, err)
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
		r.logger.Info(nil, "tag reconcile: split %q → %v (re-pointed %d documents)%s",
			e.name, e.components, reassigned, reasonSuffix(e.reason))
		budget -= int(reassigned)
	}
}

func (r *Reconciler) applyJointSplit(ctx context.Context, e *jointEntry, storeTags map[string]database.Tag) (int64, error) {
	joint, ok := storeTags[e.name]
	if !ok {
		return 0, fmt.Errorf("tag %q no longer in store", e.name)
	}

	ranked, err := r.runner.RankTags(ctx, reconcileDocID, e.components)
	if err != nil {
		return 0, fmt.Errorf("rank components: %w", err)
	}
	if len(ranked) != len(e.components) {
		return 0, fmt.Errorf("rank returned %d results for %d components", len(ranked), len(e.components))
	}
	for i := range ranked {
		candidates := ranked[i].Candidates[:0]
		for _, c := range ranked[i].Candidates {
			if c.Tag != e.name {
				candidates = append(candidates, c)
			}
		}
		ranked[i].Candidates = candidates
	}

	decisions := policy.Evaluate(e.components, ranked)
	outputs := make([]string, 0, len(decisions))
	seen := make(map[string]bool, len(decisions))
	for _, d := range decisions {
		name := d.Output
		if name == "" || seen[name] || name == e.name {
			continue
		}
		seen[name] = true
		outputs = append(outputs, name)
	}
	if len(outputs) == 0 {
		return 0, fmt.Errorf("no valid components after consolidation")
	}

	results, err := r.tag.Create(ctx, outputs)
	if err != nil {
		return 0, fmt.Errorf("create component tags: %w", err)
	}
	var componentIDs []int64
	for i, res := range results {
		switch res.Status {
		case service.Created, service.Conflict, service.Apply:
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
		r.logger.Error(nil, "tag reconcile: split %q: remove from store: %v", e.name, err)
	}
	return int64(len(docIDs)), nil
}
