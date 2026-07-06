package service

import (
	"context"
	"strings"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TagUpdatePair struct {
	ID   int64
	Name string
}

type Tag struct {
	queries  *database.Queries
	embedder tagmatcher.Embedder
	logger   *utils.Logger
}

func NewTag(queries *database.Queries, logger *utils.Logger, embedder tagmatcher.Embedder) (*Tag, error) {
	return &Tag{
		queries:  queries,
		embedder: embedder,
		logger:   logger,
	}, nil
}

func (s *Tag) Close() error {
	s.embedder.Close()
	return nil
}

func (s *Tag) Get(ctx context.Context, id int64) (database.Tag, error) {
	tag, err := s.queries.GetTag(ctx, id)
	if err != nil {
		return database.Tag{}, errs.FromDB(err, "get tag")
	}
	return tag, nil
}

func (s *Tag) GetByName(ctx context.Context, name string) (database.Tag, error) {
	tag, err := s.queries.GetTagByName(ctx, name)
	if err != nil {
		return database.Tag{}, errs.FromDB(err, "get tag by name")
	}
	return tag, nil
}

func (s *Tag) Count(ctx context.Context) (int64, error) {
	count, err := s.queries.CountTags(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count tags")
	}
	return count, nil
}

func (s *Tag) CountByName(ctx context.Context, prefix string) (int64, error) {
	count, err := s.queries.CountTagsByName(ctx, prefix+"%")
	if err != nil {
		return 0, errs.FromDB(err, "count tags by name")
	}
	return count, nil
}

func (s *Tag) ListAll(ctx context.Context) ([]database.Tag, error) {
	tags, err := s.queries.ListAllTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all tags")
	}
	return tags, nil
}

func (s *Tag) ListWithDocumentCount(ctx context.Context, limit, offset int64) ([]database.ListTagsWithDocumentCountRow, error) {
	tags, err := s.queries.ListTagsWithDocumentCount(ctx, database.ListTagsWithDocumentCountParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list tags with document count")
	}
	return tags, nil
}

func (s *Tag) SearchByNameWithDocumentCount(ctx context.Context, prefix string, limit, offset int64) ([]database.SearchTagsByNameWithDocumentCountRow, error) {
	tags, err := s.queries.SearchTagsByNameWithDocumentCount(ctx, database.SearchTagsByNameWithDocumentCountParams{
		Name:   prefix + "%",
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "search tags with document count")
	}
	return tags, nil
}

func (s *Tag) Create(ctx context.Context, names []string) ([]CreateResult[database.Tag], error) {
	results := make([]CreateResult[database.Tag], len(names))
	var newNames []string
	var newIdx []int

	existingTags, err := s.queries.ListAllTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load existing tags")
	}
	existingMap := make(map[string]database.Tag, len(existingTags))
	for _, t := range existingTags {
		existingMap[t.Name] = t
	}

	for i, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			results[i] = CreateResult[database.Tag]{Status: Invalid}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = CreateResult[database.Tag]{Entity: existing, Status: Conflict}
			continue
		}

		res, err := s.queries.CreateTag(ctx, name)
		if err != nil {
			return nil, errs.FromDB(err, "create tag "+name)
		}

		rows, err := res.RowsAffected()
		if err != nil {
			return nil, errs.FromDB(err, "rows affected for "+name)
		}

		if rows == 0 {
			existing, err := s.queries.GetTagByName(ctx, name)
			if err != nil {
				return nil, errs.FromDB(err, "get tag by name after conflict "+name)
			}
			results[i] = CreateResult[database.Tag]{Entity: existing, Status: Conflict}
			continue
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, errs.FromDB(err, "last insert id for "+name)
		}

		results[i] = CreateResult[database.Tag]{Entity: database.Tag{ID: id, Name: name}, Status: Created}
		newNames = append(newNames, name)
		newIdx = append(newIdx, i)
	}

	if len(newNames) > 0 {
		s.encodeAndAddBatch(ctx, newNames)
		for j, name := range newNames {
			results[newIdx[j]].Entity.Name = name
		}
	}

	return results, nil
}

func (s *Tag) Update(ctx context.Context, pairs []TagUpdatePair) ([]UpdateResult[database.Tag], error) {
	results := make([]UpdateResult[database.Tag], len(pairs))
	var newNames []string
	type rename struct {
		idx     int
		oldName string
	}
	var renames []rename

	allTags, err := s.queries.ListAllTags(ctx)
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
		name := strings.TrimSpace(p.Name)
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

		if err := s.queries.UpdateTag(ctx, database.UpdateTagParams{
			Name: name,
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

		newNames = append(newNames, name)
		renames = append(renames, rename{idx: i, oldName: old.Name})
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
			Name: pairs[r.idx].Name,
		}
		results[r.idx] = UpdateResult[database.Tag]{Entity: updatedTag, Status: Updated}
	}

	return results, nil
}

func (s *Tag) Delete(ctx context.Context, ids []int64) ([]DeleteResult, error) {
	results := make([]DeleteResult, len(ids))

	allTags, err := s.queries.ListAllTags(ctx)
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

		if err := s.queries.DeleteTag(ctx, id); err != nil {
			return nil, errs.FromDB(err, "delete tag")
		}

		if err := s.embedder.RemoveFromStore(ctx, []string{tag.Name}); err != nil {
			s.logger.Error(nil, "tag service: remove from store: %v", err)
		}
		results[i] = DeleteResult{ID: id, Status: Deleted}
	}

	return results, nil
}

func (s *Tag) Consolidate(ctx context.Context, docId string, queries []string) ([]string, error) {
	return s.embedder.Consolidate(ctx, docId, queries)
}

func (s *Tag) encodeAndAddBatch(ctx context.Context, names []string) {
	if err := s.embedder.AddToStore(ctx, names); err != nil {
		s.logger.Error(nil, "tag service: add to store: %v", err)
	}
}
