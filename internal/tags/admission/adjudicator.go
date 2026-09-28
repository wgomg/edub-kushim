// Package admission is the tag admission machinery the enricher delegates
// to: decomposition decisions on compound tags, pinned-policy verdicts on
// pair matches, quality judging of novel tags, and identity resolution of
// candidate names against the store. It returns decisions to the caller;
// its own writes are the verdict and quality event rows it journals, plus
// the identity resolver's alias applications through curation. The rules
// it enforces are pinned in tags/policy; the contracts are specified in
// docs/tags/consolidation.md.
package admission

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/types"
)

const (
	// the pinned no-anchor similarity cut, carried over from the
	// pre-definition quality gate and deliberately not operator-tunable
	tagQualityNoAnchorSimilarity = 0.60

	// reasons the interim paths record: no judge answered the candidate, or
	// the cached row predates the dimension rubric
	reasonJudgeUnavailable = "judge_unavailable"
	reasonLegacyPass       = "legacy_pass"
	reasonLegacyReject     = "legacy_reject"
)

// Adjudicator is the document-level admission machinery: it decomposes
// compound tags, journals pair verdicts, and judges the quality of novel
// tags. The enricher holds one and delegates the corresponding flow
// steps; the split cache is shared across documents.
type Adjudicator struct {
	config     *config.Config
	logger     *log.Logger
	queries    *database.Queries
	runner     *tools.Runner
	splitMu    sync.Mutex
	splitCache map[string]splitDecision
}

// NewAdjudicator wires the machinery. When the LLM adjudicator or quality
// judge is not configured, the corresponding steps degrade: conjunction
// splits fall back to the plain grammar split and novel tags stay, born
// provisional.
func NewAdjudicator(cfg *config.Config, logger *log.Logger, queries *database.Queries, runner *tools.Runner) *Adjudicator {
	return &Adjudicator{
		config:  cfg,
		logger:  logger,
		queries: queries,
		runner:  runner,
	}
}

type splitDecision struct {
	verdict    analyzer.TagSplitVerdict
	components []string
}

// DecomposeTags asks the LLM adjudicator (or, without one, the plain
// grammar split for conjunction joints) which compound tags decompose
// into dimension values, validates the verdict components against the
// per-document filter and the compound's part-count bound, and returns
// the tag list with approved decompositions expanded in place. Compounds
// that are not split pass through, deduplicated.
func (a *Adjudicator) DecomposeTags(ctx context.Context, names []string, filter *analyzer.TagFilter, reqID string) []string {
	var candidates []string
	for _, name := range names {
		if tags.ClassifyShape(name) == tags.ShapeCompound {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return names
	}

	decisions := a.splitVerdicts(ctx, candidates, reqID)

	components := make(map[string][]string, len(candidates))
	for _, candidate := range candidates {
		d, ok := decisions[candidate]
		if !ok || d.verdict != analyzer.TagSplitSplit {
			continue
		}
		valid := analyzer.SplitComponents(d.components, filter)
		// a compound has at most MaxCompoundWords content parts, so a
		// verdict with more surviving components is malformed
		if len(valid) < 2 || len(valid) > tags.MaxCompoundWords {
			continue
		}
		components[candidate] = valid
	}
	if len(components) == 0 {
		return names
	}

	out := make([]string, 0, len(names)+len(components))
	seen := make(map[string]bool, len(names))
	for _, t := range names {
		segments, ok := components[t]
		if !ok {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
			continue
		}
		for _, segment := range segments {
			if seen[segment] {
				continue
			}
			seen[segment] = true
			out = append(out, segment)
		}
		a.logger.Info(reqID, "tag split[%s]: %q → %v", analyzer.TagSplitVersion, t, segments)
	}
	return out
}

// ApplyDeletedTagPolicy rewrites a tag list through the deleted-tag
// blocklist: blocked tags are dropped, merged tags are rewritten to their
// merge target when that target still exists, and the result is
// deduplicated.
func (a *Adjudicator) ApplyDeletedTagPolicy(tags []string, knownTags map[string]struct{}, blocklist map[string]database.ListAllDeletedTagsRow, reqID string) []string {
	if len(blocklist) == 0 {
		return tags
	}

	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		if row, ok := blocklist[tag]; ok {
			switch row.Kind {
			case curation.DeletedTagKindBlocked:
				a.logger.Info(reqID, "deleted tag policy: dropping %q — the tag was deleted by an operator", tag)
				continue
			case curation.DeletedTagKindMerged:
				target := row.TargetName.String
				if !row.TargetName.Valid || target == "" {
					a.logger.Info(reqID, "deleted tag policy: dropping %q — the merge target is unknown", tag)
					continue
				}
				if _, ok := knownTags[target]; !ok {
					a.logger.Info(reqID, "deleted tag policy: dropping %q — it was merged into %q, which no longer exists", tag, target)
					continue
				}
				a.logger.Info(reqID, "deleted tag policy: rewriting %q to %q (merged)", tag, target)
				tag = target
			}
		}
		if seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

func (a *Adjudicator) splitVerdicts(ctx context.Context, candidates []string, reqID string) map[string]splitDecision {
	a.splitMu.Lock()
	if a.splitCache == nil {
		a.splitCache = make(map[string]splitDecision)
	}
	decisions := make(map[string]splitDecision, len(candidates))
	var uncached []string
	for _, name := range candidates {
		if d, ok := a.splitCache[name]; ok {
			decisions[name] = d
			continue
		}
		uncached = append(uncached, name)
	}
	a.splitMu.Unlock()

	if len(uncached) == 0 {
		return decisions
	}

	if !a.runner.HasTagAdjudicator() {
		// without an adjudicator only conjunction joints split: the
		// grammar split reads their explicit coordination, and blanket
		// mechanical splitting of other compounds is not allowed
		for _, name := range uncached {
			segments := textnorm.SplitJointTag(name)
			if segments == nil {
				continue
			}
			decisions[name] = splitDecision{verdict: analyzer.TagSplitSplit, components: segments}
		}
		return decisions
	}

	results, err := a.runner.AdjudicateTagSplits(ctx, uncached)
	if err != nil {
		a.logger.Error(reqID, "tag split adjudication failed, keeping %d joint tags: %v", len(uncached), err)
		return decisions
	}
	if len(results) != len(uncached) {
		a.logger.Error(reqID, "tag split adjudication returned %d verdicts for %d tags, keeping them", len(results), len(uncached))
		return decisions
	}

	a.splitMu.Lock()
	defer a.splitMu.Unlock()
	for i, r := range results {
		d := splitDecision{verdict: r.Verdict, components: r.Components}
		decisions[uncached[i]] = d
		a.splitCache[uncached[i]] = d
	}
	return decisions
}

type bandPair struct {
	query       string
	target      string
	sim         float64
	runnerUpSim float64
	verdict     types.TagVerdict
	model       string
	cached      bool
}

type reciprocalEvidence struct {
	tag string
	sim float64
}

func (a *Adjudicator) writeVerdictEvent(ctx context.Context, docId int64, d policy.Decision, bp *bandPair, reqID string) {
	var verdictModel sql.NullString
	if !bp.cached {
		verdictModel = sql.NullString{String: bp.model, Valid: bp.model != ""}
	}
	n, err := a.queries.InsertTagVerdictEvent(ctx, database.InsertTagVerdictEventParams{
		DocumentID:    docId,
		Query:         textnorm.NormalizeTag(d.Query),
		Target:        textnorm.NormalizeTag(d.Target),
		Sim:           d.Sim,
		Verdict:       bp.verdict,
		VerdictModel:  verdictModel,
		PolicyVersion: policy.Version,
	})
	if err != nil {
		a.logger.Error(reqID, "tag verdict event insert failed: %v", err)
		return
	}
	if n > 0 || bp.cached {
		return
	}

	rows, err := a.queries.LatestTagVerdictsForPairs(ctx, database.LatestTagVerdictsForPairsParams{
		PolicyVersion: policy.Version,
		Queries:       []string{d.Query},
		Targets:       []string{d.Target},
	})
	if err != nil {
		a.logger.Error(reqID, "tag verdict cache re-read after conflict failed: %v", err)
		return
	}
	if len(rows) == 0 || !rows[0].Verdict.Valid() {
		return
	}
	bp.verdict = rows[0].Verdict
	bp.cached = true
	if rows[0].VerdictModel.Valid {
		bp.model = rows[0].VerdictModel.String
	}
	if _, err := a.queries.InsertTagVerdictEvent(ctx, database.InsertTagVerdictEventParams{
		DocumentID:    docId,
		Query:         textnorm.NormalizeTag(d.Query),
		Target:        textnorm.NormalizeTag(d.Target),
		Sim:           d.Sim,
		Verdict:       bp.verdict,
		VerdictModel:  sql.NullString{},
		PolicyVersion: policy.Version,
	}); err != nil {
		a.logger.Error(reqID, "tag verdict event insert failed: %v", err)
	}
}

type novelTag struct {
	emitted    string
	category   string
	haveDetail bool
	bestSim    float64
	haveSim    bool
}

// features reads the deterministic signals; a missing detail or
// similarity is absence of evidence, not a failing signal
// (docs/tags/definition-and-faceted-retrieval.md §6.6).
func (n *novelTag) features() tags.AdmissionFeatures {
	return tags.AdmissionFeatures{
		CategoryOther: n.haveDetail && n.category == analyzer.TagCategoryOther,
		NoAnchor:      n.haveSim && n.bestSim < tagQualityNoAnchorSimilarity,
	}
}

// promptCategory is the category the judge sees: an unannotated emission
// presents as other, while the feature stays absent.
func (n *novelTag) promptCategory() string {
	if !n.haveDetail {
		return analyzer.TagCategoryOther
	}
	return n.category
}

// DecisionFromStored re-derives the admission decision from a cached
// quality event row: rows carrying the current rubric re-run
// tags.DecideAdmission over the stored judge answers plus whatever
// deterministic features the caller has (callers without an emission
// context pass a zero set); rows predating the rubric fall back to their
// pass flag until the store reset wipes them.
func DecisionFromStored(criteria json.RawMessage, pass bool, features tags.AdmissionFeatures) tags.AdmissionDecision {
	var c analyzer.TagQualityCriteria
	if err := json.Unmarshal(criteria, &c); err == nil {
		if r, ok := c.Result(); ok {
			return tags.DecideAdmission(JudgeFeatures(r, features))
		}
	}
	if pass {
		return tags.AdmissionDecision{Outcome: tags.AdmissionAdmit, Reason: reasonLegacyPass}
	}
	return tags.AdmissionDecision{Outcome: tags.AdmissionReject, Reason: reasonLegacyReject}
}

// JudgeFeatures folds the judge's rubric answers into a feature set; the
// judge slots are overwritten and the deterministic features pass through
// as given. Callers without an emission context pass a zero set.
func JudgeFeatures(r analyzer.TagQualityResult, f tags.AdmissionFeatures) tags.AdmissionFeatures {
	f.SubjectMatter = r.SubjectMatter
	f.Dimension = r.Dimension
	f.NotPersonPlaceWork = r.NotPersonPlaceWork
	f.Atomic = r.Atomic
	f.JudgeDefer = r.Defer
	return f
}

func marshalTagQualityCriteria(r analyzer.TagQualityResult) json.RawMessage {
	data, err := json.Marshal(r.Criteria())
	if err != nil {
		return json.RawMessage("{}")
	}
	return data
}

func tagQualityCriteriaLog(criteria json.RawMessage) string {
	var c analyzer.TagQualityCriteria
	if err := json.Unmarshal(criteria, &c); err != nil {
		return "unknown"
	}
	return c.String()
}

// logAdmission keeps admits silent: the audit log stays on the failure
// paths, where the reason matters.
func (a *Adjudicator) logAdmission(reqID, name, source string, d tags.AdmissionDecision, criteria json.RawMessage, model string) {
	if d.Outcome == tags.AdmissionAdmit {
		return
	}
	if model == "" {
		model = "cached"
	}
	a.logger.Info(reqID, "tagquality[%s]: %s %q (reason=%s source=%s model=%s criteria=%s)",
		analyzer.TagQualityVersion, d.Outcome, name, d.Reason, source, model, tagQualityCriteriaLog(criteria))
}

// ApplyTagQuality filters the analysis's tags through the admission
// decision: each novel candidate's features — the judge's rubric answers
// from the verdict cache or a fresh judge call, plus the emission's
// deterministic signals — resolve through tags.DecideAdmission. Rejected
// tags are dropped from the returned list; deferred and admitted tags are
// both kept, and every kept novel name's decision is returned so the
// caller can drive promotion. A missing judge, judge failure, or context
// cancellation keeps the tags, born provisional with a defer decision.
func (a *Adjudicator) ApplyTagQuality(ctx context.Context, analysis *tools.ContentAnalysisResult, decisions []policy.Decision, dbNames map[string]struct{}, reducedText string, docId int64, reqID string) ([]string, map[string]tags.AdmissionDecision) {
	typedByTerm := make(map[string]analyzer.TagDetail, len(analysis.TagDetails))
	for _, d := range analysis.TagDetails {
		typedByTerm[textnorm.NormalizeTag(d.Term)] = d
	}
	simByOutput := make(map[string]float64, len(decisions))
	for _, d := range decisions {
		if _, ok := simByOutput[d.Output]; !ok {
			simByOutput[d.Output] = d.Sim
		}
	}

	rejected := make(map[string]bool)
	decided := make(map[string]tags.AdmissionDecision)
	seenNovel := make(map[string]bool)
	var novels []*novelTag
	for _, out := range analysis.Tags {
		if out == "" {
			continue
		}
		if _, ok := dbNames[out]; ok {
			continue
		}
		if seenNovel[out] {
			continue
		}
		seenNovel[out] = true
		n := &novelTag{emitted: out}
		if d, ok := typedByTerm[out]; ok {
			n.category, n.haveDetail = d.Category, true
		}
		if sim, ok := simByOutput[out]; ok && sim > 0 {
			n.bestSim, n.haveSim = sim, true
		}
		novels = append(novels, n)
	}

	var uncached []*novelTag
	if len(novels) > 0 {
		keys := make([]string, len(novels))
		for i, n := range novels {
			keys[i] = n.emitted
		}
		rows, err := a.queries.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
			PolicyVersion: analyzer.TagQualityVersion,
			TagNames:      keys,
		})
		if err != nil {
			a.logger.Error(reqID, "tag quality cache lookup failed: %v", err)
		}
		cached := make(map[string]database.LatestTagQualityEventsForNamesRow, len(rows))
		for _, row := range rows {
			cached[row.TagName] = row
		}
		for _, n := range novels {
			row, ok := cached[n.emitted]
			if !ok {
				uncached = append(uncached, n)
				continue
			}
			decision := DecisionFromStored(row.Criteria, row.Pass, n.features())
			a.writeTagQualityEvent(ctx, docId, n.emitted, row.Criteria, row.Pass, row.Outcome.String, "", reqID)
			a.logAdmission(reqID, n.emitted, "cache", decision, row.Criteria, row.VerdictModel.String)
			if decision.Outcome == tags.AdmissionReject {
				rejected[n.emitted] = true
				continue
			}
			decided[n.emitted] = decision
		}
	}

	if len(uncached) > 0 {
		if !a.runner.HasTagQualityJudge() {
			a.logger.Error(reqID, "tagquality[%s]: judge not configured, keeping %d novel tags (born provisional)", analyzer.TagQualityVersion, len(uncached))
			for _, n := range uncached {
				decided[n.emitted] = tags.AdmissionDecision{Outcome: tags.AdmissionDefer, Reason: reasonJudgeUnavailable}
			}
		} else {
			candidates := make([]analyzer.TagQualityCandidate, len(uncached))
			for i, n := range uncached {
				candidates[i] = analyzer.TagQualityCandidate{
					Index:            i,
					Term:             n.emitted,
					Category:         n.promptCategory(),
					WhitelistedPlace: analyzer.IsWhitelistedPlace(n.emitted),
				}
			}
			results, err := a.runner.JudgeTagQuality(ctx, reducedText, candidates)
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				results, err = a.runner.JudgeTagQuality(ctx, reducedText, candidates)
			}
			if err == nil && len(results) != len(candidates) {
				err = fmt.Errorf("judge returned %d verdicts for %d candidates", len(results), len(candidates))
			}
			if err != nil {
				a.logger.Error(reqID, "tagquality[%s]: judge failed, keeping %d novel tags (born provisional): %v", analyzer.TagQualityVersion, len(uncached), err)
				for _, n := range uncached {
					decided[n.emitted] = tags.AdmissionDecision{Outcome: tags.AdmissionDefer, Reason: reasonJudgeUnavailable}
				}
			} else {
				model := a.config.ResolvedQualityLlm().Model
				for i, r := range results {
					n := uncached[i]
					outcome := tags.DecideAdmission(JudgeFeatures(r, n.features()))
					pass, criteria, finalModel := a.writeTagQualityEvent(ctx, docId, n.emitted, marshalTagQualityCriteria(r), outcome.Outcome == tags.AdmissionAdmit, outcome.Outcome.String(), model, reqID)
					decision := DecisionFromStored(criteria, pass, n.features())
					a.logAdmission(reqID, n.emitted, "judge", decision, criteria, finalModel)
					if decision.Outcome == tags.AdmissionReject {
						rejected[n.emitted] = true
						continue
					}
					decided[n.emitted] = decision
				}
			}
		}
	}

	out := make([]string, 0, len(analysis.Tags))
	for _, tag := range analysis.Tags {
		if rejected[tag] {
			continue
		}
		out = append(out, tag)
	}
	return out, decided
}

// writeTagQualityEvent records a verdict row for this document. A fresh
// verdict (model != "") writes the judged row; on conflict the row cached by
// another worker wins for this document's decision, followed by a
// cache-applied row with a NULL model, mirroring writeVerdictEvent.
func (a *Adjudicator) writeTagQualityEvent(ctx context.Context, docId int64, name string, criteria json.RawMessage, pass bool, outcome string, model string, reqID string) (bool, json.RawMessage, string) {
	verdictModel := sql.NullString{String: model, Valid: model != ""}
	n, err := a.queries.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docId,
		TagName:       textnorm.NormalizeTag(name),
		Criteria:      criteria,
		Pass:          pass,
		Outcome:       sql.NullString{String: outcome, Valid: outcome != ""},
		VerdictModel:  verdictModel,
		PolicyVersion: analyzer.TagQualityVersion,
	})
	if err != nil {
		a.logger.Error(reqID, "tag quality event insert failed: %v", err)
		return pass, criteria, model
	}
	if n > 0 || !verdictModel.Valid {
		return pass, criteria, model
	}

	rows, err := a.queries.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
		PolicyVersion: analyzer.TagQualityVersion,
		TagNames:      []string{name},
	})
	if err != nil {
		a.logger.Error(reqID, "tag quality cache re-read after conflict failed: %v", err)
		return pass, criteria, model
	}
	if len(rows) == 0 {
		return pass, criteria, model
	}
	row := rows[0]
	if _, err := a.queries.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docId,
		TagName:       textnorm.NormalizeTag(name),
		Criteria:      row.Criteria,
		Pass:          row.Pass,
		Outcome:       row.Outcome,
		VerdictModel:  sql.NullString{},
		PolicyVersion: analyzer.TagQualityVersion,
	}); err != nil {
		a.logger.Error(reqID, "tag quality event insert failed: %v", err)
	}
	if row.VerdictModel.Valid {
		model = row.VerdictModel.String
	}
	return row.Pass, row.Criteria, model
}

func (a *Adjudicator) reciprocalEvidence(ctx context.Context, documentID string, pairs []*bandPair, reqID string) map[string]reciprocalEvidence {
	targets := make([]string, 0, len(pairs))
	seen := make(map[string]struct{}, len(pairs))
	for _, bp := range pairs {
		if _, ok := seen[bp.target]; ok {
			continue
		}
		seen[bp.target] = struct{}{}
		targets = append(targets, bp.target)
	}

	evidence := make(map[string]reciprocalEvidence, len(targets))
	if len(targets) == 0 {
		return evidence
	}

	ranked, err := a.runner.RankTags(ctx, documentID, targets)
	if err != nil {
		a.logger.Error(reqID, "tag adjudication evidence rank failed: %v", err)
		return evidence
	}
	for i, target := range targets {
		if i >= len(ranked) {
			break
		}
		for _, c := range ranked[i].Candidates {
			if c.Tag == target {
				continue
			}
			evidence[target] = reciprocalEvidence{tag: c.Tag, sim: c.Similarity}
			break
		}
	}
	return evidence
}
