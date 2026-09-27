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

func (r *Reconciler) sweepIdentityEvidence(ctx context.Context, cfg *config.Config, report *Report) {
	if report.ReportOnly {
		return
	}
	resolver := admission.NewIdentityResolver(r.getConfig, r.logger, r.client, r.tag, r.runner)
	identityReport, err := resolver.ResolvePending(ctx, cfg.Enricher.TagReconcile.MaxRejudge)
	if err != nil {
		r.logger.Error("", "tag reconcile: identity evidence sweep failed: %v", err)
		return
	}
	if identityReport.Pending == 0 {
		return
	}
	report.Identity = identityReport
	r.logger.Info("", "tag reconcile: identity evidence — %d pending, %d resolved, %d folded, %d aliased, %d deferred, %d failed",
		identityReport.Pending, identityReport.Resolved, identityReport.Folded, identityReport.Aliased, identityReport.Deferred, identityReport.Failed)
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
			r.logger.Info("", "tag reconcile (report-only): would promote %q (docs=%d model=%s)", t.Name, docs, verdictModelLabel(row.VerdictModel))
			continue
		}
		if _, status, err := r.tag.Promote(ctx, t.ID, "sweep"); err != nil {
			r.logger.Error("", "tag reconcile: promote provisional tag %q: %v", t.Name, err)
		} else if status == types.PromotePromoted {
			report.Provisional.Promoted++
			r.logger.Info("", "tag reconcile: promoted %q (docs=%d model=%s)", t.Name, docs, verdictModelLabel(row.VerdictModel))
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

func (r *Reconciler) rejudgeProvisionalTags(ctx context.Context, cfg *config.Config, entries []*provisionalEntry, report *Report) {
	const chunkSize = 150

	model := cfg.ResolvedQualityLlm().Model
	for _, e := range entries {
		docID, err := r.client.FirstDocumentIDForTag(ctx, e.id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				report.Provisional.Skipped++
				r.logger.Info("", "tag reconcile: provisional tag %q has no live document — left for review", e.name)
				continue
			}
			r.logger.Error("", "tag reconcile: first document for provisional tag %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}
		doc, err := r.client.GetDocumentTextForTagQuality(ctx, docID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				report.Provisional.Skipped++
				r.logger.Info("", "tag reconcile: provisional tag %q has no live document text — left for review", e.name)
				continue
			}
			r.logger.Error("", "tag reconcile: document text for provisional tag %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}

		reduced, err := r.runner.ReduceContent(ctx, doc.TextContent.String, chunkSize,
			matcher.TargetWordCount(int(doc.WordCount), cfg.Enricher.TagMatcher.ReduceTargetWords))
		if err != nil {
			r.logger.Error("", "tag reconcile: reduce text for provisional tag %q: %v", e.name, err)
		}

		candidates := []analyzer.TagQualityCandidate{{
			Index:            0,
			Term:             e.name,
			Category:         analyzer.TagCategoryOther,
			WhitelistedPlace: analyzer.IsWhitelistedPlace(e.name),
		}}
		results, err := r.runner.JudgeTagQuality(ctx, reduced.Text, candidates)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			results, err = r.runner.JudgeTagQuality(ctx, reduced.Text, candidates)
		}
		if err != nil {
			r.logger.Error("", "tag reconcile: judge provisional tag %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}
		if len(results) != len(candidates) {
			r.logger.Error("", "tag reconcile: judge returned %d verdicts for provisional tag %q", len(results), e.name)
			report.Provisional.Failed++
			continue
		}

		res := results[0]
		criteria, err := json.Marshal(res.Criteria())
		if err != nil {
			r.logger.Error("", "tag reconcile: marshal criteria for provisional tag %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}
		pass, finalModel, err := r.recordProvisionalVerdict(ctx, docID, e.name, criteria, res.Passed(), model)
		if err != nil {
			r.logger.Error("", "tag reconcile: record provisional verdict for %q: %v", e.name, err)
			report.Provisional.Failed++
			continue
		}
		report.Provisional.Rejudged++

		if !pass {
			report.Provisional.Deferred++
			r.logger.Info("", "tag reconcile: provisional tag %q rejected by the judge — deferred to the review queue", e.name)
			continue
		}
		if e.docs < 2 {
			continue
		}
		if _, status, err := r.tag.Promote(ctx, e.id, "sweep"); err != nil {
			r.logger.Error("", "tag reconcile: promote provisional tag %q: %v", e.name, err)
		} else if status == types.PromotePromoted {
			report.Provisional.Promoted++
			r.logger.Info("", "tag reconcile: promoted %q (docs=%d model=%s)", e.name, e.docs, finalModel)
		}
	}
}

func (r *Reconciler) recordProvisionalVerdict(ctx context.Context, docID int64, name string, criteria json.RawMessage, pass bool, model string) (bool, string, error) {
	verdictModel := sql.NullString{String: model, Valid: model != ""}
	stored, err := r.client.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docID,
		TagName:       textnorm.NormalizeTag(name),
		Criteria:      criteria,
		Pass:          pass,
		VerdictModel:  verdictModel,
		PolicyVersion: analyzer.TagQualityVersion,
	})
	if err != nil {
		return false, "", err
	}
	if stored > 0 || !verdictModel.Valid {
		return pass, model, nil
	}

	// another writer recorded a fresh verdict first — keep it, never overwrite
	rows, err := r.tag.LatestQualityEventsForNames(ctx, []string{name})
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
