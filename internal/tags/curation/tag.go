// Package curation is the tag store's human-facing operations: reading and
// searching the vocabulary, creating tags through the deterministic
// identity plan, renaming, deleting with blocklist bookkeeping, promoting
// provisional tags, managing deleted-tag records, and keeping the matcher's
// embedding store in sync with the tags table. It is the single owner of
// the tag-store invariants; the pipeline side applies its writes through
// this package. The merge-review flows are documented in
// docs/tags/consolidation.md.
package curation

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/textnorm"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TagUpdatePair is one rename request: the tag's id and its new name.
type TagUpdatePair struct {
	ID   int64
	Name string
}

// The deleted-tag kinds: blocked (deleted by an operator) and merged
// (folded into a surviving target). DocumentTagSourceEnrich marks document
// tag rows created by the enrichment pipeline.
const (
	DeletedTagKindBlocked = "blocked"
	DeletedTagKindMerged  = "merged"

	DocumentTagSourceEnrich = "enrich"
)

// Tag is the tag store's read, write and administration surface: CRUD,
// planned registrations, aliases, deleted tags, promotion, and the sync
// that keeps the matcher's embedding store aligned with the tags table.
type Tag struct {
	client   *database.Client
	embedder matcher.Embedder
	logger   *log.Logger
}

// NewTag wires the store; embedder is the matcher-side vocabulary the
// tag mutations keep in sync.
func NewTag(client *database.Client, logger *log.Logger, embedder matcher.Embedder) *Tag {
	return &Tag{
		client:   client,
		embedder: embedder,
		logger:   logger,
	}
}

// Close releases the embedder.
func (s *Tag) Close() error {
	s.embedder.Close()
	return nil
}

// Get returns one tag by id, errs.KindNotFound when absent.
func (s *Tag) Get(ctx context.Context, id int64) (database.Tag, error) {
	tag, err := s.client.Queries.GetTag(ctx, id)
	if err != nil {
		return database.Tag{}, errs.FromDB(err, "get tag")
	}
	return tag, nil
}

// GetByName returns one tag by its exact name.
func (s *Tag) GetByName(ctx context.Context, name string) (database.Tag, error) {
	tag, err := s.client.Queries.GetTagByName(ctx, name)
	if err != nil {
		return database.Tag{}, errs.FromDB(err, "get tag by name")
	}
	return tag, nil
}

// Count returns how many tags exist.
func (s *Tag) Count(ctx context.Context) (int64, error) {
	count, err := s.client.Queries.CountTags(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count tags")
	}
	return count, nil
}

// CountByName counts the tags whose names start with the prefix.
func (s *Tag) CountByName(ctx context.Context, prefix string) (int64, error) {
	count, err := s.client.Queries.CountTagsByName(ctx, prefix+"%")
	if err != nil {
		return 0, errs.FromDB(err, "count tags by name")
	}
	return count, nil
}

// ListAll returns every tag, promoted and provisional.
func (s *Tag) ListAll(ctx context.Context) ([]database.Tag, error) {
	tags, err := s.client.Queries.ListAllTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all tags")
	}
	return tags, nil
}

// ListWithDocumentCount pages all tags with their live document counts.
func (s *Tag) ListWithDocumentCount(ctx context.Context, limit, offset int32) ([]database.ListTagsWithDocumentCountRow, error) {
	tags, err := s.client.Queries.ListTagsWithDocumentCount(ctx, database.ListTagsWithDocumentCountParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list tags with document count")
	}
	return tags, nil
}

// SearchByNameWithDocumentCount pages the tags whose names start with the
// prefix, with their live document counts.
func (s *Tag) SearchByNameWithDocumentCount(ctx context.Context, prefix string, limit, offset int32) ([]database.SearchTagsByNameWithDocumentCountRow, error) {
	tags, err := s.client.Queries.SearchTagsByNameWithDocumentCount(ctx, database.SearchTagsByNameWithDocumentCountParams{
		Name:   prefix + "%",
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "search tags with document count")
	}
	return tags, nil
}

// ListProvisional pages the not-yet-promoted tags with document counts.
func (s *Tag) ListProvisional(ctx context.Context, limit, offset int32) ([]database.ListProvisionalTagsWithDocumentCountRow, error) {
	tags, err := s.client.Queries.ListProvisionalTagsWithDocumentCount(ctx, database.ListProvisionalTagsWithDocumentCountParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list provisional tags")
	}
	return tags, nil
}

// CountProvisional counts the not-yet-promoted tags.
func (s *Tag) CountProvisional(ctx context.Context) (int64, error) {
	count, err := s.client.Queries.CountProvisionalTags(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count provisional tags")
	}
	return count, nil
}

// LatestQualityEventsForNames returns the most recent quality verdict per
// tag name under the current quality rule version.
func (s *Tag) LatestQualityEventsForNames(ctx context.Context, names []string) ([]database.LatestTagQualityEventsForNamesRow, error) {
	rows, err := s.client.Queries.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
		PolicyVersion: analyzer.TagQualityVersion,
		TagNames:      names,
	})
	if err != nil {
		return nil, errs.FromDB(err, "latest tag quality events")
	}
	return rows, nil
}

// PendingIdentityCounts returns, per name, how many identity events are
// still pending judgement.
func (s *Tag) PendingIdentityCounts(ctx context.Context, names []string) (map[string]int64, error) {
	rows, err := s.client.Queries.PendingTagIdentityCountsByNames(ctx, names)
	if err != nil {
		return nil, errs.FromDB(err, "pending tag identity counts")
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Name] = row.PendingCount
	}
	return counts, nil
}

// AddToStore embeds the names into the matcher store, best-effort.
func (s *Tag) AddToStore(ctx context.Context, names []string) {
	if len(names) == 0 {
		return
	}
	s.encodeAndAddBatch(ctx, names)
}

// Update renames tags in bulk, returning one result per pair (invalid,
// not found, no-op, conflict, or updated). Renamed promoted tags are
// re-embedded and their old names leave the matcher store.
func (s *Tag) Update(ctx context.Context, pairs []TagUpdatePair) ([]types.UpdateResult[database.Tag], error) {
	results := make([]types.UpdateResult[database.Tag], len(pairs))
	var newNames []string
	type rename struct {
		idx     int
		oldName string
		name    string
	}
	var renames []rename

	allTags, err := s.client.Queries.ListAllTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load all tags")
	}
	tagMap := make(map[int64]database.Tag, len(allTags))
	nameMap := make(map[string]database.Tag, len(allTags))
	for _, t := range allTags {
		tagMap[t.ID] = t
		nameMap[t.Name] = t
	}

	for i, p := range pairs {
		name := textnorm.NormalizeTag(p.Name)
		if name == "" {
			results[i] = types.UpdateResult[database.Tag]{Status: types.UpdateInvalid}
			continue
		}

		old, ok := tagMap[p.ID]
		if !ok {
			results[i] = types.UpdateResult[database.Tag]{Status: types.UpdateNotFound}
			continue
		}

		if name == old.Name {
			results[i] = types.UpdateResult[database.Tag]{Entity: old, Status: types.Noop}
			continue
		}

		if existing, ok := nameMap[name]; ok && existing.ID != p.ID {
			results[i] = types.UpdateResult[database.Tag]{Entity: existing, Status: types.UpdateConflict}
			continue
		}

		if err := s.client.Queries.UpdateTag(ctx, database.UpdateTagParams{
			Name: textnorm.NormalizeTag(name),
			ID:   p.ID,
		}); err != nil {
			if errs.IsConstraint(err) {
				var existing database.Tag
				if e, ok := nameMap[name]; ok {
					existing = e
				}
				results[i] = types.UpdateResult[database.Tag]{Entity: existing, Status: types.UpdateConflict}
				continue
			}
			return nil, errs.FromDB(err, "update tag")
		}

		if old.PromotedAt.Valid {
			newNames = append(newNames, name)
		}
		renames = append(renames, rename{idx: i, oldName: old.Name, name: name})
	}

	for _, r := range renames {
		if err := s.embedder.RemoveFromStore(ctx, []string{r.oldName}); err != nil {
			s.logger.Error("", "tag service: remove from store: %v", err)
		}
	}

	if len(newNames) > 0 {
		s.encodeAndAddBatch(ctx, newNames)
	}

	for _, r := range renames {
		updatedTag := database.Tag{
			ID:   pairs[r.idx].ID,
			Name: r.name,
		}
		results[r.idx] = types.UpdateResult[database.Tag]{Entity: updatedTag, Status: types.Updated}
	}

	return results, nil
}

// Delete removes tags and records each name in the deleted-tag blocklist
// (kind blocked) so the same vocabulary is not recreated silently; their
// names leave the matcher store.
func (s *Tag) Delete(ctx context.Context, ids []int64) ([]types.DeleteResult, error) {
	results := make([]types.DeleteResult, len(ids))

	allTags, err := s.client.Queries.ListAllTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load all tags")
	}
	tagMap := make(map[int64]database.Tag, len(allTags))
	for _, t := range allTags {
		tagMap[t.ID] = t
	}

	for i, id := range ids {
		tag, ok := tagMap[id]
		if !ok {
			results[i] = types.DeleteResult{ID: id, Status: types.DeleteNotFound}
			continue
		}

		if err := s.client.Queries.DeleteTag(ctx, id); err != nil {
			return nil, errs.FromDB(err, "delete tag")
		}

		if err := s.client.Queries.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
			Name: textnorm.NormalizeTag(tag.Name),
			Kind: DeletedTagKindBlocked,
		}); err != nil {
			s.logger.Error("", "tag service: record deleted tag %q: %v", tag.Name, err)
		}

		if err := s.embedder.RemoveFromStore(ctx, []string{tag.Name}); err != nil {
			s.logger.Error("", "tag service: remove from store: %v", err)
		}
		results[i] = types.DeleteResult{ID: id, Status: types.Deleted}
	}

	return results, nil
}

// Promote promotes one provisional tag and embeds it; already-promoted
// and not-found cases are reported through the status, and reason goes to
// the audit log.
func (s *Tag) Promote(ctx context.Context, id int64, reason string) (database.Tag, types.PromoteStatus, error) {
	tag, err := s.Get(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return database.Tag{}, types.PromoteNotFound, nil
		}
		return database.Tag{}, types.PromoteNotFound, err
	}
	if tag.PromotedAt.Valid {
		return tag, types.PromoteAlreadyPromoted, nil
	}

	promoted, err := s.client.Queries.PromoteTags(ctx, []int64{id})
	if err != nil {
		return tag, types.PromoteNotFound, errs.FromDB(err, "promote tag")
	}
	if promoted == 0 {
		return tag, types.PromoteAlreadyPromoted, nil
	}

	s.encodeAndAddBatch(ctx, []string{tag.Name})
	s.logger.Info("", "tagquality[%s]: promote %q (reason=%s)", analyzer.TagQualityVersion, tag.Name, reason)
	return tag, types.PromotePromoted, nil
}

// ListDeleted pages the deleted-tag blocklist by name prefix with its
// total count.
func (s *Tag) ListDeleted(ctx context.Context, prefix string, limit, offset int32) ([]database.DeletedTag, int64, error) {
	rows, err := s.client.Queries.ListDeletedTags(ctx, database.ListDeletedTagsParams{
		NamePrefix: prefix,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, 0, errs.FromDB(err, "list deleted tags")
	}
	total, err := s.client.Queries.CountDeletedTags(ctx, prefix)
	if err != nil {
		return nil, 0, errs.FromDB(err, "count deleted tags")
	}
	return rows, total, nil
}

// RestoreDeleted drops a deleted-tag record; blocked entries have their
// tag recreated through Create. Merged entries only clear the blocklist.
func (s *Tag) RestoreDeleted(ctx context.Context, name string) (database.DeletedTag, error) {
	row, err := s.client.Queries.GetDeletedTag(ctx, name)
	if err != nil {
		return database.DeletedTag{}, errs.FromDB(err, "restore deleted tag")
	}
	if row.Kind == DeletedTagKindBlocked {
		if _, err := s.Create(ctx, []string{row.Name}); err != nil {
			return row, err
		}
	}
	if err := s.client.Queries.DeleteDeletedTag(ctx, name); err != nil {
		return row, errs.FromDB(err, "restore deleted tag")
	}
	return row, nil
}

// RemoveFromStore drops the names from the matcher store's embeddings.
func (s *Tag) RemoveFromStore(ctx context.Context, names []string) error {
	return s.embedder.RemoveFromStore(ctx, names)
}

func (s *Tag) encodeAndAddBatch(ctx context.Context, names []string) {
	if err := s.embedder.AddToStore(ctx, names); err != nil {
		s.logger.Error("", "tag service: add to store: %v", err)
	}
}
