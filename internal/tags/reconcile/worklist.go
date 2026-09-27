package reconcile

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/types"
)

const (
	verdictMaxAge     = 90 * 24 * time.Hour
	driftMinOverlap   = 5
	driftOverlapRatio = 0.5
)

const (
	catStale    = "stale"
	catUnjudged = "unjudged"
	catDrifted  = "drifted"
	catAged     = "aged"
)

var catOrder = map[string]int{
	catStale:    0,
	catUnjudged: 1,
	catDrifted:  2,
	catAged:     3,
}

func (r *Reconciler) buildWorklist(ctx context.Context, cfg *config.Config, storeTags map[string]database.Tag, tagCounts map[string]int64, report *Report) ([]*entry, int, error) {
	var worklist []*entry
	collected := make(map[string]bool)
	invalidated := 0

	scan1, err := r.client.TagVerdictPairsWithoutFreshVerdict(ctx, policy.Version)
	if err != nil {
		return nil, 0, fmt.Errorf("tag verdict pairs without fresh verdict: %w", err)
	}

	missing, err := r.client.TagVerdictPairsWithMissingTags(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("tag verdict pairs with missing tags: %w", err)
	}
	var deadQueryPairs []database.TagVerdictPairsWithMissingTagsRow
	var deadTargetPairs [][2]string
	for _, row := range missing {
		if row.TargetMissing {
			deadTargetPairs = append(deadTargetPairs, [2]string{row.Query, row.Target})
		} else {
			deadQueryPairs = append(deadQueryPairs, row)
		}
	}

	if len(deadTargetPairs) > 0 {
		invalidated += len(deadTargetPairs)
		if !report.ReportOnly {
			queries := make([]string, 0, len(deadTargetPairs))
			targets := make([]string, 0, len(deadTargetPairs))
			for _, p := range deadTargetPairs {
				queries = append(queries, p[0])
				targets = append(targets, p[1])
			}
			if _, err := r.client.DeleteFreshTagVerdictsForPairs(ctx, database.DeleteFreshTagVerdictsForPairsParams{
				Column1: queries,
				Column2: targets,
			}); err != nil {
				r.logger.Error("", "tag reconcile: delete fresh verdicts for %d dead-target pairs: %v", len(deadTargetPairs), err)
			}
		}
		for _, p := range deadTargetPairs {
			report.InvalidatedEntries = append(report.InvalidatedEntries, MergeEntry{Query: p[0], Target: p[1]})
			r.logger.Info("", "tag reconcile: discarding cached decision %q → %q — the target tag no longer exists", p[0], p[1])
		}
	}

	if len(deadQueryPairs) > 0 {
		queries := make([]string, 0, len(deadQueryPairs))
		targets := make([]string, 0, len(deadQueryPairs))
		for _, row := range deadQueryPairs {
			queries = append(queries, row.Query)
			targets = append(targets, row.Target)
		}
		latestRows, err := r.client.LatestTagVerdictRowsForPairs(ctx, database.LatestTagVerdictRowsForPairsParams{
			Column1: queries,
			Column2: targets,
		})
		if err != nil {
			return nil, 0, fmt.Errorf("latest tag verdict rows for dead-query pairs: %w", err)
		}
		latestByPair := make(map[string]database.LatestTagVerdictRowsForPairsRow, len(latestRows))
		for _, row := range latestRows {
			latestByPair[orderedPairKey(row.Query, row.Target)] = row
		}
		for _, row := range deadQueryPairs {
			key := orderedPairKey(row.Query, row.Target)
			latest, ok := latestByPair[key]
			if !ok {
				continue
			}
			if latest.Verdict == types.TagVerdicts.Same {
				r.logger.Info("", "tag reconcile: skipping %q → %q — %q no longer exists and its documents are already tagged %q", row.Query, row.Target, row.Query, row.Target)
				continue
			}
			worklist = append(worklist, &entry{
				query:         row.Query,
				target:        row.Target,
				sim:           latest.Sim,
				verdict:       latest.Verdict,
				verdictModel:  latest.VerdictModel,
				policyVersion: latest.PolicyVersion,
				verdictAt:     latest.VerdictAt,
				documentID:    latest.DocumentID,
				category:      catStale,
				queryMissing:  true,
			})
			collected[key] = true
		}
	}

	for _, row := range scan1 {
		key := orderedPairKey(row.Query, row.Target)
		if collected[key] {
			continue
		}
		if !pairInStore(row.Query, row.Target, storeTags) {
			continue
		}
		cat := catUnjudged
		if row.PolicyVersion != policy.Version {
			cat = catStale
		}
		worklist = append(worklist, &entry{
			query:         row.Query,
			target:        row.Target,
			sim:           row.Sim,
			verdict:       row.Verdict,
			verdictModel:  row.VerdictModel,
			policyVersion: row.PolicyVersion,
			verdictAt:     row.VerdictAt,
			documentID:    row.DocumentID,
			category:      cat,
		})
		collected[key] = true
	}

	aged, err := r.client.TagVerdictPairsOlderThan(ctx, database.TagVerdictPairsOlderThanParams{
		PolicyVersion: policy.Version,
		VerdictAt:     time.Now().Add(-verdictMaxAge),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("tag verdict pairs older than: %w", err)
	}
	for _, row := range aged {
		key := orderedPairKey(row.Query, row.Target)
		if collected[key] {
			continue
		}
		if !pairInStore(row.Query, row.Target, storeTags) {
			continue
		}
		worklist = append(worklist, &entry{
			query:         row.Query,
			target:        row.Target,
			sim:           row.Sim,
			verdict:       row.Verdict,
			verdictModel:  row.VerdictModel,
			policyVersion: row.PolicyVersion,
			verdictAt:     row.VerdictAt,
			documentID:    row.DocumentID,
			category:      catAged,
		})
		collected[key] = true
	}

	kept, err := r.client.TagVerdictPairsByVerdict(ctx, policy.Version)
	if err != nil {
		return nil, 0, fmt.Errorf("tag verdict pairs by verdict: %w", err)
	}
	if len(kept) > 0 {
		pairs := make([]tags.PairNames, 0, len(kept)*2)
		for _, row := range kept {
			pairs = append(pairs, tags.PairNames{Query: row.Query, Target: row.Target},
				tags.PairNames{Query: row.Target, Target: row.Query})
		}
		stats, err := tags.NewStore(r.client.Queries).PairVerdictStats(ctx, pairs)
		if err != nil {
			return nil, 0, err
		}
		overlap := make(map[string]int64, len(stats))
		for _, s := range stats {
			key := orderedPairKey(s.Query, s.Target)
			if s.Documents > overlap[key] {
				overlap[key] = s.Documents
			}
		}
		for _, row := range kept {
			key := orderedPairKey(row.Query, row.Target)
			if collected[key] {
				continue
			}
			if !pairInStore(row.Query, row.Target, storeTags) {
				continue
			}
			ov := overlap[key]
			minCount := min(tagCounts[row.Query], tagCounts[row.Target])
			if ov < driftMinOverlap || float64(ov) < driftOverlapRatio*float64(minCount) {
				continue
			}
			worklist = append(worklist, &entry{
				query:         row.Query,
				target:        row.Target,
				sim:           row.Sim,
				verdict:       row.Verdict,
				verdictModel:  row.VerdictModel,
				policyVersion: row.PolicyVersion,
				verdictAt:     row.VerdictAt,
				documentID:    row.DocumentID,
				category:      catDrifted,
			})
			collected[key] = true
		}
	}

	r.bootstrapWorklist(ctx, cfg, &worklist, collected)

	filtered := worklist[:0]
	for _, e := range worklist {
		if textnorm.SplitJointTag(e.query) != nil {
			r.logger.Debug("", "tag reconcile: dropping pair %q → %q — %q joins concepts with a conjunction and is split apart, not merged", e.query, e.target, e.query)
			continue
		}
		filtered = append(filtered, e)
	}
	worklist = filtered

	sort.SliceStable(worklist, func(i, j int) bool {
		ci, cj := catOrder[worklist[i].category], catOrder[worklist[j].category]
		if ci != cj {
			return ci < cj
		}
		return worklist[i].verdictAt.Before(worklist[j].verdictAt)
	})

	for _, e := range worklist {
		switch e.category {
		case catStale:
			report.Worklist.Stale++
		case catUnjudged:
			report.Worklist.Unjudged++
		case catDrifted:
			report.Worklist.Drifted++
		case catAged:
			report.Worklist.Aged++
		}
	}

	return worklist, invalidated, nil
}

func (r *Reconciler) bootstrapWorklist(ctx context.Context, cfg *config.Config, worklist *[]*entry, collected map[string]bool) {
	parsed := parseBootstrapLogs(cfg.App.ConfigDir+"/logs", r.logger)
	if len(parsed) == 0 {
		return
	}

	var pairQueries, pairTargets []string
	var bandQueries []string
	for _, p := range parsed {
		if p.target != "" {
			pairQueries = append(pairQueries, p.query)
			pairTargets = append(pairTargets, p.target)
		} else {
			bandQueries = append(bandQueries, p.query)
		}
	}

	presentPairs := make(map[string]bool)
	if len(pairQueries) > 0 {
		pairs := make([]tags.PairNames, 0, len(pairQueries))
		for i := range pairQueries {
			pairs = append(pairs, tags.PairNames{Query: pairQueries[i], Target: pairTargets[i]})
		}
		stats, err := tags.NewStore(r.client.Queries).PairVerdictStats(ctx, pairs)
		if err != nil {
			r.logger.Error("", "tag reconcile bootstrap: pair presence check failed: %v", err)
			return
		}
		for _, s := range stats {
			presentPairs[orderedPairKey(s.Query, s.Target)] = true
		}
	}
	presentQueries := make(map[string]bool)
	if len(bandQueries) > 0 {
		rows, err := r.client.TagVerdictQueriesWithRows(ctx, bandQueries)
		if err != nil {
			r.logger.Error("", "tag reconcile bootstrap: query presence check failed: %v", err)
			return
		}
		for _, q := range rows {
			presentQueries[q] = true
		}
	}

	docIDs := r.resolveDocIDs(ctx, parsed)

	for _, p := range parsed {
		if p.target != "" {
			if presentPairs[orderedPairKey(p.query, p.target)] {
				continue
			}
		} else if presentQueries[p.query] {
			continue
		}
		key := orderedPairKey(p.query, p.target)
		if collected[key] {
			continue
		}
		e := &entry{
			query:        p.query,
			target:       p.target,
			sim:          p.sim,
			verdict:      p.verdict,
			verdictAt:    p.lastTime,
			documentID:   lastDocID(docIDs[p.query]),
			docIDs:       docIDs[p.query],
			queryMissing: p.absorbed,
			category:     catUnjudged,
		}
		if p.absorbed {
			e.verdict = types.TagVerdicts.Same
			e.category = catStale
		} else if p.verdict.Valid() {
			e.category = catStale
		}
		if e.target == "" {
			e.queryMissing = false
		}
		*worklist = append(*worklist, e)
		collected[key] = true
	}
}

func (r *Reconciler) resolveDocIDs(ctx context.Context, parsed map[string]*parsedPair) map[string][]int64 {
	var all []string
	for _, p := range parsed {
		all = append(all, p.docIDs...)
	}
	if len(all) == 0 {
		return nil
	}
	rows, err := r.client.GetDocumentIDsByDocumentIDs(ctx, all)
	if err != nil {
		r.logger.Error("", "tag reconcile bootstrap: resolve document ids: %v", err)
		return nil
	}
	idByDocID := make(map[string]int64, len(rows))
	for _, row := range rows {
		idByDocID[row.DocumentID] = row.ID
	}
	out := make(map[string][]int64, len(parsed))
	for q, p := range parsed {
		for _, d := range p.docIDs {
			if id, ok := idByDocID[d]; ok {
				out[q] = append(out[q], id)
			}
		}
	}
	return out
}

func lastDocID(ids []int64) int64 {
	if len(ids) == 0 {
		return 0
	}
	return ids[len(ids)-1]
}

func distinctTargets(worklist []*entry) []string {
	seen := make(map[string]bool, len(worklist))
	var out []string
	for _, e := range worklist {
		if e.target == "" || seen[e.target] {
			continue
		}
		seen[e.target] = true
		out = append(out, e.target)
	}
	return out
}
