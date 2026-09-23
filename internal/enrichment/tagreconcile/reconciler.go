package tagreconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/enrichment"
	"github.com/wgomg/edub-kushim/internal/enrichment/tagpolicy"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/contentanalyzer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/types"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	verdictMaxAge       = 90 * 24 * time.Hour
	driftMinOverlap     = 5
	driftOverlapRatio   = 0.5
	rankBatchSize       = 256
	adjudicateBatchSize = 50
	reconcileDocID      = "tagreconcile"
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

type Reconciler struct {
	getConfig func() *config.Config
	client    *database.Client
	services  *service.CrudServices
	runner    *tools.Runner
	logger    *utils.Logger
}

func NewReconciler(getConfig func() *config.Config, client *database.Client,
	services *service.CrudServices,
	runner *tools.Runner, logger *utils.Logger) *Reconciler {
	return &Reconciler{
		getConfig: getConfig,
		client:    client,
		services:  services,
		runner:    runner,
		logger:    logger,
	}
}

type Report struct {
	ReportOnly          bool                       `json:"report_only"`
	Worklist            WorklistCounts             `json:"worklist"`
	Judged              int                        `json:"judged"`
	Confirmed           int                        `json:"confirmed"`
	Flips               FlipCounts                 `json:"flips"`
	Merges              []MergeEntry               `json:"merges"`
	Retags              int                        `json:"retags"`
	Invalidated         int                        `json:"invalidated"`
	Skips               map[string]int             `json:"skips"`
	PlannedMerges       []MergeEntry               `json:"planned_merges,omitempty"`
	PlannedKeepFlips    []MergeEntry               `json:"planned_keep_flips,omitempty"`
	RetagEntries        []MergeEntry               `json:"retag_entries,omitempty"`
	InvalidatedEntries  []MergeEntry               `json:"invalidated_entries,omitempty"`
	JointSplits         []JointSplitEntry          `json:"joint_splits,omitempty"`
	PlannedJointSplits  []JointSplitEntry          `json:"planned_joint_splits,omitempty"`
	Capped              int                        `json:"capped,omitempty"`
	NoDocuments         int                        `json:"no_documents,omitempty"`
	Swept               int                        `json:"swept,omitempty"`
	AdjudicatorDisabled bool                       `json:"adjudicator_disabled,omitempty"`
	Provisional         ProvisionalCounts          `json:"provisional,omitempty"`
	Identity            *enrichment.IdentityReport `json:"identity,omitempty"`
}

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

type WorklistCounts struct {
	Stale    int `json:"stale"`
	Unjudged int `json:"unjudged"`
	Drifted  int `json:"drifted"`
	Aged     int `json:"aged"`
	Joint    int `json:"joint"`
}

type JointSplitEntry struct {
	Query      string   `json:"query"`
	Components []string `json:"components"`
	Reassigned int64    `json:"reassigned"`
	Reason     string   `json:"reason,omitempty"`
}

type FlipCounts struct {
	ToMerge int `json:"to_merge"`
	ToKeep  int `json:"to_keep"`
}

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

	trueRank     tagmatcher.RankResult
	freshRank    tagmatcher.RankResult
	freshSim     float64
	freshVerdict types.TagVerdict
	freshModel   string
	confirmed    bool
	skipReason   string
}

type reciprocalEvidence struct {
	tag string
	sim float64
}

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

	allTags, err := r.services.Tag.ListAll(ctx)
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
		r.logger.Info(nil, "tag reconcile: adjudicator is disabled — no decisions were changed (report_only=%t); pairs needing re-check: %d under older rules, %d never judged, %d with usage drift, %d stale over 90 days; %d joint tags detected (would split %d deterministically)",
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

func (r *Reconciler) sweepIdentityEvidence(ctx context.Context, cfg *config.Config, report *Report) {
	if report.ReportOnly {
		return
	}
	resolver := enrichment.NewIdentityResolver(r.getConfig, r.logger, r.client, r.services, r.runner)
	identityReport, err := resolver.ResolvePending(ctx, cfg.Enricher.TagReconcile.MaxRejudge)
	if err != nil {
		r.logger.Error(nil, "tag reconcile: identity evidence sweep failed: %v", err)
		return
	}
	if identityReport.Pending == 0 {
		return
	}
	report.Identity = identityReport
	r.logger.Info(nil, "tag reconcile: identity evidence — %d pending, %d resolved, %d folded, %d aliased, %d deferred, %d failed",
		identityReport.Pending, identityReport.Resolved, identityReport.Folded, identityReport.Aliased, identityReport.Deferred, identityReport.Failed)
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
		r.logger.Info(nil, "tag reconcile: done (report-only, nothing changed) — %d pairs re-checked, %d judged; %d merges and %d restorations planned (%d documents would move, %d tags would be created); %d joint tags would split; %d skipped, %d over cap",
			checked, report.Judged, report.Flips.ToMerge, report.Flips.ToKeep, documents, created, len(report.PlannedJointSplits), skipped, report.Capped)
		return
	}
	documents := sumReassigned(report.Merges) + sumReassigned(report.RetagEntries)
	r.logger.Info(nil, "tag reconcile: done — %d pairs re-checked, %d judged; %d merges and %d restorations applied (%d documents re-pointed); %d joint tags split; %d skipped, %d over cap",
		checked, report.Judged, report.Flips.ToMerge, report.Flips.ToKeep, documents, len(report.JointSplits), skipped, report.Capped)
}

func (r *Reconciler) sweepOrphanTags(ctx context.Context, cfg *config.Config, report *Report) {
	graceDays := cfg.Enricher.TagReconcile.DeadTagGraceDays
	cutoff := time.Now().Add(-time.Duration(graceDays) * 24 * time.Hour)

	q := r.client.Queries
	if report.ReportOnly {
		// refresh marks in a rolled-back transaction so the report predicts
		// the next apply on the same state without persisting anything
		tx, err := r.client.BeginTx(ctx, nil)
		if err != nil {
			r.logger.Error(nil, "tag reconcile: begin orphan mark transaction: %v", err)
			return
		}
		defer tx.Rollback()
		q = r.client.WithTx(tx)
	}

	if _, err := q.ClearOrphanTagMarks(ctx); err != nil {
		r.logger.Error(nil, "tag reconcile: clear orphan tag marks: %v", err)
		return
	}
	if _, err := q.MarkOrphanTags(ctx); err != nil {
		r.logger.Error(nil, "tag reconcile: mark orphan tags: %v", err)
		return
	}

	planned, err := q.ListOrphanTags(ctx, cutoff)
	if err != nil {
		r.logger.Error(nil, "tag reconcile: list orphan tags: %v", err)
		return
	}
	if len(planned) == 0 {
		return
	}

	if report.ReportOnly {
		report.Swept = len(planned)
		r.logger.Info(nil, "tag reconcile (report-only): would delete %d tags orphaned for over %d days%s",
			len(planned), graceDays, sampleNamesSuffix(planned))
		return
	}

	deleted, err := q.DeleteOrphanTags(ctx, cutoff)
	if err != nil {
		r.logger.Error(nil, "tag reconcile: delete orphan tags: %v", err)
		return
	}
	report.Swept = len(deleted)
	if err := r.services.Tag.RemoveFromStore(ctx, deleted); err != nil {
		r.logger.Error(nil, "tag reconcile: remove swept tags from store: %v", err)
	}
	r.logger.Info(nil, "tag reconcile: deleted %d tags orphaned for over %d days%s",
		len(deleted), graceDays, sampleNamesSuffix(deleted))
}

func sampleNamesSuffix(names []string) string {
	const maxSample = 5
	if len(names) > maxSample {
		return fmt.Sprintf(": %s, …", strings.Join(names[:maxSample], ", "))
	}
	return ": " + strings.Join(names, ", ")
}

type provisionalEntry struct {
	id   int64
	name string
	docs int64
}

func (r *Reconciler) sweepProvisionalTags(ctx context.Context, cfg *config.Config, report *Report) {
	graceDays := cfg.Enricher.TagReconcile.ProvisionalGraceDays
	cutoff := time.Now().Add(-time.Duration(graceDays) * 24 * time.Hour)

	worklist, err := r.client.ListProvisionalTagsOlderThan(ctx, cutoff)
	if err != nil {
		r.logger.Error(nil, "tag reconcile: list provisional tags: %v", err)
		return
	}
	report.Provisional.Worklist = len(worklist)
	if len(worklist) == 0 {
		return
	}

	ids := make([]int64, len(worklist))
	names := make([]string, len(worklist))
	for i, t := range worklist {
		ids[i] = t.ID
		names[i] = t.Name
	}
	assigned, err := r.client.CountTagDocumentsByTagIDs(ctx, ids)
	if err != nil {
		r.logger.Error(nil, "tag reconcile: count provisional tag documents: %v", err)
		return
	}
	assignedCounts := make(map[int64]int64, len(assigned))
	for _, row := range assigned {
		assignedCounts[row.TagID] = row.DocumentCount
	}

	// promotion evidence is live-document only, like the judge's text input
	liveCounts, err := tags.NewStore(r.client.Queries).LiveMass(ctx, ids)
	if err != nil {
		r.logger.Error(nil, "tag reconcile: count live provisional tag documents: %v", err)
		return
	}

	events, err := r.services.Tag.LatestQualityEventsForNames(ctx, names)
	if err != nil {
		r.logger.Error(nil, "tag reconcile: provisional tag verdict lookup: %v", err)
		return
	}
	verdicts := make(map[string]database.LatestTagQualityEventsForNamesRow, len(events))
	for _, e := range events {
		verdicts[e.TagName] = e
	}

	var rejudge []*provisionalEntry
	for _, t := range worklist {
		if assignedCounts[t.ID] == 0 {
			// the orphan sweep in this same run owns zero-assignment rows
			report.Provisional.ZeroAssign++
			continue
		}
		docs := liveCounts[t.ID]

		row, ok := verdicts[t.Name]
		if !ok {
			rejudge = append(rejudge, &provisionalEntry{id: t.ID, name: t.Name, docs: docs})
			continue
		}
		if !row.Pass || docs < 2 {
			continue
		}

		if report.ReportOnly {
			report.Provisional.Promoted++
			r.logger.Info(nil, "tag reconcile (report-only): would promote %q (docs=%d model=%s)", t.Name, docs, verdictModelLabel(row.VerdictModel))
			continue
		}
		if _, status, err := r.services.Tag.Promote(ctx, t.ID, "sweep"); err != nil {
			r.logger.Error(nil, "tag reconcile: promote provisional tag %q: %v", t.Name, err)
		} else if status == service.PromotePromoted {
			report.Provisional.Promoted++
			r.logger.Info(nil, "tag reconcile: promoted %q (docs=%d model=%s)", t.Name, docs, verdictModelLabel(row.VerdictModel))
		}
	}

	if len(rejudge) == 0 {
		return
	}

	budget := cfg.Enricher.TagReconcile.MaxRejudge
	if len(rejudge) > budget {
		rejudge = rejudge[:budget]
	}

	if report.ReportOnly {
		report.Provisional.Rejudged = len(rejudge)
		r.logger.Info(nil, "tag reconcile (report-only): would re-judge %d provisional tags older than %d days", len(rejudge), graceDays)
		return
	}
	if !r.runner.HasTagQualityJudge() {
		report.Provisional.NoJudge = len(rejudge)
		r.logger.Info(nil, "tag reconcile: tag quality judge is not configured — %d provisional tags stay queued", len(rejudge))
		return
	}

	r.rejudgeProvisionalTags(ctx, cfg, rejudge, report)
}

func (r *Reconciler) rejudgeProvisionalTags(ctx context.Context, cfg *config.Config, entries []*provisionalEntry, report *Report) {
	const chunkSize = 150

	model := cfg.ResolvedQualityLlm().Model
	for _, e := range entries {
		docID, err := r.client.FirstDocumentIDForTag(ctx, e.id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				report.Provisional.Skipped++
				r.logger.Info(nil, "tag reconcile: provisional tag %q has no live document — left for review", e.name)
				continue
			}
			r.logger.Error(nil, "tag reconcile: first document for provisional tag %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}
		doc, err := r.client.GetDocumentTextForTagQuality(ctx, docID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				report.Provisional.Skipped++
				r.logger.Info(nil, "tag reconcile: provisional tag %q has no live document text — left for review", e.name)
				continue
			}
			r.logger.Error(nil, "tag reconcile: document text for provisional tag %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}

		reduced, err := r.runner.ReduceContent(ctx, doc.TextContent.String, chunkSize,
			enrichment.TargetWordCount(int(doc.WordCount), cfg.Enricher.TagMatcher.ReduceTargetWords))
		if err != nil {
			r.logger.Error(nil, "tag reconcile: reduce text for provisional tag %q: %v", e.name, err)
		}

		candidates := []contentanalyzer.TagQualityCandidate{{
			Index:            0,
			Term:             e.name,
			Category:         contentanalyzer.TagCategoryOther,
			WhitelistedPlace: contentanalyzer.IsWhitelistedPlace(e.name),
		}}
		results, err := r.runner.JudgeTagQuality(ctx, reduced.Text, candidates)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			results, err = r.runner.JudgeTagQuality(ctx, reduced.Text, candidates)
		}
		if err != nil {
			r.logger.Error(nil, "tag reconcile: judge provisional tag %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}
		if len(results) != len(candidates) {
			r.logger.Error(nil, "tag reconcile: judge returned %d verdicts for provisional tag %q", len(results), e.name)
			report.Provisional.Failed++
			continue
		}

		res := results[0]
		criteria, err := json.Marshal(res.Criteria())
		if err != nil {
			r.logger.Error(nil, "tag reconcile: marshal criteria for provisional tag %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}
		pass, finalModel, err := r.recordProvisionalVerdict(ctx, docID, e.name, criteria, res.Passed(), model)
		if err != nil {
			r.logger.Error(nil, "tag reconcile: record provisional verdict for %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}
		report.Provisional.Rejudged++

		if !pass {
			report.Provisional.Deferred++
			r.logger.Info(nil, "tag reconcile: provisional tag %q rejected by the judge — deferred to the review queue", e.name)
			continue
		}
		if e.docs < 2 {
			continue
		}
		if _, status, err := r.services.Tag.Promote(ctx, e.id, "sweep"); err != nil {
			r.logger.Error(nil, "tag reconcile: promote provisional tag %q: %v", e.name, err)
		} else if status == service.PromotePromoted {
			report.Provisional.Promoted++
			r.logger.Info(nil, "tag reconcile: promoted %q (docs=%d model=%s)", e.name, e.docs, finalModel)
		}
	}
}

func (r *Reconciler) recordProvisionalVerdict(ctx context.Context, docID int64, name string, criteria json.RawMessage, pass bool, model string) (bool, string, error) {
	verdictModel := sql.NullString{String: model, Valid: model != ""}
	stored, err := r.client.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docID,
		TagName:       utils.NormalizeTag(name),
		Criteria:      criteria,
		Pass:          pass,
		VerdictModel:  verdictModel,
		PolicyVersion: contentanalyzer.TagQualityVersion,
	})
	if err != nil {
		return false, "", err
	}
	if stored > 0 || !verdictModel.Valid {
		return pass, model, nil
	}

	// another writer recorded a fresh verdict first — keep it, never overwrite
	rows, err := r.services.Tag.LatestQualityEventsForNames(ctx, []string{name})
	if err != nil {
		return false, "", err
	}
	if len(rows) == 0 {
		return pass, model, nil
	}
	return rows[0].Pass, verdictModelLabel(rows[0].VerdictModel), nil
}

func verdictModelLabel(model sql.NullString) string {
	if !model.Valid || model.String == "" {
		return "cached"
	}
	return model.String
}

func sumReassigned(entries []MergeEntry) int64 {
	var sum int64
	for _, e := range entries {
		sum += e.Reassigned
	}
	return sum
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " reason=" + reason
}

func (r *Reconciler) buildWorklist(ctx context.Context, cfg *config.Config, storeTags map[string]database.Tag, tagCounts map[string]int64, report *Report) ([]*entry, int, error) {
	var worklist []*entry
	collected := make(map[string]bool)
	invalidated := 0

	scan1, err := r.client.TagVerdictPairsWithoutFreshVerdict(ctx, tagpolicy.Version)
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
				r.logger.Error(nil, "tag reconcile: delete fresh verdicts for %d dead-target pairs: %v", len(deadTargetPairs), err)
			}
		}
		for _, p := range deadTargetPairs {
			report.InvalidatedEntries = append(report.InvalidatedEntries, MergeEntry{Query: p[0], Target: p[1]})
			r.logger.Info(nil, "tag reconcile: discarding cached decision %q → %q — the target tag no longer exists", p[0], p[1])
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
				r.logger.Info(nil, "tag reconcile: skipping %q → %q — %q no longer exists and its documents are already tagged %q", row.Query, row.Target, row.Query, row.Target)
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
		if row.PolicyVersion != tagpolicy.Version {
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
		PolicyVersion: tagpolicy.Version,
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

	kept, err := r.client.TagVerdictPairsByVerdict(ctx, tagpolicy.Version)
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
		if utils.SplitJointTag(e.query) != nil {
			r.logger.Debug(nil, "tag reconcile: dropping pair %q → %q — %q joins concepts with a conjunction and is split apart, not merged", e.query, e.target, e.query)
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
			r.logger.Error(nil, "tag reconcile bootstrap: pair presence check failed: %v", err)
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
			r.logger.Error(nil, "tag reconcile bootstrap: query presence check failed: %v", err)
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
		r.logger.Error(nil, "tag reconcile bootstrap: resolve document ids: %v", err)
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

func (r *Reconciler) evidencePass(ctx context.Context, cfg *config.Config, worklist []*entry) {
	for i := 0; i < len(worklist); i += rankBatchSize {
		end := min(i+rankBatchSize, len(worklist))
		queries := make([]string, 0, end-i)
		for _, e := range worklist[i:end] {
			queries = append(queries, e.query)
		}
		ranked, err := r.runner.RankTags(ctx, reconcileDocID, queries)
		if err != nil {
			r.logger.Error(nil, "tag reconcile: query rank batch failed: %v", err)
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
			r.logger.Error(nil, "tag reconcile: target rank batch failed: %v", err)
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

		synth := tagmatcher.RankResult{KeptName: e.query}
		synth.Candidates = append(synth.Candidates, tagmatcher.Candidate{Tag: target, Similarity: freshSim})
		for _, c := range rank.Candidates {
			if c.Tag == target || c.Tag == e.query {
				continue
			}
			synth.Candidates = append(synth.Candidates, c)
		}
		e.freshRank = synth

		decisions := tagpolicy.Evaluate([]string{e.query}, []tagmatcher.RankResult{synth})
		if decisions[0].Reason == tagpolicy.ReasonBand {
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
		evidence := make([]contentanalyzer.TagPairEvidence, 0, end-i)
		for _, e := range bandEntries[i:end] {
			ev := contentanalyzer.TagPairEvidence{
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
			r.logger.Error(nil, "tag reconcile: adjudication batch failed: %v", err)
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
		evidence := make([]contentanalyzer.TagPairEvidence, 0, end-i)
		for _, e := range confirm[i:end] {
			ev := contentanalyzer.TagPairEvidence{
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
			r.logger.Error(nil, "tag reconcile: confirmation batch failed: %v", err)
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
				r.logger.Error(nil, "tag reconcile: upsert verdict for %q → %q: %v", e.query, e.target, err)
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
				r.logger.Info(nil, "tag reconcile (report-only): would merge %q into %q (%d documents)%s", e.query, e.target, tagCounts[e.query], reasonSuffix(e.reason))
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
				r.logger.Error(nil, "tag reconcile: merge flip %q → %q: %v", e.query, e.target, err)
				continue
			}
			budget -= int(report.Merges[len(report.Merges)-1].Reassigned)
			continue
		}

		appliedFlip = true
		repointed, skipped, err := r.applyKeepFlip(ctx, e, blocklist)
		if err != nil {
			r.logger.Error(nil, "tag reconcile: keep flip for %q failed: %v", e.query, err)
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
			r.logger.Error(nil, "tag reconcile: document counts for report-only keep flips: %v", err)
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
		r.logger.Info(nil, "tag reconcile (report-only): would create tag %q — it was being replaced with %q; %d documents would move back%s",
			e.query, e.target, reassigned, suffix)
	case keepFlipCreatedNoDocs:
		r.logger.Info(nil, "tag reconcile (report-only): would create tag %q — it was being replaced with %q; no documents to move%s",
			e.query, e.target, suffix)
	case keepFlipMovedWithDocs:
		r.logger.Info(nil, "tag reconcile (report-only): would move %d documents tagged %q back to %q%s",
			reassigned, e.target, e.query, suffix)
	case keepFlipMovedNoDocs:
		r.logger.Info(nil, "tag reconcile (report-only): would keep %q separate from %q; no documents to move%s",
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
	res, err := r.services.TagMerge.Merge(ctx, queryTag.ID, targetTag.ID)
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
	r.logger.Info(nil, "tag reconcile: merged %q into %q (re-pointed %d documents)", e.query, e.target, res.Reassigned)
	if err := r.upsertVerdict(ctx, e); err != nil {
		r.logger.Error(nil, "tag reconcile: upsert verdict after merge %q → %q: %v", e.query, e.target, err)
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
			r.logger.Error(nil, "tag reconcile: upsert verdict for %q → %q: %v", e.query, e.target, err)
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
			r.logger.Error(nil, "tag reconcile: upsert verdict for %q → %q: %v", e.query, e.target, err)
		}
		return 0, false, nil
	}

	results, err := r.services.Tag.Create(ctx, []string{e.query})
	if err != nil {
		return 0, false, fmt.Errorf("create query tag: %w", err)
	}
	var queryTag database.Tag
	for _, res := range results {
		if res.Status == service.Created || res.Status == service.Conflict {
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
		Query:         utils.NormalizeTag(e.query),
		Target:        utils.NormalizeTag(e.target),
		Sim:           e.freshSim,
		Verdict:       e.freshVerdict,
		VerdictModel:  sql.NullString{String: e.freshModel, Valid: e.freshModel != ""},
		PolicyVersion: tagpolicy.Version,
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
	if row.Kind == service.DeletedTagKindMerged {
		r.logger.Info(nil, "tag reconcile: skipping restoration of %q — it was merged into %q", e.query, row.TargetName.String)
		return
	}
	r.logger.Info(nil, "tag reconcile: skipping restoration of %q — the tag was deleted by an operator", e.query)
}

func (r *Reconciler) logKeepFlipResult(e *entry, repointed int64) {
	switch keepFlipOutcomeOf(e, repointed) {
	case keepFlipCreatedWithDocs:
		r.logger.Info(nil, "tag reconcile: created tag %q — it was being replaced with %q; re-pointed %d documents",
			e.query, e.target, repointed)
	case keepFlipCreatedNoDocs:
		r.logger.Info(nil, "tag reconcile: created tag %q — it was being replaced with %q; no documents to move",
			e.query, e.target)
	case keepFlipMovedWithDocs:
		r.logger.Info(nil, "tag reconcile: moved %d documents tagged %q back to %q", repointed, e.target, e.query)
	case keepFlipMovedNoDocs:
		r.logger.Info(nil, "tag reconcile: kept %q separate from %q; no documents to move", e.query, e.target)
	}
}

func (r *Reconciler) upsertVerdict(ctx context.Context, e *entry) error {
	if e.documentID == 0 {
		r.logger.Warn(nil, "tag reconcile: corrected decision for %q (kept separate from %q) not recorded — no document to attribute it to; the pair will be re-checked next run", e.query, e.target)
		return nil
	}
	_, err := r.client.UpsertFreshTagVerdict(ctx, database.UpsertFreshTagVerdictParams{
		DocumentID:    e.documentID,
		Query:         utils.NormalizeTag(e.query),
		Target:        utils.NormalizeTag(e.target),
		Sim:           e.freshSim,
		Verdict:       e.freshVerdict,
		VerdictModel:  sql.NullString{String: e.freshModel, Valid: e.freshModel != ""},
		PolicyVersion: tagpolicy.Version,
	})
	return err
}

func (r *Reconciler) tagByName(ctx context.Context, name string) (database.Tag, bool) {
	tag, err := r.services.Tag.GetByName(ctx, name)
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
