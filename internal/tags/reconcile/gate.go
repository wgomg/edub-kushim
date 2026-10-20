package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
)

// hasDueWork answers whether any reconcile arm has work this pass. The evidence categories carry time-driven
// work (aged verdicts) and capped remainders that a clear signal does not cover, the grace sweeps carry
// windows that elapse without a mutation, the compound and twin worklists stand until their work is applied,
// and the bootstrap log ingest can owe work the evidence does not hold. Every probe is a superset of its
// arm's work set: a false positive costs a full pass, a false negative would skip real work.
func (r *Reconciler) hasDueWork(ctx context.Context, cfg *config.Config) (bool, error) {
	q := r.client.Queries

	pending, err := q.TagVerdictPendingPairProbe(ctx, policy.Version)
	if err != nil {
		return false, fmt.Errorf("probe pairs without a fresh verdict: %w", err)
	}
	if pending {
		return true, nil
	}

	missing, err := q.TagVerdictMissingTagProbe(ctx)
	if err != nil {
		return false, fmt.Errorf("probe verdict pairs with missing tags: %w", err)
	}
	if missing {
		return true, nil
	}

	aged, err := q.TagVerdictAgedPairProbe(ctx, database.TagVerdictAgedPairProbeParams{
		PolicyVersion: policy.Version,
		VerdictBefore: time.Now().Add(-verdictMaxAge),
	})
	if err != nil {
		return false, fmt.Errorf("probe aged verdict pairs: %w", err)
	}
	if aged {
		return true, nil
	}

	drifted, err := q.TagVerdictDriftProbe(ctx, database.TagVerdictDriftProbeParams{
		PolicyVersion: policy.Version,
		MinOverlap:    driftMinOverlap,
		OverlapRatio:  driftOverlapRatio,
	})
	if err != nil {
		return false, fmt.Errorf("probe drifted verdict pairs: %w", err)
	}
	if drifted {
		return true, nil
	}

	if parsed := parseBootstrapLogs(bootstrapLogDir(cfg), r.logger); len(parsed) > 0 {
		presentPairs, presentQueries, err := r.bootstrapPresence(ctx, parsed)
		if err != nil {
			// the ingest arm logs this failure and continues without its entries, so the probe cannot rule out
			// work: report the pass as due and let the arm degrade the same way
			r.logger.Error("", "tag reconcile: bootstrap presence probe failed: %v", err)
			return true, nil
		}
		for _, p := range parsed {
			if p.target != "" && !presentPairs[orderedPairKey(p.query, p.target)] {
				return true, nil
			}
			if p.target == "" && !presentQueries[p.query] {
				return true, nil
			}
		}
	}

	compoundWork, err := r.hasCompoundWork(ctx)
	if err != nil {
		return false, err
	}
	if compoundWork {
		return true, nil
	}

	worklists, err := tags.NewStore(q).LatestWorklists(ctx)
	if err != nil {
		return false, fmt.Errorf("probe measurement worklists: %w", err)
	}
	if !worklists.Absent && len(worklists.Worklists.Twins) > 0 {
		return true, nil
	}

	orphans, err := q.OrphanTagProbe(ctx, time.Now().Add(-graceWindow(cfg.Enricher.TagReconcile.DeadTagGraceDays)))
	if err != nil {
		return false, fmt.Errorf("probe orphaned tags: %w", err)
	}
	if orphans {
		return true, nil
	}

	provisional, err := q.ProvisionalTagProbe(ctx, time.Now().Add(-graceWindow(cfg.Enricher.TagReconcile.ProvisionalGraceDays)))
	if err != nil {
		return false, fmt.Errorf("probe provisional tags: %w", err)
	}
	if provisional {
		return true, nil
	}

	decayed, err := q.PromotedTagMassDecayProbe(ctx, tags.RecurrenceFloor)
	if err != nil {
		return false, fmt.Errorf("probe demoted promoted tags: %w", err)
	}
	if decayed {
		return true, nil
	}

	rejudge, err := q.PromotedTagRejudgeProbe(ctx, analyzer.TagQualityVersion)
	if err != nil {
		return false, fmt.Errorf("probe promoted tags without a current verdict: %w", err)
	}
	return rejudge, nil
}

// hasCompoundWork probes the stored-compound arm through the same membership rules the arm applies: any
// non-seeded, non-period compound that owes an application, a demotion, or a judgment.
func (r *Reconciler) hasCompoundWork(ctx context.Context) (bool, error) {
	rows, err := r.client.Queries.ListStoredCompoundCandidates(ctx)
	if err != nil {
		return false, fmt.Errorf("probe stored compound candidates: %w", err)
	}
	names := make([]string, 0, len(rows))
	promoted := make(map[string]bool, len(rows))
	for _, row := range rows {
		if tags.ClassifyShape(row.Name) != tags.ShapeCompound || tags.IsPeriodClass(tags.HouseForm(row.Name)) {
			continue
		}
		names = append(names, row.Name)
		promoted[row.Name] = row.PromotedAt.Valid
	}
	if len(names) == 0 {
		return false, nil
	}

	held, err := tags.IdentityHeldNames(ctx, r.client.Queries, names)
	if err != nil {
		return false, fmt.Errorf("probe compound identity holds: %w", err)
	}
	compositionHeld, states, err := tags.CompositionReview(ctx, r.client.Queries, names, analyzer.TagSplitVersion)
	if err != nil {
		return false, fmt.Errorf("probe compound composition states: %w", err)
	}
	for _, name := range names {
		if entry, _ := compoundEntryFor(name, promoted[name], 0, held, compositionHeld, states); entry != nil {
			return true, nil
		}
	}
	return false, nil
}

// graceWindow converts a configured grace period in days to the age offset a cutoff subtracts from now.
func graceWindow(days int) time.Duration {
	return time.Duration(days) * 24 * time.Hour
}
