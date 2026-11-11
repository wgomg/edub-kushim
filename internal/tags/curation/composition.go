package curation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/types"
)

// ApplyCompositionDecision records and applies an operator's decision over one provisional compound.
// Split replaces the compound with the given parts: the parts are created, the compound's documents
// are re-pointed onto them, and the compound row is deleted. Keep leaves the compound whole, applying
// the given parts alongside it when any are supplied. A split requires at least one valid part; both
// dispositions re-validate nothing against a judge, so a promoted compound is refused and left to the
// reconciliation sweep. The decision is journaled under the current split criteria version with the
// operator model, so a stale hold cannot reopen the question.
func (s *Tag) ApplyCompositionDecision(ctx context.Context, id int64, disposition string, parts []string) (types.CompositionStatus, error) {
	compound, err := s.Get(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return types.CompositionNotFound, nil
		}
		return types.CompositionNotFound, err
	}
	if compound.PromotedAt.Valid {
		return types.CompositionNotProvisional, nil
	}

	switch tags.CompositionDisposition(disposition) {
	case tags.CompositionSplit:
		// SplitCompound owns the part cleanup and reports an invalid decision when nothing survives
		return s.applyOperatorSplit(ctx, compound, parts)
	case tags.CompositionKeep:
		return s.applyOperatorKeep(ctx, compound, parts)
	default:
		return types.CompositionInvalid, nil
	}
}

// SplitOutcome is one split attempt: the status the caller maps to its own surface, the number of
// documents re-pointed, and the normalized parts the split actually applied — empty unless it applied.
type SplitOutcome struct {
	Status     types.CompositionStatus
	Reassigned int64
	Parts      []string
}

// SplitCompound replaces one stored compound with the given parts: it creates the parts, locks the
// compound, re-points its documents onto them, clears the components' orphan marks, deletes the row,
// and removes the name from the matcher store. Name is the compound's stored name as the caller read
// it: a row renamed since then refuses the split (CompositionConflict). requireProvisional refuses a
// compound promoted after the caller's check; the reconciliation sweep splits promoted compounds itself,
// so it passes false. standingSource records the provenance of the promotions the split performs: an
// operator split pins its parts, a rule-driven one does not. A refused decision removes any part rows it
// created, so a failed split leaves no new vocabulary behind.
func (s *Tag) SplitCompound(ctx context.Context, compoundID int64, name string, parts []string, requireProvisional bool, standingSource string) (SplitOutcome, error) {
	cleaned := cleanCompositionParts(parts, name)
	if len(cleaned) == 0 {
		return SplitOutcome{Status: types.CompositionInvalid}, nil
	}

	componentIDs, created, ok, err := s.createCompositionParts(ctx, cleaned, standingSource)
	if err != nil {
		return SplitOutcome{Status: types.CompositionInvalid}, err
	}
	if !ok {
		return SplitOutcome{Status: types.CompositionInvalid}, nil
	}

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		s.rollbackCreatedParts(ctx, created)
		return SplitOutcome{Status: types.CompositionInvalid}, errs.FromDB(err, "begin composition split")
	}
	defer tx.Rollback()
	txQ := s.client.WithTx(tx)

	locked, err := txQ.GetTagForUpdate(ctx, compoundID)
	if err != nil {
		s.rollbackCreatedParts(ctx, created)
		if errors.Is(err, sql.ErrNoRows) {
			return SplitOutcome{Status: types.CompositionNotFound}, nil
		}
		return SplitOutcome{Status: types.CompositionInvalid}, errs.FromDB(err, "lock compound tag")
	}
	if locked.Name != name {
		s.rollbackCreatedParts(ctx, created)
		return SplitOutcome{Status: types.CompositionConflict}, nil
	}
	if requireProvisional && locked.PromotedAt.Valid {
		s.rollbackCreatedParts(ctx, created)
		return SplitOutcome{Status: types.CompositionNotProvisional}, nil
	}

	docIDs, err := txQ.GetTagDocumentIDs(ctx, locked.ID)
	if err != nil {
		s.rollbackCreatedParts(ctx, created)
		return SplitOutcome{Status: types.CompositionInvalid}, errs.FromDB(err, "document ids for compound")
	}
	if _, err := txQ.AddComponentTagsToTagDocuments(ctx, database.AddComponentTagsToTagDocumentsParams{
		ComponentIds: componentIDs,
		JointID:      locked.ID,
	}); err != nil {
		s.rollbackCreatedParts(ctx, created)
		return SplitOutcome{Status: types.CompositionInvalid}, errs.FromDB(err, "add component tags")
	}
	if len(docIDs) > 0 {
		if err := txQ.ClearOrphanTagMarksByTagIDs(ctx, componentIDs); err != nil {
			s.rollbackCreatedParts(ctx, created)
			return SplitOutcome{Status: types.CompositionInvalid}, errs.FromDB(err, "clear component orphan tag marks")
		}
	}
	if err := txQ.DeleteTag(ctx, locked.ID); err != nil {
		s.rollbackCreatedParts(ctx, created)
		return SplitOutcome{Status: types.CompositionInvalid}, errs.FromDB(err, "delete split compound")
	}
	if err := tx.Commit(); err != nil {
		s.rollbackCreatedParts(ctx, created)
		return SplitOutcome{Status: types.CompositionInvalid}, errs.FromDB(err, "commit composition split")
	}

	if err := s.embedder.RemoveFromStore(ctx, []string{locked.Name}); err != nil {
		s.logger.Error("", "tag service: remove split compound %q from store: %v", locked.Name, err)
	}
	return SplitOutcome{Status: types.CompositionApplied, Reassigned: int64(len(docIDs)), Parts: cleaned}, nil
}

// Split replaces one live tag with the given parts regardless of promotion: the parts are created, the
// tag's documents are re-pointed onto them, and its row is deleted. The tag is named rather than merely
// identified, because the journals, the parts and the operator all speak names; a name missing from
// the store is refused. Seeded values are refused because the startup seeder would re-create them; a
// split whose parts do not validate is refused and leaves no new vocabulary behind. The decision is
// journaled under the current split criteria version with the operator model, exactly like a decided
// split, so a stale hold cannot reopen the question.
func (s *Tag) Split(ctx context.Context, name string, parts []string) (types.CompositionStatus, error) {
	compound, err := s.GetByName(ctx, name)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return types.CompositionNotFound, nil
		}
		return types.CompositionNotFound, err
	}
	if compound.Seeded {
		return types.CompositionSeeded, nil
	}

	outcome, err := s.SplitCompound(ctx, compound.ID, compound.Name, parts, false, tags.StandingOperator)
	if err != nil || outcome.Status != types.CompositionApplied {
		return outcome.Status, err
	}
	return s.journalAppliedSplit(ctx, compound.Name, outcome), nil
}

// applyOperatorSplit runs the shared split and journals the operator's decision so the review
// surface and the sweep read the same record a judge writes.
func (s *Tag) applyOperatorSplit(ctx context.Context, compound database.Tag, parts []string) (types.CompositionStatus, error) {
	outcome, err := s.SplitCompound(ctx, compound.ID, compound.Name, parts, true, tags.StandingOperator)
	if err != nil || outcome.Status != types.CompositionApplied {
		return outcome.Status, err
	}
	return s.journalAppliedSplit(ctx, compound.Name, outcome), nil
}

// journalAppliedSplit records a split that already applied under the operator model and logs the full
// reassignment, so both operator entry points leave the same audit trail; it also records the standing rule
// future emissions apply. A journal or rule write failure is logged rather than returned, because the split
// itself is already committed.
func (s *Tag) journalAppliedSplit(ctx context.Context, name string, outcome SplitOutcome) types.CompositionStatus {
	if err := s.recordOperatorComposition(ctx, name, tags.CompositionSplit,
		tags.CompositionReasonOperator, outcome.Parts); err != nil {
		s.logger.Error("", "tag service: record operator split %q: %v", name, err)
	}
	// the rule is what makes future emissions split the compound; the split itself is already committed, so
	// a failed rule write is logged loudly rather than returned
	if err := s.recordOperatorSplit(ctx, name, outcome.Parts); err != nil {
		s.logger.Error("", "tag service: record operator split rule %q (future emissions will not split): %v", name, err)
	}
	s.logger.Info("", "tag composition: operator split %q -> %v (reason=human-split, reassigned=%d)",
		name, outcome.Parts, outcome.Reassigned)
	return types.CompositionApplied
}

// recordOperatorSplit persists the standing rule future emissions apply: the candidate is keyed by the
// store's name recipe, exactly like the composition journal, and parts are the normalized names the split
// applied. A later split of the same name overwrites the rule, and an operator keep deletes it.
func (s *Tag) recordOperatorSplit(ctx context.Context, candidate string, parts []string) error {
	return s.client.Queries.UpsertTagOperatorSplit(ctx, database.UpsertTagOperatorSplitParams{
		Candidate: tags.StoredName(candidate),
		Parts:     parts,
	})
}

// ListOperatorSplits returns one page of standing operator split rules ordered by compound name, plus the
// total rule count: the durable decisions the emission gate applies as substitutions. It is the read the
// review surface lists, so an operator can revert a split after the compound row is gone.
func (s *Tag) ListOperatorSplits(ctx context.Context, limit, offset int32) ([]database.TagOperatorSplit, int64, error) {
	rows, err := s.client.Queries.ListTagOperatorSplits(ctx, database.ListTagOperatorSplitsParams{
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, 0, errs.FromDB(err, "list operator split rules")
	}
	total, err := s.client.Queries.CountTagOperatorSplits(ctx)
	if err != nil {
		return nil, 0, errs.FromDB(err, "count operator split rules")
	}
	return rows, total, nil
}

// RevertOperatorSplit removes one recorded split rule and journals the inverse operator keep, returning the
// outcome the API maps to its surface. The journal row is name-keyed and carries no document, so a stored
// compound stops reading as a pending split and the store-existing skip applies it whole. A name without a
// rule reports not found; the part tags and the documents already moved onto them are left untouched.
func (s *Tag) RevertOperatorSplit(ctx context.Context, name string) (types.CompositionStatus, error) {
	candidate := tags.StoredName(name)
	if candidate == "" {
		return types.CompositionNotFound, nil
	}
	deleted, err := s.client.Queries.DeleteTagOperatorSplit(ctx, candidate)
	if err != nil {
		return types.CompositionNotFound, errs.FromDB(err, "delete operator split rule")
	}
	if deleted == 0 {
		return types.CompositionNotFound, nil
	}
	if err := s.recordOperatorComposition(ctx, candidate, tags.CompositionKeep,
		tags.CompositionReasonOperatorKeep, nil); err != nil {
		s.logger.Error("", "tag service: record reverted operator split %q: %v", candidate, err)
	}
	s.logger.Info("", "tag composition: reverted operator split %q (reason=human-revert)", candidate)
	return types.CompositionReverted, nil
}

// applyOperatorKeep journals the operator's keep and, when parts are supplied, attaches them to the
// compound's documents alongside it. The compound row stays, so it keeps its standing and its mass, and any
// recorded split rule is deleted because a keep reverses that decision for future emissions.
// It normalizes the parts once here; a refused keep removes any part rows it created.
func (s *Tag) applyOperatorKeep(ctx context.Context, compound database.Tag, parts []string) (types.CompositionStatus, error) {
	cleaned := cleanCompositionParts(parts, compound.Name)
	if len(cleaned) > 0 {
		componentIDs, created, ok, err := s.createCompositionParts(ctx, cleaned, tags.StandingOperator)
		if err != nil {
			return types.CompositionInvalid, err
		}
		if !ok {
			return types.CompositionInvalid, nil
		}
		tx, err := s.client.BeginTx(ctx, nil)
		if err != nil {
			s.rollbackCreatedParts(ctx, created)
			return types.CompositionInvalid, errs.FromDB(err, "begin composition keep")
		}
		defer tx.Rollback()
		txQ := s.client.WithTx(tx)

		locked, err := txQ.GetTagForUpdate(ctx, compound.ID)
		if err != nil {
			s.rollbackCreatedParts(ctx, created)
			if errors.Is(err, sql.ErrNoRows) {
				return types.CompositionNotFound, nil
			}
			return types.CompositionInvalid, errs.FromDB(err, "lock compound tag")
		}
		if locked.PromotedAt.Valid {
			s.rollbackCreatedParts(ctx, created)
			return types.CompositionNotProvisional, nil
		}

		if _, err := txQ.AddComponentTagsToTagDocuments(ctx, database.AddComponentTagsToTagDocumentsParams{
			ComponentIds: componentIDs,
			JointID:      locked.ID,
		}); err != nil {
			s.rollbackCreatedParts(ctx, created)
			return types.CompositionInvalid, errs.FromDB(err, "add along-side component tags")
		}
		if err := txQ.ClearOrphanTagMarksByTagIDs(ctx, componentIDs); err != nil {
			s.rollbackCreatedParts(ctx, created)
			return types.CompositionInvalid, errs.FromDB(err, "clear along-side orphan tag marks")
		}
		if err := tx.Commit(); err != nil {
			s.rollbackCreatedParts(ctx, created)
			return types.CompositionInvalid, errs.FromDB(err, "commit composition keep")
		}
	}

	// a keep is the inverse decision of a recorded split: the rule goes, so the compound is applied whole
	// again on future emissions; the keep itself already applied, so a failed delete is logged, not returned
	if _, err := s.client.Queries.DeleteTagOperatorSplit(ctx, tags.StoredName(compound.Name)); err != nil {
		s.logger.Error("", "tag service: delete operator split rule %q: %v", compound.Name, err)
	}
	if err := s.recordOperatorComposition(ctx, compound.Name, tags.CompositionKeep,
		tags.CompositionReasonOperatorKeep, cleaned); err != nil {
		return types.CompositionInvalid, err
	}
	s.logger.Info("", "tag composition: operator keep %q (parts=%v, reason=human-keep)", compound.Name, parts)
	return types.CompositionApplied, nil
}

// createdParts is the set of part rows one decision created, kept so a decision that cannot apply can
// remove them again instead of leaving new vocabulary entries behind.
type createdParts struct {
	ids   []int64
	names []string
}

// createCompositionParts creates the requested parts and returns their ids plus the rows it created.
// A part the store refuses reports ok false after the rows created so far are removed, so the caller
// never proceeds with a subset.
func (s *Tag) createCompositionParts(ctx context.Context, parts []string, standingSource string) ([]int64, createdParts, bool, error) {
	results, err := s.Create(ctx, parts, standingSource)
	if err != nil {
		return nil, createdParts{}, false, err
	}
	ids := make([]int64, 0, len(results))
	created := createdParts{}
	for _, result := range results {
		switch result.Status {
		case types.Created:
			ids = append(ids, result.Entity.ID)
			created.ids = append(created.ids, result.Entity.ID)
			created.names = append(created.names, result.Entity.Name)
		case types.Conflict, types.Apply:
			// an existing value: the decision may attach it, but it is not ours to remove
			ids = append(ids, result.Entity.ID)
		default:
			s.rollbackCreatedParts(ctx, created)
			return nil, createdParts{}, false, nil
		}
	}
	return ids, created, true, nil
}

// rollbackCreatedParts deletes the rows a refused decision created, so the decision fails loudly
// without leaving promoted zero-document values behind. Store removal is best-effort, like every
// other store mutation here.
func (s *Tag) rollbackCreatedParts(ctx context.Context, created createdParts) {
	for _, id := range created.ids {
		if err := s.client.Queries.DeleteTag(ctx, id); err != nil {
			s.logger.Error("", "tag composition: roll back created part %d: %v", id, err)
		}
	}
	if len(created.names) > 0 {
		if err := s.embedder.RemoveFromStore(ctx, created.names); err != nil {
			s.logger.Error("", "tag composition: remove rolled-back parts from store: %v", err)
		}
	}
}

// recordOperatorComposition journals one operator decision as composition evidence under the current
// split criteria version, so the hold lookup and the review surface read it exactly like a judge row.
func (s *Tag) recordOperatorComposition(ctx context.Context, candidate string, disposition tags.CompositionDisposition, reason string, parts []string) error {
	return s.RecordCompositionEvidence(ctx, []tags.CompositionRow{{
		Candidate:       candidate,
		Parts:           parts,
		Disposition:     disposition,
		Reason:          reason,
		CriteriaVersion: analyzer.TagSplitVersion,
		Model:           "operator",
	}})
}

// cleanCompositionParts normalizes each requested part and drops empties, duplicates, and the compound
// itself, so a decision never splits a value into itself.
func cleanCompositionParts(parts []string, compound string) []string {
	self := tags.StoredName(compound)
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, raw := range parts {
		name := textnorm.NormalizeTag(raw)
		if name == "" {
			continue
		}
		key := tags.StoredName(name)
		if key == self {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}
