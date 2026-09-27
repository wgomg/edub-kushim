package reconcile

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/types"
)

func (r *Reconciler) applyPass(ctx context.Context, cfg *config.Config, worklist []*entry, report *Report, tagCounts map[string]int64, blocklist map[string]database.ListAllDeletedTagsRow) {
	budget := cfg.Enricher.TagReconcile.MaxRetag
	appliedFlip := false
	var plannedKeepFlips []*entry

	for i, e := range worklist {
		if e.skipReason != "" {
			report.Skips[e.skipReason]++
			continue
		}
		if !e.freshVerdict.Valid() {
			continue
		}
		report.Judged++
		if !e.confirmed {
			continue
		}
		report.Confirmed++

		flipToMerge := e.freshVerdict == types.TagVerdicts.Same && e.verdict != types.TagVerdicts.Same
		flipToKeep := e.freshVerdict != types.TagVerdicts.Same && e.verdict == types.TagVerdicts.Same

		if !flipToMerge && !flipToKeep {
			if err := r.upsertVerdict(ctx, e); err != nil {
				r.logger.Error("", "tag reconcile: upsert verdict for %q → %q: %v", e.query, e.target, err)
			}
			continue
		}

		if report.ReportOnly {
			if flipToMerge {
				report.Flips.ToMerge++
				report.PlannedMerges = append(report.PlannedMerges, MergeEntry{
					Query:      e.query,
					Target:     e.target,
					Reassigned: tagCounts[e.query],
					Reason:     e.reason,
				})
				r.logger.Info("", "tag reconcile (report-only): would merge %q into %q (%d documents)%s", e.query, e.target, tagCounts[e.query], reasonSuffix(e.reason))
			} else {
				plannedKeepFlips = append(plannedKeepFlips, e)
			}
			continue
		}

		if budget <= 0 && appliedFlip {
			for _, rest := range worklist[i+1:] {
				if rest.skipReason != "" {
					report.Skips[rest.skipReason]++
				} else {
					report.Capped++
				}
			}
			break
		}

		if flipToMerge {
			appliedFlip = true
			if err := r.applyMergeFlip(ctx, e, report); err != nil {
				r.logger.Error("", "tag reconcile: merge flip %q → %q: %v", e.query, e.target, err)
				continue
			}
			budget -= int(report.Merges[len(report.Merges)-1].Reassigned)
			continue
		}

		appliedFlip = true
		repointed, skipped, err := r.applyKeepFlip(ctx, e, blocklist)
		if err != nil {
			r.logger.Error("", "tag reconcile: keep flip for %q failed: %v", e.query, err)
			continue
		}
		if skipped {
			continue
		}
		report.Flips.ToKeep++
		report.Retags += repointed
		if repointed > 0 {
			report.RetagEntries = append(report.RetagEntries, MergeEntry{
				Query:      e.query,
				Target:     e.target,
				Reassigned: int64(repointed),
				Reason:     e.reason,
				CreatesTag: e.queryMissing,
			})
		} else {
			report.NoDocuments++
		}
		budget -= repointed
	}

	if len(plannedKeepFlips) > 0 {
		r.resolvePlannedKeepFlips(ctx, plannedKeepFlips, report)
	}
}

func (r *Reconciler) resolvePlannedKeepFlips(ctx context.Context, entries []*entry, report *Report) {
	var needCounts []*entry
	for _, e := range entries {
		if len(e.docIDs) == 0 {
			needCounts = append(needCounts, e)
		}
	}

	counts := map[string]int64{}
	if len(needCounts) > 0 {
		resolved, err := r.pairDocumentCounts(ctx, needCounts)
		if err != nil {
			r.logger.Error("", "tag reconcile: document counts for report-only keep flips: %v", err)
		} else {
			counts = resolved
		}
	}

	for _, e := range entries {
		reassigned := int64(len(e.docIDs))
		if reassigned == 0 {
			reassigned = counts[orderedPairKey(e.query, e.target)]
		}
		report.Flips.ToKeep++
		report.PlannedKeepFlips = append(report.PlannedKeepFlips, MergeEntry{
			Query:      e.query,
			Target:     e.target,
			Reassigned: reassigned,
			Reason:     e.reason,
			CreatesTag: e.queryMissing,
		})
		r.logPlannedKeepFlip(e, reassigned)
	}
}

type keepFlipOutcome int

const (
	keepFlipCreatedWithDocs keepFlipOutcome = iota
	keepFlipCreatedNoDocs
	keepFlipMovedWithDocs
	keepFlipMovedNoDocs
)

func keepFlipOutcomeOf(e *entry, documents int64) keepFlipOutcome {
	switch {
	case e.queryMissing && documents > 0:
		return keepFlipCreatedWithDocs
	case e.queryMissing:
		return keepFlipCreatedNoDocs
	case documents > 0:
		return keepFlipMovedWithDocs
	default:
		return keepFlipMovedNoDocs
	}
}

func (r *Reconciler) logPlannedKeepFlip(e *entry, reassigned int64) {
	suffix := reasonSuffix(e.reason)
	switch keepFlipOutcomeOf(e, reassigned) {
	case keepFlipCreatedWithDocs:
		r.logger.Info("", "tag reconcile (report-only): would create tag %q — it was being replaced with %q; %d documents would move back%s",
			e.query, e.target, reassigned, suffix)
	case keepFlipCreatedNoDocs:
		r.logger.Info("", "tag reconcile (report-only): would create tag %q — it was being replaced with %q; no documents to move%s",
			e.query, e.target, suffix)
	case keepFlipMovedWithDocs:
		r.logger.Info("", "tag reconcile (report-only): would move %d documents tagged %q back to %q%s",
			reassigned, e.target, e.query, suffix)
	case keepFlipMovedNoDocs:
		r.logger.Info("", "tag reconcile (report-only): would keep %q separate from %q; no documents to move%s",
			e.query, e.target, suffix)
	}
}

func (r *Reconciler) pairDocumentCounts(ctx context.Context, entries []*entry) (map[string]int64, error) {
	pairs := make([]tags.PairNames, 0, len(entries))
	counts := make(map[string]int64, len(entries))
	for _, e := range entries {
		pairs = append(pairs, tags.PairNames{Query: e.query, Target: e.target})
		counts[orderedPairKey(e.query, e.target)] = 0
	}

	stats, err := tags.NewStore(r.client.Queries).PairVerdictStats(ctx, pairs)
	if err != nil {
		return nil, err
	}
	for _, s := range stats {
		counts[orderedPairKey(s.Query, s.Target)] = s.Documents
	}
	return counts, nil
}

func (r *Reconciler) applyMergeFlip(ctx context.Context, e *entry, report *Report) error {
	queryTag, ok := r.tagByName(ctx, e.query)
	if !ok {
		return fmt.Errorf("query tag %q not in store", e.query)
	}
	targetTag, ok := r.tagByName(ctx, e.target)
	if !ok {
		return fmt.Errorf("target tag %q not in store", e.target)
	}
	res, err := r.merges.Merge(ctx, queryTag.ID, targetTag.ID)
	if err != nil {
		return err
	}
	report.Flips.ToMerge++
	report.Merges = append(report.Merges, MergeEntry{
		Query:      e.query,
		Target:     e.target,
		Reassigned: res.Reassigned,
		Reason:     e.reason,
	})
	r.logger.Info("", "tag reconcile: merged %q into %q (re-pointed %d documents)", e.query, e.target, res.Reassigned)
	if err := r.upsertVerdict(ctx, e); err != nil {
		r.logger.Error("", "tag reconcile: upsert verdict after merge %q → %q: %v", e.query, e.target, err)
	}
	return nil
}

func (r *Reconciler) applyKeepFlip(ctx context.Context, e *entry, blocklist map[string]database.ListAllDeletedTagsRow) (int, bool, error) {
	targetTag, ok := r.tagByName(ctx, e.target)
	if !ok {
		return 0, false, fmt.Errorf("target tag %q not in store", e.target)
	}

	if row, deleted := blocklist[e.query]; deleted {
		r.logDeletedTagKeepFlip(e, row)
		if err := r.upsertVerdict(ctx, e); err != nil {
			r.logger.Error("", "tag reconcile: upsert verdict for %q → %q: %v", e.query, e.target, err)
		}
		return 0, true, nil
	}

	docIDs := e.docIDs
	var err error
	if len(docIDs) == 0 {
		docIDs, err = r.client.DocumentIDsForPairs(ctx, database.DocumentIDsForPairsParams{
			Column1: []string{e.query},
			Column2: []string{e.target},
		})
		if err != nil {
			return 0, false, fmt.Errorf("document ids for pair: %w", err)
		}
	}
	if len(docIDs) == 0 {
		r.logKeepFlipResult(e, 0)
		if err := r.upsertVerdict(ctx, e); err != nil {
			r.logger.Error("", "tag reconcile: upsert verdict for %q → %q: %v", e.query, e.target, err)
		}
		return 0, false, nil
	}

	results, err := r.tag.Create(ctx, []string{e.query})
	if err != nil {
		return 0, false, fmt.Errorf("create query tag: %w", err)
	}
	var queryTag database.Tag
	for _, res := range results {
		if res.Status == types.Created || res.Status == types.Conflict {
			queryTag = res.Entity
		}
	}
	if queryTag.ID == 0 {
		return 0, false, fmt.Errorf("create query tag %q: invalid result", e.query)
	}

	tx, err := r.client.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	txQ := r.client.WithTx(tx)

	repointed, err := txQ.RepointDocumentTagsForDocs(ctx, database.RepointDocumentTagsForDocsParams{
		TargetID:    queryTag.ID,
		SourceID:    targetTag.ID,
		DocumentIds: docIDs,
	})
	if err != nil {
		return 0, false, fmt.Errorf("repoint document tags: %w", err)
	}
	if repointed > 0 {
		if err := txQ.ClearOrphanTagMarksByTagIDs(ctx, []int64{queryTag.ID}); err != nil {
			return 0, false, fmt.Errorf("clear orphan tag marks: %w", err)
		}
	}
	if _, err := txQ.UpsertFreshTagVerdict(ctx, database.UpsertFreshTagVerdictParams{
		DocumentID:    e.documentID,
		Query:         textnorm.NormalizeTag(e.query),
		Target:        textnorm.NormalizeTag(e.target),
		Sim:           e.freshSim,
		Verdict:       e.freshVerdict,
		VerdictModel:  sql.NullString{String: e.freshModel, Valid: e.freshModel != ""},
		PolicyVersion: policy.Version,
	}); err != nil {
		return 0, false, fmt.Errorf("upsert verdict: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("commit: %w", err)
	}

	r.logKeepFlipResult(e, repointed)
	return int(repointed), false, nil
}

func (r *Reconciler) logDeletedTagKeepFlip(e *entry, row database.ListAllDeletedTagsRow) {
	if row.Kind == curation.DeletedTagKindMerged {
		r.logger.Info("", "tag reconcile: skipping restoration of %q — it was merged into %q", e.query, row.TargetName.String)
		return
	}
	r.logger.Info("", "tag reconcile: skipping restoration of %q — the tag was deleted by an operator", e.query)
}

func (r *Reconciler) logKeepFlipResult(e *entry, repointed int64) {
	switch keepFlipOutcomeOf(e, repointed) {
	case keepFlipCreatedWithDocs:
		r.logger.Info("", "tag reconcile: created tag %q — it was being replaced with %q; re-pointed %d documents",
			e.query, e.target, repointed)
	case keepFlipCreatedNoDocs:
		r.logger.Info("", "tag reconcile: created tag %q — it was being replaced with %q; no documents to move",
			e.query, e.target)
	case keepFlipMovedWithDocs:
		r.logger.Info("", "tag reconcile: moved %d documents tagged %q back to %q", repointed, e.target, e.query)
	case keepFlipMovedNoDocs:
		r.logger.Info("", "tag reconcile: kept %q separate from %q; no documents to move", e.query, e.target)
	}
}

func (r *Reconciler) upsertVerdict(ctx context.Context, e *entry) error {
	if e.documentID == 0 {
		r.logger.Warn("", "tag reconcile: corrected decision for %q (kept separate from %q) not recorded — no document to attribute it to; the pair will be re-checked next run", e.query, e.target)
		return nil
	}
	_, err := r.client.UpsertFreshTagVerdict(ctx, database.UpsertFreshTagVerdictParams{
		DocumentID:    e.documentID,
		Query:         textnorm.NormalizeTag(e.query),
		Target:        textnorm.NormalizeTag(e.target),
		Sim:           e.freshSim,
		Verdict:       e.freshVerdict,
		VerdictModel:  sql.NullString{String: e.freshModel, Valid: e.freshModel != ""},
		PolicyVersion: policy.Version,
	})
	return err
}

func (r *Reconciler) tagByName(ctx context.Context, name string) (database.Tag, bool) {
	tag, err := r.tag.GetByName(ctx, name)
	if err != nil {
		return database.Tag{}, false
	}
	return tag, true
}

func orderedPairKey(a, b string) string {
	return a + "\x00" + b
}

func pairInStore(query, target string, storeTags map[string]database.Tag) bool {
	_, qok := storeTags[query]
	_, tok := storeTags[target]
	return qok && tok
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " reason=" + reason
}
