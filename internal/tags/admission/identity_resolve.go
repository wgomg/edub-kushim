package admission

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/types"
)

// IdentityResolver settles the identity evidence the enricher and the
// sweeps leave pending: it re-runs the judging body on pending pairs,
// applies folds and aliases through the curation surface, and reports what
// it did. It is what the tagidentity verb, the sweep and the task handler
// share.
type IdentityResolver struct {
	getConfig func() *config.Config
	logger    *log.Logger
	client    *database.Client
	runner    *tools.Runner
	tag       *curation.Tag
}

// NewIdentityResolver wires the resolver to the dynamic config, the
// database, the tools runner and the curation tag service (its alias
// applications go through it).
func NewIdentityResolver(getConfig func() *config.Config, logger *log.Logger, client *database.Client, tag *curation.Tag, runner *tools.Runner) *IdentityResolver {
	return &IdentityResolver{
		getConfig: getConfig,
		logger:    logger,
		client:    client,
		runner:    runner,
		tag:       tag,
	}
}

// IdentityReport counts what one resolution pass did: pending events it
// saw, folds applied, aliases created, deferrals, and failures.
type IdentityReport struct {
	Pending  int `json:"pending"`
	Resolved int `json:"resolved"`
	Folded   int `json:"folded"`
	Aliased  int `json:"aliased"`
	Deferred int `json:"deferred"`
	Failed   int `json:"failed"`
}

// IdentityReportPrefix marks the serialized IdentityReport inside the
// tagidentity command's output; the task handler extracts the JSON that
// follows it.
const IdentityReportPrefix = "tagidentity-report: "

type pendingCandidate struct {
	name string
	rows map[string]database.TagIdentityEvent
}

// ResolvePending settles up to budget pending identity events: it folds
// base-form proposals the judge approves, aliases shape-related pairs, and
// defers everything the judging body cannot answer yet. The per-pair
// verdict cache converges the triggered run and the scheduled sweep on
// whichever runs first. Disabling the adjudicator leaves the events
// pending and only counts them.
func (r *IdentityResolver) ResolvePending(ctx context.Context, budget int) (*IdentityReport, error) {
	report := &IdentityReport{}
	if budget <= 0 {
		return report, nil
	}
	cfg := r.getConfig()
	if !cfg.Enricher.TagAdjudicator.Enabled || !r.runner.HasTagAdjudicator() {
		pending, err := r.client.Queries.CountPendingTagIdentityEvents(ctx)
		if err != nil {
			return nil, fmt.Errorf("count pending identity events: %w", err)
		}
		report.Pending = int(pending)
		if pending > 0 {
			r.logger.Info("", "tag identity: adjudicator disabled — %d pending events left", pending)
		}
		return report, nil
	}

	events, err := r.client.Queries.ListPendingTagIdentityEvents(ctx, database.ListPendingTagIdentityEventsParams{
		PageOffset: 0,
		PageLimit:  int32(budget),
	})
	if err != nil {
		return nil, fmt.Errorf("list pending identity events: %w", err)
	}
	report.Pending = len(events)
	if len(events) == 0 {
		return report, nil
	}

	candidates := make(map[string]*pendingCandidate)
	var pairs []*identityPair
	var pairRows []database.TagIdentityEvent
	for _, event := range events {
		if event.Kind == tags.IdentityKindSameIntent {
			if !event.Partner.Valid {
				continue
			}
			pairs = append(pairs, &identityPair{query: event.Candidate, target: event.Partner.String})
			pairRows = append(pairRows, event)
			continue
		}
		entry := candidates[event.Candidate]
		if entry == nil {
			entry = &pendingCandidate{name: event.Candidate, rows: map[string]database.TagIdentityEvent{}}
			candidates[event.Candidate] = entry
		}
		entry.rows[event.Kind] = event
	}

	r.resolveCandidates(ctx, cfg, candidates, report)
	r.resolvePairs(ctx, cfg, pairs, pairRows, report)
	return report, nil
}

func (r *IdentityResolver) resolveCandidates(ctx context.Context, cfg *config.Config, candidates map[string]*pendingCandidate, report *IdentityReport) {
	if len(candidates) == 0 {
		return
	}
	if !r.runner.HasTagIdentityJudge() {
		report.Deferred += len(candidates)
		r.logger.Info("", "tag identity: identity judge not configured — %d candidates stay pending", len(candidates))
		return
	}

	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)

	model := cfg.ResolvedAdjudicatorLlm().Model
	for start := 0; start < len(names); start += identityBatchSize {
		end := min(start+identityBatchSize, len(names))
		batch := names[start:end]
		inputs := make([]analyzer.TagIdentityCandidate, len(batch))
		for i, name := range batch {
			inputs[i] = analyzer.TagIdentityCandidate{Index: i, Term: name, ProposedFold: tags.ProposedFold(name)}
		}
		results, err := r.runner.JudgeTagIdentity(ctx, inputs)
		if err == nil && len(results) != len(batch) {
			err = fmt.Errorf("identity judge returned %d verdicts for %d candidates", len(results), len(batch))
		}
		if err != nil {
			r.logger.Error("", "tag identity: judge failed for %d candidates: %v", len(batch), err)
			report.Failed += len(batch)
			continue
		}
		for i, result := range results {
			entry := candidates[batch[i]]
			criteria := result.CriteriaRecord(tags.ProposedFold(entry.name))
			r.resolveRow(ctx, entry, tags.IdentityKindBaseForm,
				boolVerdict(result.FoldToBase, tags.IdentityVerdictFold, tags.IdentityVerdictStand), criteria, model, report)
			r.resolveRow(ctx, entry, tags.IdentityKindFoldGate,
				boolVerdict(result.FoldPreservesDenotation, tags.IdentityVerdictConfirmed, tags.IdentityVerdictRejected), criteria, model, report)
			r.resolveRow(ctx, entry, tags.IdentityKindConventionalForm,
				boolVerdict(result.ConventionalForm, tags.IdentityVerdictStands, tags.IdentityVerdictDefer), criteria, model, report)

			if !result.ConventionalForm {
				report.Deferred++
				continue
			}
			if !result.FoldToBase || !result.FoldPreservesDenotation {
				continue
			}
			folded := tags.HouseForm(tags.ProposedFold(entry.name))
			if folded == "" || folded == entry.name {
				continue
			}
			if applied, err := r.applyFold(ctx, entry.name, folded); err != nil {
				r.logger.Error("", "tag identity: fold %q -> %q failed: %v", entry.name, folded, err)
				report.Failed++
				continue
			} else if applied {
				report.Folded++
				r.logger.Info("", "tag identity: fold %q -> %q", entry.name, folded)
			}
		}
	}
}

func (r *IdentityResolver) resolveRow(ctx context.Context, entry *pendingCandidate, kind, verdict string, criteria []byte, model string, report *IdentityReport) {
	row, ok := entry.rows[kind]
	if !ok {
		return
	}
	n, err := r.client.Queries.ResolveTagIdentityEvent(ctx, database.ResolveTagIdentityEventParams{
		ID:       row.ID,
		Verdict:  sql.NullString{String: verdict, Valid: true},
		Model:    sql.NullString{String: model, Valid: model != ""},
		Criteria: criteria,
	})
	if err != nil {
		r.logger.Error("", "tag identity: resolve %s %q failed: %v", kind, entry.name, err)
		report.Failed++
		return
	}
	report.Resolved += int(n)
}

func (r *IdentityResolver) resolvePairs(ctx context.Context, cfg *config.Config, pairs []*identityPair, rows []database.TagIdentityEvent, report *IdentityReport) {
	if len(pairs) == 0 {
		return
	}
	evidence := make([]analyzer.TagPairEvidence, len(pairs))
	for i, p := range pairs {
		evidence[i] = analyzer.TagPairEvidence{Query: p.query, Target: p.target}
	}

	model := cfg.ResolvedAdjudicatorLlm().Model
	for start := 0; start < len(pairs); start += identityBatchSize {
		end := min(start+identityBatchSize, len(pairs))
		batch := pairs[start:end]
		results, err := r.runner.AdjudicateTags(ctx, evidence[start:end])
		if err == nil && len(results) != len(batch) {
			err = fmt.Errorf("adjudicator returned %d verdicts for %d pairs", len(results), len(batch))
		}
		if err != nil {
			r.logger.Error("", "tag identity: adjudication failed for %d pairs: %v", len(batch), err)
			report.Failed += len(batch)
			continue
		}
		for i, result := range results {
			if !result.Verdict.Valid() {
				continue
			}
			p := batch[i]
			p.verdict = result.Verdict
			p.direction = result.Direction
			p.model = model
			p.decided = true
			criteria, err := json.Marshal(map[string]string{"direction": p.direction})
			if err != nil {
				criteria = []byte("{}")
			}
			if _, err := r.client.Queries.ResolveTagIdentityEvent(ctx, database.ResolveTagIdentityEventParams{
				ID:       rows[start+i].ID,
				Verdict:  sql.NullString{String: string(p.verdict), Valid: true},
				Model:    sql.NullString{String: model, Valid: model != ""},
				Criteria: criteria,
			}); err != nil {
				r.logger.Error("", "tag identity: resolve pair %q -> %q failed: %v", p.query, p.target, err)
				report.Failed++
				continue
			}
			report.Resolved++
		}
	}

	winnerByLoser := make(map[string]string)
	for _, p := range pairs {
		if !p.decided || p.verdict != types.TagVerdicts.Same {
			continue
		}
		winner, loser := p.query, p.target
		switch p.direction {
		case analyzer.TagDirectionTarget:
			winner, loser = p.target, p.query
		case analyzer.TagDirectionQuery:
			winner, loser = p.query, p.target
		default:
			if tags.TieBreak(p.query, p.target) == p.query {
				winner, loser = p.query, p.target
			} else {
				winner, loser = p.target, p.query
			}
		}
		winnerByLoser[loser] = winner
	}
	finalWinner := func(name string) string {
		seen := make(map[string]bool)
		for {
			winner, ok := winnerByLoser[name]
			if !ok || seen[name] {
				return name
			}
			seen[name] = true
			name = winner
		}
	}

	var actions []tags.AliasAction
	for loser := range winnerByLoser {
		winner := finalWinner(loser)
		if winner == loser {
			continue
		}
		action := tags.AliasAction{AliasName: loser, Canonical: winner}
		if _, err := r.client.Queries.GetTagByName(ctx, loser); err == nil {
			action.RemoveExistingTag = true
		} else if !errors.Is(err, sql.ErrNoRows) {
			r.logger.Error("", "tag identity: alias loser lookup %q failed: %v", loser, err)
			report.Failed++
			continue
		}
		actions = append(actions, action)
	}
	if len(actions) == 0 {
		return
	}
	if err := r.tag.ApplyAliasPlan(ctx, actions); err != nil {
		r.logger.Error("", "tag identity: apply alias plan failed: %v", err)
		report.Failed += len(actions)
		return
	}
	report.Aliased += len(actions)
	for _, action := range actions {
		r.logger.Info("", "tag identity: alias %q -> %q", action.AliasName, action.Canonical)
	}
}

func (r *IdentityResolver) applyFold(ctx context.Context, from, to string) (bool, error) {
	// the block can land while the fold waits for resolution
	if _, err := r.client.Queries.GetDeletedTag(ctx, textnorm.NormalizeTag(to)); err == nil {
		r.logger.Info("", "tag identity: fold %q -> %q skipped: target is deleted", from, to)
		return false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	if _, err := r.client.Queries.GetTagByName(ctx, to); err == nil {
		if err := r.tag.ApplyAliasPlan(ctx, []tags.AliasAction{{AliasName: from, Canonical: to, RemoveExistingTag: true}}); err != nil {
			return false, err
		}
		return true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	tag, err := r.client.Queries.GetTagByName(ctx, from)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	results, err := r.tag.Update(ctx, []curation.TagUpdatePair{{ID: tag.ID, Name: to}})
	if err != nil {
		return false, err
	}
	switch results[0].Status {
	case service.Updated, service.Noop:
		return true, nil
	case service.UpdateConflict:
		if err := r.tag.ApplyAliasPlan(ctx, []tags.AliasAction{{AliasName: from, Canonical: to, RemoveExistingTag: true}}); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, fmt.Errorf("rename %q to %q: %v", from, to, results[0].Status)
	}
}
