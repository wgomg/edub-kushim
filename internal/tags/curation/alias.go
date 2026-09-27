package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/textnorm"
)

// ApplyAliasPlan applies the planned alias actions in one transaction:
// blocked names are skipped, the loser's own aliases are retargeted (no
// alias chains), its documents are reassigned and its tag row deleted
// when RemoveExistingTag is set, and the removed names leave the matcher
// store after commit.
func (s *Tag) ApplyAliasPlan(ctx context.Context, actions []tags.AliasAction) error {
	if len(actions) == 0 {
		return nil
	}
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return errs.FromDB(err, "begin alias plan")
	}
	defer tx.Rollback()
	txQ := s.client.Queries.WithTx(tx)

	existingTags, err := txQ.ListAllTags(ctx)
	if err != nil {
		return errs.FromDB(err, "load existing tags")
	}
	existing := make(map[string]database.Tag, len(existingTags))
	for _, t := range existingTags {
		existing[t.Name] = t
	}

	deleted, err := txQ.ListAllDeletedTags(ctx)
	if err != nil {
		return errs.FromDB(err, "load deleted tags")
	}
	blocked := make(map[string]struct{}, len(deleted))
	for _, row := range deleted {
		blocked[row.Name] = struct{}{}
	}

	var removed []string
	for _, action := range actions {
		if _, isBlocked := blocked[textnorm.NormalizeTag(action.AliasName)]; isBlocked {
			continue
		}
		if err := s.applyAliasAction(ctx, txQ, existing, action); err != nil {
			return err
		}
		if action.RemoveExistingTag {
			removed = append(removed, action.AliasName)
		}
	}

	if err := tx.Commit(); err != nil {
		return errs.FromDB(err, "commit alias plan")
	}
	if len(removed) > 0 {
		if err := s.embedder.RemoveFromStore(ctx, removed); err != nil {
			s.logger.Error("", "tag service: remove aliased names from store: %v", err)
		}
	}
	return nil
}

func (s *Tag) applyAliasAction(ctx context.Context, q *database.Queries, existing map[string]database.Tag, action tags.AliasAction) error {
	aliasName := textnorm.NormalizeTag(action.AliasName)
	canonicalName := textnorm.NormalizeTag(action.Canonical)
	if aliasName == "" || canonicalName == "" || aliasName == canonicalName {
		return nil
	}

	canonical, ok := existing[canonicalName]
	if !ok {
		tag, err := q.GetTagByName(ctx, canonicalName)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return errs.FromDB(err, "get alias target "+canonicalName)
		}
		canonical = tag
	}

	if _, err := q.GetTagAliasByName(ctx, aliasName); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return errs.FromDB(err, "get tag alias "+aliasName)
	}

	if action.RemoveExistingTag {
		loser, err := q.GetTagForUpdateByName(ctx, aliasName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return errs.FromDB(err, "get alias loser "+aliasName)
		}
		if loser.ID == canonical.ID {
			return nil
		}

		reassigned, err := q.ReassignDocumentTags(ctx, database.ReassignDocumentTagsParams{
			SourceID: loser.ID,
			TargetID: canonical.ID,
		})
		if err != nil {
			return errs.FromDB(err, "reassign document tags for alias "+aliasName)
		}
		if reassigned > 0 {
			if err := q.ClearOrphanTagMarksByTagIDs(ctx, []int64{canonical.ID}); err != nil {
				return errs.FromDB(err, "clear orphan tag marks for alias "+aliasName)
			}
		}
		if _, err := q.DeleteDocumentTagsForTag(ctx, loser.ID); err != nil {
			return errs.FromDB(err, "delete leftover document tags for alias "+aliasName)
		}
		// no alias chains: the loser's own aliases re-target the new canonical
		if _, err := q.RetargetTagAliases(ctx, database.RetargetTagAliasesParams{
			NewTagID: canonical.ID,
			OldTagID: loser.ID,
		}); err != nil {
			return errs.FromDB(err, "retarget aliases for "+aliasName)
		}
		if err := q.DeleteTag(ctx, loser.ID); err != nil {
			return errs.FromDB(err, "delete alias loser "+aliasName)
		}
	}

	if _, err := q.InsertTagAlias(ctx, database.InsertTagAliasParams{
		AliasName:      aliasName,
		CanonicalTagID: canonical.ID,
		RuleSetVersion: tags.IdentityRuleSetVersion,
	}); err != nil {
		return errs.FromDB(err, "insert tag alias "+aliasName)
	}
	return nil
}

func (s *Tag) recordIdentityEvidence(ctx context.Context, q *database.Queries, rows []tags.EvidenceRow) error {
	for _, row := range rows {
		candidate := textnorm.NormalizeTag(row.Candidate)
		if candidate == "" {
			continue
		}
		criteria := row.Criteria
		if len(criteria) == 0 {
			criteria = json.RawMessage("{}")
		}
		var partner sql.NullString
		if p := textnorm.NormalizeTag(row.Partner); p != "" {
			partner = sql.NullString{String: p, Valid: true}
		}
		var verdict, model sql.NullString
		if row.Verdict != "" {
			verdict = sql.NullString{String: row.Verdict, Valid: true}
		}
		if row.Model != "" {
			model = sql.NullString{String: row.Model, Valid: true}
		}
		if _, err := q.InsertTagIdentityEvent(ctx, database.InsertTagIdentityEventParams{
			Kind:           row.Kind,
			Candidate:      candidate,
			Partner:        partner,
			Criteria:       criteria,
			Verdict:        verdict,
			Model:          model,
			RuleSetVersion: tags.IdentityRuleSetVersion,
		}); err != nil {
			return errs.FromDB(err, "insert tag identity event "+candidate)
		}
	}
	return nil
}
