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
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/types"
)

const (
	tagQualityTLow               = -1.0
	tagQualityNoAnchorSimilarity = 0.60
)

type Adjudicator struct {
	config     *config.Config
	logger     *log.Logger
	queries    *database.Queries
	runner     *tools.Runner
	splitMu    sync.Mutex
	splitCache map[string]splitDecision
}

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

func (a *Adjudicator) SplitJointTags(ctx context.Context, tags []string, filter *analyzer.TagFilter, logId *string) []string {
	var candidates []string
	for _, t := range tags {
		if textnorm.SplitJointTag(t) != nil {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		return tags
	}

	decisions := a.splitVerdicts(ctx, candidates, logId)

	maxComponents := a.config.Enricher.ContentAnalyzer.MaxTags
	components := make(map[string][]string, len(candidates))
	for _, candidate := range candidates {
		d, ok := decisions[candidate]
		if !ok || d.verdict != analyzer.TagSplitSplit {
			continue
		}
		valid := analyzer.SplitComponents(d.components, filter)
		if len(valid) < 2 || len(valid) > maxComponents {
			continue
		}
		components[candidate] = valid
	}
	if len(components) == 0 {
		return tags
	}

	out := make([]string, 0, len(tags)+len(components))
	seen := make(map[string]bool, len(tags))
	for _, t := range tags {
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
		a.logger.Info(logId, "tag split: %q → %v", t, segments)
	}
	return out
}

func (a *Adjudicator) ApplyDeletedTagPolicy(tags []string, knownTags map[string]struct{}, blocklist map[string]database.ListAllDeletedTagsRow, logId *string) []string {
	if len(blocklist) == 0 {
		return tags
	}

	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		if row, ok := blocklist[tag]; ok {
			switch row.Kind {
			case curation.DeletedTagKindBlocked:
				a.logger.Info(logId, "deleted tag policy: dropping %q — the tag was deleted by an operator", tag)
				continue
			case curation.DeletedTagKindMerged:
				target := row.TargetName.String
				if !row.TargetName.Valid || target == "" {
					a.logger.Info(logId, "deleted tag policy: dropping %q — the merge target is unknown", tag)
					continue
				}
				if _, ok := knownTags[target]; !ok {
					a.logger.Info(logId, "deleted tag policy: dropping %q — it was merged into %q, which no longer exists", tag, target)
					continue
				}
				a.logger.Info(logId, "deleted tag policy: rewriting %q to %q (merged)", tag, target)
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

func (a *Adjudicator) splitVerdicts(ctx context.Context, candidates []string, logId *string) map[string]splitDecision {
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
		for _, name := range uncached {
			decisions[name] = splitDecision{verdict: analyzer.TagSplitSplit, components: textnorm.SplitJointTag(name)}
		}
		return decisions
	}

	results, err := a.runner.AdjudicateTagSplits(ctx, uncached)
	if err != nil {
		a.logger.Error(logId, "tag split adjudication failed, keeping %d joint tags: %v", len(uncached), err)
		return decisions
	}
	if len(results) != len(uncached) {
		a.logger.Error(logId, "tag split adjudication returned %d verdicts for %d tags, keeping them", len(results), len(uncached))
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

func (a *Adjudicator) writeVerdictEvent(ctx context.Context, docId int64, d policy.Decision, bp *bandPair, logId *string) {
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
		a.logger.Error(logId, "tag verdict event insert failed: %v", err)
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
		a.logger.Error(logId, "tag verdict cache re-read after conflict failed: %v", err)
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
		a.logger.Error(logId, "tag verdict event insert failed: %v", err)
	}
}

type novelTag struct {
	emitted  string
	category string
	standard *bool
	bestSim  float64
	haveSim  bool
}

func tagQualityScore(standardTerm *bool, category string, bestSim float64, haveSim bool, noAnchorSim float64) int {
	score := 0
	switch {
	case standardTerm == nil:
	case *standardTerm:
		score += 1
	default:
		score -= 1
	}
	if category == analyzer.TagCategoryOther {
		score -= 1
	}
	if haveSim && bestSim < noAnchorSim {
		score -= 1
	}
	return score
}

func isStandardLabel(v *bool) string {
	switch {
	case v == nil:
		return "absent"
	case *v:
		return "true"
	default:
		return "false"
	}
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

func (a *Adjudicator) ApplyTagQuality(ctx context.Context, analysis *tools.ContentAnalysisResult, decisions []policy.Decision, dbNames map[string]struct{}, reducedText string, docId int64, logId *string) []string {
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
		n := &novelTag{emitted: out, category: analyzer.TagCategoryOther}
		if d, ok := typedByTerm[out]; ok {
			n.standard = d.IsStandardTerm
			if d.Category != "" {
				n.category = d.Category
			}
		}
		if sim, ok := simByOutput[out]; ok && sim > 0 {
			n.bestSim, n.haveSim = sim, true
		}
		if score := tagQualityScore(n.standard, n.category, n.bestSim, n.haveSim, tagQualityNoAnchorSimilarity); float64(score) < tagQualityTLow {
			a.logger.Info(logId, "tagquality[%s]: reject %q (reason=weak_score score=%d is_standard=%s category=%s best_sim=%.3f)",
				analyzer.TagQualityVersion, out, score, isStandardLabel(n.standard), n.category, n.bestSim)
			rejected[out] = true
			continue
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
			a.logger.Error(logId, "tag quality cache lookup failed: %v", err)
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
			a.writeTagQualityEvent(ctx, docId, n.emitted, row.Criteria, row.Pass, "", logId)
			if !row.Pass {
				rejected[n.emitted] = true
				a.logger.Info(logId, "tagquality[%s]: reject %q (reason=judge cached=true model=%s criteria=%s)",
					analyzer.TagQualityVersion, n.emitted, row.VerdictModel.String, tagQualityCriteriaLog(row.Criteria))
			}
		}
	}

	if len(uncached) > 0 {
		if !a.runner.HasTagQualityJudge() {
			a.logger.Error(logId, "tagquality[%s]: judge not configured, keeping %d novel tags (born provisional)", analyzer.TagQualityVersion, len(uncached))
		} else {
			candidates := make([]analyzer.TagQualityCandidate, len(uncached))
			for i, n := range uncached {
				candidates[i] = analyzer.TagQualityCandidate{
					Index:            i,
					Term:             n.emitted,
					Category:         n.category,
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
				a.logger.Error(logId, "tagquality[%s]: judge failed, keeping %d novel tags (born provisional): %v", analyzer.TagQualityVersion, len(uncached), err)
			} else {
				model := a.config.ResolvedQualityLlm().Model
				for i, r := range results {
					n := uncached[i]
					pass, criteria, finalModel := a.writeTagQualityEvent(ctx, docId, n.emitted, marshalTagQualityCriteria(r), r.Passed(), model, logId)
					if !pass {
						rejected[n.emitted] = true
						a.logger.Info(logId, "tagquality[%s]: reject %q (reason=judge criteria=%s model=%s)",
							analyzer.TagQualityVersion, n.emitted, tagQualityCriteriaLog(criteria), finalModel)
					}
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
	return out
}

// writeTagQualityEvent records a verdict row for this document. A fresh
// verdict (model != "") writes the judged row; on conflict the row cached by
// another worker wins for this document's decision, followed by a
// cache-applied row with a NULL model, mirroring writeVerdictEvent.
func (a *Adjudicator) writeTagQualityEvent(ctx context.Context, docId int64, name string, criteria json.RawMessage, pass bool, model string, logId *string) (bool, json.RawMessage, string) {
	verdictModel := sql.NullString{String: model, Valid: model != ""}
	n, err := a.queries.InsertTagQualityEvent(ctx, database.InsertTagQualityEventParams{
		DocumentID:    docId,
		TagName:       textnorm.NormalizeTag(name),
		Criteria:      criteria,
		Pass:          pass,
		VerdictModel:  verdictModel,
		PolicyVersion: analyzer.TagQualityVersion,
	})
	if err != nil {
		a.logger.Error(logId, "tag quality event insert failed: %v", err)
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
		a.logger.Error(logId, "tag quality cache re-read after conflict failed: %v", err)
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
		VerdictModel:  sql.NullString{},
		PolicyVersion: analyzer.TagQualityVersion,
	}); err != nil {
		a.logger.Error(logId, "tag quality event insert failed: %v", err)
	}
	if row.VerdictModel.Valid {
		model = row.VerdictModel.String
	}
	return row.Pass, row.Criteria, model
}

func (a *Adjudicator) reciprocalEvidence(ctx context.Context, documentID string, pairs []*bandPair, logId *string) map[string]reciprocalEvidence {
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
		a.logger.Error(logId, "tag adjudication evidence rank failed: %v", err)
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
