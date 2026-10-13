package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/types"
)

func (r *Reconciler) applyPass(ctx context.Context, cfg *config.Config, worklist []*entry, report *Report, tagCounts map[string]int64, blocklist map[string]struct{}) {
	budget := cfg.Enricher.TagReconcile.MaxRetag
	appliedFlip := false
	var plannedKeepFlips []*entry
	var aliasFlips []*entry

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

		flipToAlias := e.freshVerdict == types.TagVerdicts.Same && e.verdict != types.TagVerdicts.Same
		flipToKeep := e.freshVerdict != types.TagVerdicts.Same && e.verdict == types.TagVerdicts.Same

		if !flipToAlias && !flipToKeep {
			if err := r.upsertVerdict(ctx, e); err != nil {
				r.logger.Error("", "tag reconcile: upsert verdict for %q → %q: %v", e.query, e.target, err)
			}
			continue
		}

		if report.ReportOnly {
			if flipToAlias {
				report.Flips.ToAlias++
				winner, loser := tags.CanonicalLoser(e.direction, e.query, e.target)
				report.PlannedAliases = append(report.PlannedAliases, PairEntry{
					Query:      loser,
					Target:     winner,
					Reassigned: tagCounts[loser],
					Reason:     e.reason,
				})
				r.logger.Info("", "tag reconcile (report-only): would alias %q onto %q (%d documents)%s", loser, winner, tagCounts[loser], reasonSuffix(e.reason))
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

		if flipToAlias {
			appliedFlip = true
			aliasFlips = append(aliasFlips, e)
			// the loser's full count is an upper bound on the batch's re-pointing
			_, loser := tags.CanonicalLoser(e.direction, e.query, e.target)
			budget -= int(tagCounts[loser])
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
			report.RetagEntries = append(report.RetagEntries, PairEntry{
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

	if len(aliasFlips) > 0 {
		r.applyAliasFlips(ctx, aliasFlips, report)
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
		report.PlannedKeepFlips = append(report.PlannedKeepFlips, PairEntry{
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

// applyAliasFlips collapses the confirmed pairs onto their winners in one alias plan: each loser's
// assignments re-point, its own aliases re-target, its tag row is removed and the loser becomes an
// alias row of the canonical — the names resolve and survive, never deleted and never blocklisted.
// The winner is the canonical the judged direction names, and an action the plan skipped is tallied
// under alias_skip and leaves no verdict behind.
func (r *Reconciler) applyAliasFlips(ctx context.Context, entries []*entry, report *Report) {
	actions := make([]tags.AliasAction, 0, len(entries))
	for _, e := range entries {
		winner, loser := tags.CanonicalLoser(e.direction, e.query, e.target)
		actions = append(actions, tags.AliasAction{
			AliasName:         loser,
			Canonical:         winner,
			RemoveExistingTag: true,
		})
	}
	outcomes, err := r.tag.ApplyAliasPlan(ctx, actions)
	if err != nil {
		r.logger.Error("", "tag reconcile: alias plan failed for %d pairs: %v", len(entries), err)
		for range entries {
			report.Skips["alias_error"]++
		}
		return
	}
	for i, e := range entries {
		if !outcomes[i].Applied {
			report.Skips["alias_skip"]++
			continue
		}
		winner, loser := tags.CanonicalLoser(e.direction, e.query, e.target)
		report.Flips.ToAlias++
		report.Aliases = append(report.Aliases, PairEntry{
			Query:      loser,
			Target:     winner,
			Reassigned: outcomes[i].Reassigned,
			Reason:     e.reason,
		})
		r.logger.Info("", "tag reconcile: aliased %q onto %q (%d documents)", loser, winner, outcomes[i].Reassigned)
		if err := r.upsertVerdict(ctx, e); err != nil {
			r.logger.Error("", "tag reconcile: upsert verdict after alias %q → %q: %v", e.query, e.target, err)
		}
	}
}

func (r *Reconciler) applyKeepFlip(ctx context.Context, e *entry, blocklist map[string]struct{}) (int, bool, error) {
	targetTag, ok := r.tagByName(ctx, e.target)
	if !ok {
		return 0, false, fmt.Errorf("target tag %q not in store", e.target)
	}

	if _, deleted := blocklist[e.query]; deleted {
		r.logDeletedTagKeepFlip(e)
		if err := r.upsertVerdict(ctx, e); err != nil {
			r.logger.Error("", "tag reconcile: upsert verdict for %q → %q: %v", e.query, e.target, err)
		}
		return 0, true, nil
	}

	docIDs := e.docIDs
	if len(docIDs) == 0 {
		var err error
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

	// a prior same verdict collapsed the query into the target as an alias; the fresh keep verdict
	// withdraws it so the re-created name is not shadowed by the alias
	if err := r.withdrawAlias(ctx, e, targetTag); err != nil {
		return 0, false, err
	}

	results, err := r.tag.Create(ctx, []string{e.query})
	if err != nil {
		return 0, false, fmt.Errorf("create query tag: %w", err)
	}
	var queryTag database.Tag
	aliased := false
	for _, res := range results {
		switch res.Status {
		case types.Created, types.Conflict:
			queryTag = res.Entity
		case types.Apply, types.Alias:
			aliased = true
		}
	}
	if queryTag.ID == 0 {
		if aliased {
			// the name resolves through an existing alias onto another canonical
			// the fresh verdict does not touch; record it and settle
			if err := r.upsertVerdict(ctx, e); err != nil {
				r.logger.Error("", "tag reconcile: upsert verdict for %q → %q: %v", e.query, e.target, err)
			}
			return 0, false, nil
		}
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

// withdrawAlias removes the alias row that collapsed the query name onto the target, if one exists:
// the fresh keep verdict means the name must stand on its own, and the recreate that follows would
// otherwise be shadowed by the alias. An alias onto another canonical is left alone, and a blocked
// name is handled before this point.
func (r *Reconciler) withdrawAlias(ctx context.Context, e *entry, targetTag database.Tag) error {
	queryName := textnorm.NormalizeTag(e.query)
	alias, err := r.client.Queries.GetTagAliasByName(ctx, queryName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up alias %q: %w", e.query, err)
	}
	if alias.CanonicalTagID != targetTag.ID {
		return nil
	}
	if _, err := r.client.Queries.DeleteTagAliasByName(ctx, queryName); err != nil {
		return fmt.Errorf("withdraw alias %q: %w", e.query, err)
	}
	r.logger.Info("", "tag reconcile: withdrew the alias %q (it was collapsed into %q)", e.query, e.target)
	return nil
}

func (r *Reconciler) logDeletedTagKeepFlip(e *entry) {
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
