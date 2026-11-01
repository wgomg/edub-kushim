package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/admission"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/types"
)

func (r *Reconciler) sweepOrphanTags(ctx context.Context, cfg *config.Config, report *Report) {
	graceDays := cfg.Enricher.TagReconcile.DeadTagGraceDays
	cutoff := time.Now().Add(-time.Duration(graceDays) * 24 * time.Hour)

	q := r.client.Queries
	if report.ReportOnly {
		// refresh marks in a rolled-back transaction so the report predicts
		// the next apply on the same state without persisting anything
		tx, err := r.client.BeginTx(ctx, nil)
		if err != nil {
			r.logger.Error("", "tag reconcile: begin orphan mark transaction: %v", err)
			return
		}
		defer tx.Rollback()
		q = r.client.WithTx(tx)
	}

	if _, err := q.ClearOrphanTagMarks(ctx); err != nil {
		r.logger.Error("", "tag reconcile: clear orphan tag marks: %v", err)
		return
	}
	if _, err := q.MarkOrphanTags(ctx); err != nil {
		r.logger.Error("", "tag reconcile: mark orphan tags: %v", err)
		return
	}

	planned, err := q.ListOrphanTags(ctx, cutoff)
	if err != nil {
		r.logger.Error("", "tag reconcile: list orphan tags: %v", err)
		return
	}
	if len(planned) == 0 {
		return
	}

	if report.ReportOnly {
		report.Swept = len(planned)
		r.logger.Info("", "tag reconcile (report-only): would delete %d tags orphaned for over %d days%s",
			len(planned), graceDays, sampleNamesSuffix(planned))
		return
	}

	deleted, err := q.DeleteOrphanTags(ctx, cutoff)
	if err != nil {
		r.logger.Error("", "tag reconcile: delete orphan tags: %v", err)
		return
	}
	report.Swept = len(deleted)
	if err := r.tag.RemoveFromStore(ctx, deleted); err != nil {
		r.logger.Error("", "tag reconcile: remove swept tags from store: %v", err)
	}
	r.logger.Info("", "tag reconcile: deleted %d tags orphaned for over %d days%s",
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

// provisionalOutcome applies one provisional tag's decision: an admitted
// decision at or above the live-mass floor promotes; a reject or defer
// logs once and stays for review; an admit below the floor stays a silent
// pending candidate.
func (r *Reconciler) provisionalOutcome(name string, decision tags.AdmissionDecision, docs int64) (promote, deferred bool) {
	if tags.DecidePromotion(decision.Outcome, docs) {
		return true, false
	}
	if decision.Outcome != tags.AdmissionAdmit {
		r.logger.Info("", "tag reconcile: provisional tag %q left for review (outcome=%s reason=%s)", name, decision.Outcome, decision.Reason)
		return false, true
	}
	return false, false
}

func (r *Reconciler) sweepProvisionalTags(ctx context.Context, cfg *config.Config, report *Report) {
	graceDays := cfg.Enricher.TagReconcile.ProvisionalGraceDays
	cutoff := time.Now().Add(-time.Duration(graceDays) * 24 * time.Hour)

	worklist, err := r.client.ListProvisionalTagsOlderThan(ctx, cutoff)
	if err != nil {
		r.logger.Error("", "tag reconcile: list provisional tags: %v", err)
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
	held, err := tags.IdentityHeldNames(ctx, r.client.Queries, names)
	if err != nil {
		r.logger.Error("", "tag reconcile: provisional identity hold lookup: %v", err)
		return
	}
	compositionHeld, err := tags.CompositionHeldNames(ctx, r.client.Queries, names, analyzer.TagSplitVersion)
	if err != nil {
		r.logger.Error("", "tag reconcile: provisional composition hold lookup: %v", err)
		return
	}
	assigned, err := r.client.CountTagDocumentsByTagIDs(ctx, ids)
	if err != nil {
		r.logger.Error("", "tag reconcile: count provisional tag documents: %v", err)
		return
	}
	assignedCounts := make(map[int64]int64, len(assigned))
	for _, row := range assigned {
		assignedCounts[row.TagID] = row.DocumentCount
	}

	// promotion evidence is live-document only, like the judge's text input
	liveCounts, err := tags.NewStore(r.client.Queries).LiveMass(ctx, ids)
	if err != nil {
		r.logger.Error("", "tag reconcile: count live provisional tag documents: %v", err)
		return
	}

	events, err := r.tag.LatestQualityEventsForNames(ctx, names)
	if err != nil {
		r.logger.Error("", "tag reconcile: provisional tag verdict lookup: %v", err)
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
		if anchor, ok := held[t.Name]; ok {
			report.Provisional.Skipped++
			r.logger.Info("", "tag reconcile: identity hold %q (anchor=%q) — left for review", t.Name, anchor)
			continue
		}
		if reason, ok := compositionHeld[t.Name]; ok {
			report.Provisional.Skipped++
			r.logger.Info("", "tag reconcile: composition hold %q (reason=%s) — left for review", t.Name, reason)
			continue
		}
		docs := liveCounts[t.ID]

		row, ok := verdicts[t.Name]
		if !ok {
			rejudge = append(rejudge, &provisionalEntry{id: t.ID, name: t.Name, docs: docs})
			continue
		}
		decision := admission.DecisionFromStored(row.Criteria, row.Pass, tags.AdmissionFeatures{})
		if promote, _ := r.provisionalOutcome(t.Name, decision, docs); !promote {
			continue
		}

		if report.ReportOnly {
			report.Provisional.Promoted++
			r.logger.Info("", "tag reconcile (report-only): would promote %q (docs=%d model=%s rule_set=decision-%s floor=%d)",
				t.Name, docs, verdictModelLabel(row.VerdictModel), tags.DecisionRuleSetVersion, tags.RecurrenceFloor)
			continue
		}
		if _, status, err := r.tag.Promote(ctx, t.ID, "sweep"); err != nil {
			r.logger.Error("", "tag reconcile: promote provisional tag %q: %v", t.Name, err)
		} else if status == types.PromotePromoted {
			report.Provisional.Promoted++
			r.logger.Info("", "tag reconcile: promoted %q (docs=%d model=%s rule_set=decision-%s floor=%d)",
				t.Name, docs, verdictModelLabel(row.VerdictModel), tags.DecisionRuleSetVersion, tags.RecurrenceFloor)
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
		r.logger.Info("", "tag reconcile (report-only): would re-judge %d provisional tags older than %d days", len(rejudge), graceDays)
		return
	}
	if !r.runner.HasTagQualityJudge() {
		report.Provisional.NoJudge = len(rejudge)
		r.logger.Info("", "tag reconcile: tag quality judge is not configured — %d provisional tags stay queued", len(rejudge))
		return
	}

	r.rejudgeProvisionalTags(ctx, cfg, rejudge, report)
}

// errNoJudgeText marks a stored name with no live document text, which callers count as skipped, not failed.
var errNoJudgeText = errors.New("no live document text")

// rejudgeStoredName decides one stored name afresh under the current criteria from its first live document
// and records the verdict, so the provisional sweep and the demotion arm judge identically. A name with no
// live document text yields errNoJudgeText, and any other failure is returned for the caller to count.
func (r *Reconciler) rejudgeStoredName(ctx context.Context, cfg *config.Config, tagID int64, name string) (tags.AdmissionDecision, string, error) {
	docID, err := r.client.FirstDocumentIDForTag(ctx, tagID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			r.logger.Info("", "tag reconcile: tag %q has no live document — left for review", name)
			return tags.AdmissionDecision{}, "", errNoJudgeText
		}
		return tags.AdmissionDecision{}, "", err
	}
	doc, err := r.client.GetDocumentTextForTagQuality(ctx, docID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			r.logger.Info("", "tag reconcile: tag %q has no live document text — left for review", name)
			return tags.AdmissionDecision{}, "", errNoJudgeText
		}
		return tags.AdmissionDecision{}, "", err
	}

	reduced, err := r.runner.ReduceContent(ctx, doc.TextContent.String,
		matcher.TargetWordCount(int(doc.WordCount), cfg.Enricher.TagMatcher.ReduceTargetWords))
	if err != nil {
		r.logger.Error("", "tag reconcile: reduce text for tag %q: %v", name, err)
	}

	candidates := []analyzer.TagQualityCandidate{{
		Index:    0,
		Term:     name,
		Category: analyzer.TagCategoryOther,
	}}
	results, err := r.runner.JudgeTagQuality(ctx, reduced.Text, candidates)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		results, err = r.runner.JudgeTagQuality(ctx, reduced.Text, candidates)
	}
	if err != nil {
		return tags.AdmissionDecision{}, "", err
	}
	if len(results) != len(candidates) {
		return tags.AdmissionDecision{}, "", fmt.Errorf("judge returned %d verdicts for %q", len(results), name)
	}

	res := results[0]
	// the parts pass owns the dimension question: resolve its verdict for this name and stamp it, so a
	// re-judged name is not decided on a fabricated false dimension; an absent or abstained verdict defers
	partStore := admission.NewVerdictStore(r.client.Queries, r.runner, r.logger, cfg.ResolvedAdjudicatorLlm().Model, nil)
	dimension, abstain, verr := partStore.DimensionFeature(ctx, name, docID, "")
	if verr != nil {
		return tags.AdmissionDecision{}, "", verr
	}
	if abstain {
		res.Defer = true
	} else {
		res.Dimension = dimension
	}
	criteria, err := json.Marshal(res.Criteria())
	if err != nil {
		return tags.AdmissionDecision{}, "", err
	}
	decision := tags.DecideAdmission(admission.JudgeFeatures(res, tags.AdmissionFeatures{}))
	model := cfg.ResolvedQualityLlm().Model
	storedCriteria, pass, finalModel, err := r.recordProvisionalVerdict(ctx, docID, name, criteria, decision.Outcome == tags.AdmissionAdmit, decision.Outcome.String(), model)
	if err != nil {
		return tags.AdmissionDecision{}, "", err
	}
	decision = admission.DecisionFromStored(storedCriteria, pass, tags.AdmissionFeatures{})
	return decision, finalModel, nil
}

func (r *Reconciler) rejudgeProvisionalTags(ctx context.Context, cfg *config.Config, entries []*provisionalEntry, report *Report) {
	for _, e := range entries {
		decision, finalModel, err := r.rejudgeStoredName(ctx, cfg, e.id, e.name)
		if err != nil {
			if errors.Is(err, errNoJudgeText) {
				report.Provisional.Skipped++
				continue
			}
			r.logger.Warn("", "tag reconcile: judge provisional tag %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}
		report.Provisional.Rejudged++

		if promote, deferred := r.provisionalOutcome(e.name, decision, e.docs); !promote {
			if deferred {
				report.Provisional.Deferred++
			}
			continue
		}
		if _, status, err := r.tag.Promote(ctx, e.id, "sweep"); err != nil {
			r.logger.Error("", "tag reconcile: promote provisional tag %q: %v", e.name, err)
		} else if status == types.PromotePromoted {
			report.Provisional.Promoted++
			r.logger.Info("", "tag reconcile: promoted %q (docs=%d model=%s rule_set=decision-%s floor=%d)",
				e.name, e.docs, finalModel, tags.DecisionRuleSetVersion, tags.RecurrenceFloor)
		}
	}
}

// sweepPromotedTags withdraws the standing of values the current rules no longer back: their live mass fell
// below the recurrence floor, or a stale verdict re-judges to reject or defer. A current-version verdict is
// respected as it stands, so an operator's promotion survives until the next criteria version; held names
// and judge failures stay promoted.
func (r *Reconciler) sweepPromotedTags(ctx context.Context, cfg *config.Config, report *Report) {
	worklist, err := r.client.ListPromotedTagsForDemotion(ctx)
	if err != nil {
		r.logger.Error("", "tag reconcile: list promoted tags for demotion: %v", err)
		return
	}
	report.Demotion.Worklist = len(worklist)
	if len(worklist) == 0 {
		return
	}

	ids := make([]int64, len(worklist))
	names := make([]string, len(worklist))
	for i, t := range worklist {
		ids[i] = t.ID
		names[i] = t.Name
	}

	held, err := tags.IdentityHeldNames(ctx, r.client.Queries, names)
	if err != nil {
		r.logger.Error("", "tag reconcile: demotion identity hold lookup: %v", err)
		return
	}
	compositionHeld, err := tags.CompositionHeldNames(ctx, r.client.Queries, names, analyzer.TagSplitVersion)
	if err != nil {
		r.logger.Error("", "tag reconcile: demotion composition hold lookup: %v", err)
		return
	}

	liveCounts, err := tags.NewStore(r.client.Queries).LiveMass(ctx, ids)
	if err != nil {
		r.logger.Error("", "tag reconcile: count promoted tag live documents: %v", err)
		return
	}

	events, err := r.tag.LatestQualityEventsForNames(ctx, names)
	if err != nil {
		r.logger.Error("", "tag reconcile: promoted tag verdict lookup: %v", err)
		return
	}
	verdicts := make(map[string]database.LatestTagQualityEventsForNamesRow, len(events))
	for _, e := range events {
		verdicts[e.TagName] = e
	}

	var massDecay []database.ListPromotedTagsForDemotionRow
	var rejudge []database.ListPromotedTagsForDemotionRow
	for _, t := range worklist {
		if anchor, ok := held[t.Name]; ok {
			report.Demotion.Held++
			r.logger.Info("", "tag reconcile: demotion identity hold %q (anchor=%q) — left for review", t.Name, anchor)
			continue
		}
		if reason, ok := compositionHeld[t.Name]; ok {
			report.Demotion.Held++
			r.logger.Info("", "tag reconcile: demotion composition hold %q (reason=%s) — left for review", t.Name, reason)
			continue
		}

		if tags.DecideMassDemotion(liveCounts[t.ID]) {
			massDecay = append(massDecay, t)
			continue
		}

		if _, ok := verdicts[t.Name]; ok {
			// a current-version verdict: the value stands as decided; only live mass can still demote it
			continue
		}
		rejudge = append(rejudge, t)
	}

	// revocations and re-judgments draw on one per-run bound, so a bulk document purge cannot make a single
	// run fire an unbounded burst of store RPCs; whatever the bound defers stays on the worklist for the next run
	budget := cfg.Enricher.TagReconcile.MaxRejudge
	if len(massDecay) > budget {
		report.Capped += len(massDecay) - budget
		massDecay = massDecay[:budget]
	}
	for _, t := range massDecay {
		report.Demotion.MassDecay++
		r.demote(ctx, report, t.ID, t.Name, tags.DemotionMassDecay)
	}

	if len(rejudge) == 0 {
		return
	}

	remaining := max(budget-len(massDecay), 0)
	if len(rejudge) > remaining {
		report.Capped += len(rejudge) - remaining
		rejudge = rejudge[:remaining]
	}
	if len(rejudge) == 0 {
		return
	}

	if report.ReportOnly {
		report.Demotion.Rejudged = len(rejudge)
		r.logger.Info("", "tag reconcile (report-only): would re-judge %d promoted tags under the current criteria", len(rejudge))
		return
	}
	if !r.runner.HasTagQualityJudge() {
		report.Demotion.NoJudge = len(rejudge)
		r.logger.Info("", "tag reconcile: tag quality judge is not configured — %d promoted tags keep their influence", len(rejudge))
		return
	}

	for _, t := range rejudge {
		decision, _, err := r.rejudgeStoredName(ctx, cfg, t.ID, t.Name)
		if err != nil {
			if errors.Is(err, errNoJudgeText) {
				report.Demotion.Skipped++
				continue
			}
			r.logger.Warn("", "tag reconcile: judge promoted tag %q: %v", t.Name, err)
			report.Demotion.Failed++
			continue
		}
		report.Demotion.Rejudged++
		if decision.Outcome == tags.AdmissionAdmit {
			continue
		}
		r.demote(ctx, report, t.ID, t.Name, decision.Reason)
	}
}

func (r *Reconciler) demote(ctx context.Context, report *Report, id int64, name, reason string) {
	if report.ReportOnly {
		r.planDemotion(report, name, reason)
		return
	}
	r.applyDemotion(ctx, id, name, reason, report)
}

// planDemotion records one demotion a report-only run would apply, so the planned list, the tally, and the
// log line have a single owner instead of being repeated at each reporting call site.
func (r *Reconciler) planDemotion(report *Report, name, reason string) {
	report.Demotion.Demoted++
	report.PlannedDemotions = append(report.PlannedDemotions, DemotionEntry{Name: name, Reason: reason})
	r.logger.Info("", "tag reconcile (report-only): would demote %q (reason=%s rule_set=decision-%s)",
		name, reason, tags.DecisionRuleSetVersion)
}

func (r *Reconciler) recordProvisionalVerdict(ctx context.Context, docID int64, name string, criteria json.RawMessage, pass bool, outcome string, model string) (json.RawMessage, bool, string, error) {
	verdictModel := sql.NullString{String: model, Valid: model != ""}
	stored, err := r.client.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docID,
		TagName:       textnorm.NormalizeTag(name),
		Criteria:      criteria,
		Pass:          pass,
		Outcome:       sql.NullString{String: outcome, Valid: outcome != ""},
		VerdictModel:  verdictModel,
		PolicyVersion: analyzer.TagQualityVersion,
	})
	if err != nil {
		return nil, false, "", err
	}
	if stored > 0 || !verdictModel.Valid {
		return criteria, pass, model, nil
	}

	// another writer recorded a fresh verdict first — keep it, never overwrite
	rows, err := r.tag.LatestQualityEventsForNames(ctx, []string{name})
	if err != nil {
		return nil, false, "", err
	}
	if len(rows) == 0 {
		return criteria, pass, model, nil
	}
	return rows[0].Criteria, rows[0].Pass, verdictModelLabel(rows[0].VerdictModel), nil
}

func verdictModelLabel(model sql.NullString) string {
	if !model.Valid || model.String == "" {
		return "cached"
	}
	return model.String
}

// sweepTwins works the canonicalization worklist the measurement substrate proposes (extension-algebra D1).
// The rules dispose, never the measurement: a pinned variant class aliases to the PreferCanonical winner
// with no judge call, and the rest become pending same-intent evidence the run's own identity sweep settles,
// so a store-side pair is judged exactly as an emitted one. A loser aliases and is never deleted; held names
// stay for review. A failed hold, alias, settled, or mass lookup aborts the arm rather than disabling the
// gate, the pair list is bounded by MaxRejudge, and the alias documents by MaxRetag.
func (r *Reconciler) sweepTwins(ctx context.Context, cfg *config.Config, report *Report, pairKeys map[string]struct{}) {
	worklists, err := tags.NewStore(r.client.Queries).LatestWorklists(ctx)
	if err != nil {
		r.logger.Error("", "tag reconcile: load measurement worklists: %v", err)
		return
	}
	report.Measurement = &MeasurementRun{
		RunID:      worklists.RunID.String(),
		SnapshotAt: worklists.SnapshotAt,
		Absent:     worklists.Absent,
	}
	report.Twins.Worklist = len(worklists.Worklists.Twins)
	report.Worklist.Twins = report.Twins.Worklist
	report.Siblings = SiblingCounts{Worklist: len(worklists.Worklists.Siblings)}
	for _, sibling := range worklists.Worklists.Siblings {
		if sibling.Gray {
			report.Siblings.Gray++
		} else {
			report.Siblings.Firm++
		}
	}
	if worklists.Absent || len(worklists.Worklists.Twins) == 0 {
		return
	}

	twins := worklists.Worklists.Twins
	if len(twins) > cfg.Enricher.TagReconcile.MaxRejudge {
		report.Capped += len(twins) - cfg.Enricher.TagReconcile.MaxRejudge
		twins = twins[:cfg.Enricher.TagReconcile.MaxRejudge]
	}

	participantNames := make(map[string]struct{}, len(twins)*2)
	for _, twin := range twins {
		participantNames[twin.A] = struct{}{}
		participantNames[twin.B] = struct{}{}
	}
	names := make([]string, 0, len(participantNames))
	for name := range participantNames {
		names = append(names, name)
	}

	// the run's own splits and flips may have removed rows since the snapshot, so read the live names fresh
	allTags, err := r.tag.ListAll(ctx)
	if err != nil {
		r.logger.Error("", "tag reconcile: twins: list store names failed: %v", err)
		return
	}
	live := make(map[string]struct{}, len(allTags))
	tagID := make(map[string]int64, len(allTags))
	for _, t := range allTags {
		live[t.Name] = struct{}{}
		tagID[t.Name] = t.ID
	}

	// a failed lookup would silently disable the review gate in apply mode, so the arm aborts for the run
	// instead of degrading to empty safety state
	held, err := tags.IdentityHeldNames(ctx, r.client.Queries, names)
	if err != nil {
		r.logger.Error("", "tag reconcile: twins: identity hold lookup failed, arm skipped: %v", err)
		return
	}
	aliases, err := tags.ResolveAliases(ctx, r.client.Queries, names)
	if err != nil {
		r.logger.Error("", "tag reconcile: twins: alias lookup failed, arm skipped: %v", err)
		return
	}
	settled, err := r.settledTwinPairs(ctx, names)
	if err != nil {
		r.logger.Error("", "tag reconcile: twins: settled-pair lookup failed, arm skipped: %v", err)
		return
	}

	seen := make(map[string]struct{}, len(twins))
	var aliasActions []tags.AliasAction
	var pendingRows []tags.EvidenceRow
	for _, twin := range twins {
		a, b := twin.A, twin.B
		if _, ok := live[a]; !ok {
			report.Twins.Skipped++
			continue
		}
		if _, ok := live[b]; !ok {
			report.Twins.Skipped++
			continue
		}
		if _, ok := held[a]; ok {
			report.Twins.Held++
			continue
		}
		if _, ok := held[b]; ok {
			report.Twins.Held++
			continue
		}
		if textnorm.SplitJointTag(a) != nil || textnorm.SplitJointTag(b) != nil {
			report.Twins.Skipped++
			continue
		}
		if aliases[a] != "" || aliases[b] != "" {
			report.Twins.Skipped++
			continue
		}
		key := twinPairKey(a, b)
		if _, ok := settled[key]; ok {
			report.Twins.Settled++
			continue
		}
		if _, ok := pairKeys[key]; ok {
			report.Twins.Skipped++
			continue
		}
		if _, ok := seen[key]; ok {
			report.Twins.Skipped++
			continue
		}
		seen[key] = struct{}{}

		if tags.VariantClass(a, b) != "" {
			winner := tags.PreferCanonical(a, b)
			loser := a
			if winner == a {
				loser = b
			}
			if report.ReportOnly {
				report.PlannedAliases = append(report.PlannedAliases, PairEntry{Query: loser, Target: winner, Reason: "deterministic"})
				report.Twins.Planned++
				r.logger.Info("", "tag reconcile (report-only): would alias twin %q onto %q (deterministic variant)", loser, winner)
				continue
			}
			aliasActions = append(aliasActions, tags.AliasAction{AliasName: loser, Canonical: winner, RemoveExistingTag: true})
			continue
		}

		if report.ReportOnly {
			report.Twins.Pending++
			continue
		}
		pendingRows = append(pendingRows, tags.EvidenceRow{
			Kind:      tags.IdentityKindSameIntent,
			Candidate: a,
			Partner:   b,
		})
	}

	if len(aliasActions) > 0 {
		aliasActions = r.boundTwinAliases(ctx, cfg, aliasActions, tagID, report)
	}

	if len(aliasActions) > 0 {
		outcomes, err := r.tag.ApplyAliasPlan(ctx, aliasActions)
		if err != nil {
			r.logger.Error("", "tag reconcile: twins: alias plan failed for %d pairs: %v", len(aliasActions), err)
			for range aliasActions {
				report.Skips["alias_error"]++
			}
		} else {
			for i, outcome := range outcomes {
				if !outcome.Applied {
					report.Skips["alias_skip"]++
					continue
				}
				report.Twins.Mechanical++
				report.Aliases = append(report.Aliases, PairEntry{Query: aliasActions[i].AliasName, Target: aliasActions[i].Canonical, Reassigned: outcome.Reassigned, Reason: "deterministic"})
				r.logger.Info("", "tag reconcile: aliased twin %q onto %q (%d documents)", aliasActions[i].AliasName, aliasActions[i].Canonical, outcome.Reassigned)
			}
		}
	}

	if len(pendingRows) > 0 {
		if err := r.tag.RecordIdentityEvidence(ctx, pendingRows); err != nil {
			r.logger.Error("", "tag reconcile: twins: record %d pending pairs failed: %v", len(pendingRows), err)
			report.Skips["identity_evidence_error"] += len(pendingRows)
		} else {
			report.Twins.Pending += len(pendingRows)
		}
	}
}

// boundTwinAliases truncates the twin alias actions to the run's document budget, using each loser's live
// mass as the re-pointing estimate the pair arm also uses; a mass lookup failure drops the whole batch for
// the run and the pairs past the budget are tallied under Capped.
func (r *Reconciler) boundTwinAliases(ctx context.Context, cfg *config.Config, actions []tags.AliasAction, tagID map[string]int64, report *Report) []tags.AliasAction {
	ids := make([]int64, 0, len(actions))
	for _, action := range actions {
		ids = append(ids, tagID[action.AliasName])
	}
	mass, err := tags.NewStore(r.client.Queries).LiveMass(ctx, ids)
	if err != nil {
		r.logger.Error("", "tag reconcile: twins: live mass lookup failed, arm skipped: %v", err)
		report.Skips["alias_error"] += len(actions)
		return nil
	}
	budget := cfg.Enricher.TagReconcile.MaxRetag
	var kept []tags.AliasAction
	for i, action := range actions {
		if budget <= 0 {
			report.Capped += len(actions) - i
			break
		}
		kept = append(kept, action)
		budget -= int(mass[tagID[action.AliasName]])
	}
	return kept
}

// settledTwinPairs returns the orientation-free keys of the pairs already settled at the current rule-set
// version, so the arm never re-proposes a pair the identity machinery has judged; a settled same-intent
// verdict names the pair whichever way it was stored.
func (r *Reconciler) settledTwinPairs(ctx context.Context, names []string) (map[string]struct{}, error) {
	if len(names) == 0 {
		return map[string]struct{}{}, nil
	}
	rows, err := r.client.Queries.LatestTagIdentityEventsForCandidates(ctx, database.LatestTagIdentityEventsForCandidatesParams{
		RuleSetVersion: tags.IdentityRuleSetVersion,
		Candidates:     names,
	})
	if err != nil {
		return nil, err
	}
	settled := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.Kind != tags.IdentityKindSameIntent || !row.Partner.Valid {
			continue
		}
		settled[twinPairKey(row.Candidate, row.Partner.String)] = struct{}{}
	}
	return settled, nil
}

// twinPairKey is the orientation-free key for a candidate pair, so a lookup matches either stored order.
func twinPairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}
