package service

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/contentanalyzer"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TagUpdatePair struct {
	ID   int64
	Name string
}

const (
	DeletedTagKindBlocked = "blocked"
	DeletedTagKindMerged  = "merged"

	DocumentTagSourceEnrich = "enrich"
)

type Tag struct {
	client   *database.Client
	embedder tagmatcher.Embedder
	logger   *utils.Logger
}

func NewTag(client *database.Client, logger *utils.Logger, embedder tagmatcher.Embedder) *Tag {
	return &Tag{
		client:   client,
		embedder: embedder,
		logger:   logger,
	}
}

func (s *Tag) Close() error {
	s.embedder.Close()
	return nil
}

func (s *Tag) Get(ctx context.Context, id int64) (database.Tag, error) {
	tag, err := s.client.Queries.GetTag(ctx, id)
	if err != nil {
		return database.Tag{}, errs.FromDB(err, "get tag")
	}
	return tag, nil
}

func (s *Tag) GetByName(ctx context.Context, name string) (database.Tag, error) {
	tag, err := s.client.Queries.GetTagByName(ctx, name)
	if err != nil {
		return database.Tag{}, errs.FromDB(err, "get tag by name")
	}
	return tag, nil
}

func (s *Tag) Count(ctx context.Context) (int64, error) {
	count, err := s.client.Queries.CountTags(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count tags")
	}
	return count, nil
}

func (s *Tag) CountByName(ctx context.Context, prefix string) (int64, error) {
	count, err := s.client.Queries.CountTagsByName(ctx, prefix+"%")
	if err != nil {
		return 0, errs.FromDB(err, "count tags by name")
	}
	return count, nil
}

func (s *Tag) ListAll(ctx context.Context) ([]database.Tag, error) {
	tags, err := s.client.Queries.ListAllTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all tags")
	}
	return tags, nil
}

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

func (s *Tag) CountProvisional(ctx context.Context) (int64, error) {
	count, err := s.client.Queries.CountProvisionalTags(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count provisional tags")
	}
	return count, nil
}

func (s *Tag) LatestQualityEventsForNames(ctx context.Context, names []string) ([]database.LatestTagQualityEventsForNamesRow, error) {
	rows, err := s.client.Queries.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
		PolicyVersion: contentanalyzer.TagQualityVersion,
		TagNames:      names,
	})
	if err != nil {
		return nil, errs.FromDB(err, "latest tag quality events")
	}
	return rows, nil
}

func (s *Tag) Create(ctx context.Context, names []string) ([]CreateResult[database.Tag], error) {
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return nil, errs.FromDB(err, "begin tag create")
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)
	results, newNames, err := s.CreateTx(ctx, txQ, names, false)
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

func (s *Tag) CreateAndUnblock(ctx context.Context, names []string) ([]CreateResult[database.Tag], error) {
	results, err := s.Create(ctx, names)
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		if r.Status != Created && r.Status != Conflict {
			continue
		}
		if err := s.client.Queries.DeleteDeletedTag(ctx, r.Entity.Name); err != nil {
			return nil, errs.FromDB(err, "clear deleted tag "+r.Entity.Name)
		}
	}
	return results, nil
}

func (s *Tag) CreateTx(ctx context.Context, q *database.Queries, names []string, quarantined bool) ([]CreateResult[database.Tag], []string, error) {
	results := make([]CreateResult[database.Tag], len(names))
	var newNames []string
	var newIdx []int
	var newIDs []int64

	existingTags, err := q.ListAllTags(ctx)
	if err != nil {
		return nil, nil, errs.FromDB(err, "load existing tags")
	}
	existingMap := make(map[string]database.Tag, len(existingTags))
	for _, t := range existingTags {
		existingMap[t.Name] = t
	}

	for i, raw := range names {
		name := utils.NormalizeTag(raw)
		if name == "" {
			results[i] = CreateResult[database.Tag]{Status: Invalid}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = CreateResult[database.Tag]{Entity: existing, Status: Conflict}
			continue
		}

		id, err := q.CreateTag(ctx, utils.NormalizeTag(name))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				existing, err := q.GetTagByName(ctx, name)
				if err != nil {
					return nil, nil, errs.FromDB(err, "get tag by name after conflict "+name)
				}
				results[i] = CreateResult[database.Tag]{Entity: existing, Status: Conflict}
				continue
			}
			return nil, nil, errs.FromDB(err, "create tag "+name)
		}

		results[i] = CreateResult[database.Tag]{Entity: database.Tag{ID: id, Name: name}, Status: Created}
		newNames = append(newNames, name)
		newIdx = append(newIdx, i)
		newIDs = append(newIDs, id)
	}

	for j, name := range newNames {
		results[newIdx[j]].Entity.Name = name
	}

	if !quarantined && len(newIDs) > 0 {
		if _, err := q.PromoteTags(ctx, newIDs); err != nil {
			return nil, nil, errs.FromDB(err, "promote new tags")
		}
	}

	return results, newNames, nil
}

func (s *Tag) AddToStore(ctx context.Context, names []string) {
	if len(names) == 0 {
		return
	}
	s.encodeAndAddBatch(ctx, names)
}

func (s *Tag) Update(ctx context.Context, pairs []TagUpdatePair) ([]UpdateResult[database.Tag], error) {
	results := make([]UpdateResult[database.Tag], len(pairs))
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
		name := utils.NormalizeTag(p.Name)
		if name == "" {
			results[i] = UpdateResult[database.Tag]{Status: UpdateInvalid}
			continue
		}

		old, ok := tagMap[p.ID]
		if !ok {
			results[i] = UpdateResult[database.Tag]{Status: UpdateNotFound}
			continue
		}

		if name == old.Name {
			results[i] = UpdateResult[database.Tag]{Entity: old, Status: Noop}
			continue
		}

		if existing, ok := nameMap[name]; ok && existing.ID != p.ID {
			results[i] = UpdateResult[database.Tag]{Entity: existing, Status: UpdateConflict}
			continue
		}

		if err := s.client.Queries.UpdateTag(ctx, database.UpdateTagParams{
			Name: utils.NormalizeTag(name),
			ID:   p.ID,
		}); err != nil {
			if errs.IsConstraint(err) {
				var existing database.Tag
				if e, ok := nameMap[name]; ok {
					existing = e
				}
				results[i] = UpdateResult[database.Tag]{Entity: existing, Status: UpdateConflict}
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
			s.logger.Error(nil, "tag service: remove from store: %v", err)
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
		results[r.idx] = UpdateResult[database.Tag]{Entity: updatedTag, Status: Updated}
	}

	return results, nil
}

func (s *Tag) Delete(ctx context.Context, ids []int64) ([]DeleteResult, error) {
	results := make([]DeleteResult, len(ids))

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
			results[i] = DeleteResult{ID: id, Status: DeleteNotFound}
			continue
		}

		if err := s.client.Queries.DeleteTag(ctx, id); err != nil {
			return nil, errs.FromDB(err, "delete tag")
		}

		if err := s.client.Queries.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
			Name: utils.NormalizeTag(tag.Name),
			Kind: DeletedTagKindBlocked,
		}); err != nil {
			s.logger.Error(nil, "tag service: record deleted tag %q: %v", tag.Name, err)
		}

		if err := s.embedder.RemoveFromStore(ctx, []string{tag.Name}); err != nil {
			s.logger.Error(nil, "tag service: remove from store: %v", err)
		}
		results[i] = DeleteResult{ID: id, Status: Deleted}
	}

	return results, nil
}

func (s *Tag) Promote(ctx context.Context, id int64, reason string) (database.Tag, PromoteStatus, error) {
	tag, err := s.Get(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return database.Tag{}, PromoteNotFound, nil
		}
		return database.Tag{}, PromoteNotFound, err
	}
	if tag.PromotedAt.Valid {
		return tag, PromoteAlreadyPromoted, nil
	}

	promoted, err := s.client.Queries.PromoteTags(ctx, []int64{id})
	if err != nil {
		return tag, PromoteNotFound, errs.FromDB(err, "promote tag")
	}
	if promoted == 0 {
		return tag, PromoteAlreadyPromoted, nil
	}

	s.encodeAndAddBatch(ctx, []string{tag.Name})
	s.logger.Info(nil, "tagquality[%s]: promote %q (reason=%s)", contentanalyzer.TagQualityVersion, tag.Name, reason)
	return tag, PromotePromoted, nil
}

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

func (s *Tag) RemoveFromStore(ctx context.Context, names []string) error {
	return s.embedder.RemoveFromStore(ctx, names)
}

func (s *Tag) encodeAndAddBatch(ctx context.Context, names []string) {
	if err := s.embedder.AddToStore(ctx, names); err != nil {
		s.logger.Error(nil, "tag service: add to store: %v", err)
	}
}
