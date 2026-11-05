package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/tools"
)

// ReaskReport counts one split re-ask pass: the eligible abstentions it selected, the ones the judge
// decided on the second ask, the ones it deferred again, and the ones it could not ask.
type ReaskReport struct {
	PartValues int `json:"part_values"`
	Compounds  int `json:"compounds"`
	Decided    int `json:"decided"`
	Deferred   int `json:"deferred"`
	Failed     int `json:"failed"`
}

// ReaskRunner re-asks the split gate's cached abstentions so a single noisy deferral cannot withhold a
// compound forever. It supersedes a stale abstention by deleting its cached row and re-running the
// standing write path, so a decided answer lands under the same guards and versions the gate reads.
type ReaskRunner struct {
	getConfig func() *config.Config
	logger    *log.Logger
	client    *database.Client
	runner    *tools.Runner
}

// NewReaskRunner wires the pass to the dynamic configuration, the database, the judge runner and the
// logger.
func NewReaskRunner(getConfig func() *config.Config, logger *log.Logger, client *database.Client, runner *tools.Runner) *ReaskRunner {
	return &ReaskRunner{getConfig: getConfig, logger: logger, client: client, runner: runner}
}

// Reask re-asks up to budget eligible abstentions: stale part values and stale compound candidates are
// re-judged through the verdict store, which supersedes the stale abstention atomically — the old defer row
// is replaced by whatever the judge answers, in one transaction. A decided answer is what releases the
// compounds that abstention withheld; a fresh abstention is re-cached and keeps withholding; a value the
// judge leaves unanswered keeps its cached abstention. An abstention is stale after the configured
// reask_defer_days, so a high-volume instance can lower the window; while younger than that window a part
// value is also eligible when it withholds more than one compound it was in force for. Nothing runs when
// the adjudicator is disabled or absent.
func (r *ReaskRunner) Reask(ctx context.Context, budget int) (*ReaskReport, error) {
	report := &ReaskReport{}
	if budget <= 0 {
		return report, nil
	}
	cfg := r.getConfig()
	if !cfg.Enricher.TagAdjudicator.Enabled || !r.runner.HasTagAdjudicator() {
		return report, nil
	}

	staleBefore := time.Now().Add(-time.Duration(cfg.Enricher.TagReconcile.ReaskDeferDays) * 24 * time.Hour)
	queries := r.client.Queries
	promotedNames, err := queries.ListPromotedTagNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("list promoted tag names: %w", err)
	}
	store := admission.NewVerdictStore(
		queries,
		r.runner,
		r.logger,
		cfg.ResolvedAdjudicatorLlm().Model,
		tags.PromotedKeys(tags.StoreNames(promotedNames)),
	)

	partValues, err := queries.ListRetentionStaleDeferredPartValues(ctx, database.ListRetentionStaleDeferredPartValuesParams{
		PolicyVersion: analyzer.TagPartDimensionVersion,
		StaleBefore:   staleBefore,
		PageLimit:     int32(budget),
	})
	if err != nil {
		return nil, fmt.Errorf("list retention-stale deferred part values: %w", err)
	}
	if len(partValues) < budget {
		young, err := queries.ListYoungDeferredPartValues(ctx, database.ListYoungDeferredPartValuesParams{
			PolicyVersion:        analyzer.TagPartDimensionVersion,
			StaleBefore:          staleBefore,
			SplitCriteriaVersion: analyzer.TagSplitVersion,
			PageLimit:            int32(budget - len(partValues)),
		})
		if err != nil {
			return nil, fmt.Errorf("list multi-compound deferred part values: %w", err)
		}
		partValues = append(partValues, young...)
	}
	report.PartValues = len(partValues)
	if len(partValues) > 0 {
		verdicts, err := store.SupersedeParts(ctx, r.client, partValues, 0, "")
		if err != nil {
			return nil, err
		}
		for _, verdict := range verdicts {
			if verdict.Deferred {
				report.Deferred++
			} else {
				report.Decided++
			}
		}
		report.Failed += len(partValues) - len(verdicts)
	}

	candidates, err := queries.ListEligibleDeferredCompoundCandidates(ctx, database.ListEligibleDeferredCompoundCandidatesParams{
		SplitCriteriaVersion: analyzer.TagSplitVersion,
		StaleBefore:          staleBefore,
		PageLimit:            int32(budget),
	})
	if err != nil {
		return nil, fmt.Errorf("list eligible deferred compound candidates: %w", err)
	}
	report.Compounds = len(candidates)
	if len(candidates) > 0 {
		verdicts, err := store.SupersedeCompounds(ctx, r.client, candidates, 0, "")
		if err != nil {
			return nil, err
		}
		for _, verdict := range verdicts {
			if verdict.Deferred {
				report.Deferred++
			} else {
				report.Decided++
			}
		}
		report.Failed += len(candidates) - len(verdicts)
	}
	return report, nil
}

// ReaskTaskHandler runs one split re-ask pass as a blocking task; the report it returns is stored as the
// task result. It runs under the maintenance lock the caller holds, like the other tag passes.
type ReaskTaskHandler struct {
	runner *ReaskRunner
}

// NewReaskTaskHandler wires the handler to the re-ask pass.
func NewReaskTaskHandler(runner *ReaskRunner) *ReaskTaskHandler {
	return &ReaskTaskHandler{runner: runner}
}

// Handle runs the pass with the configured re-judge budget and returns the serialized report.
func (h *ReaskTaskHandler) Handle(ctx context.Context, _ task.Task) (json.RawMessage, error) {
	report, err := h.runner.Reask(ctx, h.runner.getConfig().Enricher.TagReconcile.MaxRejudge)
	if err != nil {
		return nil, fmt.Errorf("tag split reask: %w", err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal tag split reask report: %w", err)
	}
	h.runner.logger.Info("", "tag split reask run: parts=%d compounds=%d decided=%d deferred=%d failed=%d",
		report.PartValues, report.Compounds, report.Decided, report.Deferred, report.Failed)
	return raw, nil
}
