package admission

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/policy"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/types"
)

const identityBatchSize = 40

type identityCandidate struct {
	input      string
	name       string
	fold       string
	resolved   string
	aliasOf    string
	gray       bool
	reviewHold bool
}

type identityPair struct {
	query       string
	target      string
	sim         float64
	runnerUpSim float64
	verdict     types.TagVerdict
	direction   string
	model       string
	cached      bool
	decided     bool
}

type identityAnswer struct {
	FoldToBase              bool
	FoldPreservesDenotation bool
	ConventionalForm        bool
	criteria                []byte
	model                   string
}

// IdentityPlanInput is the data PlanIdentity plans against: the document's
// row id and public id, the candidate names, their ranked matches, and the
// store's known names.
type IdentityPlanInput struct {
	DocID      int64
	DocumentID string
	Names      []string
	Ranked     []matcher.RankResult
	DBNames    map[string]struct{}
}

// PlanIdentity plans the document's candidate tags against the store: the
// pinned policy decides, aliases resolve, and the judging body's answers
// settle fold proposals where they exist. It returns the output names for
// the document, the identity plan whose registrations and evidence rows
// ride into the enrichment transaction (so adjudication stays outside it),
// and the policy decisions journaled per pair. A non-nil error is returned
// only when the provider account cannot pay; every other failure degrades
// to leaving the affected names provisional.
func (a *Adjudicator) PlanIdentity(ctx context.Context, in IdentityPlanInput, reqID string) ([]string, tags.Plan, []policy.Decision, error) {
	decisions := policy.Evaluate(in.Names, in.Ranked)
	plan := tags.Plan{}
	aliasMap := a.aliasMapFor(ctx, in.Names, reqID)

	byKey := tags.StoreNamesOf(in.DBNames)

	candidates := make([]*identityCandidate, len(in.Names))
	for i, input := range in.Names {
		cand := &identityCandidate{input: input, name: tags.StoredName(input)}
		candidates[i] = cand
		if canonical, ok := aliasMap[cand.name]; ok {
			cand.name = canonical
			cand.resolved = canonical
			continue
		}
		// any key hit resolves, equality included: a store-exact emission
		// is an application onto its own row, never a fold or shape
		// candidate, and mints no identity evidence
		if existing, ok := byKey[tags.Key(cand.name)]; ok {
			cand.resolved = existing
		}
	}

	pairs := make([]*identityPair, 0)
	pairAt := make(map[string]*identityPair)
	addPair := func(query, target string, sim, runnerUp float64) {
		if query == "" || target == "" || query == target || tags.Key(query) == tags.Key(target) {
			return
		}
		key := query + "\x00" + target
		if p, ok := pairAt[key]; ok {
			if sim > p.sim {
				p.sim = sim
				p.runnerUpSim = runnerUp
			}
			return
		}
		p := &identityPair{query: query, target: target, sim: sim, runnerUpSim: runnerUp}
		pairAt[key] = p
		pairs = append(pairs, p)
	}

	ix := tags.NewNameIndex(nil)
	flagged := make(map[string]bool)
	for i, cand := range candidates {
		if cand.resolved != "" {
			continue
		}
		if fold := tags.ProposedFold(cand.name); fold != "" && fold != cand.name {
			cand.fold = fold
			flagged[cand.name] = true
		} else if fold := tags.ProposedDerivationalFold(cand.name, byKey); fold != "" {
			cand.fold = fold
			flagged[cand.name] = true
		} else if fold := tags.ProposedMorphologicalFold(cand.name, byKey); fold != "" {
			cand.fold = fold
			flagged[cand.name] = true
		}
		for existing := range in.DBNames {
			if !ix.ShapeRelated(cand.name, existing) {
				continue
			}
			flagged[cand.name] = true
			addPair(cand.name, existing, 0, 0)
		}
		if i < len(decisions) && decisions[i].Reason == policy.ReasonBand && decisions[i].Target != "" {
			flagged[cand.name] = true
			addPair(cand.name, decisions[i].Target, decisions[i].Sim, decisions[i].RunnerUpSim)
		}
	}
	for i, a := range candidates {
		if a.resolved != "" {
			continue
		}
		for j := i + 1; j < len(candidates); j++ {
			b := candidates[j]
			if b.resolved != "" || a.name == b.name {
				continue
			}
			if !ix.ShapeRelated(a.name, b.name) {
				continue
			}
			flagged[a.name] = true
			flagged[b.name] = true
			addPair(a.name, b.name, 0, 0)
		}
	}

	answers, err := a.identityAnswers(ctx, flagged, candidates, reqID)
	if err != nil {
		return nil, tags.Plan{}, nil, err
	}
	for _, cand := range candidates {
		if cand.resolved != "" {
			continue
		}
		answer, ok := answers[cand.name]
		if !ok {
			continue
		}
		plan.Evidence = append(plan.Evidence,
			tags.EvidenceRow{Kind: tags.IdentityKindBaseForm, Candidate: cand.name, Criteria: answer.criteria,
				Verdict: boolVerdict(answer.FoldToBase, tags.IdentityVerdictFold, tags.IdentityVerdictStand), Model: answer.model},
			tags.EvidenceRow{Kind: tags.IdentityKindFoldGate, Candidate: cand.name, Criteria: answer.criteria,
				Verdict: boolVerdict(answer.FoldPreservesDenotation, tags.IdentityVerdictConfirmed, tags.IdentityVerdictRejected), Model: answer.model},
			tags.EvidenceRow{Kind: tags.IdentityKindConventionalForm, Candidate: cand.name, Criteria: answer.criteria,
				Verdict: boolVerdict(answer.ConventionalForm, tags.IdentityVerdictStands, tags.IdentityVerdictDefer), Model: answer.model},
		)
		if !answer.ConventionalForm {
			cand.gray = true
			continue
		}
		if cand.fold == "" {
			continue
		}
		if !answer.FoldToBase || !answer.FoldPreservesDenotation {
			// a refused fold beside a live store value is the disjoint-twin
			// class: hold the birth for review instead of letting it
			// promote on mass
			if anchor, ok := byKey[tags.Key(cand.fold)]; ok {
				cand.reviewHold = true
				a.logger.Info(reqID, "identity[%s]: fold rejected beside store anchor %q -> %q, held for review",
					tags.IdentityRuleSetVersion, cand.name, anchor)
			}
			cand.fold = ""
			continue
		}
		folded := tags.HouseForm(cand.fold)
		if folded == cand.name {
			cand.fold = ""
			continue
		}
		if existing, ok := byKey[tags.Key(folded)]; ok {
			a.logger.Debug(reqID, "identity[%s]: fold applied %q -> %q", tags.IdentityRuleSetVersion, cand.name, existing)
			cand.resolved = existing
			continue
		}
		a.logger.Debug(reqID, "identity[%s]: fold applied %q -> %q", tags.IdentityRuleSetVersion, cand.name, folded)
		cand.name = folded
		// the fold is settled; only a genuinely pending fold keeps
		// cand.fold set for the judge-unavailable check below
		cand.fold = ""
	}

	err = a.adjudicateIdentityPairs(ctx, in.DocID, in.DocumentID, pairs, reqID)
	if err != nil {
		return nil, tags.Plan{}, nil, err
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

	for _, p := range pairs {
		if !p.decided {
			continue
		}
		if p.verdict != types.TagVerdicts.Same {
			a.logger.Info(reqID, "identity[%s]: separate %q and %q (verdict=%s cached=%t)",
				tags.IdentityRuleSetVersion, p.query, p.target, p.verdict, p.cached)
			continue
		}
		loser := p.query
		if finalWinner(p.query) == p.query {
			loser = p.target
		}
		winner := finalWinner(loser)
		if _, isStore := in.DBNames[loser]; isStore {
			plan.Aliases = append(plan.Aliases, tags.AliasAction{AliasName: loser, Canonical: winner, RemoveExistingTag: true})
		} else {
			plan.Aliases = append(plan.Aliases, tags.AliasAction{AliasName: loser, Canonical: winner})
		}
		a.logger.Info(reqID, "identity[%s]: alias %q -> %q (verdict=%s direction=%s cached=%t)",
			tags.IdentityRuleSetVersion, loser, winner, p.verdict, p.direction, p.cached)
	}

	pendingPartner := make(map[string]string)
	pendingCandidates := make(map[string]bool)
	for _, p := range pairs {
		if p.decided {
			continue
		}
		if _, ok := pendingPartner[p.query]; !ok {
			pendingPartner[p.query] = p.target
		}
		pendingCandidates[p.query] = true
		if _, isStore := in.DBNames[p.target]; !isStore {
			pendingCandidates[p.target] = true
		}
		a.logger.Info(reqID, "identity[%s]: defer %q vs %q to review (adjudication unavailable)",
			tags.IdentityRuleSetVersion, p.query, p.target)
	}

	// the triggered run or the sweep resolves the fold when the judge returns
	judgeUnavailable := make(map[string]bool)
	for _, cand := range candidates {
		if cand.resolved != "" || cand.fold == "" {
			continue
		}
		if _, ok := answers[cand.name]; !ok {
			judgeUnavailable[cand.name] = true
		}
	}

	seenOutputs := make(map[string]bool)
	var outputs []string
	for i, cand := range candidates {
		reg := tags.Registration{Input: cand.input, Disposition: tags.DispositionRegister}
		switch {
		case cand.resolved != "":
			reg.Disposition = tags.DispositionApply
			reg.Name = cand.name
			reg.Resolved = cand.resolved
		case winnerByLoser[cand.name] != "":
			reg.Disposition = tags.DispositionAlias
			reg.Resolved = finalWinner(cand.name)
		default:
			reg.Name = cand.name
			reg.Fold = cand.fold
			if cand.gray {
				reg.Disposition = tags.DispositionDefer
				reg.Provisional = true
			} else if partner, ok := pendingPartner[cand.name]; ok {
				reg.Provisional = true
				plan.Evidence = append(plan.Evidence, tags.EvidenceRow{
					Kind:      tags.IdentityKindSameIntent,
					Candidate: cand.name,
					Partner:   partner,
				})
			} else if pendingCandidates[cand.name] {
				reg.Provisional = true
			}
			if cand.reviewHold {
				reg.Provisional = true
			}
			if judgeUnavailable[cand.name] {
				reg.Provisional = true
				a.logger.Debug(reqID, "identity[%s]: pending evidence minted for %q (fold verdicts missing, judge unavailable)",
					tags.IdentityRuleSetVersion, cand.name)
				plan.Evidence = append(plan.Evidence,
					tags.EvidenceRow{Kind: tags.IdentityKindBaseForm, Candidate: cand.name},
					tags.EvidenceRow{Kind: tags.IdentityKindFoldGate, Candidate: cand.name},
					tags.EvidenceRow{Kind: tags.IdentityKindConventionalForm, Candidate: cand.name},
				)
			}
		}
		plan.Registrations = append(plan.Registrations, reg)

		output := reg.Name
		if output == "" {
			output = reg.Resolved
		}
		resolved := ""
		if reg.Resolved != "" {
			resolved = fmt.Sprintf(" resolved=%q", reg.Resolved)
		}
		a.logger.Debug(reqID, "identity[%s]: %s %q%s provisional=%t",
			tags.IdentityRuleSetVersion, reg.Disposition, output, resolved, reg.Provisional)
		if output != "" && !seenOutputs[output] {
			seenOutputs[output] = true
			outputs = append(outputs, output)
		}
		if i < len(decisions) && output != "" {
			decisions[i].Output = output
		}
	}

	return outputs, plan, decisions, nil
}

// identityAnswers resolves the flagged candidates' fold answers from the
// cache or batched judge calls. A non-nil error is returned only when the
// provider account cannot pay; every other failure leaves the affected
// candidates provisional.
func (a *Adjudicator) identityAnswers(ctx context.Context, flagged map[string]bool, candidates []*identityCandidate, reqID string) (map[string]identityAnswer, error) {
	out := make(map[string]identityAnswer)
	if len(flagged) == 0 {
		return out, nil
	}

	names := make([]string, 0, len(flagged))
	for _, cand := range candidates {
		if flagged[cand.name] {
			names = append(names, cand.name)
		}
	}

	rows, err := a.queries.LatestTagIdentityEventsForCandidates(ctx, database.LatestTagIdentityEventsForCandidatesParams{
		RuleSetVersion: tags.IdentityRuleSetVersion,
		Candidates:     names,
	})
	if err != nil {
		a.logger.Error(reqID, "identity verdict cache lookup failed: %v", err)
	}
	cached := make(map[string]map[string]database.TagIdentityEvent, len(rows))
	for _, row := range rows {
		if cached[row.Candidate] == nil {
			cached[row.Candidate] = map[string]database.TagIdentityEvent{}
		}
		cached[row.Candidate][row.Kind] = row
	}

	var uncached []*identityCandidate
	for _, cand := range candidates {
		if !flagged[cand.name] {
			continue
		}
		entry, ok := cached[cand.name]
		if !ok || !entry[tags.IdentityKindBaseForm].Verdict.Valid ||
			!entry[tags.IdentityKindFoldGate].Verdict.Valid ||
			!entry[tags.IdentityKindConventionalForm].Verdict.Valid {
			uncached = append(uncached, cand)
			continue
		}
		answer := identityAnswer{
			FoldToBase:              entry[tags.IdentityKindBaseForm].Verdict.String == tags.IdentityVerdictFold,
			FoldPreservesDenotation: entry[tags.IdentityKindFoldGate].Verdict.String == tags.IdentityVerdictConfirmed,
			ConventionalForm:        entry[tags.IdentityKindConventionalForm].Verdict.String == tags.IdentityVerdictStands,
			criteria:                entry[tags.IdentityKindBaseForm].Criteria,
			model:                   entry[tags.IdentityKindBaseForm].Model.String,
		}
		out[cand.name] = answer
		a.logger.Debug(reqID, "identity[%s]: %q fold_to_base=%t fold_preserves_denotation=%t conventional_form=%t cached=%t",
			tags.IdentityRuleSetVersion, cand.name, answer.FoldToBase, answer.FoldPreservesDenotation, answer.ConventionalForm, true)
	}

	if len(uncached) == 0 {
		return out, nil
	}
	if !a.runner.HasTagIdentityJudge() {
		a.logger.Info(reqID, "identity[%s]: judge not configured — %d candidates stay provisional", tags.IdentityRuleSetVersion, len(uncached))
		return out, nil
	}

	for start := 0; start < len(uncached); start += identityBatchSize {
		end := min(start+identityBatchSize, len(uncached))
		batch := uncached[start:end]
		inputs := make([]analyzer.TagIdentityCandidate, len(batch))
		for i, cand := range batch {
			inputs[i] = analyzer.TagIdentityCandidate{Index: i, Term: cand.name, ProposedFold: cand.fold}
		}
		results, err := a.runner.JudgeTagIdentity(ctx, inputs)
		if err != nil && retryableJudgeFailure(err) {
			a.logger.Warn(reqID, "identity[%s]: judge attempt failed, retrying once: %v", tags.IdentityRuleSetVersion, err)
			results, err = a.runner.JudgeTagIdentity(ctx, inputs)
		}
		if err == nil && len(results) != len(batch) {
			err = fmt.Errorf("identity judge returned %d verdicts for %d candidates", len(results), len(batch))
		}
		if err != nil {
			if isCreditError(err) {
				return nil, err
			}
			names := make([]string, len(batch))
			for i, cand := range batch {
				names[i] = cand.name
			}
			a.logger.Warn(reqID, "identity[%s]: judge failed for %q, leaving them provisional: %v", tags.IdentityRuleSetVersion, names, err)
			continue
		}
		model := a.config.ResolvedAdjudicatorLlm().Model
		for i, r := range results {
			answer := identityAnswer{
				FoldToBase:              r.FoldToBase,
				FoldPreservesDenotation: r.FoldPreservesDenotation,
				ConventionalForm:        r.ConventionalForm,
				criteria:                r.CriteriaRecord(batch[i].fold),
				model:                   model,
			}
			out[batch[i].name] = answer
			a.logger.Debug(reqID, "identity[%s]: %q fold_to_base=%t fold_preserves_denotation=%t conventional_form=%t cached=%t",
				tags.IdentityRuleSetVersion, batch[i].name, answer.FoldToBase, answer.FoldPreservesDenotation, answer.ConventionalForm, false)
		}
	}
	return out, nil
}

// adjudicateIdentityPairs settles the shape-related pairs the identity
// judge answers, recording one same-intent event per decided pair. A
// non-nil error is returned only when the provider account cannot pay;
// every other failure leaves the batch's unresolved pairs provisional.
func (a *Adjudicator) adjudicateIdentityPairs(ctx context.Context, docID int64, documentID string, pairs []*identityPair, reqID string) error {
	if len(pairs) == 0 {
		return nil
	}
	if !a.config.Enricher.TagAdjudicator.Enabled || !a.runner.HasTagAdjudicator() {
		return nil
	}

	queries := make([]string, 0, len(pairs))
	for _, p := range pairs {
		queries = append(queries, p.query)
	}
	rows, err := a.queries.LatestTagIdentityEventsForCandidates(ctx, database.LatestTagIdentityEventsForCandidatesParams{
		RuleSetVersion: tags.IdentityRuleSetVersion,
		Candidates:     queries,
	})
	if err != nil {
		a.logger.Error(reqID, "identity pair cache lookup failed: %v", err)
	}
	cached := make(map[string]database.TagIdentityEvent, len(rows))
	for _, row := range rows {
		if row.Kind != tags.IdentityKindSameIntent || !row.Partner.Valid || !row.Verdict.Valid {
			continue
		}
		cached[row.Candidate+"\x00"+row.Partner.String] = row
	}

	var unresolved []*identityPair
	for _, p := range pairs {
		row, ok := cached[p.query+"\x00"+p.target]
		if !ok {
			unresolved = append(unresolved, p)
			continue
		}
		p.verdict, _ = types.ParseTagVerdict(row.Verdict.String)
		p.direction = identityDirection(row.Criteria)
		p.cached = true
		p.decided = true
		p.model = row.Model.String
	}
	if len(unresolved) == 0 {
		return nil
	}

	evidence := a.identityPairEvidence(ctx, documentID, unresolved, reqID)
	for start := 0; start < len(evidence); start += identityBatchSize {
		end := min(start+identityBatchSize, len(evidence))
		batch := unresolved[start:end]
		results, err := a.runner.AdjudicateTags(ctx, evidence[start:end])
		if err != nil && retryableJudgeFailure(err) {
			a.logger.Warn(reqID, "identity[%s]: adjudicator attempt failed, retrying once: %v", tags.IdentityRuleSetVersion, err)
			results, err = a.runner.AdjudicateTags(ctx, evidence[start:end])
		}
		if err == nil && len(results) != len(batch) {
			err = fmt.Errorf("adjudicator returned %d verdicts for %d pairs", len(results), len(batch))
		}
		if err != nil {
			if isCreditError(err) {
				return err
			}
			labels := make([]string, len(batch))
			for i, p := range batch {
				labels[i] = p.query + " -> " + p.target
			}
			a.logger.Warn(reqID, "identity[%s]: adjudication failed for %q, leaving them provisional: %v", tags.IdentityRuleSetVersion, labels, err)
			continue
		}
		model := a.config.ResolvedAdjudicatorLlm().Model
		for i, r := range results {
			if !r.Verdict.Valid() {
				continue
			}
			p := batch[i]
			p.verdict = r.Verdict
			p.direction = r.Direction
			p.model = model
			p.decided = true
		}
	}

	for _, p := range unresolved {
		if !p.decided {
			continue
		}
		a.recordIdentityPair(ctx, docID, p, reqID)
	}
	return nil
}

func identityDirection(criteria []byte) string {
	var parsed struct {
		Direction string `json:"direction"`
	}
	if err := json.Unmarshal(criteria, &parsed); err != nil {
		return ""
	}
	switch parsed.Direction {
	case analyzer.TagDirectionQuery, analyzer.TagDirectionTarget, analyzer.TagDirectionTie:
		return parsed.Direction
	}
	return ""
}

func (a *Adjudicator) identityPairEvidence(ctx context.Context, documentID string, pairs []*identityPair, reqID string) []analyzer.TagPairEvidence {
	evidence := make([]analyzer.TagPairEvidence, len(pairs))
	for i, p := range pairs {
		evidence[i] = analyzer.TagPairEvidence{
			Query:       p.query,
			Target:      p.target,
			Sim:         p.sim,
			RunnerUpSim: p.runnerUpSim,
		}
	}
	if documentID == "" {
		return evidence
	}
	targets := make([]string, 0, len(pairs))
	seen := make(map[string]struct{}, len(pairs))
	for _, p := range pairs {
		if _, ok := seen[p.target]; ok {
			continue
		}
		seen[p.target] = struct{}{}
		targets = append(targets, p.target)
	}
	reciprocal := a.reciprocalEvidence(ctx, documentID, bandPairsFor(targets), reqID)
	for i, p := range pairs {
		if ev, ok := reciprocal[p.target]; ok {
			evidence[i].TargetRunnerUpTag = ev.tag
			evidence[i].TargetRunnerUpSim = ev.sim
		}
	}
	return evidence
}

func bandPairsFor(targets []string) []*bandPair {
	pairs := make([]*bandPair, len(targets))
	for i, target := range targets {
		pairs[i] = &bandPair{target: target}
	}
	return pairs
}

func (a *Adjudicator) recordIdentityPair(ctx context.Context, docID int64, p *identityPair, reqID string) {
	criteria, err := json.Marshal(map[string]string{"direction": p.direction})
	if err != nil {
		criteria = []byte("{}")
	}
	var model sql.NullString
	if p.model != "" {
		model = sql.NullString{String: p.model, Valid: true}
	}
	n, err := a.queries.InsertTagIdentityEvent(ctx, database.InsertTagIdentityEventParams{
		Kind:           tags.IdentityKindSameIntent,
		Candidate:      textnorm.NormalizeTag(p.query),
		Partner:        sql.NullString{String: textnorm.NormalizeTag(p.target), Valid: true},
		Criteria:       criteria,
		Verdict:        sql.NullString{String: string(p.verdict), Valid: true},
		Model:          model,
		RuleSetVersion: tags.IdentityRuleSetVersion,
	})
	if err != nil {
		a.logger.Error(reqID, "identity event insert failed: %v", err)
		return
	}
	if n == 0 && !p.cached {
		rows, err := a.queries.LatestTagIdentityEventsForCandidates(ctx, database.LatestTagIdentityEventsForCandidatesParams{
			RuleSetVersion: tags.IdentityRuleSetVersion,
			Candidates:     []string{textnorm.NormalizeTag(p.query)},
		})
		if err != nil {
			a.logger.Error(reqID, "identity cache re-read after conflict failed: %v", err)
			return
		}
		for _, row := range rows {
			if row.Kind != tags.IdentityKindSameIntent || !row.Partner.Valid ||
				row.Partner.String != textnorm.NormalizeTag(p.target) || !row.Verdict.Valid {
				continue
			}
			p.verdict, _ = types.ParseTagVerdict(row.Verdict.String)
			p.direction = identityDirection(row.Criteria)
			p.cached = true
			if row.Model.Valid {
				p.model = row.Model.String
			}
			break
		}
	}

	if docID <= 0 {
		return
	}
	a.writeVerdictEvent(ctx, docID, policy.Decision{
		Query:  p.query,
		Target: p.target,
		Output: p.query,
		Sim:    p.sim,
	}, &bandPair{query: p.query, target: p.target, sim: p.sim, verdict: p.verdict, model: p.model, cached: p.cached}, reqID)
}

func boolVerdict(value bool, whenTrue, whenFalse string) string {
	if value {
		return whenTrue
	}
	return whenFalse
}

// OutputByInput maps each registration's normalized raw input to the
// plan's output for it — the registered name, or the resolved canonical
// when the plan applies an existing value. Callers translating names
// they captured before the plan (tag details, composition parts) land on
// the names the plan will write.
func OutputByInput(plan tags.Plan) map[string]string {
	outputByInput := make(map[string]string, len(plan.Registrations))
	for _, reg := range plan.Registrations {
		input := textnorm.NormalizeTag(reg.Input)
		output := reg.Name
		if output == "" {
			output = reg.Resolved
		}
		if input != "" && output != "" {
			outputByInput[input] = output
		}
	}
	return outputByInput
}

// RenameTagDetails rewrites the analysis's tag details after identity
// resolution so folded and applied outputs keep their category: each
// detail's term is replaced by its plan output.
func RenameTagDetails(details []analyzer.TagDetail, plan tags.Plan) []analyzer.TagDetail {
	outputByInput := OutputByInput(plan)
	for i := range details {
		if output, ok := outputByInput[textnorm.NormalizeTag(details[i].Term)]; ok {
			details[i].Term = output
		}
	}
	return details
}

// CanonicalSuggestions resolves a list of tag suggestions to their
// canonical (alias-collapsed) forms, deduplicated and in first-appearance
// order.
func (a *Adjudicator) CanonicalSuggestions(ctx context.Context, names []string, reqID string) []string {
	if len(names) == 0 {
		return names
	}
	aliasMap := a.aliasMapFor(ctx, names, reqID)
	if len(aliasMap) == 0 {
		return names
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		canonical := name
		if resolved, ok := aliasMap[textnorm.NormalizeTag(name)]; ok {
			canonical = resolved
		}
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		out = append(out, canonical)
	}
	return out
}

func (a *Adjudicator) aliasMapFor(ctx context.Context, names []string, reqID string) map[string]string {
	out, err := tags.ResolveAliases(ctx, a.queries, names)
	if err != nil {
		a.logger.Error(reqID, "identity alias lookup failed: %v", err)
		return map[string]string{}
	}
	return out
}
