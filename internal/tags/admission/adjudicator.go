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
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
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

	// reasonLanguageReview marks a non-English value the language guard could not translate: it is held
	// for review instead of being admitted plain
	reasonLanguageReview = "language_review"
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
// with this sample. Exported as the vocabulary of the verdict store's parts reader.
type PartVerdict struct {
	Dimensional  bool
	Deferred     bool
	Disagreement bool
}

// CompoundVerdict is one compound candidate's verdict from the persisted cache or a fresh judge call: the
// status, whether the judge abstained, the model behind a fresh call, whether it came from the cache, and
// whether a concurrent writer's stored row disagreed with this sample. Exported as the vocabulary of the
// verdict store's compounds reader.
type CompoundVerdict struct {
	Status       analyzer.TagCompoundStatus
	Deferred     bool
	Model        string
	Cached       bool
	Disagreement bool
}

// DecomposeTags runs the split gate over the compound candidates: the context-free parts pass judges each
// candidate's content words (fold-resolved through the attested table and the promoted store view) once,
// and the context-free compound pass reads what each phrase denotes. Both verdicts persist in their own
// append-only tables, keyed by value and candidate, so a re-run reads them back instead of re-rolling; the
// two answer sets fold into a tags.CompositionFeatures and dispose through tags.DecideComposition —
// the parts test selects the branch, the split branch expands the compound into its resolved parts, the
// keep branches leave it whole and apply its judged-dimensional parts alongside, and the drop branch
// removes it. The returned tag list is the input with approved decompositions expanded in place, dropped
// compounds removed, and kept compounds' alongside parts inserted after them, plus one composition
// evidence row per judged candidate. Ordering is load-bearing: fold-resolve, filter, then the
// remainder enters the judged downstream. A compound whose key already exists in the store is an
// application, not a candidate, only when a persisted verdict under the current criteria version decides
// it: it is then skipped before judgment, and a keep among those store-existing decided compounds gets its
// parts applied alongside on every emission. A store-existing compound with no verdict, or only a degraded
// judge_unavailable keep, stays a candidate and is re-judged until judgment succeeds when an adjudicator is
// configured; without one it is skipped as before, because no verdict is reachable and re-judging would
// only append rows forever.
// storeKeys is the store's names plus alias keys, which the store-existing test reads; promotedNames is
// the promoted tag names' Key→name view the fold resolution and the reconstructibility signal read — seam
// fold resolution anchors only on promoted rows, and a resolved part is reconstructible only when it is
// one. docId is the emitting document, stamped on fresh part and compound verdict rows (0 stores NULL
// provenance, as the stored-compound sweep does). A non-nil error is returned when the provider account
// cannot pay or the adjudication-state lookup fails; every other failure degrades to keeping the
// compounds. Without a configured adjudicator the conjunction grammar fallback stands: explicit
// coordination splits mechanically and every other compound keeps whole under judge_unavailable.
func (a *Adjudicator) DecomposeTags(ctx context.Context, names []string, storeKeys map[string]struct{}, promotedNames map[string]string, filter *analyzer.TagFilter, docId int64, reqID string) ([]string, []tags.CompositionRow, error) {
	var candidates []string
	seenCandidates := make(map[string]bool, len(names))
	var storeExisting []string
	seenStore := make(map[string]bool, len(names))
	for _, name := range names {
		if tags.ClassifyShape(name) != tags.ShapeCompound {
			continue
		}
		if _, stored := storeKeys[tags.Key(name)]; stored {
			if !seenStore[name] {
				seenStore[name] = true
				storeExisting = append(storeExisting, name)
			}
			continue
		}
		if seenCandidates[name] {
			continue
		}
		seenCandidates[name] = true
		candidates = append(candidates, name)
	}

	var skipped []string
	if len(storeExisting) > 0 {
		states, err := tags.CompositionStatesForNames(ctx, a.queries, storeExisting, analyzer.TagSplitVersion)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range storeExisting {
			state, ok := states[tags.StoredName(name)]
			switch {
			case ok && state.Decided:
				a.logger.Debug(reqID, "tag split[%s]: skip %q (reason=store_existing disposition=%s)",
					analyzer.TagSplitVersion, name, state.Disposition)
				if state.Disposition == tags.CompositionKeep {
					skipped = append(skipped, name)
				}
			case tags.IsPeriodClass(tags.HouseForm(name)):
				// a seeded period is a store value decided by fiat: applied whole, never re-judged, and with
				// no parts alongside — its ordinal and unit are not dimensions
				a.logger.Debug(reqID, "tag split[%s]: skip %q (reason=store_existing period)", analyzer.TagSplitVersion, name)
			case !a.runner.HasTagAdjudicator():
				a.logger.Debug(reqID, "tag split[%s]: skip %q (reason=store_existing undecided, no adjudicator)",
					analyzer.TagSplitVersion, name)
			default:
				a.logger.Debug(reqID, "tag split[%s]: store-existing %q re-judged (reason=undecided)", analyzer.TagSplitVersion, name)
				seenCandidates[name] = true
				candidates = append(candidates, name)
			}
		}
	}

	if len(candidates) == 0 && len(skipped) == 0 {
		return names, nil, nil
	}

	alongsideByCompound := a.storedCoverage(ctx, skipped, promotedNames, storeKeys, filter, reqID)

	if !a.runner.HasTagAdjudicator() {
		expanded, rows, err := a.conjunctionSplits(names, candidates, promotedNames, storeKeys, filter, reqID)
		if err != nil {
			return nil, nil, err
		}
		return withAlongside(expanded, alongsideByCompound), rows, nil
	}

	rows, err := a.JudgeCompounds(ctx, candidates, storeKeys, promotedNames, filter, docId, reqID)
	if err != nil {
		return nil, nil, err
	}
	components := make(map[string][]string, len(candidates))
	dropped := make(map[string]bool, len(candidates))
	for _, row := range rows {
		switch row.Disposition {
		case tags.CompositionSplit:
			components[row.Candidate] = row.Parts
		case tags.CompositionDrop:
			dropped[row.Candidate] = true
		default:
			if len(row.Parts) > 0 {
				alongsideByCompound[row.Candidate] = row.Parts
			}
		}
	}
	return withAlongside(expandCandidates(names, components, dropped), alongsideByCompound), rows, nil
}

// JudgeCompounds is the compound judgment the emission gate's DecomposeTags and the stored-compound sweep
// share, so a candidate disposes identically on either path. It returns one criterion-versioned row per
// candidate, in order: a split row's parts are its components, a keep row's are the parts applied alongside.
// The rows carry no document — docId stamps only the fresh verdict rows the judgment writes (0 is the
// sweep's document-less provenance) — and the emission path stamps each returned row's own document.
func (a *Adjudicator) JudgeCompounds(ctx context.Context, candidates []string, liveNames map[string]struct{}, promotedNames map[string]string, filter *analyzer.TagFilter, docId int64, reqID string) ([]tags.CompositionRow, error) {
	store := NewVerdictStore(a.queries, a.runner, a.logger, a.config.ResolvedAdjudicatorLlm().Model, tags.PromotedKeys(promotedNames))
	fans := make(map[string]Fan, len(candidates))
	for _, candidate := range candidates {
		fan, ferr := a.resolveFan(ctx, store, candidate, promotedNames, liveNames, docId, reqID)
		if ferr != nil {
			return nil, ferr
		}
		fans[candidate] = fan
	}

	seenValues := make(map[string]bool)
	var values []string
	for _, candidate := range candidates {
		for _, seg := range tags.DeriveSegments(candidate) {
			resolved := fans[candidate].Resolved[seg]
			if resolved == "" || seenValues[resolved] {
				continue
			}
			seenValues[resolved] = true
			values = append(values, resolved)
		}
	}

	partVerdicts, err := store.Parts(ctx, values, docId, reqID)
	if err != nil {
		return nil, err
	}
	// a segment the store already holds is a value by definition, so it reads dimensional and its cover span
	// needs no parts verdict; a phrase-fold abstention is not seeded here — it is scoped to the fan that
	// produced it, so it never holds an unrelated compound through the shared verdict map
	orderedFans := make([]Fan, 0, len(candidates))
	for _, candidate := range candidates {
		orderedFans = append(orderedFans, fans[candidate])
	}
	seedStoreValues(partVerdicts, orderedFans, liveNames)
	compoundVerdicts, err := store.Compounds(ctx, candidates, docId, reqID)
	if err != nil {
		return nil, err
	}

	rows := make([]tags.CompositionRow, 0, len(candidates))
	for _, candidate := range candidates {
		cv := compoundVerdicts[candidate]
		fan := fans[candidate]
		// the coarsest cover's segments when the fan has a full multi-segment cover, otherwise the flat
		// resolved content the unit branch's deferred/unavailable reads consume
		parts, coverComplete := coverParts(fan, liveNames, partVerdicts)
		features := compositionFeatures(parts, coverComplete, fan.DeferredSet(), partVerdicts, cv, promotedNames)
		decision := tags.DecideComposition(features)
		reconstructible := features.Reconstructible
		row := tags.CompositionRow{
			Candidate:       candidate,
			CriteriaVersion: analyzer.TagSplitVersion,
			Reconstructible: &reconstructible,
		}
		switch decision.Disposition {
		case tags.CompositionSplit:
			valid := analyzer.SplitComponents(parts, filter)
			// a compound has at most MaxCompoundWords content segments, so a verdict with more surviving
			// components is malformed
			if len(valid) < 2 || len(valid) > tags.MaxCompoundWords {
				a.logger.Debug(reqID, "tag split[%s]: invalid components for %q proposed=%q",
					analyzer.TagSplitVersion, candidate, parts)
				row.Disposition = tags.CompositionKeep
				row.Reason = tags.CompositionReasonInvalidComponents
				row.Model = freshModel(cv)
				break
			}
			row.Disposition = tags.CompositionSplit
			row.Parts = valid
			row.Reason, row.Model = splitSource(cv)
			a.logger.Info(reqID, "tag split[%s]: split %q → %q", analyzer.TagSplitVersion, candidate, valid)
		case tags.CompositionDrop:
			row.Disposition = tags.CompositionDrop
			row.Reason = decision.Reason
			row.Model = freshModel(cv)
			a.logger.Info(reqID, "tag split[%s]: drop %q (reason=%s)", analyzer.TagSplitVersion, candidate, row.Reason)
		default:
			row.Disposition = tags.CompositionKeep
			row.Reason = decision.Reason
			row.Model = freshModel(cv)
			if parts := alongsideCover(fan, liveNames, partVerdicts, filter); len(parts) > 0 {
				row.Parts = parts
			}
			a.logger.Info(reqID, "tag split[%s]: keep %q (reason=%s cached=%t)", analyzer.TagSplitVersion, candidate, row.Reason, cv.Cached)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ConjunctionComponents disposes of an explicit coordination compound with no adjudicator: it splits on the
// joint-tag grammar, folds each segment onto its store anchor, and validates the survivors under the
// component bounds, returning the validated components or nil when the name is not an explicit coordination
// or the survivors are malformed. The emission gate's conjunction fallback and the stored-compound sweep
// share it, so an adjudicator-free store decomposes a conjunction identically on both paths.
func ConjunctionComponents(candidate string, promotedNames map[string]string, liveNames map[string]struct{}, filter *analyzer.TagFilter) []string {
	segments := textnorm.SplitJointTag(candidate)
	if segments == nil {
		return nil
	}
	parts := analyzer.NormalizeTags(segments)
	for i, part := range parts {
		if anchor := tags.AnchoredFold(part, promotedNames, liveNames); anchor != "" && anchor != part {
			parts[i] = anchor
		}
	}
	valid := analyzer.SplitComponents(parts, filter)
	if len(valid) < 2 || len(valid) > tags.MaxCompoundWords {
		return nil
	}
	return valid
}

// conjunctionSplits is the no-adjudicator fallback: explicit coordination splits mechanically through the
// joint-tag grammar (fold-resolved and validated like a judged split), every other compound keeps whole
// under judge_unavailable. It persists nothing and writes only composition rows.
func (a *Adjudicator) conjunctionSplits(names, candidates []string, promotedNames map[string]string, liveNames map[string]struct{}, filter *analyzer.TagFilter, reqID string) ([]string, []tags.CompositionRow, error) {
	components := make(map[string][]string, len(candidates))
	dropped := make(map[string]bool, len(candidates))
	rows := make([]tags.CompositionRow, 0, len(candidates))
	for _, candidate := range candidates {
		row := tags.CompositionRow{Candidate: candidate, CriteriaVersion: analyzer.TagSplitVersion}
		if textnorm.SplitJointTag(candidate) == nil {
			row.Disposition = tags.CompositionKeep
			row.Reason = tags.CompositionReasonJudgeUnavailable
			a.logger.Info(reqID, "tag split[%s]: keep %q (reason=%s cached=false)", analyzer.TagSplitVersion, candidate, row.Reason)
			rows = append(rows, row)
			continue
		}
		valid := ConjunctionComponents(candidate, promotedNames, liveNames, filter)
		if valid == nil {
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

// compositionFeatures folds the two passes' verdicts into the decision's feature set: the compound status
// (or the abstention), the parts' dimensional reading, the judge-outage flag a missing verdict sets, the
// cross-sample disagreement either pass observed, and the reconstructibility signal — every cover segment
// dimensional and every resolved cover segment a promoted store label (the established-value proxy the fold
// anchor already receives). coverComplete reports whether the candidate's content sequence has a full,
// multi-segment cover: when it does, the combination branch owns the decision and the parts
// are the cover's segments; when it does not, the unit branch owns it and parts carries the flat resolved
// content for the deferred/unavailable reads. deferred carries the candidate's own phrase-fold abstentions,
// so a hold stays scoped to the phrase that abstained.
func compositionFeatures(parts []string, coverComplete bool, deferred map[string]bool, verdicts map[string]PartVerdict, cv CompoundVerdict, promotedNames map[string]string) tags.CompositionFeatures {
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

	allDimensional := coverComplete && len(parts) > 0
	for _, part := range parts {
		if deferred[part] {
			f.PartsDeferred = true
			allDimensional = false
			continue
		}
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
	if !coverComplete && !f.PartsDeferred && !f.JudgeUnavailable && len(parts) > 0 {
		// no full multi-segment cover: the unit branch owns the decision
		f.PartsNotDimensional = true
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

// Fan is one candidate's decomposition fan: its content words, the applied form of every fan segment (a
// content word or a contiguous bigram; a segment is absent when it is a dropped time designation), and the
// content words whose phrase-context fold abstained.
type Fan struct {
	Content  []string
	Resolved map[string]string
	deferred []string
}

// DeferredSet returns the fan's abstained words as a set the per-candidate disposition reads, so a hold
// stays scoped to the phrase that abstained rather than leaking through the shared value-keyed verdict map;
// nil when the fan has none.
func (f Fan) DeferredSet() map[string]bool {
	if len(f.deferred) == 0 {
		return nil
	}
	set := make(map[string]bool, len(f.deferred))
	for _, word := range f.deferred {
		set[word] = true
	}
	return set
}

// Resolve adapts the fan to the cover scan: a fan span resolves to its applied name when the store already
// holds that resolved form (a store value is a value by definition) or the parts pass judged the resolved
// segment dimensional and not deferred; every other span reports not-usable. A word this fan's own
// phrase-context fold abstained on is not usable even when the parts pass called it dimensional, so an
// abstention in one phrase never lends a cover to another compound that shares the word.
func (f Fan) Resolve(storeKeys map[string]struct{}, verdicts map[string]PartVerdict) func(string) (string, bool) {
	deferred := f.DeferredSet()
	return func(span string) (string, bool) {
		resolved, ok := f.Resolved[span]
		if !ok || resolved == "" || deferred[resolved] {
			return "", false
		}
		if _, stored := storeKeys[tags.Key(resolved)]; stored {
			return resolved, true
		}
		v, ok := verdicts[resolved]
		if ok && v.Dimensional && !v.Deferred {
			return resolved, true
		}
		return "", false
	}
}

// FlatResolved returns the candidate's resolved content words in order, dropping the ones no resolution
// reached; it is the flat reading the unit branch's deferred/unavailable scans consume.
func (f Fan) FlatResolved() []string {
	out := make([]string, 0, len(f.Content))
	for _, w := range f.Content {
		if resolved := f.Resolved[w]; resolved != "" {
			out = append(out, resolved)
		}
	}
	return out
}

// resolveContentSingles resolves a candidate's content words through the deterministic time-designation gate
// and the attested seam fold, returning the content words with their position-aligned resolved singles: a
// time-designation word the live view does not hold is emptied, a mechanically folded word carries its
// anchor, and every other word is itself. Handing back both slices keeps a caller from re-deriving the
// content words just to index the singles. liveNames is the live store key set, promotedNames the promoted
// store view the fold anchors on.
func resolveContentSingles(candidate string, promotedNames map[string]string, liveNames map[string]struct{}) (content, singles []string) {
	content = tags.DeriveParts(candidate)
	singles = make([]string, len(content))
	for i, word := range content {
		if tags.IsTimeDesignationDrop(word, liveNames) {
			continue
		}
		singles[i] = word
		if anchor := tags.AnchoredFold(word, promotedNames, liveNames); anchor != "" && anchor != word {
			singles[i] = anchor
		}
	}
	return content, singles
}

// buildFan builds the fan resolution from position-aligned resolved singles: a bigram is literal (the
// attestation table is single-token) and present only when both its words resolved and the bigram is not a
// time designation the store lacks. A nil or short singles slice leaves the matching content words
// unresolved, so a caller with no resolutions passes nil.
func buildFan(content, singles []string, liveNames map[string]struct{}) Fan {
	resolved := make(map[string]string, len(content)*2)
	for i, word := range content {
		if i < len(singles) && singles[i] != "" {
			resolved[word] = singles[i]
		}
	}
	for i := 0; i+1 < len(content); i++ {
		if i+1 >= len(singles) || singles[i] == "" || singles[i+1] == "" {
			continue
		}
		bigram := content[i] + " " + content[i+1]
		if tags.IsTimeDesignationDrop(bigram, liveNames) {
			continue
		}
		resolved[bigram] = bigram
	}
	return Fan{Content: content, Resolved: resolved}
}

// phraseFoldKeys returns the residual phrase-context fold keys a candidate's words raise: every content word
// whose own single is unresolved (not time-dropped, not mechanically folded) and whose primary-sense base is
// not live while another attested base is.
func phraseFoldKeys(content, singles []string, phrase string, liveNames map[string]struct{}) []PhraseFoldKey {
	var keys []PhraseFoldKey
	for i, word := range content {
		if i >= len(singles) || singles[i] == "" || singles[i] != word {
			continue
		}
		for _, anchor := range tags.ResidualFoldAnchors(word, liveNames) {
			keys = append(keys, PhraseFoldKey{Phrase: phrase, Part: word, Anchor: anchor})
		}
	}
	return keys
}

// applyPhraseFolds completes a fan's singles with its residual phrase-context folds: a word the caller left
// unresolved is folded onto its anchor on a confirmed answer, and an abstention (or an ambiguity across
// several live bases) leaves it unresolved and reports it deferred, so the compound holds for review instead
// of landing on a wrong tag. The returned singles and deferred words are what the caller's fan carries.
func applyPhraseFolds(content, singles []string, phrase string, verdicts map[PhraseFoldKey]PhraseFoldVerdict, liveNames map[string]struct{}) (resolved, deferred []string) {
	resolved = singles
	for i, word := range content {
		if i >= len(resolved) || resolved[i] == "" || resolved[i] != word {
			continue
		}
		anchors := tags.ResidualFoldAnchors(word, liveNames)
		switch {
		case len(anchors) > 1:
			// several live sibling senses: the phrase context cannot separate them, hold
			deferred = append(deferred, word)
		case len(anchors) == 1:
			v := verdicts[PhraseFoldKey{Phrase: phrase, Part: word, Anchor: anchors[0]}]
			switch {
			case v.Fold:
				resolved[i] = anchors[0]
			case v.Deferred:
				deferred = append(deferred, word)
			}
		}
	}
	return resolved, deferred
}

// seedStoreValues marks every resolved segment the store already holds as dimensional in the parts-verdict
// map: a store value is a value by definition, so its cover span needs no parts verdict.
func seedStoreValues(verdicts map[string]PartVerdict, fans []Fan, storeKeys map[string]struct{}) {
	for _, fan := range fans {
		for _, resolved := range fan.Resolved {
			if resolved == "" {
				continue
			}
			if _, stored := storeKeys[tags.Key(resolved)]; !stored {
				continue
			}
			if _, ok := verdicts[resolved]; !ok {
				verdicts[resolved] = PartVerdict{Dimensional: true}
			}
		}
	}
}

// coverParts returns the segments the composition decision reads for a fan: the coarsest cover's segments
// when the fan has a full multi-segment cover, otherwise the fan's flat resolved content for the unit
// branch's deferred/unavailable reads. The boolean reports whether a full cover exists.
func coverParts(fan Fan, storeKeys map[string]struct{}, verdicts map[string]PartVerdict) (parts []string, coverComplete bool) {
	cover := tags.GreedyCover(fan.Content, fan.Resolve(storeKeys, verdicts), false)
	if len(cover) >= 2 {
		return cover, true
	}
	return fan.FlatResolved(), false
}

// resolveFan derives the judged candidate's fan and completes it with the residual phrase-context folds: the
// words that raise a fold question are asked together, a confirmed answer folds the word onto its anchor,
// and an abstention defers it so the compound holds for review. Returns a non-nil error only for credit
// exhaustion.
func (a *Adjudicator) resolveFan(ctx context.Context, store *VerdictStore, candidate string, promotedNames map[string]string, liveNames map[string]struct{}, docId int64, reqID string) (Fan, error) {
	content, singles := resolveContentSingles(candidate, promotedNames, liveNames)
	if tags.IsPeriodClass(tags.HouseForm(candidate)) {
		// a period is valid by fiat and does not decompose: its fan carries no resolved segments
		return buildFan(content, nil, liveNames), nil
	}
	phrase := tags.HouseForm(candidate)
	keys := phraseFoldKeys(content, singles, phrase, liveNames)
	var deferred []string
	if len(keys) > 0 {
		verdicts, err := store.PhraseFolds(ctx, keys, docId, reqID)
		if err != nil {
			return Fan{}, err
		}
		singles, deferred = applyPhraseFolds(content, singles, phrase, verdicts, liveNames)
	}
	fan := buildFan(content, singles, liveNames)
	fan.deferred = deferred
	return fan, nil
}

// alongsideCover returns the parts a kept compound applies alongside itself: the greedy longest-leftmost
// partial cover over the fan's usable spans, validated by the per-document filter. A span no parts verdict
// or store value supports is skipped, so a covered bigram applies whole and a bare covered word applies on
// its own.
func alongsideCover(fan Fan, storeKeys map[string]struct{}, verdicts map[string]PartVerdict, filter *analyzer.TagFilter) []string {
	return analyzer.SplitComponents(tags.GreedyCover(fan.Content, fan.Resolve(storeKeys, verdicts), true), filter)
}

// storedCoverage returns the judged-dimensional parts each store-existing decided keep applies alongside
// itself, keyed by the emitted compound name, reading part verdicts from the persisted cache only: a stored
// compound gets its parts applied on every emission without model calls, so coverage is idempotent and a
// failed cache read simply retries next time. The map is empty when there is nothing to apply, and every
// application is logged at info so the emission's review can see it.
func (a *Adjudicator) storedCoverage(ctx context.Context, skipped []string, promotedNames map[string]string, liveNames map[string]struct{}, filter *analyzer.TagFilter, reqID string) map[string][]string {
	alongsideByCompound := make(map[string][]string)
	if len(skipped) == 0 {
		return alongsideByCompound
	}
	fans := make(map[string]Fan, len(skipped))
	seenValues := make(map[string]bool)
	var values []string
	for _, name := range skipped {
		content, singles := resolveContentSingles(name, promotedNames, liveNames)
		fan := buildFan(content, singles, liveNames)
		fans[name] = fan
		for _, seg := range tags.DeriveSegments(name) {
			resolved := fan.Resolved[seg]
			if resolved == "" || seenValues[resolved] {
				continue
			}
			seenValues[resolved] = true
			values = append(values, resolved)
		}
	}
	cached := readCachedPartVerdicts(ctx, a.queries, a.logger, values, reqID)
	for _, name := range skipped {
		fan := fans[name]
		// a cached verdict drives the alongside application; a segment without one (never judged, or a
		// criteria-version bump) is a coverage gap the review queue must see rather than a silent omission;
		// with a warning logged here, the durable pending-coverage accounting belongs to the completeness audit.
		for _, resolved := range fan.Resolved {
			if resolved == "" {
				continue
			}
			if _, stored := liveNames[tags.Key(resolved)]; stored {
				continue
			}
			if _, ok := cached[resolved]; !ok {
				a.logger.Warn(reqID, "tag split[%s]: %q part %q has no cached verdict, pending coverage (store_existing)",
					analyzer.TagSplitVersion, name, resolved)
			}
		}
		parts := alongsideCover(fan, liveNames, cached, filter)
		if len(parts) == 0 {
			continue
		}
		alongsideByCompound[name] = parts
		a.logger.Info(reqID, "tag split[%s]: keep %q applies parts %q alongside (store_existing)",
			analyzer.TagSplitVersion, name, parts)
	}
	return alongsideByCompound
}

// withAlongside returns the tag list with each kept compound's alongside parts inserted immediately after
// it, first-seen order and deduplicated: a compound absent from the list (dropped or split) cannot carry
// parts, and a part already present is not repeated.
func withAlongside(names []string, alongsideByCompound map[string][]string) []string {
	if len(alongsideByCompound) == 0 {
		return names
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		for _, part := range alongsideByCompound[name] {
			if seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

// splitSource names a split decision's verdict source and the model that judged it: a cached compound
// verdict has no fresh call behind it and no model; a fresh judge call carries its model. Exported so the
// stored-compound sweep records the same verdict source the emission gate does.
func splitSource(cv CompoundVerdict) (reason, model string) {
	if cv.Cached {
		return tags.CompositionReasonCached, ""
	}
	return tags.CompositionReasonJudge, cv.Model
}

// freshModel is the model a composition row records: the judge's model for a fresh verdict, empty for a
// cached one. Exported so the stored-compound sweep records the same model the emission gate does.
func freshModel(cv CompoundVerdict) string {
	if cv.Cached {
		return ""
	}
	return cv.Model
}

// ApplyDeletedTagPolicy drops a tag list's blocked names: an operator deletion lands in the blocklist
// and enrichment cannot resurrect it. The result is deduplicated.
func (a *Adjudicator) ApplyDeletedTagPolicy(tags []string, blocklist map[string]struct{}, reqID string) []string {
	if len(blocklist) == 0 {
		return tags
	}

	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		if _, ok := blocklist[tag]; ok {
			a.logger.Info(reqID, "deleted tag policy: dropping %q — the tag was deleted by an operator", tag)
			continue
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

// bandPair carries the verdict provenance a stored pair row needs: the judge's answer and whether it
// came from the cache.
type bandPair struct {
	verdict types.TagVerdict
	model   string
	cached  bool
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
	f.NotPerson = r.NotPerson
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

// ResolveLanguages is the language guard: it detects non-English values that are not
// already in the store and routes each through the judged translation to its English canonical form,
// rewriting the analysis's tags and tag details so the English name flows through identity and admission
// as any English name. A value the judge returns unchanged is already English — including technical
// jargon the dictionary misses — and stays; an uncertain, failed, or missing translation leaves the value
// in place and returns it in the pending set, so the quality gate holds it for review instead of admitting
// it plain. Without an adjudicator the guard is inert (nothing can be translated or deferred). storeKeys
// is the store's Key-keyed name set; a stored value is never re-translated.
func (a *Adjudicator) ResolveLanguages(ctx context.Context, analysis *tools.ContentAnalysisResult, storeKeys map[string]struct{}, reqID string) (map[string]struct{}, error) {
	if !a.runner.HasTagAdjudicator() {
		return nil, nil
	}
	var flagged []string
	seen := make(map[string]bool, len(analysis.Tags))
	for _, name := range analysis.Tags {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if _, stored := storeKeys[tags.Key(name)]; stored {
			continue
		}
		if tags.DetectNonEnglish(name) {
			flagged = append(flagged, name)
		}
	}
	if len(flagged) == 0 {
		return nil, nil
	}

	translations := make(map[string]string, len(flagged))
	pending := make(map[string]struct{})
	// a translation is new text the emission filter never saw; the bare filter re-applies the tag bounds
	translationFilter := analyzer.NewTagFilter(nil, nil, nil)
	for start := 0; start < len(flagged); start += verdictBatchSize {
		end := min(start+verdictBatchSize, len(flagged))
		batch := flagged[start:end]
		results, err := a.runner.JudgeTagTranslations(ctx, batch)
		if err != nil && retryableJudgeFailure(err) {
			a.logger.Warn(reqID, "taglanguage[%s]: translation attempt failed, retrying once: %v", analyzer.TagTranslationVersion, err)
			results, err = a.runner.JudgeTagTranslations(ctx, batch)
		}
		missing := make(map[int]bool)
		if err != nil {
			if isCreditError(err) {
				return nil, err
			}
			var incomplete *analyzer.IncompleteReplyError
			if !errors.As(err, &incomplete) {
				a.logger.Warn(reqID, "taglanguage[%s]: translation failed for %q, deferring them: %v", analyzer.TagTranslationVersion, batch, err)
				for _, name := range batch {
					pending[name] = struct{}{}
				}
				continue
			}
			missing = incomplete.MissingSet()
		}
		for i, name := range batch {
			if missing[i] || i >= len(results) {
				pending[name] = struct{}{}
				continue
			}
			r := results[i]
			translated := strings.TrimSpace(r.Translation)
			switch {
			case r.Defer || translated == "":
				pending[name] = struct{}{}
			case strings.EqualFold(translated, name):
				// already English; the judge confirmed the jargon
			default:
				house := tags.HouseForm(translated)
				if !translationFilter.Accept(house) {
					a.logger.Warn(reqID, "taglanguage[%s]: translation %q → %q fails the tag bounds, holding for review",
						analyzer.TagTranslationVersion, name, house)
					pending[name] = struct{}{}
					continue
				}
				translations[name] = house
			}
		}
	}

	if len(translations) > 0 {
		out := make([]string, 0, len(analysis.Tags))
		seenOut := make(map[string]bool, len(analysis.Tags))
		for _, name := range analysis.Tags {
			resolved := name
			if t, ok := translations[name]; ok {
				resolved = t
				a.logger.Info(reqID, "taglanguage[%s]: translate %q → %q", analyzer.TagTranslationVersion, name, resolved)
			}
			if resolved == "" || seenOut[resolved] {
				continue
			}
			seenOut[resolved] = true
			out = append(out, resolved)
		}
		for i := range analysis.TagDetails {
			if t, ok := translations[textnorm.NormalizeTag(analysis.TagDetails[i].Term)]; ok {
				analysis.TagDetails[i].Term = t
			}
		}
		analysis.Tags = out
	}
	return pending, nil
}

// TagQualityInput is the per-document input the quality gate decides over: the analysis whose tags are
// filtered, the matcher decisions, the store names a candidate is checked against, the document text the
// subject-matter criterion reads, the document id, and the names the language guard holds for review
// (ForceDefer).
type TagQualityInput struct {
	Analysis    *tools.ContentAnalysisResult
	Decisions   []policy.Decision
	DBNames     map[string]struct{}
	ReducedText string
	DocID       int64
	ForceDefer  map[string]struct{}
}

// ApplyTagQuality filters the analysis's tags through the admission decision: each novel candidate's
// features — the parts pass's dimension verdict plus the judge's rubric answers (cached or fresh) and the
// emission's deterministic signals — resolve through tags.DecideAdmission. The parts pass owns the
// dimension question, so the gate resolves each uncached candidate's dimension verdict from the shared
// parts cache and stamps it; an absent or abstained verdict defers rather than counting as a false answer.
// A name in ForceDefer is held for review and never admitted plain. Rejected tags are dropped from the
// returned list; deferred and admitted tags are both kept, and every kept novel name's decision is
// returned so the caller can drive promotion. A missing judge, judge failure, or context cancellation
// keeps the tags, born provisional with a defer decision; a partial judge reply salvages its returned
// entries and defers only the missing indices; a credit-exhaustion failure is the only error returned, so
// the caller can pause the batch.
func (a *Adjudicator) ApplyTagQuality(ctx context.Context, in TagQualityInput, reqID string) ([]string, map[string]tags.AdmissionDecision, error) {
	analysis := in.Analysis
	decisions := in.Decisions
	dbNames := in.DBNames
	reducedText := in.ReducedText
	docId := in.DocID
	forceDefer := in.ForceDefer
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
		if _, held := forceDefer[out]; held {
			// a detected non-English value the language guard could not translate is held for review,
			// never admitted plain
			decided[out] = tags.AdmissionDecision{Outcome: tags.AdmissionDefer, Reason: reasonLanguageReview}
			a.logger.Info(reqID, "taglanguage[%s]: defer %q to review (non-English, untranslated)", analyzer.TagTranslationVersion, out)
			continue
		}
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

	var dimensionVerdicts map[string]PartVerdict
	if len(uncached) > 0 && a.runner.HasTagAdjudicator() {
		// the parts pass owns the dimension question: resolve each uncached novel name's
		// dimension verdict from the shared parts cache, judging the misses, and stamp it onto the
		// quality result so the admission decision reads one verdict, never two. Without an adjudicator
		// there is no verdict to read, so the stamp abstains and the quality result defers.
		names := make([]string, len(uncached))
		for i, n := range uncached {
			names[i] = n.emitted
		}
		partStore := NewVerdictStore(a.queries, a.runner, a.logger, a.config.ResolvedAdjudicatorLlm().Model, tags.PromotedGuardKeys(dbNames))
		var derr error
		dimensionVerdicts, derr = partStore.Parts(ctx, names, docId, reqID)
		if derr != nil {
			return nil, nil, derr
		}
	}
	// an absent or abstained parts verdict is not a false answer: it defers rather than fabricating a
	// failing signal the model never produced
	stampDimension := func(r *analyzer.TagQualityResult, name string) {
		v, ok := dimensionVerdicts[name]
		dimension, abstain := PartDimensionFeature(v, ok)
		if abstain {
			r.Defer = true
			return
		}
		r.Dimension = dimension
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
						Index:    i,
						Term:     n.emitted,
						Category: n.promptCategory(),
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
							stampDimension(&results[i], n.emitted)
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
				for i := range results {
					n := batch[i]
					stampDimension(&results[i], n.emitted)
					decision := a.decideQuality(ctx, docId, n, results[i], model, reqID)
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
