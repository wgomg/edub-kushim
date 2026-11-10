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
	"github.com/wgomg/edub-kushim/internal/types"
)

// Fold collapses one live tag name onto another through ApplyAliasPlan, the operator's manual fold:
// the canonical tag keeps its row and standing, the folded name's documents move onto it, its aliases
// re-target, its row is deleted, and the alias table records the collapse. The folded name must be a
// live tag (FoldNotFound otherwise); seeded values are operator-owned inventory the seeder re-creates,
// so folding one away reports FoldSeeded. A canonical that is missing or an alias that already exists
// leaves the store untouched and reports FoldNotApplied. It returns the number of documents re-pointed.
func (s *Tag) Fold(ctx context.Context, alias, canonical string) (types.FoldStatus, int64, error) {
	aliasName := textnorm.NormalizeTag(alias)
	canonicalName := textnorm.NormalizeTag(canonical)
	if aliasName == "" || canonicalName == "" || aliasName == canonicalName {
		return types.FoldNotApplied, 0, nil
	}

	loser, err := s.GetByName(ctx, aliasName)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return types.FoldNotFound, 0, nil
		}
		return types.FoldNotApplied, 0, err
	}
	if loser.Seeded {
		return types.FoldSeeded, 0, nil
	}

	outcomes, err := s.ApplyAliasPlan(ctx, []tags.AliasAction{{
		AliasName:         aliasName,
		Canonical:         canonicalName,
		RemoveExistingTag: true,
	}})
	if err != nil {
		return types.FoldNotApplied, 0, err
	}
	if len(outcomes) == 0 || !outcomes[0].Applied {
		return types.FoldNotApplied, 0, nil
	}

	s.logger.Info("", "tag fold: %q -> %q (reason=human-fold, reassigned=%d)",
		aliasName, canonicalName, outcomes[0].Reassigned)
	return types.FoldApplied, outcomes[0].Reassigned, nil
}

// ApplyAliasPlan applies the planned alias actions in one transaction and returns, per action in order,
// what happened: whether it applied and how many documents it re-pointed; blocked names are skipped,
// the loser's own aliases re-target (no alias chains), its documents are reassigned and its tag row
// deleted when RemoveExistingTag is set, and the removed names leave the matcher store after the commit.
// A skipped action reports Applied false with zero documents.
func (s *Tag) ApplyAliasPlan(ctx context.Context, actions []tags.AliasAction) ([]tags.AliasOutcome, error) {
	outcomes := make([]tags.AliasOutcome, len(actions))
	if len(actions) == 0 {
		return outcomes, nil
	}
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return nil, errs.FromDB(err, "begin alias plan")
	}
	defer tx.Rollback()
	txQ := s.client.Queries.WithTx(tx)

	existingTags, err := txQ.ListAllTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load existing tags")
	}
	existing := make(map[string]database.Tag, len(existingTags))
	for _, t := range existingTags {
		existing[t.Name] = t
	}

	deleted, err := txQ.ListAllDeletedTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load deleted tags")
	}
	blocked := make(map[string]struct{}, len(deleted))
	for _, row := range deleted {
		blocked[row.Name] = struct{}{}
	}

	var removed []string
	for i, action := range actions {
		if _, isBlocked := blocked[textnorm.NormalizeTag(action.AliasName)]; isBlocked {
			continue
		}
		outcome, err := s.applyAliasAction(ctx, txQ, existing, action)
		if err != nil {
			return nil, err
		}
		outcomes[i] = outcome
		if action.RemoveExistingTag && outcome.Applied {
			removed = append(removed, action.AliasName)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, errs.FromDB(err, "commit alias plan")
	}
	if len(removed) > 0 {
		if err := s.embedder.RemoveFromStore(ctx, removed); err != nil {
			s.logger.Error("", "tag service: remove aliased names from store: %v", err)
		}
	}
	return outcomes, nil
}

func (s *Tag) applyAliasAction(ctx context.Context, q *database.Queries, existing map[string]database.Tag, action tags.AliasAction) (tags.AliasOutcome, error) {
	var none tags.AliasOutcome
	aliasName := textnorm.NormalizeTag(action.AliasName)
	canonicalName := textnorm.NormalizeTag(action.Canonical)
	if aliasName == "" || canonicalName == "" || aliasName == canonicalName {
		return none, nil
	}

	canonical, ok := existing[canonicalName]
	if !ok {
		tag, err := q.GetTagByName(ctx, canonicalName)
		if errors.Is(err, sql.ErrNoRows) {
			return none, nil
		}
		if err != nil {
			return none, errs.FromDB(err, "get alias target "+canonicalName)
		}
		canonical = tag
	}

	if _, err := q.GetTagAliasByName(ctx, aliasName); err == nil {
		return none, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return none, errs.FromDB(err, "get tag alias "+aliasName)
	}

	if action.RemoveExistingTag {
		loser, err := q.GetTagForUpdateByName(ctx, aliasName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return none, nil
			}
			return none, errs.FromDB(err, "get alias loser "+aliasName)
		}
		if loser.ID == canonical.ID {
			return none, nil
		}

		repointed, err := q.ReassignDocumentTags(ctx, database.ReassignDocumentTagsParams{
			SourceID: loser.ID,
			TargetID: canonical.ID,
		})
		if err != nil {
			return none, errs.FromDB(err, "reassign document tags for alias "+aliasName)
		}
		if repointed > 0 {
			if err := q.ClearOrphanTagMarksByTagIDs(ctx, []int64{canonical.ID}); err != nil {
				return none, errs.FromDB(err, "clear orphan tag marks for alias "+aliasName)
			}
		}
		if _, err := q.DeleteDocumentTagsForTag(ctx, loser.ID); err != nil {
			return none, errs.FromDB(err, "delete leftover document tags for alias "+aliasName)
		}
		// no alias chains: the loser's own aliases re-target the new canonical
		if _, err := q.RetargetTagAliases(ctx, database.RetargetTagAliasesParams{
			NewTagID: canonical.ID,
			OldTagID: loser.ID,
		}); err != nil {
			return none, errs.FromDB(err, "retarget aliases for "+aliasName)
		}
		if err := q.DeleteTag(ctx, loser.ID); err != nil {
			return none, errs.FromDB(err, "delete alias loser "+aliasName)
		}
		if _, err := q.InsertTagAlias(ctx, database.InsertTagAliasParams{
			AliasName:      textnorm.NormalizeTag(aliasName),
			CanonicalTagID: canonical.ID,
			RuleSetVersion: tags.IdentityRuleSetVersion,
		}); err != nil {
			return none, errs.FromDB(err, "insert tag alias "+aliasName)
		}
		return tags.AliasOutcome{Applied: true, Reassigned: repointed}, nil
	}

	if _, err := q.InsertTagAlias(ctx, database.InsertTagAliasParams{
		AliasName:      textnorm.NormalizeTag(aliasName),
		CanonicalTagID: canonical.ID,
		RuleSetVersion: tags.IdentityRuleSetVersion,
	}); err != nil {
		return none, errs.FromDB(err, "insert tag alias "+aliasName)
	}
	return tags.AliasOutcome{Applied: true}, nil
}

// AliasesByTagIDs loads the recorded aliases of the given canonical tags, grouped by tag id and ordered
// by alias name; an empty id list reads nothing.
func (s *Tag) AliasesByTagIDs(ctx context.Context, tagIDs []int64) (map[int64][]database.TagAlias, error) {
	out := make(map[int64][]database.TagAlias, len(tagIDs))
	if len(tagIDs) == 0 {
		return out, nil
	}
	rows, err := s.client.Queries.ListTagAliasesByCanonicalIDs(ctx, tagIDs)
	if err != nil {
		return nil, errs.FromDB(err, "list tag aliases")
	}
	for _, row := range rows {
		out[row.CanonicalTagID] = append(out[row.CanonicalTagID], row)
	}
	return out, nil
}

// RemoveAlias deletes one recorded alias, so the name stops resolving to its canonical and leaves the
// filter vocabulary; the canonical tag and its documents stay untouched, and no matcher-store entry
// changes because alias names were never embedded. Seeded aliases are refused, since the startup seeder
// would restore them and the removal could not stick. Removing an absent alias name is reported as not
// found rather than as a failure, which makes a repeated removal harmless.
func (s *Tag) RemoveAlias(ctx context.Context, name string) (types.AliasRemoveStatus, error) {
	aliasName := textnorm.NormalizeTag(name)
	if aliasName == "" {
		return types.AliasRemoveNotFound, nil
	}

	row, err := s.client.Queries.GetTagAliasByName(ctx, aliasName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.AliasRemoveNotFound, nil
		}
		return types.AliasRemoveNotFound, errs.FromDB(err, "get tag alias "+aliasName)
	}
	if row.Seeded {
		return types.AliasRemoveSeeded, nil
	}

	removed, err := s.client.Queries.DeleteTagAliasByName(ctx, aliasName)
	if err != nil {
		return types.AliasRemoveNotFound, errs.FromDB(err, "delete tag alias "+aliasName)
	}
	if removed == 0 {
		return types.AliasRemoveNotFound, nil
	}

	s.logger.Info("", "tag alias: removed %q (canonical_tag_id=%d)", aliasName, row.CanonicalTagID)
	return types.AliasRemoveRemoved, nil
}

// PromoteAlias turns one recorded alias back into a live tag: the alias mapping is deleted and the name
// is registered through the operator creation path, so it takes the standing a hand-created tag takes
// and enters the matcher store, and its name is cleared from the deleted-tag blocklist. The canonical
// keeps its documents — the fold that created the alias already re-pointed them — so the promoted tag
// starts empty. The alias row is deleted inside the transaction before the registration is planned: the
// plan resolves names through the alias table, and a live row would resolve this name back onto the
// canonical instead of registering it. A name that the plan would alias or merge is not promoted; the
// transaction rolls back and the mapping stays.
func (s *Tag) PromoteAlias(ctx context.Context, name string) (types.AliasPromoteStatus, database.Tag, error) {
	aliasName := textnorm.NormalizeTag(name)
	if aliasName == "" {
		return types.AliasPromoteNotFound, database.Tag{}, nil
	}

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return types.AliasPromoteNotFound, database.Tag{}, errs.FromDB(err, "begin alias promote")
	}
	defer tx.Rollback()
	txQ := s.client.Queries.WithTx(tx)

	if _, err := txQ.GetTagAliasByName(ctx, aliasName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.AliasPromoteNotFound, database.Tag{}, nil
		}
		return types.AliasPromoteNotFound, database.Tag{}, errs.FromDB(err, "get tag alias "+aliasName)
	}

	removed, err := txQ.DeleteTagAliasByName(ctx, aliasName)
	if err != nil {
		return types.AliasPromoteNotApplied, database.Tag{}, errs.FromDB(err, "delete tag alias "+aliasName)
	}
	if removed == 0 {
		return types.AliasPromoteNotFound, database.Tag{}, nil
	}

	plan, err := s.buildPlan(ctx, txQ, []string{aliasName}, true)
	if err != nil {
		return types.AliasPromoteNotApplied, database.Tag{}, err
	}
	results, newNames, err := s.CreatePlannedTx(ctx, txQ, plan, false)
	if err != nil {
		return types.AliasPromoteNotApplied, database.Tag{}, err
	}
	if len(results) != 1 || results[0].Status != types.Created {
		return types.AliasPromoteNotApplied, database.Tag{}, nil
	}

	created, err := txQ.GetTagByName(ctx, aliasName)
	if err != nil {
		return types.AliasPromoteNotApplied, database.Tag{}, errs.FromDB(err, "get promoted tag "+aliasName)
	}
	if err := txQ.DeleteDeletedTag(ctx, aliasName); err != nil {
		return types.AliasPromoteNotApplied, database.Tag{}, errs.FromDB(err, "clear deleted tag "+aliasName)
	}
	if err := tx.Commit(); err != nil {
		return types.AliasPromoteNotApplied, database.Tag{}, errs.FromDB(err, "commit alias promote")
	}

	if len(newNames) > 0 {
		s.encodeAndAddBatch(ctx, newNames)
	}
	s.logger.Info("", "tag alias: promoted %q to a tag (id=%d)", aliasName, created.ID)
	return types.AliasPromotePromoted, created, nil
}

// RecordIdentityEvidence is the reconcile sweep's write path for pending identity evidence: rows are
// normalized and versioned exactly as the emission path's, so the two writers stay interchangeable.
func (s *Tag) RecordIdentityEvidence(ctx context.Context, rows []tags.EvidenceRow) error {
	return s.recordIdentityEvidence(ctx, s.client.Queries, rows)
}

// Separate records the operator's decision that one identity-held provisional tag names its own value
// instead of the anchor its refused fold proposed. Operator separation rows are journaled for every
// hold kind under the current rule set version, so the hold lookup stops withholding the value while
// every later fold decision keeps refusing the fold. The tag itself is untouched — it stays provisional
// and answers to its verdict — and both promotion paths already gate on the hold, so clearing the record
// is what releases it. A promoted tag has left the review queue and a tag without an identity hold has
// nothing to separate, so both are refused through the status.
func (s *Tag) Separate(ctx context.Context, id int64) (types.SeparateStatus, error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return types.SeparateNotFound, errs.FromDB(err, "begin separate tag")
	}
	defer tx.Rollback()
	txQ := s.client.WithTx(tx)

	tag, err := txQ.GetTagForUpdate(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.SeparateNotFound, nil
		}
		return types.SeparateNotFound, errs.FromDB(err, "lock separated tag")
	}
	if tag.PromotedAt.Valid {
		return types.SeparateNotProvisional, nil
	}

	name := textnorm.NormalizeTag(tag.Name)
	held, err := tags.IdentityHeldNames(ctx, txQ, []string{name})
	if err != nil {
		return types.SeparateWithoutHold, errs.FromDB(err, "load separate identity hold")
	}
	hold, ok := held[name]
	if !ok {
		return types.SeparateWithoutHold, nil
	}

	criteria, _ := json.Marshal(map[string]string{"proposed_fold": hold.Anchor})
	kinds := tags.IdentityHoldKinds()
	rows := make([]tags.EvidenceRow, 0, len(kinds))
	for _, kind := range kinds {
		rows = append(rows, tags.EvidenceRow{
			Kind:      kind,
			Candidate: name,
			Criteria:  criteria,
			Verdict:   tags.IdentityVerdictSeparate,
			Model:     "operator",
		})
	}
	if err := s.recordIdentityEvidence(ctx, txQ, rows); err != nil {
		return types.SeparateNotFound, err
	}
	if err := tx.Commit(); err != nil {
		return types.SeparateNotFound, errs.FromDB(err, "commit separate tag")
	}

	s.logger.Info("", "identity[%s]: operator separate %q from %q (reason=human-separate)",
		tags.IdentityRuleSetVersion, name, hold.Anchor)
	return types.SeparateApplied, nil
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
		version := row.RuleSetVersion
		if version == "" {
			version = tags.IdentityRuleSetVersion
		}
		if _, err := q.InsertTagIdentityEvent(ctx, database.InsertTagIdentityEventParams{
			Kind:           row.Kind,
			Candidate:      candidate,
			Partner:        partner,
			Criteria:       criteria,
			Verdict:        verdict,
			Model:          model,
			RuleSetVersion: version,
		}); err != nil {
			return errs.FromDB(err, "insert tag identity event "+candidate)
		}
	}
	return nil
}
