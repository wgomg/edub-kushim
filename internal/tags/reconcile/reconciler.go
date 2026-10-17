// Package reconcile is the tag store's scheduled maintenance: it sweeps the recorded verdict evidence
// for stale, unjudged and drifted pairs, re-ranks and re-adjudicates what changed, and applies or plans
// alias collapses, keep flips, compound splits and provisional-tag outcomes through the curation surface.
// It is the scheduled reconciliation flow of docs/tags/consolidation.md, the machine-driven complement
// of the human worklist review.
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
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Reconciler is the scheduled tag-store maintainer: it re-judges stale verdict evidence, flips decisions
// whose evidence changed, plans and applies alias collapses and compound splits, and settles provisional
// tags, capped per run and report-only when configured.
type Reconciler struct {
	getConfig func() *config.Config
	client    *database.Client
	tag       *curation.Tag
	runner    *tools.Runner
	logger    *log.Logger
}

// NewReconciler wires the reconciler: the dynamic config, the database, the curation surface it writes
// through, the tools runner, and the logger.
func NewReconciler(getConfig func() *config.Config, client *database.Client,
	tag *curation.Tag, runner *tools.Runner, logger *log.Logger) *Reconciler {
	return &Reconciler{
		getConfig: getConfig,
		client:    client,
		tag:       tag,
		runner:    runner,
		logger:    logger,
	}
}

// Report is the stored audit of one reconcile run. It is serialized as the task result and parsed by the web
// UI, so its JSON vocabulary is a compatibility surface rather than free to rename.
type Report struct {
	ReportOnly            bool                 `json:"report_only"`
	Worklist              WorklistCounts       `json:"worklist"`
	Judged                int                  `json:"judged"`
	Confirmed             int                  `json:"confirmed"`
	Flips                 FlipCounts           `json:"flips"`
	Aliases               []PairEntry          `json:"aliases"`
	Retags                int                  `json:"retags"`
	Invalidated           int                  `json:"invalidated"`
	Skips                 map[string]int       `json:"skips"`
	PlannedAliases        []PairEntry          `json:"planned_aliases,omitempty"`
	PlannedKeepFlips      []PairEntry          `json:"planned_keep_flips,omitempty"`
	RetagEntries          []PairEntry          `json:"retag_entries,omitempty"`
	InvalidatedEntries    []PairEntry          `json:"invalidated_entries,omitempty"`
	CompoundSplits        []CompoundSplitEntry `json:"compound_splits,omitempty"`
	PlannedCompoundSplits []CompoundSplitEntry `json:"planned_compound_splits,omitempty"`
	Demotion              DemotionCounts       `json:"demotion"`
	Demoted               []DemotionEntry      `json:"demoted,omitempty"`
	PlannedDemotions      []DemotionEntry      `json:"planned_demotions,omitempty"`
	Capped                int                  `json:"capped,omitempty"`
	NoDocuments           int                  `json:"no_documents,omitempty"`
	Swept                 int                  `json:"swept,omitempty"`
	AdjudicatorDisabled   bool                 `json:"adjudicator_disabled,omitempty"`
	Twins                 TwinCounts           `json:"twins"`
	Siblings              SiblingCounts        `json:"siblings"`
	Measurement           *MeasurementRun      `json:"measurement,omitempty"`
	Provisional           ProvisionalCounts    `json:"provisional"`
}

// SiblingCounts tallies the containment worklist for the run report: how many pairs want a human's eye and
// how many stand as evidence. Containment has no automatic action, so the counts are the whole of the arm's
// output — nothing merges, holds, or demotes.
type SiblingCounts struct {
	Worklist int `json:"worklist"`
	Gray     int `json:"gray"`
	Firm     int `json:"firm"`
}

// TwinCounts is the twin arm's outcome tally for one run: the pairs the latest measurement run proposed and
// how the arm resolved them. A proposed pair may be filtered out (Settled, Held, Skipped) rather than
// resolved, so the counters describe dispositions and do not partition Worklist.
type TwinCounts struct {
	Worklist   int `json:"worklist"`
	Settled    int `json:"settled"`
	Held       int `json:"held"`
	Skipped    int `json:"skipped"`
	Mechanical int `json:"mechanical"`
	Planned    int `json:"planned"`
	Pending    int `json:"pending"`
}

// MeasurementRun identifies the measurement run the twin arm read, so a report can be traced to its input;
// Absent records that the store has no run yet, where the twin worklist is empty by definition.
type MeasurementRun struct {
	RunID      string    `json:"run_id,omitempty"`
	SnapshotAt time.Time `json:"snapshot_at,omitzero"`
	Absent     bool      `json:"absent,omitempty"`
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

// WorklistCounts sizes each arm the run fetched before any cap: the four pair re-check categories, the
// stored compounds the sweep will consider, and the twin pairs the measurement run proposed. It feeds the
// report's checked total; it is not itself what the run acted on.
type WorklistCounts struct {
	Stale     int `json:"stale"`
	Unjudged  int `json:"unjudged"`
	Drifted   int `json:"drifted"`
	Aged      int `json:"aged"`
	Compounds int `json:"compounds"`
	Twins     int `json:"twins"`
}

// CompoundSplitEntry is one stored-compound split in the report. Reassigned is the compound's current
// document count when planned and the documents actually re-pointed when applied; Reason names the verdict
// source (deterministic or adjudicated).
type CompoundSplitEntry struct {
	Query      string   `json:"query"`
	Components []string `json:"components"`
	Reassigned int64    `json:"reassigned"`
	Reason     string   `json:"reason,omitempty"`
}

// DemotionCounts reports the demotion arm's work and outcomes by trigger. The counts do not partition the
// applied total — a held name is counted and skipped — so they describe dispositions, not a sum.
type DemotionCounts struct {
	Worklist    int `json:"worklist"`
	MassDecay   int `json:"mass_decay"`
	NoDimension int `json:"no_dimension"`
	Rejudged    int `json:"rejudged"`
	Demoted     int `json:"demoted"`
	Held        int `json:"held"`
	NoJudge     int `json:"no_judge"`
	Skipped     int `json:"skipped"`
	Failed      int `json:"failed"`
}

// DemotionEntry names a demoted value and the trigger behind it, or, in a report-only run, one that would
// be demoted.
type DemotionEntry struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

// FlipCounts counts the evidence-driven direction flips: decisions that became aliases and keeps that
// flipped back.
type FlipCounts struct {
	ToAlias int `json:"to_alias"`
	ToKeep  int `json:"to_keep"`
}

// PairEntry is one planned or applied pair action: the query, its target canonical, how many documents
// applying it re-pointed, the reason, and whether applying it would first create the target tag. Alias
// collapses, keep-flip retags and invalidations all report through this entry.
type PairEntry struct {
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
	freshSim     float64
	freshVerdict types.TagVerdict
	freshModel   string
	direction    string
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

	// a mid-run mutation re-arms the flag, so no change is swallowed by this open
	if err := r.client.Queries.SetTagStoreSignal(ctx, database.SetTagStoreSignalParams{
		Name:  tags.SignalReconcileChanged,
		Value: tags.SignalClear,
	}); err != nil {
		return report, fmt.Errorf("clear store-changed signal: %w", err)
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
	promotedNames := make([]string, 0, len(allTags))
	for _, t := range allTags {
		storeTags[t.Name] = t
		if t.PromotedAt.Valid {
			promotedNames = append(promotedNames, t.Name)
		}
	}
	promotedView := tags.StoreNames(promotedNames)
	deletedTags, err := r.client.ListAllDeletedTags(ctx)
	if err != nil {
		return report, fmt.Errorf("list deleted tags: %w", err)
	}
	blocklist := make(map[string]struct{}, len(deletedTags))
	for _, row := range deletedTags {
		blocklist[row.Name] = struct{}{}
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

	// the store's live key set, built as the emission gate builds it, so both fans cover the same words
	storeTagKeys, err := r.client.ListStoreTagKeys(ctx)
	if err != nil {
		return report, fmt.Errorf("list store tag keys: %w", err)
	}
	liveNames := make(map[string]struct{}, len(storeTagKeys))
	for _, key := range storeTagKeys {
		liveNames[tags.Key(key)] = struct{}{}
	}

	progress("worklist", "building worklist", 0)

	worklist, invalidated, err := r.buildWorklist(ctx, cfg, storeTags, tagCounts, &report)
	if err != nil {
		return report, err
	}
	report.Invalidated = invalidated

	pairKeys := make(map[string]struct{}, len(worklist))
	for _, entry := range worklist {
		pairKeys[twinPairKey(entry.query, entry.target)] = struct{}{}
	}

	compoundWorklist := r.buildCompoundWorklist(ctx, allTags, tagCounts, &report)
	report.Worklist.Compounds = len(compoundWorklist)

	if !cfg.Enricher.TagAdjudicator.Enabled {
		report.AdjudicatorDisabled = true
		r.decideCompoundSplits(ctx, cfg, compoundWorklist, promotedView, liveNames)
		r.planCompoundSplits(compoundWorklist, &report)
		r.sweepTwins(ctx, cfg, &report, pairKeys)
		r.logger.Info("", "tag reconcile: adjudicator is disabled — no decisions were changed (report_only=%t); pairs needing re-check: %d under older rules, %d never judged, %d with usage drift, %d stale over 90 days; %d stored compounds (would split %d deterministically); %d twin pairs proposed",
			report.ReportOnly, report.Worklist.Stale, report.Worklist.Unjudged, report.Worklist.Drifted, report.Worklist.Aged, report.Worklist.Compounds, len(report.PlannedCompoundSplits), report.Worklist.Twins)
		return report, nil
	}

	if len(worklist) > cfg.Enricher.TagReconcile.MaxRejudge {
		report.Capped = len(worklist) - cfg.Enricher.TagReconcile.MaxRejudge
		worklist = worklist[:cfg.Enricher.TagReconcile.MaxRejudge]
	}
	if remaining := cfg.Enricher.TagReconcile.MaxRejudge - len(worklist); len(compoundWorklist) > remaining {
		report.Capped += len(compoundWorklist) - remaining
		compoundWorklist = compoundWorklist[:remaining]
	}

	progress("evidence", "ranking worklist pairs", 0)
	r.evidencePass(ctx, cfg, worklist)

	progress("splits", "disposing stored compounds", 0)
	r.decideCompoundSplits(ctx, cfg, compoundWorklist, promotedView, liveNames)

	progress("apply", "applying flips", 0)
	r.applyPass(ctx, cfg, worklist, &report, tagCounts, blocklist)
	if report.ReportOnly {
		r.planCompoundSplits(compoundWorklist, &report)
	} else {
		r.applyCompoundSplits(ctx, cfg, compoundWorklist, &report, storeTags)
	}
	r.sweepTwins(ctx, cfg, &report, pairKeys)
	r.sweepPromotedTags(ctx, cfg, &report)
	r.sweepOrphanTags(ctx, cfg, &report)
	r.sweepProvisionalTags(ctx, cfg, &report)
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
		documents := sumReassigned(report.PlannedAliases) + sumReassigned(report.PlannedKeepFlips)
		created := 0
		for _, e := range report.PlannedKeepFlips {
			if e.CreatesTag {
				created++
			}
		}
		r.logger.Info("", "tag reconcile: done (report-only, nothing changed) — %d pairs re-checked, %d judged; %d aliases and %d restorations planned (%d documents would move, %d tags would be created); %d stored compounds would split; %d twin pairs proposed (%d would alias); %d sibling pairs for review; %d demotions would revoke influence (%d below the floor, %d decided drops, %d re-judged); %d skipped, %d over cap",
			checked, report.Judged, report.Flips.ToAlias, report.Flips.ToKeep, documents, created, len(report.PlannedCompoundSplits), report.Twins.Worklist, report.Twins.Planned, report.Siblings.Worklist,
			report.Demotion.Demoted, report.Demotion.MassDecay, report.Demotion.NoDimension, report.Demotion.Rejudged, skipped, report.Capped)
		return
	}
	documents := sumReassigned(report.Aliases) + sumReassigned(report.RetagEntries)
	r.logger.Info("", "tag reconcile: done — %d pairs re-checked, %d judged; %d aliases and %d restorations applied (%d documents re-pointed); %d stored compounds split; %d twin pairs proposed (%d aliased); %d sibling pairs for review; %d demotions revoked influence (%d below the floor, %d decided drops, %d re-judged), %d held for review; %d skipped, %d over cap",
		checked, report.Judged, report.Flips.ToAlias, report.Flips.ToKeep, documents, len(report.CompoundSplits), report.Twins.Worklist, report.Twins.Mechanical, report.Siblings.Worklist,
		report.Demotion.Demoted, report.Demotion.MassDecay, report.Demotion.NoDimension, report.Demotion.Rejudged, report.Demotion.Held, skipped, report.Capped)
}

func sumReassigned(entries []PairEntry) int64 {
	var sum int64
	for _, e := range entries {
		sum += e.Reassigned
	}
	return sum
}
