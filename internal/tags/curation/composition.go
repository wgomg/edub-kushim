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
// compound promoted after the caller's check; the reconciliation sweep splits promoted compounds
// itself, so it passes false. A refused decision removes any part rows it created, so a failed split
// leaves no new vocabulary behind.
func (s *Tag) SplitCompound(ctx context.Context, compoundID int64, name string, parts []string, requireProvisional bool) (SplitOutcome, error) {
	cleaned := cleanCompositionParts(parts, name)
	if len(cleaned) == 0 {
		return SplitOutcome{Status: types.CompositionInvalid}, nil
	}

	componentIDs, created, ok, err := s.createCompositionParts(ctx, cleaned)
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

// applyOperatorSplit runs the shared split and journals the operator's decision so the review
// surface and the sweep read the same record a judge writes.
func (s *Tag) applyOperatorSplit(ctx context.Context, compound database.Tag, parts []string) (types.CompositionStatus, error) {
	outcome, err := s.SplitCompound(ctx, compound.ID, compound.Name, parts, true)
	if err != nil || outcome.Status != types.CompositionApplied {
		return outcome.Status, err
	}

	if err := s.recordOperatorComposition(ctx, compound.Name, tags.CompositionSplit,
		tags.CompositionReasonOperator, outcome.Parts); err != nil {
		s.logger.Error("", "tag service: record operator split %q: %v", compound.Name, err)
	}
	s.logger.Info("", "tag composition: operator split %q -> %v (reason=human-split, reassigned=%d)",
		compound.Name, outcome.Parts, outcome.Reassigned)
	return types.CompositionApplied, nil
}

// applyOperatorKeep journals the operator's keep and, when parts are supplied, attaches them to the
// compound's documents alongside it. The compound row stays, so it keeps its standing and its mass.
// It normalizes the parts once here; a refused keep removes any part rows it created.
func (s *Tag) applyOperatorKeep(ctx context.Context, compound database.Tag, parts []string) (types.CompositionStatus, error) {
	cleaned := cleanCompositionParts(parts, compound.Name)
	if len(cleaned) > 0 {
		componentIDs, created, ok, err := s.createCompositionParts(ctx, cleaned)
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
func (s *Tag) createCompositionParts(ctx context.Context, parts []string) ([]int64, createdParts, bool, error) {
	results, err := s.Create(ctx, parts)
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
