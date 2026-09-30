package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Create plans and applies registrations for the names in one
// transaction: conflicts resolve to the existing tag, aliases apply to
// their canonical, and new tags are born (provisional where the identity
// machinery flags them) and embedded into the matcher store.
func (s *Tag) Create(ctx context.Context, names []string) ([]types.CreateResult[database.Tag], error) {
	return s.create(ctx, names, false)
}

// CreateAndUnblock is Create plus clearing the deleted-tag blocklist
// entries of everything it created, resolved or aliased, so the
// cleared-blocklist path keeps working. The blocklist re-check inside the
// plan is exempted for the same reason.
func (s *Tag) CreateAndUnblock(ctx context.Context, names []string) ([]types.CreateResult[database.Tag], error) {
	results, err := s.create(ctx, names, true)
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		switch r.Status {
		case types.Created, types.Conflict, types.Apply, types.Alias:
		default:
			continue
		}
		if err := s.client.Queries.DeleteDeletedTag(ctx, r.Entity.Name); err != nil {
			return nil, errs.FromDB(err, "clear deleted tag "+r.Entity.Name)
		}
	}
	return results, nil
}

func (s *Tag) create(ctx context.Context, names []string, ignoreBlocklist bool) ([]types.CreateResult[database.Tag], error) {
	plan, err := s.buildPlan(ctx, s.client.Queries, names, ignoreBlocklist)
	if err != nil {
		return nil, err
	}

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return nil, errs.FromDB(err, "begin tag create")
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)
	results, newNames, err := s.CreatePlannedTx(ctx, txQ, plan, false)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, errs.FromDB(err, "commit tag create")
	}

	if len(newNames) > 0 {
		s.encodeAndAddBatch(ctx, newNames)
	}
	return results, nil
}

// PlanRegistration plans the registrations for the names without running
// any judging call in the request: identity evidence is recorded and
// judgement happens out of band.
func (s *Tag) PlanRegistration(ctx context.Context, names []string) (tags.Plan, error) {
	return s.buildPlan(ctx, s.client.Queries, names, false)
}

func (s *Tag) buildPlan(ctx context.Context, q *database.Queries, names []string, ignoreBlocklist bool) (tags.Plan, error) {
	view, err := s.storeView(ctx, q, names, ignoreBlocklist)
	if err != nil {
		return tags.Plan{}, err
	}
	plan := tags.PlanDeterministic(names, view)
	plan.SkipBlocklistCheck = ignoreBlocklist
	return plan, nil
}

func (s *Tag) storeView(ctx context.Context, q *database.Queries, names []string, ignoreBlocklist bool) (tags.StoreView, error) {
	allTags, err := q.ListAllTags(ctx)
	if err != nil {
		return tags.StoreView{}, errs.FromDB(err, "load existing tags")
	}
	view := tags.StoreView{
		Names:   make([]string, 0, len(allTags)),
		Aliases: map[string]string{},
		Blocked: map[string]struct{}{},
	}
	for _, t := range allTags {
		view.Names = append(view.Names, t.Name)
	}

	aliases, err := tags.ResolveAliases(ctx, q, names)
	if err != nil {
		return tags.StoreView{}, errs.FromDB(err, "load tag aliases")
	}
	view.Aliases = aliases

	if !ignoreBlocklist {
		deleted, err := q.ListAllDeletedTags(ctx)
		if err != nil {
			return tags.StoreView{}, errs.FromDB(err, "load deleted tags")
		}
		for _, row := range deleted {
			view.Blocked[row.Name] = struct{}{}
		}
	}
	return view, nil
}

// CreatePlannedTx applies a planned registration set inside the caller's
// transaction: it resolves each disposition against the live store, writes
// the plan's aliases, records the plan's identity evidence, and promotes
// non-provisional new tags unless quarantined. It returns the per-
// registration results and the names it created.
func (s *Tag) CreatePlannedTx(ctx context.Context, q *database.Queries, plan tags.Plan, quarantined bool) ([]types.CreateResult[database.Tag], []string, error) {
	results := make([]types.CreateResult[database.Tag], len(plan.Registrations))
	var newNames []string
	var promoteIDs []int64

	existingTags, err := q.ListAllTags(ctx)
	if err != nil {
		return nil, nil, errs.FromDB(err, "load existing tags")
	}
	existingMap := make(map[string]database.Tag, len(existingTags))
	byKey := make(map[string]database.Tag, len(existingTags))
	for _, t := range existingTags {
		existingMap[t.Name] = t
		if _, ok := byKey[tags.Key(t.Name)]; !ok {
			byKey[tags.Key(t.Name)] = t
		}
	}

	aliasNames := make([]string, 0, len(plan.Registrations)+len(plan.Aliases))
	for _, reg := range plan.Registrations {
		if name := textnorm.NormalizeTag(reg.Name); name != "" {
			aliasNames = append(aliasNames, name)
		}
	}
	for _, action := range plan.Aliases {
		if name := textnorm.NormalizeTag(action.AliasName); name != "" {
			aliasNames = append(aliasNames, name)
		}
	}
	aliases, err := tags.ResolveAliases(ctx, q, aliasNames)
	if err != nil {
		return nil, nil, errs.FromDB(err, "load tag aliases")
	}

	blocked := map[string]struct{}{}
	if !plan.SkipBlocklistCheck {
		deleted, err := q.ListAllDeletedTags(ctx)
		if err != nil {
			return nil, nil, errs.FromDB(err, "load deleted tags")
		}
		for _, row := range deleted {
			blocked[row.Name] = struct{}{}
		}
	}

	for i, reg := range plan.Registrations {
		name := textnorm.NormalizeTag(reg.Name)

		if reg.Disposition == tags.DispositionInvalid {
			results[i] = types.CreateResult[database.Tag]{Status: types.Invalid}
			continue
		}
		if reg.Disposition == tags.DispositionAlias {
			canonical, ok := existingMap[textnorm.NormalizeTag(reg.Resolved)]
			if !ok {
				results[i] = types.CreateResult[database.Tag]{Status: types.Invalid}
				continue
			}
			results[i] = types.CreateResult[database.Tag]{Entity: canonical, Status: types.Alias}
			continue
		}
		if name == "" {
			results[i] = types.CreateResult[database.Tag]{Status: types.Invalid}
			continue
		}
		if _, isBlocked := blocked[name]; isBlocked {
			results[i] = types.CreateResult[database.Tag]{Status: types.Invalid}
			continue
		}

		if canonicalName, ok := aliases[name]; ok {
			canonical, ok := existingMap[canonicalName]
			if !ok {
				results[i] = types.CreateResult[database.Tag]{Status: types.Invalid}
				continue
			}
			results[i] = types.CreateResult[database.Tag]{Entity: canonical, Status: types.Apply}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = types.CreateResult[database.Tag]{Entity: existing, Status: types.Conflict}
			continue
		}

		if reg.Resolved != "" {
			if canonical, ok := existingMap[textnorm.NormalizeTag(reg.Resolved)]; ok {
				status := types.Apply
				if reg.Disposition == tags.DispositionConflict {
					status = types.Conflict
				}
				results[i] = types.CreateResult[database.Tag]{Entity: canonical, Status: status}
				continue
			}
		}

		if existing, ok := byKey[tags.Key(name)]; ok && existing.Name != name {
			results[i] = types.CreateResult[database.Tag]{Entity: existing, Status: types.Apply}
			continue
		}

		id, err := q.CreateTag(ctx, textnorm.NormalizeTag(name))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				existing, err := q.GetTagByName(ctx, name)
				if err != nil {
					return nil, nil, errs.FromDB(err, "get tag by name after conflict "+name)
				}
				results[i] = types.CreateResult[database.Tag]{Entity: existing, Status: types.Conflict}
				continue
			}
			return nil, nil, errs.FromDB(err, "create tag "+name)
		}

		created := database.Tag{ID: id, Name: name}
		existingMap[name] = created
		if _, ok := byKey[tags.Key(name)]; !ok {
			byKey[tags.Key(name)] = created
		}
		results[i] = types.CreateResult[database.Tag]{Entity: created, Status: types.Created}
		newNames = append(newNames, name)
		if !reg.Provisional {
			promoteIDs = append(promoteIDs, id)
		}
	}

	for _, action := range plan.Aliases {
		if _, isBlocked := blocked[textnorm.NormalizeTag(action.AliasName)]; isBlocked {
			continue
		}
		if err := s.applyAliasAction(ctx, q, existingMap, action); err != nil {
			return nil, nil, err
		}
	}

	if err := s.recordIdentityEvidence(ctx, q, plan.Evidence); err != nil {
		return nil, nil, err
	}

	if err := s.recordCompositionEvidence(ctx, q, plan.Composition); err != nil {
		return nil, nil, err
	}

	if !quarantined && len(promoteIDs) > 0 {
		if _, err := q.PromoteTags(ctx, promoteIDs); err != nil {
			return nil, nil, errs.FromDB(err, "promote new tags")
		}
	}

	return results, newNames, nil
}

// recordCompositionEvidence writes the plan's decomposition verdicts as
// evidence rows, normalizing candidate and part names at this write
// boundary. Empty parts encode as an empty JSON array, never null, and a
// rejected name's row stays: it documents the gate's verdict.
func (s *Tag) recordCompositionEvidence(ctx context.Context, q *database.Queries, rows []tags.CompositionRow) error {
	for _, row := range rows {
		candidate := textnorm.NormalizeTag(row.Candidate)
		if candidate == "" {
			continue
		}
		parts := make([]string, 0, len(row.Parts))
		for _, part := range row.Parts {
			if p := textnorm.NormalizeTag(part); p != "" {
				parts = append(parts, p)
			}
		}
		encoded, err := json.Marshal(parts)
		if err != nil {
			return fmt.Errorf("encode tag composition parts %s: %w", candidate, err)
		}
		var model sql.NullString
		if row.Model != "" {
			model = sql.NullString{String: row.Model, Valid: true}
		}
		if err := q.InsertTagCompositionEvent(ctx, database.InsertTagCompositionEventParams{
			DocumentID:      row.DocumentID,
			Candidate:       candidate,
			Parts:           encoded,
			Disposition:     row.Disposition,
			Reason:          row.Reason,
			CriteriaVersion: row.CriteriaVersion,
			Model:           model,
		}); err != nil {
			return errs.FromDB(err, "insert tag composition event "+candidate)
		}
	}
	return nil
}
