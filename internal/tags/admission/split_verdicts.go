package admission

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tools"
)

// verdictBatchSize bounds one judged call's value or candidate list.
const verdictBatchSize = 40

// tagCompoundDeferStatus is the persisted compound row status a judge abstention writes; it is not one of the
// judge's readings.
const tagCompoundDeferStatus = "defer"

// VerdictStore reads and writes the split gate's persisted verdicts: the parts pass's dimensional answers
// keyed by resolved value, and the compound pass's statuses keyed by candidate. Both are append-only and
// version-filtered, so a re-run reads a prior verdict instead of re-judging; a fresh verdict persists, and a
// concurrent writer's stored row wins with the disagreement flag set. One store serves the emission gate and
// the stored-joint sweep, so both judge and persist the same rows under one failure policy: a transient
// failure retries once, an incomplete reply salvages the entries it returned and defers only the missing
// ones, every other non-credit failure leaves the verdicts absent, and credit exhaustion is the only error.
type VerdictStore struct {
	queries  *database.Queries
	runner   *tools.Runner
	logger   *log.Logger
	model    string
	promoted map[string]struct{}
}

// NewVerdictStore wires the store to its queries, judge runner, logger, and the model a fresh verdict row
// records. promoted is the promoted store values' key set (Key(storeName)); a fresh false dimension
// verdict on a promoted value is deferred rather than cached.
func NewVerdictStore(queries *database.Queries, runner *tools.Runner, logger *log.Logger, model string, promoted map[string]struct{}) *VerdictStore {
	return &VerdictStore{queries: queries, runner: runner, logger: logger, model: model, promoted: promoted}
}

// PartDimensionFeature reads a value's parts verdict into the quality gate's dimension feature: a real
// verdict yields its answer, while an absent or abstained verdict is not a false answer — it reports
// abstain so the caller defers instead of fabricating a failing signal. The parts pass is the
// single owner of the dimension question, so every consumer reads this one mapping.
func PartDimensionFeature(v PartVerdict, ok bool) (dimension bool, abstain bool) {
	if !ok || v.Deferred {
		return false, true
	}
	return v.Dimensional, false
}

// DimensionFeature resolves one value's dimension feature for the quality gate: it reads the shared parts
// cache, judging a miss, and maps the verdict through PartDimensionFeature. The first return is the
// feature and abstain is true when the verdict is absent or abstained, so the caller defers instead of
// reading a fabricated false. It is the single-name seam the reconcile provisional sweep shares with the
// document gate, which resolves a whole batch through Parts.
func (s *VerdictStore) DimensionFeature(ctx context.Context, value string, docId int64, reqID string) (dimension bool, abstain bool, err error) {
	verdicts, err := s.Parts(ctx, []string{value}, docId, reqID)
	if err != nil {
		return false, false, err
	}
	v, ok := verdicts[value]
	dimension, abstain = PartDimensionFeature(v, ok)
	return dimension, abstain, nil
}

// runVerdictBatches drives one verdict family's uncached keys through its judge in bounded batches under
// the policy every family observes: a transient failure retries once, a credit error aborts the whole run,
// a truncated reply applies the entries it did return and leaves the missing indices to the caller's
// abstention path, and a reply of the wrong length is discarded. judge answers one batch; apply persists
// one answered key. The returned error is non-nil only for credit exhaustion. Parts, Compounds, and
// PhraseFolds share it so their retry, credit, and salvage behavior cannot drift apart.
func runVerdictBatches[K any, R any](
	ctx context.Context,
	s *VerdictStore,
	reqID, version string,
	uncached []K,
	judge func(context.Context, []K) ([]R, error),
	apply func(K, R) error,
) error {
	for i := 0; i < len(uncached); i += verdictBatchSize {
		end := min(i+verdictBatchSize, len(uncached))
		batch := uncached[i:end]
		results, err := judge(ctx, batch)
		if err != nil && retryableJudgeFailure(err) {
			s.logger.Warn(reqID, "tag split[%s]: judge attempt failed, retrying once: %v", version, err)
			results, err = judge(ctx, batch)
		}
		if err != nil {
			if isCreditError(err) {
				return err
			}
			var incomplete *analyzer.IncompleteReplyError
			if !errors.As(err, &incomplete) {
				s.logger.Warn(reqID, "tag split[%s]: judge failed for %d keys, deferring them: %v", version, len(batch), err)
				continue
			}
			missing := make(map[int]bool, len(incomplete.Missing))
			for _, idx := range incomplete.Missing {
				missing[idx] = true
			}
			salvaged, deferred := 0, 0
			for j, r := range results {
				if j >= len(batch) || missing[j] {
					deferred++
					continue
				}
				if aerr := apply(batch[j], r); aerr != nil {
					return aerr
				}
				salvaged++
			}
			s.logger.Warn(reqID, "tag split[%s]: partial reply, salvaged %d of %d keys and deferred %d (re-judged on re-emission): %v",
				version, salvaged, len(batch), deferred, err)
			continue
		}
		if len(results) != len(batch) {
			s.logger.Warn(reqID, "tag split[%s]: judge returned %d verdicts for %d keys, deferring them", version, len(results), len(batch))
			continue
		}
		for j, r := range results {
			if aerr := apply(batch[j], r); aerr != nil {
				return aerr
			}
		}
	}
	return nil
}

// Parts resolves each distinct resolved part value's dimensional verdict from the persisted cache or one
// judged call over the uncached remainder, in bounded batches. Values the judge left unanswered are absent
// from the result, so a candidate holding any of them folds to judge_unavailable. A non-nil error is
// returned only for credit exhaustion.
func (s *VerdictStore) Parts(ctx context.Context, values []string, docId int64, reqID string) (map[string]PartVerdict, error) {
	verdicts := make(map[string]PartVerdict, len(values))
	if len(values) == 0 {
		return verdicts, nil
	}
	rows, err := s.queries.LatestTagPartVerdictsForValues(ctx, database.LatestTagPartVerdictsForValuesParams{
		PolicyVersion: analyzer.TagPartDimensionVersion,
		PartValues:    values,
	})
	if err != nil {
		s.logger.Error(reqID, "tag split[%s]: part verdict cache lookup failed: %v", analyzer.TagPartDimensionVersion, err)
	}
	for _, row := range rows {
		verdicts[row.Value] = partVerdictFromRow(row, s.logger, reqID)
	}
	var uncached []string
	for _, value := range values {
		if _, ok := verdicts[value]; !ok {
			uncached = append(uncached, value)
		}
	}
	if err := runVerdictBatches(ctx, s, reqID, analyzer.TagPartDimensionVersion, uncached,
		func(ctx context.Context, batch []string) ([]analyzer.TagPartResult, error) {
			return s.runner.JudgeTagParts(ctx, batch)
		},
		func(value string, r analyzer.TagPartResult) error {
			verdict, werr := s.writePartVerdict(ctx, docId, value, r, reqID)
			if werr != nil {
				return werr
			}
			verdicts[value] = verdict
			return nil
		}); err != nil {
		return nil, err
	}
	return verdicts, nil
}

// readCachedPartVerdicts reads the persisted dimensional verdicts for the resolved values, never judging:
// the store-existing coverage path applies parts a decision already judged, so application makes no model
// calls and stays idempotent across re-emissions. A lookup failure logs and returns the empty map, since
// coverage retries on the next emission; values without a cached verdict are absent.
func readCachedPartVerdicts(ctx context.Context, q *database.Queries, logger *log.Logger, values []string, reqID string) map[string]PartVerdict {
	verdicts := make(map[string]PartVerdict, len(values))
	if len(values) == 0 {
		return verdicts
	}
	rows, err := q.LatestTagPartVerdictsForValues(ctx, database.LatestTagPartVerdictsForValuesParams{
		PolicyVersion: analyzer.TagPartDimensionVersion,
		PartValues:    values,
	})
	if err != nil {
		logger.Error(reqID, "tag split[%s]: part verdict cache lookup failed: %v", analyzer.TagPartDimensionVersion, err)
		return verdicts
	}
	for _, row := range rows {
		verdicts[row.Value] = partVerdictFromRow(row, logger, reqID)
	}
	return verdicts
}

// writePartVerdict persists one fresh part verdict. On conflict the row another worker cached first wins, and
// a stored answer differing from this sample sets the disagreement flag the decision reads.
func (s *VerdictStore) writePartVerdict(ctx context.Context, docId int64, value string, r analyzer.TagPartResult, reqID string) (PartVerdict, error) {
	r, serr := s.settlePartFalse(ctx, value, r, reqID)
	if serr != nil {
		return PartVerdict{}, serr
	}
	criteria, err := json.Marshal(r.Criteria())
	if err != nil {
		criteria = json.RawMessage("{}")
	}
	fresh := PartVerdict{Dimensional: r.Dimensional, Deferred: r.Defer}
	n, err := s.queries.InsertTagPartDimensionEvent(ctx, database.InsertTagPartDimensionEventParams{
		DocumentID:    sql.NullInt64{Int64: docId, Valid: docId != 0},
		Value:         value,
		Criteria:      criteria,
		Dimensional:   r.Dimensional,
		VerdictModel:  sql.NullString{String: s.model, Valid: s.model != ""},
		PolicyVersion: analyzer.TagPartDimensionVersion,
	})
	if err != nil {
		s.logger.Error(reqID, "tag split[%s]: part verdict insert failed for %q: %v", analyzer.TagPartDimensionVersion, value, err)
		return fresh, nil
	}
	if n > 0 {
		return fresh, nil
	}

	rows, err := s.queries.LatestTagPartVerdictsForValues(ctx, database.LatestTagPartVerdictsForValuesParams{
		PolicyVersion: analyzer.TagPartDimensionVersion,
		PartValues:    []string{value},
	})
	if err != nil || len(rows) == 0 {
		if err != nil {
			s.logger.Error(reqID, "tag split[%s]: part verdict cache re-read after conflict failed: %v", analyzer.TagPartDimensionVersion, err)
		}
		return fresh, nil
	}
	stored := partVerdictFromRow(rows[0], s.logger, reqID)
	stored.Disagreement = stored.Dimensional != r.Dimensional || stored.Deferred != r.Defer
	if stored.Disagreement {
		s.logger.Warn(reqID, "tag split[%s]: part verdict disagreement for %q: fresh(%s) stored(dimensional=%t defer=%t)",
			analyzer.TagPartDimensionVersion, value, r.Criteria(), stored.Dimensional, stored.Deferred)
	}
	return stored, nil
}

// settlePartFalse guards the destructive direction of a fresh part verdict: a model false on a value the
// store already holds promoted is not cached — the store signal wins and the verdict defers to review — and
// any other fresh false takes one corroborating sample before it is cached, so a single noisy rejection
// cannot sweep the whole generation. A true or abstained verdict, and a corroborated false,
// pass through unchanged.
func (s *VerdictStore) settlePartFalse(ctx context.Context, value string, r analyzer.TagPartResult, reqID string) (analyzer.TagPartResult, error) {
	if r.Dimensional || r.Defer {
		return r, nil
	}
	if _, ok := s.promoted[tags.Key(value)]; ok {
		s.logger.Warn(reqID, "tag split[%s]: %q is a promoted store value; false dimension deferred to review",
			analyzer.TagPartDimensionVersion, value)
		return analyzer.TagPartResult{Index: r.Index, Dimensional: false, Defer: true}, nil
	}
	second, err := s.runner.JudgeTagParts(ctx, []string{value})
	if err != nil {
		if isCreditError(err) {
			// credit exhaustion is the only error that escapes the parts pass; the caller pauses the batch
			return analyzer.TagPartResult{}, err
		}
		s.logger.Warn(reqID, "tag split[%s]: false dimension for %q could not be corroborated, deferring: %v",
			analyzer.TagPartDimensionVersion, value, err)
		return analyzer.TagPartResult{Index: r.Index, Dimensional: false, Defer: true}, nil
	}
	if len(second) != 1 {
		s.logger.Warn(reqID, "tag split[%s]: false dimension for %q could not be corroborated (no verdict), deferring",
			analyzer.TagPartDimensionVersion, value)
		return analyzer.TagPartResult{Index: r.Index, Dimensional: false, Defer: true}, nil
	}
	if second[0].Dimensional || second[0].Defer {
		s.logger.Warn(reqID, "tag split[%s]: false dimension for %q not corroborated (dimensional=%t defer=%t), deferring",
			analyzer.TagPartDimensionVersion, value, second[0].Dimensional, second[0].Defer)
		return analyzer.TagPartResult{Index: r.Index, Dimensional: false, Defer: true}, nil
	}
	return r, nil
}

// partVerdictFromRow reads a persisted part row back into a verdict; the criteria JSON is authoritative for
// the abstention, with the dimensional column as fallback.
func partVerdictFromRow(row database.LatestTagPartVerdictsForValuesRow, logger *log.Logger, reqID string) PartVerdict {
	v := PartVerdict{Dimensional: row.Dimensional}
	var c analyzer.TagPartCriteria
	if err := json.Unmarshal(row.Criteria, &c); err != nil {
		logger.Warn(reqID, "tag split[%s]: part verdict criteria unreadable for %q, using the dimensional column: %v",
			analyzer.TagPartDimensionVersion, row.Value, err)
		return v
	}
	if r, ok := c.Result(); ok {
		v.Dimensional = r.Dimensional
		v.Deferred = r.Defer
	}
	return v
}

// PhraseFoldKey identifies one residual phrase-context fold question: the phrase a part appears in, the
// part, and the anchor store value the part might denote.
type PhraseFoldKey struct {
	Phrase string
	Part   string
	Anchor string
}

// PhraseFoldVerdict is one residual fold's resolved answer: a confirmed fold, or an abstention.
type PhraseFoldVerdict struct {
	Fold     bool
	Deferred bool
}

// PhraseFolds resolves each residual phrase-context fold key from the persisted cache or judged calls over
// the uncached remainder, in bounded batches, mirroring Parts. A confirmed fold or an abstention persists;
// keys the judge left unanswered are absent from the result, so the caller leaves the part unfolded. A
// non-nil error is returned only for credit exhaustion.
func (s *VerdictStore) PhraseFolds(ctx context.Context, keys []PhraseFoldKey, docId int64, reqID string) (map[PhraseFoldKey]PhraseFoldVerdict, error) {
	verdicts := make(map[PhraseFoldKey]PhraseFoldVerdict, len(keys))
	if len(keys) == 0 {
		return verdicts, nil
	}
	phraseSet := make(map[string]struct{}, len(keys))
	phrases := make([]string, 0, len(keys))
	wanted := make(map[PhraseFoldKey]bool, len(keys))
	for _, k := range keys {
		wanted[k] = true
		if _, ok := phraseSet[k.Phrase]; ok {
			continue
		}
		phraseSet[k.Phrase] = struct{}{}
		phrases = append(phrases, k.Phrase)
	}
	rows, err := s.queries.LatestTagPhraseFoldVerdictsForPhrases(ctx, database.LatestTagPhraseFoldVerdictsForPhrasesParams{
		CriteriaVersion: analyzer.TagPhraseFoldVersion,
		Phrases:         phrases,
	})
	if err != nil {
		s.logger.Error(reqID, "tag split[%s]: phrase fold cache lookup failed: %v", analyzer.TagPhraseFoldVersion, err)
	}
	for _, row := range rows {
		key := PhraseFoldKey{Phrase: row.Phrase, Part: row.Part, Anchor: row.Anchor}
		if !wanted[key] {
			continue
		}
		verdicts[key] = phraseFoldVerdictFromRow(row, s.logger, reqID)
	}
	seen := make(map[PhraseFoldKey]bool, len(keys))
	var uncached []PhraseFoldKey
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, ok := verdicts[k]; !ok {
			uncached = append(uncached, k)
		}
	}
	if err := runVerdictBatches(ctx, s, reqID, analyzer.TagPhraseFoldVersion, uncached,
		func(ctx context.Context, batch []PhraseFoldKey) ([]analyzer.TagPhraseFoldResult, error) {
			candidates := make([]analyzer.TagPhraseFoldCandidate, len(batch))
			for j, k := range batch {
				candidates[j] = analyzer.TagPhraseFoldCandidate{Index: j, Phrase: k.Phrase, Part: k.Part, Anchor: k.Anchor}
			}
			return s.runner.JudgeTagPhraseFolds(ctx, candidates)
		},
		func(key PhraseFoldKey, r analyzer.TagPhraseFoldResult) error {
			verdicts[key] = s.writePhraseFoldVerdict(ctx, docId, key, r, reqID)
			return nil
		}); err != nil {
		return nil, err
	}
	return verdicts, nil
}

// writePhraseFoldVerdict persists one fresh residual fold verdict, mirroring writePartVerdict: the first
// writer's row wins on conflict.
func (s *VerdictStore) writePhraseFoldVerdict(ctx context.Context, docId int64, key PhraseFoldKey, r analyzer.TagPhraseFoldResult, reqID string) PhraseFoldVerdict {
	criteria, err := json.Marshal(r.Criteria())
	if err != nil {
		criteria = json.RawMessage("{}")
	}
	fresh := PhraseFoldVerdict{Fold: r.Fold, Deferred: r.Defer}
	n, err := s.queries.InsertTagPhraseFoldEvent(ctx, database.InsertTagPhraseFoldEventParams{
		DocumentID:      sql.NullInt64{Int64: docId, Valid: docId != 0},
		Phrase:          key.Phrase,
		Part:            key.Part,
		Anchor:          key.Anchor,
		Criteria:        criteria,
		Fold:            r.Fold,
		VerdictModel:    sql.NullString{String: s.model, Valid: s.model != ""},
		CriteriaVersion: analyzer.TagPhraseFoldVersion,
	})
	if err != nil {
		s.logger.Error(reqID, "tag split[%s]: phrase fold insert failed for %q/%q → %q: %v", analyzer.TagPhraseFoldVersion, key.Phrase, key.Part, key.Anchor, err)
		return fresh
	}
	if n > 0 {
		return fresh
	}

	rows, err := s.queries.LatestTagPhraseFoldVerdictsForPhrases(ctx, database.LatestTagPhraseFoldVerdictsForPhrasesParams{
		CriteriaVersion: analyzer.TagPhraseFoldVersion,
		Phrases:         []string{key.Phrase},
	})
	if err != nil || len(rows) == 0 {
		if err != nil {
			s.logger.Error(reqID, "tag split[%s]: phrase fold cache re-read after conflict failed: %v", analyzer.TagPhraseFoldVersion, err)
		}
		return fresh
	}
	for _, row := range rows {
		if row.Part == key.Part && row.Anchor == key.Anchor {
			return phraseFoldVerdictFromRow(row, s.logger, reqID)
		}
	}
	return fresh
}

// phraseFoldVerdictFromRow reads a persisted phrase-fold row back into a verdict; the criteria JSON is
// authoritative for the abstention, with the fold column as fallback.
func phraseFoldVerdictFromRow(row database.LatestTagPhraseFoldVerdictsForPhrasesRow, logger *log.Logger, reqID string) PhraseFoldVerdict {
	v := PhraseFoldVerdict{Fold: row.Fold}
	var c analyzer.TagPhraseFoldCriteria
	if err := json.Unmarshal(row.Criteria, &c); err != nil {
		logger.Warn(reqID, "tag split[%s]: phrase fold criteria unreadable for %q/%q → %q, using the fold column: %v",
			analyzer.TagPhraseFoldVersion, row.Phrase, row.Part, row.Anchor, err)
		return v
	}
	if r, ok := c.Result(); ok {
		v.Fold = r.Fold
		v.Deferred = r.Defer
	}
	return v
}

// Compounds resolves each compound candidate's status from the persisted cache or one judged call over the
// uncached remainder, in bounded batches, mirroring Parts. Candidates the judge left unanswered are absent
// from the result, so they fold to judge_unavailable. A non-nil error is returned only for credit exhaustion.
func (s *VerdictStore) Compounds(ctx context.Context, candidates []string, docId int64, reqID string) (map[string]CompoundVerdict, error) {
	verdicts := make(map[string]CompoundVerdict, len(candidates))
	if len(candidates) == 0 {
		return verdicts, nil
	}
	rows, err := s.queries.LatestTagCompoundStatusForCandidates(ctx, database.LatestTagCompoundStatusForCandidatesParams{
		SplitCriteriaVersion: analyzer.TagSplitVersion,
		Candidates:           candidates,
	})
	if err != nil {
		s.logger.Error(reqID, "tag split[%s]: compound verdict cache lookup failed: %v", analyzer.TagSplitVersion, err)
	}
	for _, row := range rows {
		verdicts[row.Candidate] = s.compoundVerdictFromRow(row, reqID)
	}
	var uncached []string
	for _, candidate := range candidates {
		if _, ok := verdicts[candidate]; !ok {
			uncached = append(uncached, candidate)
		}
	}
	if err := runVerdictBatches(ctx, s, reqID, analyzer.TagSplitVersion, uncached,
		func(ctx context.Context, batch []string) ([]analyzer.TagCompoundResult, error) {
			return s.runner.JudgeTagCompounds(ctx, batch)
		},
		func(candidate string, r analyzer.TagCompoundResult) error {
			if !r.Status.Valid() {
				s.logger.Warn(reqID, "tag split[%s]: compound judge returned no status for %q", analyzer.TagSplitVersion, candidate)
				return nil
			}
			verdicts[candidate] = s.writeCompoundVerdict(ctx, docId, candidate, r, reqID)
			return nil
		}); err != nil {
		return nil, err
	}
	return verdicts, nil
}

// writeCompoundVerdict persists one fresh compound verdict, mirroring writePartVerdict: the first writer's
// row wins on conflict and a differing sample sets the disagreement flag.
func (s *VerdictStore) writeCompoundVerdict(ctx context.Context, docId int64, candidate string, r analyzer.TagCompoundResult, reqID string) CompoundVerdict {
	criteria, err := json.Marshal(r.Criteria())
	if err != nil {
		criteria = json.RawMessage("{}")
	}
	fresh := CompoundVerdict{Status: r.Status, Deferred: r.Defer, Model: s.model}
	n, err := s.queries.InsertTagCompoundStatusEvent(ctx, database.InsertTagCompoundStatusEventParams{
		DocumentID:           sql.NullInt64{Int64: docId, Valid: docId != 0},
		Candidate:            candidate,
		Status:               compoundRowStatus(r),
		Criteria:             criteria,
		VerdictModel:         sql.NullString{String: s.model, Valid: s.model != ""},
		SplitCriteriaVersion: analyzer.TagSplitVersion,
	})
	if err != nil {
		s.logger.Error(reqID, "tag split[%s]: compound verdict insert failed for %q: %v", analyzer.TagSplitVersion, candidate, err)
		return fresh
	}
	if n > 0 {
		return fresh
	}

	rows, err := s.queries.LatestTagCompoundStatusForCandidates(ctx, database.LatestTagCompoundStatusForCandidatesParams{
		SplitCriteriaVersion: analyzer.TagSplitVersion,
		Candidates:           []string{candidate},
	})
	if err != nil || len(rows) == 0 {
		if err != nil {
			s.logger.Error(reqID, "tag split[%s]: compound verdict cache re-read after conflict failed: %v", analyzer.TagSplitVersion, err)
		}
		return fresh
	}
	stored := s.compoundVerdictFromRow(rows[0], reqID)
	stored.Disagreement = stored.Status != fresh.Status || stored.Deferred != fresh.Deferred
	if stored.Disagreement {
		s.logger.Warn(reqID, "tag split[%s]: compound verdict disagreement for %q: fresh(%s) stored(status=%s defer=%t)",
			analyzer.TagSplitVersion, candidate, r.Criteria(), stored.Status, stored.Deferred)
	}
	return stored
}

// compoundRowStatus maps a judge answer to the row's status: an abstention persists as the defer status,
// every other reading as itself.
func compoundRowStatus(r analyzer.TagCompoundResult) string {
	if r.Defer {
		return tagCompoundDeferStatus
	}
	return string(r.Status)
}

// compoundVerdictFromRow reads a persisted compound row back into a verdict; the criteria JSON is
// authoritative for the reading and the abstention, with the status column as fallback.
func (s *VerdictStore) compoundVerdictFromRow(row database.LatestTagCompoundStatusForCandidatesRow, reqID string) CompoundVerdict {
	var c analyzer.TagCompoundCriteria
	if err := json.Unmarshal(row.Criteria, &c); err != nil {
		s.logger.Warn(reqID, "tag split[%s]: compound verdict criteria unreadable for %q, using the status column: %v",
			analyzer.TagSplitVersion, row.Candidate, err)
	} else if r, ok := c.Result(); ok {
		return CompoundVerdict{Status: r.Status, Deferred: r.Defer, Cached: true, Model: row.VerdictModel.String}
	}
	if row.Status == tagCompoundDeferStatus {
		return CompoundVerdict{Deferred: true, Cached: true, Model: row.VerdictModel.String}
	}
	return CompoundVerdict{Status: analyzer.TagCompoundStatus(row.Status), Cached: true, Model: row.VerdictModel.String}
}
