package reconcile

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
)

const (
	rankBatchSize       = 256
	adjudicateBatchSize = 50
	reconcileDocID      = "tagreconcile"
)

type reciprocalEvidence struct {
	tag string
	sim float64
}

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

	targets := distinctTargets(worklist)
	targetEvidence := make(map[string]reciprocalEvidence, len(targets))
	for i := 0; i < len(targets); i += rankBatchSize {
		end := min(i+rankBatchSize, len(targets))
		batch := targets[i:end]
		ranked, err := r.runner.RankTags(ctx, reconcileDocID, batch)
		if err != nil {
			r.logger.Error("", "tag reconcile: target rank batch failed: %v", err)
			continue
		}
		for j, t := range batch {
			if j >= len(ranked) {
				continue
			}
			for _, c := range ranked[j].Candidates {
				if c.Tag == t {
					continue
				}
				targetEvidence[t] = reciprocalEvidence{tag: c.Tag, sim: c.Similarity}
				break
			}
		}
	}

	queryEvidence := make(map[string]reciprocalEvidence, len(worklist))
	for _, e := range worklist {
		for _, c := range e.trueRank.Candidates {
			if c.Tag == e.query {
				continue
			}
			queryEvidence[e.query] = reciprocalEvidence{tag: c.Tag, sim: c.Similarity}
			break
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

		synth := matcher.RankResult{KeptName: e.query}
		synth.Candidates = append(synth.Candidates, matcher.Candidate{Tag: target, Similarity: freshSim})
		for _, c := range rank.Candidates {
			if c.Tag == target || c.Tag == e.query {
				continue
			}
			synth.Candidates = append(synth.Candidates, c)
		}
		e.freshRank = synth

		decisions := policy.Evaluate([]string{e.query}, []matcher.RankResult{synth})
		if decisions[0].Reason == policy.ReasonBand {
			bandEntries = append(bandEntries, e)
		}
	}

	if len(bandEntries) > 0 {
		r.adjudicateBand(ctx, cfg, bandEntries, targetEvidence, queryEvidence)
	}
}

func (r *Reconciler) adjudicateBand(ctx context.Context, cfg *config.Config, bandEntries []*entry, targetEvidence, queryEvidence map[string]reciprocalEvidence) {
	adjudicatorModel := cfg.ResolvedAdjudicatorLlm().Model

	for i := 0; i < len(bandEntries); i += adjudicateBatchSize {
		end := min(i+adjudicateBatchSize, len(bandEntries))
		evidence := make([]analyzer.TagPairEvidence, 0, end-i)
		for _, e := range bandEntries[i:end] {
			ev := analyzer.TagPairEvidence{
				Query:  e.query,
				Target: e.target,
				Sim:    e.freshSim,
			}
			if len(e.freshRank.Candidates) > 1 {
				ev.RunnerUpSim = e.freshRank.Candidates[1].Similarity
			}
			if rec, ok := targetEvidence[e.target]; ok {
				ev.TargetRunnerUpTag = rec.tag
				ev.TargetRunnerUpSim = rec.sim
			}
			evidence = append(evidence, ev)
		}
		results, err := r.runner.AdjudicateTags(ctx, evidence)
		if err != nil {
			r.logger.Error("", "tag reconcile: adjudication batch failed: %v", err)
			for _, e := range bandEntries[i:end] {
				e.skipReason = "adjudication_error"
			}
			continue
		}
		for j, res := range results {
			e := bandEntries[i:end][j]
			if !res.Verdict.Valid() {
				e.skipReason = "ambiguous"
				continue
			}
			e.freshVerdict = res.Verdict
			e.freshModel = adjudicatorModel
			e.reason = "adjudicated"
		}
	}

	// Hysteresis: a fresh verdict differing from the stored one is confirmed
	// by a second, orientation-reversed pass
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
	if len(confirm) == 0 {
		return
	}

	for i := 0; i < len(confirm); i += adjudicateBatchSize {
		end := min(i+adjudicateBatchSize, len(confirm))
		evidence := make([]analyzer.TagPairEvidence, 0, end-i)
		for _, e := range confirm[i:end] {
			ev := analyzer.TagPairEvidence{
				Query:  e.target,
				Target: e.query,
				Sim:    e.freshSim,
			}
			if rec, ok := targetEvidence[e.target]; ok {
				ev.RunnerUpSim = rec.sim
			}
			if rec, ok := queryEvidence[e.query]; ok {
				ev.TargetRunnerUpTag = rec.tag
				ev.TargetRunnerUpSim = rec.sim
			}
			evidence = append(evidence, ev)
		}
		results, err := r.runner.AdjudicateTags(ctx, evidence)
		if err != nil {
			r.logger.Error("", "tag reconcile: confirmation batch failed: %v", err)
			for _, e := range confirm[i:end] {
				e.skipReason = "confirmation_error"
			}
			continue
		}
		for j, res := range results {
			e := confirm[i:end][j]
			if !res.Verdict.Valid() {
				e.skipReason = "confirmation_ambiguous"
				continue
			}
			if res.Verdict == e.freshVerdict {
				e.confirmed = true
			} else {
				e.skipReason = "confirmation_disagreement"
			}
		}
	}
}
