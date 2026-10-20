package admission

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tools"
)

// AuditReportPrefix marks the serialized AuditReport inside the task result, the fork-and-extract
// convention the other tag tasks share.
const AuditReportPrefix = "tagaudit-report: "

// auditBatchSize bounds one applicability judge call's candidate list, matching the shared judged batching.
const auditBatchSize = 40

// auditTriggerSimilarity is the matcher-similarity floor at which a candidate the document does not carry
// becomes a coverage flag. It is calibration-owned and pinned here, not a user setting: it is a tuned model
// threshold with no operator-meaningful interpretation (the D22-verified band).
const auditTriggerSimilarity = 0.52

// auditStrongSimilarity is the matcher similarity at or above which a trigger candidate reaches the judge
// without lexical support. Candidates in [trigger floor, strong) survive only with lexical support
// (docs/tags/completeness.md §3.3).
const auditStrongSimilarity = 0.60

// auditCaveatSampled qualifies a coverage measurement whose gap classes are run-scoped over a sample
// rather than the whole store.
const auditCaveatSampled = "sampled"

// coverageInsertChunk bounds one bulk pending-coverage insert statement.
const coverageInsertChunk = 1000

// AuditReport is one completeness audit run's counters and coverage tallies, keyed to the sweep it drew
// from. ZeroWork marks a run that skipped the pending-coverage derivation and drew no documents.
type AuditReport struct {
	ZeroWork        bool           `json:"zero_work"`
	RunID           string         `json:"run_id"`
	CriteriaVersion string         `json:"criteria_version"`
	SweepCount      int64          `json:"sweep_count"`
	EligiblePool    int64          `json:"eligible_pool"`
	Sampled         int            `json:"sampled"`
	Flagged         int            `json:"flagged"`
	MatcherMissing  int            `json:"matcher_unavailable"`
	NoAdjudicator   bool           `json:"no_adjudicator"`
	Coverage        map[string]int `json:"coverage"`
	Fills           int            `json:"fills"`
	Flags           int            `json:"flags"`
	Proposals       int            `json:"proposals"`
	Calls           int            `json:"calls"`
	Cached          int            `json:"cached"`
	Salvaged        int            `json:"salvaged"`
	PendingCoverage int            `json:"pending_coverage"`
}

// AuditRunner runs the completeness audit: the worklist, the trigger/lexical/judged verification, the
// coverage dispositions, and the durable pending-coverage derivation. It runs in a kushim child process
// under the maintenance lock; edub never holds it.
type AuditRunner struct {
	cfg    func() *config.Config
	logger *log.Logger
	client *database.Client
	runner *tools.Runner
}

// NewAuditRunner wires the audit to its config, logger, database, and tools runner.
func NewAuditRunner(cfg func() *config.Config, logger *log.Logger, client *database.Client, runner *tools.Runner) *AuditRunner {
	return &AuditRunner{cfg: cfg, logger: logger, client: client, runner: runner}
}

// auditDocStats tallies one document's audit outcome.
type auditDocStats struct {
	skipped        bool
	matcherMissing bool
	noAdjudicator  bool
	fills          int
	flags          int
	proposals      int
	calls          int
	cached         int
	salvaged       int
}

// auditOutcome is one document's audit result: its coverage readings and tallies.
type auditOutcome struct {
	Records []tags.CoverageRecord
	Stats   auditDocStats
}

// auditWorklist is one sweep's draw: the documents to audit and the sweep facts the report carries.
type auditWorklist struct {
	DocumentIDs  []int64
	Sampled      int
	Flagged      int
	SweepCount   int64
	EligiblePool int64
}

// storeView carries the promoted and live store name views the audit's derivations read.
type storeView struct {
	Promoted map[string]string
	Live     map[string]struct{}
}

// pendingCoverage is the pending-coverage derivation's result: the rows written per dimension.
type pendingCoverage struct {
	ByDimension map[string]int
	Total       int
}

// pendingFill is one dimension the audit attaches to a document.
type pendingFill struct {
	dim   string
	tagID int64
}

// resolvedVerdict is one flagged dimension's applicability answer with its provenance.
type resolvedVerdict struct {
	result analyzer.TagAuditResult
	fresh  bool
	model  string
	raw    json.RawMessage
	source tags.CoverageSource
}

// Run performs one completeness audit: it derives the durable pending-coverage rows store-wide, audits a
// sample and the flag-material documents, disposes each coverage reading, and records the per-dimension
// coverage measurement. runID identifies the run in the stored rows and the report. An unchanged store
// skips the store-wide derivation and reuses the durable rows already written; a failed run re-arms the
// store-changed signal for the next one.
func (a *AuditRunner) Run(ctx context.Context, runID uuid.UUID) (*AuditReport, error) {
	report, err := a.run(ctx, runID)
	if err != nil {
		return report, tags.RearmStoreSignal(ctx, a.client, tags.SignalAuditChanged, err)
	}
	return report, nil
}

func (a *AuditRunner) run(ctx context.Context, runID uuid.UUID) (*AuditReport, error) {
	cfg := a.cfg()
	report := &AuditReport{
		RunID:           runID.String(),
		CriteriaVersion: analyzer.TagAuditVersion,
		Coverage:        map[string]int{},
	}

	changed, err := tags.OpenStoreSignal(ctx, a.client, tags.SignalAuditChanged)
	if err != nil {
		return nil, err
	}

	view, err := a.loadStoreView(ctx)
	if err != nil {
		return nil, err
	}

	twins, err := a.canonicalizationPairs(ctx)
	if err != nil {
		return nil, err
	}

	// a clear signal means every mutation the derivation reads landed before its last run: the derivable rows
	// stand and the store-wide read is skipped. A policy-version bump with no store mutation is invisible to
	// the signal; the staleness it leaves is accepted and heals on the next mutation
	var pending pendingCoverage
	if changed {
		pending, err = a.derivePendingCoverage(ctx, view)
		if err != nil {
			return nil, err
		}
		report.PendingCoverage = pending.Total
	}

	limit := int32(cfg.Enricher.TagAudit.SampleSize)
	floor := int64(cfg.Enricher.ContentAnalyzer.MinTags)
	worklist, err := a.worklist(ctx, limit, floor)
	if err != nil {
		return nil, err
	}
	report.SweepCount = worklist.SweepCount
	report.EligiblePool = worklist.EligiblePool
	report.Sampled = worklist.Sampled
	report.Flagged = worklist.Flagged
	report.ZeroWork = !changed && worklist.Sampled == 0 && worklist.Flagged == 0

	bar := auditTriggerSimilarity
	var records []tags.CoverageRecord
	for _, docID := range worklist.DocumentIDs {
		outcome, derr := a.auditDocument(ctx, docID, worklist.SweepCount, view, twins, bar)
		if derr != nil {
			if isCreditError(derr) {
				return nil, derr
			}
			a.logger.Warn("", "tag audit: document %d failed: %v", docID, derr)
			continue
		}
		if outcome.Stats.skipped {
			continue
		}
		if outcome.Stats.matcherMissing {
			report.MatcherMissing++
		}
		report.NoAdjudicator = report.NoAdjudicator || outcome.Stats.noAdjudicator
		report.Fills += outcome.Stats.fills
		report.Flags += outcome.Stats.flags
		report.Proposals += outcome.Stats.proposals
		report.Calls += outcome.Stats.calls
		report.Cached += outcome.Stats.cached
		report.Salvaged += outcome.Stats.salvaged
		records = append(records, outcome.Records...)
	}

	if err := a.writeMeasurements(ctx, runID, records, pending.ByDimension); err != nil {
		return nil, err
	}

	for _, rec := range records {
		report.Coverage[string(rec.Class)]++
	}
	for _, n := range pending.ByDimension {
		report.Coverage[string(tags.CoveragePendingCoverage)] += n
	}

	a.logger.Info("", "tag audit run %s: sweep=%d eligible_pool=%d sampled=%d flagged=%d matcher_unavailable=%d no_adjudicator=%t fills=%d flags=%d proposals=%d calls=%d cached=%d salvaged=%d pending_coverage=%d coverage=%v",
		report.RunID, report.SweepCount, report.EligiblePool, report.Sampled, report.Flagged, report.MatcherMissing, report.NoAdjudicator,
		report.Fills, report.Flags, report.Proposals, report.Calls, report.Cached, report.Salvaged,
		report.PendingCoverage, report.Coverage)
	return report, nil
}

// loadStoreView loads the promoted store view (the fold anchor) and the live store key set the derivations
// and liveness checks read.
func (a *AuditRunner) loadStoreView(ctx context.Context) (storeView, error) {
	q := a.client.Queries
	names, err := q.ListStoreTagKeys(ctx)
	if err != nil {
		return storeView{}, fmt.Errorf("list store tag keys: %w", err)
	}
	storeNames := tags.StoreNames(names)
	live := make(map[string]struct{}, len(storeNames))
	for key := range storeNames {
		live[key] = struct{}{}
	}
	promotedNames, err := q.ListPromotedTagNames(ctx)
	if err != nil {
		return storeView{}, fmt.Errorf("list promoted tag names: %w", err)
	}
	return storeView{Promoted: tags.StoreNames(promotedNames), Live: live}, nil
}

// canonicalizationPairs reads the latest measurement run's twin worklist; a store with no run yields none.
func (a *AuditRunner) canonicalizationPairs(ctx context.Context) ([]tags.Twin, error) {
	snap, err := tags.NewStore(a.client.Queries).LatestWorklists(ctx)
	if err != nil {
		return nil, err
	}
	if snap.Absent {
		return nil, nil
	}
	return snap.Worklists.Twins, nil
}

// worklist builds the audit's document set from the epoch register: it reads the sweep, rolls the epoch
// over when the eligible pool is exhausted, and draws both the random sample and the below-floor flagged
// documents from the pool — live documents whose register stamp is below the sweep, meaning they have
// never been audited or were last audited in an earlier sweep.
func (a *AuditRunner) worklist(ctx context.Context, limit int32, floor int64) (auditWorklist, error) {
	q := a.client.Queries
	raw, err := q.GetTagAuditSweep(ctx)
	if err != nil {
		return auditWorklist{}, fmt.Errorf("read tag audit sweep: %w", err)
	}
	sweep, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return auditWorklist{}, fmt.Errorf("parse tag audit sweep: %w", err)
	}
	pool, err := q.CountEligibleLiveDocuments(ctx, sweep)
	if err != nil {
		return auditWorklist{}, fmt.Errorf("count eligible documents: %w", err)
	}
	if pool == 0 {
		sweep++
		if err := q.SetTagAuditSweep(ctx, strconv.FormatInt(sweep, 10)); err != nil {
			return auditWorklist{}, fmt.Errorf("write tag audit sweep: %w", err)
		}
		if pool, err = q.CountEligibleLiveDocuments(ctx, sweep); err != nil {
			return auditWorklist{}, fmt.Errorf("count eligible documents: %w", err)
		}
		a.logger.Info("", "tag audit: sweep rolled over to %d (every live document eligible again)", sweep)
	}

	sample, err := q.SampleEligibleLiveDocumentIDs(ctx, database.SampleEligibleLiveDocumentIDsParams{SweepCount: sweep, PageLimit: limit})
	if err != nil {
		return auditWorklist{}, fmt.Errorf("sample eligible documents: %w", err)
	}
	below, err := q.ListEligibleLiveDocumentsBelowTagFloor(ctx, database.ListEligibleLiveDocumentsBelowTagFloorParams{SweepCount: sweep, TagFloor: floor, PageLimit: limit})
	if err != nil {
		return auditWorklist{}, fmt.Errorf("list eligible below-floor documents: %w", err)
	}

	worklist := auditWorklist{SweepCount: sweep, EligiblePool: pool}
	seen := make(map[int64]struct{}, len(sample)+len(below))
	for _, id := range sample {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		worklist.DocumentIDs = append(worklist.DocumentIDs, id)
	}
	worklist.Sampled = len(worklist.DocumentIDs)
	for _, id := range below {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		worklist.DocumentIDs = append(worklist.DocumentIDs, id)
		worklist.Flagged++
	}
	return worklist, nil
}

// derivePendingCoverage reads the store-wide composition evidence and writes the durable pending-coverage
// rows (D35): every live document's store-keyed compound whose resolved fan segment lacks a cached parts
// verdict and is not itself a live value.
func (a *AuditRunner) derivePendingCoverage(ctx context.Context, view storeView) (pendingCoverage, error) {
	q := a.client.Queries
	rows, err := q.ListLiveDocumentCompoundTags(ctx)
	if err != nil {
		return pendingCoverage{}, fmt.Errorf("list live document compound tags: %w", err)
	}
	type compoundRef struct {
		doc      int64
		compound string
	}
	refs := make([]compoundRef, 0, len(rows))
	segments := make(map[string]struct{})
	for _, r := range rows {
		if tags.ClassifyShape(r.TagName) != tags.ShapeCompound {
			continue
		}
		refs = append(refs, compoundRef{doc: r.DocumentID, compound: r.TagName})
		for _, seg := range tags.ResolvedSegments(r.TagName, view.Promoted, view.Live) {
			segments[seg] = struct{}{}
		}
	}
	if len(refs) == 0 {
		return pendingCoverage{ByDimension: map[string]int{}}, nil
	}

	values := make([]string, 0, len(segments))
	for v := range segments {
		values = append(values, v)
	}
	cached, err := a.cachedPartValues(ctx, values)
	if err != nil {
		return pendingCoverage{}, err
	}

	pendingByDoc := make(map[int64][]string)
	for _, ref := range refs {
		for _, seg := range tags.PendingCoverageSegments(ref.compound, view.Promoted, view.Live, cached) {
			pendingByDoc[ref.doc] = append(pendingByDoc[ref.doc], seg)
		}
	}
	if len(pendingByDoc) == 0 {
		return pendingCoverage{ByDimension: map[string]int{}}, nil
	}

	docIDs := make([]int64, 0, len(pendingByDoc))
	for doc := range pendingByDoc {
		docIDs = append(docIDs, doc)
	}
	existingRows, err := q.PendingCoverageNamesForDocuments(ctx, docIDs)
	if err != nil {
		return pendingCoverage{}, fmt.Errorf("read pending coverage names: %w", err)
	}
	recorded := make(map[int64]map[string]struct{}, len(docIDs))
	for _, row := range existingRows {
		if recorded[row.DocumentID] == nil {
			recorded[row.DocumentID] = map[string]struct{}{}
		}
		recorded[row.DocumentID][row.TagName] = struct{}{}
	}

	var newDocIDs []int64
	var newNames []string
	pendingByDim := map[string]int{}
	for doc, segs := range pendingByDoc {
		seen := make(map[string]struct{}, len(segs))
		for _, seg := range segs {
			if _, ok := recorded[doc][seg]; ok {
				continue
			}
			if _, ok := seen[seg]; ok {
				continue
			}
			seen[seg] = struct{}{}
			newDocIDs = append(newDocIDs, doc)
			newNames = append(newNames, seg)
			pendingByDim[seg]++
		}
	}
	for start := 0; start < len(newDocIDs); start += coverageInsertChunk {
		end := min(start+coverageInsertChunk, len(newDocIDs))
		if _, err := q.InsertTagCoverageEventsBulk(ctx, database.InsertTagCoverageEventsBulkParams{
			DocumentIds:     newDocIDs[start:end],
			TagNames:        newNames[start:end],
			Class:           string(tags.CoveragePendingCoverage),
			Source:          string(tags.CoverageSourceSkip),
			CriteriaVersion: analyzer.TagAuditVersion,
			Action:          string(tags.CoverageActionNone),
			RuleSetVersion:  tags.RuleSetVersion,
		}); err != nil {
			return pendingCoverage{}, fmt.Errorf("insert pending coverage: %w", err)
		}
	}
	return pendingCoverage{ByDimension: pendingByDim, Total: len(newDocIDs)}, nil
}

// cachedPartValues returns the set of resolved values that already carry a parts verdict under the current
// policy version, in bounded lookups. A failed lookup is returned, never treated as "nothing is cached":
// the pending-coverage rows must not be written from an incomplete cache read.
func (a *AuditRunner) cachedPartValues(ctx context.Context, values []string) (map[string]struct{}, error) {
	cached := make(map[string]struct{}, len(values))
	q := a.client.Queries
	for i := 0; i < len(values); i += verdictBatchSize {
		end := min(i+verdictBatchSize, len(values))
		rows, err := q.LatestTagPartVerdictsForValues(ctx, database.LatestTagPartVerdictsForValuesParams{
			PolicyVersion: analyzer.TagPartDimensionVersion,
			PartValues:    values[i:end],
		})
		if err != nil {
			return nil, fmt.Errorf("read parts verdicts: %w", err)
		}
		for _, row := range rows {
			cached[row.Value] = struct{}{}
		}
	}
	return cached, nil
}

// auditDocument verifies one document's coverage and writes its readings, fills, and register stamp.
func (a *AuditRunner) auditDocument(ctx context.Context, docID, sweep int64, view storeView, twins []tags.Twin, bar float64) (auditOutcome, error) {
	var stats auditDocStats
	q := a.client.Queries

	content, err := q.GetDocumentContentForAudit(ctx, docID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			stats.skipped = true
			return auditOutcome{Stats: stats}, nil
		}
		return auditOutcome{Stats: stats}, fmt.Errorf("load document content: %w", err)
	}
	text := content.TextContent.String
	hash := content.TextHash.String
	if strings.TrimSpace(text) == "" {
		stats.skipped = true
		return auditOutcome{Stats: stats}, nil
	}

	tagRows, err := q.GetDocumentTags(ctx, docID)
	if err != nil {
		return auditOutcome{Stats: stats}, fmt.Errorf("load document tags: %w", err)
	}
	carriedKeys := make(map[string]struct{}, len(tagRows))
	carried := make([]string, 0, len(tagRows))
	for _, t := range tagRows {
		carriedKeys[tags.Key(t.Name)] = struct{}{}
		carried = append(carried, t.Name)
	}

	cfg := a.cfg()
	judgeModel := cfg.ResolvedAdjudicatorLlm().Model
	chunkSize := 150
	llmText := text
	if r, rerr := a.runner.ReduceContent(ctx, text, chunkSize, matcher.TargetWordCount(int(content.WordCount), cfg.Enricher.TextReducer.TargetWords)); rerr == nil {
		llmText = r.Text
	}
	tagsText := text
	if r, rerr := a.runner.ReduceContent(ctx, text, chunkSize, matcher.TargetWordCount(int(content.WordCount), cfg.Enricher.TagMatcher.ReduceTargetWords)); rerr == nil {
		tagsText = r.Text
	}

	type flaggedDim struct {
		dim    string
		source tags.CoverageSource
	}
	var judgeWork []flaggedDim
	var records []tags.CoverageRecord

	if a.runner.HasTagAdjudicator() {
		result, merr := a.runner.MatchTags(ctx, strconv.FormatInt(docID, 10), tagsText)
		if merr != nil {
			stats.matcherMissing = true
		} else {
			for _, c := range result.Matches {
				if c.Similarity < bar {
					continue
				}
				if _, ok := carriedKeys[tags.Key(c.Tag)]; ok {
					continue
				}
				source := tags.CoverageSourceTrigger
				if c.Similarity < auditStrongSimilarity {
					if !lexicalSupport(text, c.Tag) {
						continue
					}
					source = tags.CoverageSourceLexical
				}
				judgeWork = append(judgeWork, flaggedDim{dim: c.Tag, source: source})
			}
		}
	} else {
		stats.noAdjudicator = true
	}

	var needsJudge []flaggedDim
	for _, f := range judgeWork {
		if class, ok := deterministicClass(f.dim, carried, view, twins); ok {
			recorded, cErr := a.coverageClassRecorded(ctx, docID, f.dim, class)
			if cErr != nil {
				return auditOutcome{Stats: stats}, cErr
			}
			if recorded {
				continue
			}
			records = append(records, tags.CoverageRecord{
				DocumentID: docID, Dimension: f.dim, Class: class, Source: f.source,
				TextHash: hash, CriteriaVersion: analyzer.TagAuditVersion,
				Action: tags.CoverageActionNone, RuleSetVersion: tags.RuleSetVersion,
			})
			continue
		}
		needsJudge = append(needsJudge, f)
	}

	verdicts := make(map[string]resolvedVerdict, len(needsJudge))
	var uncached []flaggedDim
	for _, f := range needsJudge {
		if ro, rerr := q.LatestTagCoverageReviewOutcome(ctx, database.LatestTagCoverageReviewOutcomeParams{DocumentID: docID, TagName: f.dim}); rerr == nil {
			if ro.ReviewOutcome.Valid && ro.ReviewOutcome.String == "not_applies" && ro.TextHash == hash {
				continue
			}
		}
		if v, ok := a.cachedAuditVerdict(ctx, docID, f.dim, hash); ok {
			v.source = f.source
			verdicts[f.dim] = v
			stats.cached++
			continue
		}
		uncached = append(uncached, f)
	}

	for i := 0; i < len(uncached); i += auditBatchSize {
		end := min(i+auditBatchSize, len(uncached))
		batch := uncached[i:end]
		candidates := make([]analyzer.TagAuditCandidate, len(batch))
		for j, f := range batch {
			candidates[j] = analyzer.TagAuditCandidate{Index: j, Term: f.dim}
		}
		results, jerr := a.runner.JudgeTagAudit(ctx, llmText, candidates)
		stats.calls++
		if jerr != nil && retryableJudgeFailure(jerr) {
			a.logger.Warn("", "tag audit: judge attempt failed, retrying once: %v", jerr)
			results, jerr = a.runner.JudgeTagAudit(ctx, llmText, candidates)
			stats.calls++
		}
		if jerr != nil {
			if isCreditError(jerr) {
				return auditOutcome{Stats: stats}, jerr
			}
			var incomplete *analyzer.IncompleteReplyError
			if !errors.As(jerr, &incomplete) {
				a.logger.Warn("", "tag audit: judge failed for %d candidates, deferring them: %v", len(batch), jerr)
				continue
			}
			missing := incomplete.MissingSet()
			for j, r := range results {
				if j >= len(batch) || missing[j] {
					continue
				}
				verdicts[batch[j].dim] = freshVerdict(r, batch[j].source, judgeModel)
				stats.salvaged++
			}
			a.logger.Warn("", "tag audit: partial reply, salvaged %d of %d candidates (missing re-audited next run): %v",
				stats.salvaged, len(batch), jerr)
			continue
		}
		if len(results) != len(batch) {
			a.logger.Warn("", "tag audit: judge returned %d verdicts for %d candidates, deferring them", len(results), len(batch))
			continue
		}
		for j, r := range results {
			verdicts[batch[j].dim] = freshVerdict(r, batch[j].source, judgeModel)
		}
	}

	var fills []pendingFill
	for _, f := range needsJudge {
		rv, ok := verdicts[f.dim]
		if !ok {
			continue
		}
		rec := tags.CoverageRecord{
			DocumentID: docID, Dimension: f.dim, Source: rv.source, TextHash: hash,
			CriteriaVersion: analyzer.TagAuditVersion, RuleSetVersion: tags.RuleSetVersion,
		}
		if rv.fresh {
			rec.Verdict = rv.raw
			rec.VerdictModel = rv.model
		}
		switch {
		case rv.result.Defer:
			rec.Class = tags.CoverageGray
			rec.Action = tags.CoverageActionFlag
			if rv.fresh {
				stats.flags++
			}
		case rv.result.Applies:
			tag, terr := q.GetTagByName(ctx, f.dim)
			if terr != nil {
				rec.Class = tags.CoverageMissing
				rec.Action = tags.CoverageActionProposal
				if rv.fresh {
					stats.proposals++
				}
				break
			}
			rec.Class = tags.CoverageExplicit
			rec.Action = tags.CoverageActionFill
			rec.Provisional = !tag.PromotedAt.Valid
			fills = append(fills, pendingFill{dim: f.dim, tagID: tag.ID})
			stats.fills++
		default:
			rec.Class = tags.CoverageNotApplies
			rec.Action = tags.CoverageActionNone
		}
		// a cached verdict already carries its row: only a fresh judgement writes one, so re-audits
		// neither duplicate rows nor reopen a settled reading; the fill still runs for a cached applies
		// whose attachment was lost, since it is idempotent
		if rv.fresh {
			records = append(records, rec)
		}
	}

	// the trigger is the only flag source: when the matcher or the adjudicator was unavailable no reading
	// was taken, so the document stays eligible for the rest of the sweep instead of burning its slot
	if stats.noAdjudicator || stats.matcherMissing {
		return auditOutcome{Records: records, Stats: stats}, nil
	}

	if err := a.persistDocument(ctx, docID, sweep, records, fills); err != nil {
		return auditOutcome{Stats: stats}, err
	}
	return auditOutcome{Records: records, Stats: stats}, nil
}

// persistDocument writes a document's register stamp, fills, and coverage rows in one transaction, so an
// attachment and its evidence always land together.
func (a *AuditRunner) persistDocument(ctx context.Context, docID, sweep int64, records []tags.CoverageRecord, fills []pendingFill) error {
	tx, err := a.client.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tag audit transaction: %w", err)
	}
	defer tx.Rollback()
	q := a.client.Queries.WithTx(tx)

	// a document reaching the persist step has been audited this sweep: its register stamp records that
	// sweep so the register does not draw it again until the next one (a document skipped for deletion or
	// empty text, or whose audit was degraded, never gets here and stays eligible)
	if err := q.UpsertTagAuditRegister(ctx, database.UpsertTagAuditRegisterParams{DocumentID: docID, SweepCount: sweep}); err != nil {
		return fmt.Errorf("stamp tag audit register: %w", err)
	}

	for _, f := range fills {
		if err := q.AddDocumentTag(ctx, database.AddDocumentTagParams{DocumentID: docID, TagID: f.tagID, Source: curation.DocumentTagSourceAudit}); err != nil {
			return fmt.Errorf("attach audited tag %q: %w", f.dim, err)
		}
	}
	for _, rec := range records {
		verdict := rec.Verdict
		if len(verdict) == 0 {
			verdict = json.RawMessage("{}")
		}
		if _, err := q.InsertTagCoverageEvent(ctx, database.InsertTagCoverageEventParams{
			DocumentID:      docID,
			TagName:         rec.Dimension,
			Class:           string(rec.Class),
			Source:          string(rec.Source),
			Verdict:         verdict,
			VerdictModel:    sql.NullString{String: rec.VerdictModel, Valid: rec.VerdictModel != ""},
			TextHash:        rec.TextHash,
			CriteriaVersion: rec.CriteriaVersion,
			Action:          string(rec.Action),
			Provisional:     rec.Provisional,
			RuleSetVersion:  rec.RuleSetVersion,
		}); err != nil {
			return fmt.Errorf("record coverage for %q: %w", rec.Dimension, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tag audit transaction: %w", err)
	}
	return nil
}

// coverageClassRecorded reports whether the document and dimension already carry an open reading of the
// class under the current criteria version, so a re-audit records the deterministic pending class once. A
// pair with no reading is simply not recorded; a failed read is returned rather than mistaken for one.
func (a *AuditRunner) coverageClassRecorded(ctx context.Context, docID int64, dim string, class tags.CoverageClass) (bool, error) {
	row, err := a.client.Queries.LatestTagCoverageEventForPair(ctx, database.LatestTagCoverageEventForPairParams{DocumentID: docID, TagName: dim})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read latest coverage reading: %w", err)
	}
	return row.Class == string(class) && row.CriteriaVersion == analyzer.TagAuditVersion, nil
}

// cachedAuditVerdict reads a fresh applicability verdict for the document, dimension and content hash.
func (a *AuditRunner) cachedAuditVerdict(ctx context.Context, docID int64, dim, hash string) (resolvedVerdict, bool) {
	row, err := a.client.Queries.LatestTagCoverageVerdict(ctx, database.LatestTagCoverageVerdictParams{
		DocumentID:      docID,
		TagName:         dim,
		TextHash:        hash,
		CriteriaVersion: analyzer.TagAuditVersion,
	})
	if err != nil {
		return resolvedVerdict{}, false
	}
	var c analyzer.TagAuditCriteria
	if err := json.Unmarshal(row.Verdict, &c); err != nil {
		return resolvedVerdict{}, false
	}
	r, ok := c.Result()
	if !ok {
		return resolvedVerdict{}, false
	}
	return resolvedVerdict{result: r, model: row.VerdictModel.String, raw: row.Verdict}, true
}

func freshVerdict(r analyzer.TagAuditResult, source tags.CoverageSource, model string) resolvedVerdict {
	raw, err := json.Marshal(r.Criteria())
	if err != nil {
		raw = json.RawMessage("{}")
	}
	return resolvedVerdict{result: r, fresh: true, model: model, raw: raw, source: source}
}

// deterministicClass classifies a flagged dimension by the pending classes that need no judge call: a
// still-whole compound whose cover resolves to it is pending-split, and a carried label recorded as a
// canonicalization twin is pending-canonicalization (docs/tags/completeness.md §3.4).
func deterministicClass(dim string, carried []string, view storeView, twins []tags.Twin) (tags.CoverageClass, bool) {
	for _, c := range carried {
		if tags.ClassifyShape(c) == tags.ShapeCompound && tags.CoversDimension(c, dim, view.Promoted, view.Live) {
			return tags.CoveragePendingSplit, true
		}
	}
	key := tags.Key(dim)
	for _, c := range carried {
		if tags.Key(c) == key {
			continue
		}
		if tags.TwinPartner(twins, c, dim) {
			return tags.CoveragePendingCanonicalization, true
		}
	}
	return "", false
}

// lexicalSupport reports whether the dimension's content-word stems appear in the document text, the cheap
// narrowing the weak trigger band requires (docs/tags/completeness.md §3.3).
func lexicalSupport(text, dimension string) bool {
	lower := strings.ToLower(text)
	for _, word := range tags.DeriveParts(dimension) {
		stems := strings.Fields(tags.ComputeSignature(word).Stems)
		if len(stems) == 0 {
			continue
		}
		if strings.Contains(lower, stems[0]) {
			return true
		}
	}
	return false
}

// writeMeasurements records the per-dimension coverage counts in the measurement substrate: explicit is the
// store-wide live mass and the gap classes are the run's tallies, with the sampled caveat.
func (a *AuditRunner) writeMeasurements(ctx context.Context, runID uuid.UUID, records []tags.CoverageRecord, pendingByDim map[string]int) error {
	q := a.client.Queries
	tally := make(map[string]map[string]int)
	for _, rec := range records {
		if tally[rec.Dimension] == nil {
			tally[rec.Dimension] = map[string]int{}
		}
		tally[rec.Dimension][string(rec.Class)]++
	}
	for dim, n := range pendingByDim {
		if tally[dim] == nil {
			tally[dim] = map[string]int{}
		}
		tally[dim][string(tags.CoveragePendingCoverage)] += n
	}
	if len(tally) == 0 {
		return nil
	}

	dims := make([]string, 0, len(tally))
	for dim := range tally {
		dims = append(dims, dim)
	}
	massByDim := make(map[string]int64, len(dims))
	massRows, err := q.CountLiveTagDocumentsByNames(ctx, dims)
	if err != nil {
		return fmt.Errorf("count coverage mass: %w", err)
	}
	for _, row := range massRows {
		massByDim[row.Name] = row.DocumentCount
	}

	snapshotAt, err := q.SnapshotNow(ctx)
	if err != nil {
		return fmt.Errorf("read audit snapshot: %w", err)
	}

	measurement := make([]tags.Record, 0, len(tally))
	for dim, classes := range tally {
		numbers := map[string]any{"explicit": massByDim[dim]}
		for class, n := range classes {
			// explicit stays the store-wide live mass; the run's tally overlays only the gap classes, so a
			// judged fill never replaces the global count with its sampled one
			if class == string(tags.CoverageExplicit) {
				continue
			}
			numbers[class] = n
		}
		measurement = append(measurement, tags.Record{
			Operator: tags.OperatorCoverage,
			Operands: []string{dim},
			Numbers:  numbers,
			State:    tags.StateSufficient,
			Caveats:  []string{auditCaveatSampled},
		})
	}
	return tags.NewStore(q).InsertRecords(ctx, runID, snapshotAt, tags.RuleSetVersion, measurement)
}
