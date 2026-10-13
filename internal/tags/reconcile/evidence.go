package reconcile

import (
	"context"
	"errors"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/types"
)

const (
	rankBatchSize       = 256
	adjudicateBatchSize = 50
	reconcileDocID      = "tagreconcile"
)

func (r *Reconciler) evidencePass(ctx context.Context, cfg *config.Config, worklist []*entry) {
	for i := 0; i < len(worklist); i += rankBatchSize {
		end := min(i+rankBatchSize, len(worklist))
		queries := make([]string, 0, end-i)
		for _, e := range worklist[i:end] {
			queries = append(queries, e.query)
		}
		ranked, err := r.runner.RankTags(ctx, reconcileDocID, queries)
		if err != nil {
			r.logger.Error("", "tag reconcile: query rank batch failed: %v", err)
			for _, e := range worklist[i:end] {
				e.skipReason = "rank_error"
			}
			continue
		}
		for j, e := range worklist[i:end] {
			if j >= len(ranked) {
				e.skipReason = "rank_error"
				continue
			}
			e.trueRank = ranked[j]
		}
	}

	var bandEntries []*entry
	for _, e := range worklist {
		if e.skipReason != "" {
			continue
		}
		rank := e.trueRank
		if e.queryMissing && len(rank.Candidates) > 0 && rank.Candidates[0].Tag == e.query {
			e.skipReason = "query_reborn"
			continue
		}

		var freshSim float64
		target := e.target
		if target == "" {
			// Band-keep bootstrap entries carry no stored target; today's top
			// non-identity candidate is the pair's target
			for _, c := range rank.Candidates {
				if c.Tag == e.query {
					continue
				}
				target = c.Tag
				freshSim = c.Similarity
				break
			}
			if target == "" {
				e.skipReason = "below_floor"
				continue
			}
		} else {
			found := false
			for _, c := range rank.Candidates {
				if c.Tag == target {
					freshSim = c.Similarity
					found = true
					break
				}
			}
			if !found {
				e.skipReason = "below_floor"
				continue
			}
		}
		e.target = target
		e.freshSim = freshSim

		synth := matcher.RankResult{}
		synth.Candidates = append(synth.Candidates, matcher.Candidate{Tag: target, Similarity: freshSim})
		for _, c := range rank.Candidates {
			if c.Tag == target || c.Tag == e.query {
				continue
			}
			synth.Candidates = append(synth.Candidates, c)
		}

		decisions := policy.Evaluate([]string{e.query}, []matcher.RankResult{synth})
		if decisions[0].Reason == policy.ReasonBand {
			bandEntries = append(bandEntries, e)
		}
	}

	if len(bandEntries) > 0 {
		r.adjudicateBand(ctx, cfg, bandEntries)
	}
}

func (r *Reconciler) adjudicateBand(ctx context.Context, cfg *config.Config, bandEntries []*entry) {
	adjudicatorModel := cfg.ResolvedAdjudicatorLlm().Model

	judgedEntries := make([]*entry, 0, len(bandEntries))
	for _, e := range bandEntries {
		class := tags.VariantClass(e.query, e.target)
		if class == "" {
			judgedEntries = append(judgedEntries, e)
			continue
		}
		e.freshVerdict = types.TagVerdicts.Same
		// verdict_model is the fresh-verdict arbiter, so a non-null provenance marker keeps the pair settled
		e.freshModel = "deterministic"
		e.reason = "deterministic"
		r.logger.Info("", "tag reconcile: deterministic %s variant %q ~ %q resolved same", class, e.query, e.target)
	}

	for i := 0; i < len(judgedEntries); i += adjudicateBatchSize {
		end := min(i+adjudicateBatchSize, len(judgedEntries))
		evidence := make([]analyzer.TagPairEvidence, 0, end-i)
		for _, e := range judgedEntries[i:end] {
			evidence = append(evidence, analyzer.TagPairEvidence{Query: e.query, Target: e.target})
		}
		results, err := r.runner.AdjudicateTags(ctx, evidence)
		if err != nil {
			var incomplete *analyzer.IncompleteReplyError
			if !errors.As(err, &incomplete) {
				r.logger.Error("", "tag reconcile: adjudication batch failed: %v", err)
				for _, e := range judgedEntries[i:end] {
					e.skipReason = "adjudication_error"
				}
				continue
			}
			missing := incomplete.MissingSet()
			batch := judgedEntries[i:end]
			salvaged, deferred := 0, 0
			for j := range batch {
				e := batch[j]
				if missing[j] {
					e.skipReason = "ambiguous"
					deferred++
					continue
				}
				e.freshVerdict = results[j].Verdict
				e.freshModel = adjudicatorModel
				e.reason = "adjudicated"
				salvaged++
			}
			r.logger.Warn("", "tag reconcile: partial adjudication reply, salvaged %d of %d pairs and deferred %d", salvaged, len(batch), deferred)
			continue
		}
		for j, res := range results {
			e := judgedEntries[i:end][j]
			e.freshVerdict = res.Verdict
			e.freshModel = adjudicatorModel
			e.reason = "adjudicated"
		}
	}

	// Hysteresis: a fresh verdict differing from the stored one is confirmed by a second, orientation-reversed
	// pass, except a deterministic variant pair, which its own class confirms without a second judge call
	var confirm []*entry
	for _, e := range bandEntries {
		if e.skipReason != "" || !e.freshVerdict.Valid() {
			continue
		}
		if e.freshVerdict == e.verdict {
			e.confirmed = true
			continue
		}
		confirm = append(confirm, e)
	}
	toConfirm := make([]*entry, 0, len(confirm))
	for _, e := range confirm {
		class := tags.VariantClass(e.target, e.query)
		if class == "" {
			toConfirm = append(toConfirm, e)
			continue
		}
		e.confirmed = true
		r.logger.Info("", "tag reconcile: deterministic %s variant %q ~ %q confirmed", class, e.query, e.target)
	}
	if len(toConfirm) == 0 {
		return
	}

	for i := 0; i < len(toConfirm); i += adjudicateBatchSize {
		end := min(i+adjudicateBatchSize, len(toConfirm))
		evidence := make([]analyzer.TagPairEvidence, 0, end-i)
		for _, e := range toConfirm[i:end] {
			evidence = append(evidence, analyzer.TagPairEvidence{Query: e.target, Target: e.query})
		}
		results, err := r.runner.AdjudicateTags(ctx, evidence)
		if err != nil {
			var incomplete *analyzer.IncompleteReplyError
			if !errors.As(err, &incomplete) {
				r.logger.Error("", "tag reconcile: confirmation batch failed: %v", err)
				for _, e := range toConfirm[i:end] {
					e.skipReason = "confirmation_error"
				}
				continue
			}
			missing := incomplete.MissingSet()
			batch := toConfirm[i:end]
			salvaged, deferred := 0, 0
			for j := range batch {
				e := batch[j]
				if missing[j] {
					e.skipReason = "confirmation_ambiguous"
					deferred++
					continue
				}
				if results[j].Verdict == e.freshVerdict {
					e.confirmed = true
				} else {
					e.skipReason = "confirmation_disagreement"
				}
				salvaged++
			}
			r.logger.Warn("", "tag reconcile: partial confirmation reply, salvaged %d of %d pairs and deferred %d", salvaged, len(batch), deferred)
			continue
		}
		for j, res := range results {
			e := toConfirm[i:end][j]
			if res.Verdict == e.freshVerdict {
				e.confirmed = true
			} else {
				e.skipReason = "confirmation_disagreement"
			}
		}
	}
}
