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

	// qualityBatchSize bounds one quality-judge call's candidate list
	qualityBatchSize = 40

	// reasons the interim paths record: no judge answered the candidate, or
	// the cached row predates the dimension rubric
	reasonJudgeUnavailable = "judge_unavailable"
	reasonLegacyPass       = "legacy_pass"
	reasonLegacyReject     = "legacy_reject"
)

// Adjudicator is the document-level admission machinery: it decomposes compound tags, journals pair
// verdicts, and judges the quality of novel tags. The enricher holds one and delegates the corresponding
// flow steps.
type Adjudicator struct {
	config  *config.Config
	logger  *log.Logger
	queries *database.Queries
	runner  *tools.Runner
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

// PartVerdict is one resolved part value's verdict from the persisted cache or a fresh judge call: the
// dimensional answer, whether the judge abstained, and whether a concurrent writer's stored row disagreed
// with this sample. Exported so the stored-joint sweep folds the same persisted rows through
// CompositionFeatures instead of re-implementing the fold.
type PartVerdict struct {
	Dimensional  bool
	Deferred     bool
	Disagreement bool
}

// CompoundVerdict is one compound candidate's verdict from the persisted cache or a fresh judge call: the
// status, whether the judge abstained, the model behind a fresh call, whether it came from the cache, and
// whether a concurrent writer's stored row disagreed with this sample. Exported so the stored-joint sweep
// folds the same persisted rows through CompositionFeatures instead of re-implementing the fold.
type CompoundVerdict struct {
	Status       analyzer.TagCompoundStatus
	Deferred     bool
	Model        string
	Cached       bool
	Disagreement bool
}

// DecomposeTags runs the split gate over the compound candidates: the context-free parts pass judges each
// candidate's content words (fold-resolved through the promoted store view) once, and the context-free
// compound pass reads what each phrase denotes. Both verdicts persist in their own append-only tables, keyed
// by value and candidate, so a re-run reads them back instead of re-rolling; the two answer sets fold
// into a tags.CompositionFeatures and dispose through tags.DecideComposition — the split branch expands the
// compound into its resolved parts, the keep branches leave it whole, and the drop branch removes it. The
// returned tag list is the input with approved decompositions expanded in place and dropped compounds
// removed, plus one composition evidence row per judged candidate. The ordering is load-bearing:
// fold-resolve, filter (title-echo exempt), then the remainder enters the judged downstream. A compound
// whose key already exists in the store is an application, not a candidate: it is skipped before judgment
// and produces no row. storeKeys is the store's names plus alias keys, which the store-existing skip reads;
// promotedNames is the promoted tag names' Key→name view the fold resolution and the reconstructibility
// signal read — seam fold resolution anchors only on promoted rows, and a resolved part is reconstructible
// only when it is one. docId is the emitting document, stamped on fresh part and compound verdict rows (0
// stores NULL provenance, as the stored-joint sweep does). A non-nil error is returned only when the
// provider account cannot pay; every other failure degrades to keeping the compounds. Without a configured
// adjudicator the conjunction grammar fallback stands: explicit coordination splits mechanically and every
// other compound keeps whole under judge_unavailable.
func (a *Adjudicator) DecomposeTags(ctx context.Context, names []string, storeKeys map[string]struct{}, promotedNames map[string]string, filter *analyzer.TagFilter, docId int64, reqID string) ([]string, []tags.CompositionRow, error) {
	var candidates []string
	seenCandidates := make(map[string]bool, len(names))
	for _, name := range names {
		if tags.ClassifyShape(name) != tags.ShapeCompound {
			continue
		}
		if _, stored := storeKeys[tags.Key(name)]; stored {
			a.logger.Debug(reqID, "tag split[%s]: skip %q (reason=store_existing)", analyzer.TagSplitVersion, name)
			continue
		}
		if seenCandidates[name] {
			continue
		}
		seenCandidates[name] = true
		candidates = append(candidates, name)
	}
	if len(candidates) == 0 {
		return names, nil, nil
	}

	if !a.runner.HasTagAdjudicator() {
		return a.conjunctionSplits(names, candidates, promotedNames, filter, reqID)
	}

	resolved := make(map[string][]string, len(candidates))
	seenValues := make(map[string]bool)
	var values []string
	for _, candidate := range candidates {
		parts := tags.DeriveParts(candidate)
		rparts := make([]string, 0, len(parts))
		for _, part := range parts {
			resolvedPart := part
			if anchor := tags.AnchoredFold(part, promotedNames); anchor != "" && anchor != part {
				a.logger.Debug(reqID, "tag split[%s]: part %q fold-resolves to store anchor %q",
					analyzer.TagSplitVersion, part, anchor)
				resolvedPart = anchor
			}
			rparts = append(rparts, resolvedPart)
			if !seenValues[resolvedPart] {
				seenValues[resolvedPart] = true
				values = append(values, resolvedPart)
			}
		}
		resolved[candidate] = rparts
	}

	store := NewVerdictStore(a.queries, a.runner, a.logger, a.config.ResolvedAdjudicatorLlm().Model)
	partVerdicts, err := store.Parts(ctx, values, docId, reqID)
	if err != nil {
		return nil, nil, err
	}
	compoundVerdicts, err := store.Compounds(ctx, candidates, docId, reqID)
	if err != nil {
		return nil, nil, err
	}

	components := make(map[string][]string, len(candidates))
	dropped := make(map[string]bool, len(candidates))
	rows := make([]tags.CompositionRow, 0, len(candidates))
	for _, candidate := range candidates {
		cv := compoundVerdicts[candidate]
		features := CompositionFeatures(resolved[candidate], partVerdicts, cv, promotedNames)
		decision := tags.DecideComposition(features)
		reconstructible := features.Reconstructible
		row := tags.CompositionRow{
			Candidate:       candidate,
			CriteriaVersion: analyzer.TagSplitVersion,
			Reconstructible: &reconstructible,
		}
		switch decision.Disposition {
		case tags.CompositionSplit:
			valid := analyzer.SplitComponents(resolved[candidate], filter)
			// a compound has at most MaxCompoundWords content parts, so a verdict with more surviving components is malformed
			if len(valid) < 2 || len(valid) > tags.MaxCompoundWords {
				a.logger.Debug(reqID, "tag split[%s]: invalid components for %q proposed=%q",
					analyzer.TagSplitVersion, candidate, resolved[candidate])
				row.Disposition = tags.CompositionKeep
				row.Reason = tags.CompositionReasonInvalidComponents
				row.Model = freshModel(cv)
				break
			}
			components[candidate] = valid
			row.Disposition = tags.CompositionSplit
			row.Parts = valid
			row.Reason, row.Model = splitSource(cv)
			a.logger.Info(reqID, "tag split[%s]: split %q → %q", analyzer.TagSplitVersion, candidate, valid)
		case tags.CompositionDrop:
			dropped[candidate] = true
			row.Disposition = tags.CompositionDrop
			row.Reason = decision.Reason
			row.Model = freshModel(cv)
			a.logger.Info(reqID, "tag split[%s]: drop %q (reason=%s)", analyzer.TagSplitVersion, candidate, row.Reason)
		default:
			row.Disposition = tags.CompositionKeep
			row.Reason = decision.Reason
			row.Model = freshModel(cv)
			a.logger.Info(reqID, "tag split[%s]: keep %q (reason=%s cached=%t)", analyzer.TagSplitVersion, candidate, row.Reason, cv.Cached)
		}
		rows = append(rows, row)
	}
	return expandCandidates(names, components, dropped), rows, nil
}

// conjunctionSplits is the no-adjudicator fallback: explicit coordination splits mechanically through the
// joint-tag grammar (fold-resolved and validated like a judged split), every other compound keeps whole
// under judge_unavailable. It persists nothing and writes only composition rows.
func (a *Adjudicator) conjunctionSplits(names, candidates []string, promotedNames map[string]string, filter *analyzer.TagFilter, reqID string) ([]string, []tags.CompositionRow, error) {
	components := make(map[string][]string, len(candidates))
	dropped := make(map[string]bool, len(candidates))
	rows := make([]tags.CompositionRow, 0, len(candidates))
	for _, candidate := range candidates {
		row := tags.CompositionRow{Candidate: candidate, CriteriaVersion: analyzer.TagSplitVersion}
		segments := textnorm.SplitJointTag(candidate)
		if segments == nil {
			row.Disposition = tags.CompositionKeep
			row.Reason = tags.CompositionReasonJudgeUnavailable
			a.logger.Info(reqID, "tag split[%s]: keep %q (reason=%s cached=false)", analyzer.TagSplitVersion, candidate, row.Reason)
			rows = append(rows, row)
			continue
		}
		parts := analyzer.NormalizeTags(segments)
		for i, part := range parts {
			if anchor := tags.AnchoredFold(part, promotedNames); anchor != "" && anchor != part {
				parts[i] = anchor
			}
		}
		valid := analyzer.SplitComponents(parts, filter)
		if len(valid) < 2 || len(valid) > tags.MaxCompoundWords {
			row.Disposition = tags.CompositionKeep
			row.Reason = tags.CompositionReasonInvalidComponents
			a.logger.Info(reqID, "tag split[%s]: keep %q (reason=%s cached=false)", analyzer.TagSplitVersion, candidate, row.Reason)
			rows = append(rows, row)
			continue
		}
		components[candidate] = valid
		row.Disposition = tags.CompositionSplit
		row.Parts = valid
		row.Reason = tags.CompositionReasonConjunction
		a.logger.Info(reqID, "tag split[%s]: split %q → %q", analyzer.TagSplitVersion, candidate, valid)
		rows = append(rows, row)
	}
	return expandCandidates(names, components, dropped), rows, nil
}

// CompositionFeatures folds the two passes' verdicts into the decision's feature set: the compound status
// (or the abstention), the parts' dimensional reading, the judge-outage flag a missing verdict sets, the
// cross-sample disagreement either pass observed, and the reconstructibility signal — all parts dimensional
// and every resolved part a promoted store label (the established-value proxy the fold anchor already
// receives). Shared by the emission gate and the stored-joint sweep so both dispose the same persisted rows
// identically.
func CompositionFeatures(parts []string, verdicts map[string]PartVerdict, cv CompoundVerdict, promotedNames map[string]string) tags.CompositionFeatures {
	var f tags.CompositionFeatures
	switch {
	case cv.Deferred:
		f.CompoundDeferred = true
	case cv.Status == "":
		f.JudgeUnavailable = true
	case cv.Status == analyzer.TagCompoundNamedUnit:
		f.CompoundNamedUnit = true
	case cv.Status == analyzer.TagCompoundComposition:
		f.CompoundComposition = true
	case cv.Status == analyzer.TagCompoundNoDimension:
		f.CompoundNoDimension = true
	}
	if cv.Disagreement {
		f.SampleDisagreement = true
	}

	allDimensional := len(parts) > 0
	for _, part := range parts {
		v, ok := verdicts[part]
		if !ok {
			f.JudgeUnavailable = true
			allDimensional = false
			continue
		}
		if v.Disagreement {
			f.SampleDisagreement = true
		}
		switch {
		case v.Deferred:
			f.PartsDeferred = true
			allDimensional = false
		case !v.Dimensional:
			f.PartsNotDimensional = true
			allDimensional = false
		}
	}
	if allDimensional {
		f.PartsDimensional = true
		f.Reconstructible = tags.AllPartsPromoted(parts, promotedNames)
	}
	return f
}

// expandCandidates applies the split expansions and drops to the tag list in place, deduplicating; split
// components replace their compound, dropped compounds leave, and every other name passes through.
func expandCandidates(names []string, components map[string][]string, dropped map[string]bool) []string {
	if len(components) == 0 && len(dropped) == 0 {
		return names
	}
	out := make([]string, 0, len(names)+len(components))
	seen := make(map[string]bool, len(names))
	for _, t := range names {
		if dropped[t] {
			continue
		}
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
	}
	return out
}

// splitSource names a split decision's verdict source and the model that judged it: a cached compound
// verdict has no fresh call behind it and no model; a fresh judge call carries its model.
func splitSource(cv CompoundVerdict) (reason, model string) {
	if cv.Cached {
		return tags.CompositionReasonCached, ""
	}
	return tags.CompositionReasonJudge, cv.Model
}

// freshModel is the model a composition row records: the judge's model for a fresh verdict, empty for a
// cached one.
func freshModel(cv CompoundVerdict) string {
	if cv.Cached {
		return ""
	}
	return cv.Model
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

// retryableJudgeFailure classifies a judged call's failure: transport and
// generic provider errors get one retry, while parse failures, incomplete
// replies, output-cap hits, credit exhaustion, content-too-large, token-limit
// and context errors never do. A partial reply's omission is systematic at a
// fixed output budget, so a retry would re-omit.
func retryableJudgeFailure(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var parseErr *analyzer.ReplyParseError
	var incErr *analyzer.IncompleteReplyError
	var capErr *analyzer.OutputCapError
	var credErr *analyzer.InsufficientCreditsError
	var ctle *analyzer.ContentTooLargeError
	var tokErr *analyzer.TokenLimitError
	return !errors.As(err, &parseErr) && !errors.As(err, &incErr) && !errors.As(err, &capErr) &&
		!errors.As(err, &credErr) && !errors.As(err, &ctle) && !errors.As(err, &tokErr)
}

// isCreditError reports whether err carries the provider account's
// credit-exhaustion failure, the one class that pauses the batch.
func isCreditError(err error) bool {
	var credErr *analyzer.InsufficientCreditsError
	return errors.As(err, &credErr)
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
	f.NotFormGenre = r.NotFormGenre
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

// logAdmission journals every decided candidate: admits at DEBUG,
// rejections and defers at INFO where the reason matters. The model
// label reads cached when no fresh judge call produced the verdict.
func (a *Adjudicator) logAdmission(reqID, name, source string, d tags.AdmissionDecision, criteria json.RawMessage, model string) {
	if model == "" {
		model = "cached"
	}
	level := a.logger.Info
	if d.Outcome == tags.AdmissionAdmit {
		level = a.logger.Debug
	}
	level(reqID, "tagquality[%s]: %s %q (reason=%s source=%s model=%s criteria=%s)",
		analyzer.TagQualityVersion, d.Outcome, name, d.Reason, source, model, tagQualityCriteriaLog(criteria))
}

// decideQuality writes one freshly judged candidate's verdict row and logs
// the resulting decision, returning it so the caller can record a rejection
// or a kept decision.
func (a *Adjudicator) decideQuality(ctx context.Context, docId int64, n *novelTag, r analyzer.TagQualityResult, model, reqID string) tags.AdmissionDecision {
	outcome := tags.DecideAdmission(JudgeFeatures(r, n.features()))
	pass, criteria, finalModel := a.writeTagQualityEvent(ctx, docId, n.emitted, marshalTagQualityCriteria(r), outcome.Outcome == tags.AdmissionAdmit, outcome.Outcome.String(), model, reqID)
	decision := DecisionFromStored(criteria, pass, n.features())
	a.logAdmission(reqID, n.emitted, "judge", decision, criteria, finalModel)
	return decision
}

// ApplyTagQuality filters the analysis's tags through the admission
// decision: each novel candidate's features — the judge's rubric answers
// from the verdict cache or a fresh judge call, plus the emission's
// deterministic signals — resolve through tags.DecideAdmission. Rejected
// tags are dropped from the returned list; deferred and admitted tags are
// both kept, and every kept novel name's decision is returned so the
// caller can drive promotion. A missing judge, judge failure, or context
// cancellation keeps the tags, born provisional with a defer decision; a
// partial judge reply salvages its returned entries and defers only the
// missing indices; a credit-exhaustion failure is the only error returned,
// so the caller can pause the batch.
func (a *Adjudicator) ApplyTagQuality(ctx context.Context, analysis *tools.ContentAnalysisResult, decisions []policy.Decision, dbNames map[string]struct{}, reducedText string, docId int64, reqID string) ([]string, map[string]tags.AdmissionDecision, error) {
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
			model := a.config.ResolvedQualityLlm().Model
			for start := 0; start < len(uncached); start += qualityBatchSize {
				end := min(start+qualityBatchSize, len(uncached))
				batch := uncached[start:end]
				candidates := make([]analyzer.TagQualityCandidate, len(batch))
				for i, n := range batch {
					candidates[i] = analyzer.TagQualityCandidate{
						Index:            i,
						Term:             n.emitted,
						Category:         n.promptCategory(),
						WhitelistedPlace: analyzer.IsWhitelistedPlace(n.emitted),
					}
				}
				results, err := a.runner.JudgeTagQuality(ctx, reducedText, candidates)
				if err != nil && retryableJudgeFailure(err) {
					a.logger.Warn(reqID, "tagquality[%s]: judge attempt failed, retrying once: %v", analyzer.TagQualityVersion, err)
					results, err = a.runner.JudgeTagQuality(ctx, reducedText, candidates)
				}
				if err == nil && len(results) != len(batch) {
					err = fmt.Errorf("judge returned %d verdicts for %d candidates", len(results), len(batch))
				}
				if err != nil {
					var incomplete *analyzer.IncompleteReplyError
					switch {
					case errors.As(err, &incomplete):
						missing := incomplete.MissingSet()
						var deferred []string
						salvaged := 0
						for i := range batch {
							n := batch[i]
							if missing[i] {
								decided[n.emitted] = tags.AdmissionDecision{Outcome: tags.AdmissionDefer, Reason: reasonJudgeUnavailable}
								deferred = append(deferred, n.emitted)
								continue
							}
							salvaged++
							decision := a.decideQuality(ctx, docId, n, results[i], model, reqID)
							if decision.Outcome == tags.AdmissionReject {
								rejected[n.emitted] = true
								continue
							}
							decided[n.emitted] = decision
						}
						a.logger.Warn(reqID, "tagquality[%s]: partial judge reply, salvaged %d of %d candidates and deferred %q (born provisional): %v",
							analyzer.TagQualityVersion, salvaged, len(batch), deferred, err)
					case isCreditError(err):
						return nil, nil, err
					default:
						names := make([]string, len(batch))
						for i, n := range batch {
							names[i] = n.emitted
						}
						a.logger.Warn(reqID, "tagquality[%s]: judge failed for %q, keeping them (born provisional): %v",
							analyzer.TagQualityVersion, names, err)
						for _, n := range batch {
							decided[n.emitted] = tags.AdmissionDecision{Outcome: tags.AdmissionDefer, Reason: reasonJudgeUnavailable}
						}
					}
					continue
				}
				for i, r := range results {
					n := batch[i]
					decision := a.decideQuality(ctx, docId, n, r, model, reqID)
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
	return out, decided, nil
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
