// Package reconcile is the tag store's scheduled maintenance: it sweeps
// the recorded verdict evidence for stale, unjudged and drifted pairs,
// re-ranks and re-adjudicates what changed, and applies or plans merges,
// keep flips, joint splits and provisional-tag outcomes through the
// curation surface. It is the scheduled reconciliation flow of
// docs/tags/consolidation.md, the machine-driven complement of the human
// worklist review.
package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Reconciler is the scheduled tag-store maintainer: it re-judges stale
// verdict evidence, flips decisions whose evidence changed, plans and
// applies merges and joint splits, and settles provisional tags, capped
// per run and report-only when configured.
type Reconciler struct {
	getConfig func() *config.Config
	client    *database.Client
	tag       *curation.Tag
	merges    *curation.TagMerge
	runner    *tools.Runner
	logger    *log.Logger
}

// NewReconciler wires the reconciler: the dynamic config, the database,
// the curation surfaces it writes through, the tools runner, and the
// logger.
func NewReconciler(getConfig func() *config.Config, client *database.Client,
	tag *curation.Tag, merges *curation.TagMerge,
	runner *tools.Runner, logger *log.Logger) *Reconciler {
	return &Reconciler{
		getConfig: getConfig,
		client:    client,
		tag:       tag,
		merges:    merges,
		runner:    runner,
		logger:    logger,
	}
}

// Report summarizes one reconcile run: the worklist by category, the
// judged, confirmed and flipped counts, the applied and planned merges,
// keep flips, retags, invalidations and joint splits, and the
// provisional-tag outcomes.
type Report struct {
	ReportOnly          bool                      `json:"report_only"`
	Worklist            WorklistCounts            `json:"worklist"`
	Judged              int                       `json:"judged"`
	Confirmed           int                       `json:"confirmed"`
	Flips               FlipCounts                `json:"flips"`
	Merges              []MergeEntry              `json:"merges"`
	Retags              int                       `json:"retags"`
	Invalidated         int                       `json:"invalidated"`
	Skips               map[string]int            `json:"skips"`
	PlannedMerges       []MergeEntry              `json:"planned_merges,omitempty"`
	PlannedKeepFlips    []MergeEntry              `json:"planned_keep_flips,omitempty"`
	RetagEntries        []MergeEntry              `json:"retag_entries,omitempty"`
	InvalidatedEntries  []MergeEntry              `json:"invalidated_entries,omitempty"`
	JointSplits         []JointSplitEntry         `json:"joint_splits,omitempty"`
	PlannedJointSplits  []JointSplitEntry         `json:"planned_joint_splits,omitempty"`
	Capped              int                       `json:"capped,omitempty"`
	NoDocuments         int                       `json:"no_documents,omitempty"`
	Swept               int                       `json:"swept,omitempty"`
	AdjudicatorDisabled bool                      `json:"adjudicator_disabled,omitempty"`
	Provisional         ProvisionalCounts         `json:"provisional"`
	Identity            *admission.IdentityReport `json:"identity,omitempty"`
}

// ProvisionalCounts tallies what the run did with provisional tags:
// worklist hits, zero-assignment outcomes, promotions, rejudgements,
// deferrals, judge misses, skips and failures.
type ProvisionalCounts struct {
	Worklist   int `json:"worklist"`
	ZeroAssign int `json:"zero_assignment"`
	Promoted   int `json:"promoted"`
	Rejudged   int `json:"rejudged"`
	Deferred   int `json:"deferred"`
	NoJudge    int `json:"no_judge"`
	Skipped    int `json:"skipped"`
	Failed     int `json:"failed"`
}

// WorklistCounts counts the worklist entries by category (stale,
// unjudged, drifted, aged) plus the joint-tag splits.
type WorklistCounts struct {
	Stale    int `json:"stale"`
	Unjudged int `json:"unjudged"`
	Drifted  int `json:"drifted"`
	Aged     int `json:"aged"`
	Joint    int `json:"joint"`
}

// JointSplitEntry is one planned or applied joint-tag split: the query,
// its approved components, how many documents were reassigned, and the
// reason when it was not applied.
type JointSplitEntry struct {
	Query      string   `json:"query"`
	Components []string `json:"components"`
	Reassigned int64    `json:"reassigned"`
	Reason     string   `json:"reason,omitempty"`
}

// FlipCounts counts the evidence-driven direction flips: decisions that
// became merges and keeps that flipped back.
type FlipCounts struct {
	ToMerge int `json:"to_merge"`
	ToKeep  int `json:"to_keep"`
}

// MergeEntry is one planned or applied merge: the query, its target, how
// many documents applying it reassigned, the reason, and whether applying
// it would first create the target tag.
type MergeEntry struct {
	Query      string `json:"query"`
	Target     string `json:"target"`
	Reassigned int64  `json:"reassigned"`
	Reason     string `json:"reason,omitempty"`
	CreatesTag bool   `json:"creates_tag"`
}

type entry struct {
	query         string
	target        string
	sim           float64
	verdict       types.TagVerdict
	verdictModel  sql.NullString
	policyVersion string
	verdictAt     time.Time
	documentID    int64
	category      string
	queryMissing  bool
	reason        string
	docIDs        []int64

	trueRank     matcher.RankResult
	freshRank    matcher.RankResult
	freshSim     float64
	freshVerdict types.TagVerdict
	freshModel   string
	confirmed    bool
	skipReason   string
}

// Run performs one reconciliation pass: it sweeps the recorded evidence,
// re-judges what changed, and applies or plans the outcomes. progress, if
// nil, is ignored; report-only runs fill the planned lists without
// applying anything.
func (r *Reconciler) Run(ctx context.Context, progress task.ProgressFunc) (Report, error) {
	cfg := r.getConfig()
	report := Report{ReportOnly: cfg.Enricher.TagReconcile.ReportOnly, Skips: map[string]int{}}
	if progress == nil {
		progress = func(string, string, float64) {}
	}

	_, err := r.client.GetLastCompletedTagReconcileTask(ctx)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return report, fmt.Errorf("get last completed tag reconcile task: %w", err)
		}
		report.ReportOnly = true
	}

	if !cfg.Enricher.TagAdjudicator.Enabled {
		report.ReportOnly = true
	}

	allTags, err := r.tag.ListAll(ctx)
	if err != nil {
		return report, fmt.Errorf("list tags: %w", err)
	}
	storeTags := make(map[string]database.Tag, len(allTags))
	for _, t := range allTags {
		storeTags[t.Name] = t
	}
	deletedTags, err := r.client.ListAllDeletedTags(ctx)
	if err != nil {
		return report, fmt.Errorf("list deleted tags: %w", err)
	}
	blocklist := make(map[string]database.ListAllDeletedTagsRow, len(deletedTags))
	for _, row := range deletedTags {
		blocklist[row.Name] = row
	}
	tagCounts := make(map[string]int64, len(allTags))
	{
		rows, err := r.client.ListAllTagsWithDocumentCount(ctx)
		if err != nil {
			return report, fmt.Errorf("list tags with document count: %w", err)
		}
		for _, row := range rows {
			tagCounts[row.Name] = row.DocumentCount
		}
	}

	progress("worklist", "building worklist", 0)

	worklist, invalidated, err := r.buildWorklist(ctx, cfg, storeTags, tagCounts, &report)
	if err != nil {
		return report, err
	}
	report.Invalidated = invalidated

	jointWorklist := r.buildJointWorklist(allTags, tagCounts)
	report.Worklist.Joint = len(jointWorklist)

	if !cfg.Enricher.TagAdjudicator.Enabled {
		report.AdjudicatorDisabled = true
		r.decideJointSplits(ctx, cfg, jointWorklist)
		r.planJointSplits(jointWorklist, &report)
		r.logger.Info("", "tag reconcile: adjudicator is disabled — no decisions were changed (report_only=%t); pairs needing re-check: %d under older rules, %d never judged, %d with usage drift, %d stale over 90 days; %d joint tags detected (would split %d deterministically)",
			report.ReportOnly, report.Worklist.Stale, report.Worklist.Unjudged, report.Worklist.Drifted, report.Worklist.Aged, report.Worklist.Joint, len(report.PlannedJointSplits))
		return report, nil
	}

	if len(worklist) > cfg.Enricher.TagReconcile.MaxRejudge {
		report.Capped = len(worklist) - cfg.Enricher.TagReconcile.MaxRejudge
		worklist = worklist[:cfg.Enricher.TagReconcile.MaxRejudge]
	}
	if remaining := cfg.Enricher.TagReconcile.MaxRejudge - len(worklist); len(jointWorklist) > remaining {
		report.Capped += len(jointWorklist) - remaining
		jointWorklist = jointWorklist[:remaining]
	}

	progress("evidence", "ranking worklist pairs", 0)
	r.evidencePass(ctx, cfg, worklist)

	progress("splits", "adjudicating joint tags", 0)
	r.decideJointSplits(ctx, cfg, jointWorklist)

	progress("apply", "applying flips", 0)
	r.applyPass(ctx, cfg, worklist, &report, tagCounts, blocklist)
	if report.ReportOnly {
		r.planJointSplits(jointWorklist, &report)
	} else {
		r.applyJointSplits(ctx, cfg, jointWorklist, &report, storeTags)
	}
	r.sweepOrphanTags(ctx, cfg, &report)
	r.sweepProvisionalTags(ctx, cfg, &report)
	r.sweepIdentityEvidence(ctx, cfg, &report)
	r.logSummary(report)

	progress("report", "reporting", 1)
	return report, nil
}

func (r *Reconciler) logSummary(report Report) {
	checked := report.Worklist.Stale + report.Worklist.Unjudged + report.Worklist.Drifted + report.Worklist.Aged
	skipped := 0
	for _, n := range report.Skips {
		skipped += n
	}
	if report.ReportOnly {
		documents := sumReassigned(report.PlannedMerges) + sumReassigned(report.PlannedKeepFlips)
		created := 0
		for _, e := range report.PlannedKeepFlips {
			if e.CreatesTag {
				created++
			}
		}
		r.logger.Info("", "tag reconcile: done (report-only, nothing changed) — %d pairs re-checked, %d judged; %d merges and %d restorations planned (%d documents would move, %d tags would be created); %d joint tags would split; %d skipped, %d over cap",
			checked, report.Judged, report.Flips.ToMerge, report.Flips.ToKeep, documents, created, len(report.PlannedJointSplits), skipped, report.Capped)
		return
	}
	documents := sumReassigned(report.Merges) + sumReassigned(report.RetagEntries)
	r.logger.Info("", "tag reconcile: done — %d pairs re-checked, %d judged; %d merges and %d restorations applied (%d documents re-pointed); %d joint tags split; %d skipped, %d over cap",
		checked, report.Judged, report.Flips.ToMerge, report.Flips.ToKeep, documents, len(report.JointSplits), skipped, report.Capped)
}

func sumReassigned(entries []MergeEntry) int64 {
	var sum int64
	for _, e := range entries {
		sum += e.Reassigned
	}
	return sum
}
