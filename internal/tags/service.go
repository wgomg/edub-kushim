package tags

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/cache"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const batchSize = 32

type CreateStatus int

const (
	Created CreateStatus = iota
	Conflict
	Invalid
)

type CreateResult struct {
	Tag    database.Tag
	Status CreateStatus
}

type UpdateStatus int

const (
	Updated UpdateStatus = iota
	UpdateConflict
	UpdateNotFound
	UpdateInvalid
	Noop
)

type UpdateResult struct {
	Tag    database.Tag
	Status UpdateStatus
}

type UpdatePair struct {
	ID   int64
	Name string
}

type DeleteStatus int

const (
	Deleted DeleteStatus = iota
	DeleteNotFound
)

type DeleteResult struct {
	ID     int64
	Status DeleteStatus
}

var (
	ErrNotFound = errors.New("tag not found")
)

type TagService struct {
	queries *database.Queries
	store   *cache.EmbeddingStore
	encoder tagmatcher.TagMatcher
	logger  *utils.Logger
}

func NewTagService(queries *database.Queries, store *cache.EmbeddingStore, logger *utils.Logger, tmCfg config.TagMatcherConfig) (*TagService, error) {
	encoder, err := tagmatcher.NewTagMatcher(logger, tmCfg)
	if err != nil {
		return nil, fmt.Errorf("new tag matcher: %w", err)
	}
	return &TagService{
		queries: queries,
		store:   store,
		encoder: encoder,
		logger:  logger,
	}, nil
}

func (s *TagService) Close() error {
	s.encoder.Close()
	return nil
}

func (s *TagService) Get(ctx context.Context, id int64) (database.Tag, error) {
	tag, err := s.queries.GetTag(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return database.Tag{}, ErrNotFound
		}
		return database.Tag{}, fmt.Errorf("get tag: %w", err)
	}
	return tag, nil
}

func (s *TagService) GetByName(ctx context.Context, name string) (database.Tag, error) {
	tag, err := s.queries.GetTagByName(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return database.Tag{}, ErrNotFound
		}
		return database.Tag{}, fmt.Errorf("get tag by name: %w", err)
	}
	return tag, nil
}

func (s *TagService) List(ctx context.Context, limit, offset int64) ([]database.Tag, error) {
	tags, err := s.queries.ListTags(ctx, database.ListTagsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	return tags, nil
}

func (s *TagService) ListAll(ctx context.Context) ([]database.Tag, error) {
	tags, err := s.queries.ListAllTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all tags: %w", err)
	}
	return tags, nil
}

func (s *TagService) Search(ctx context.Context, prefix string, limit int64) ([]database.Tag, error) {
	tags, err := s.queries.SearchTagsByName(ctx, database.SearchTagsByNameParams{
		Name:  prefix + "%",
		Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("search tags: %w", err)
	}
	return tags, nil
}

func (s *TagService) Create(ctx context.Context, names []string) ([]CreateResult, error) {
	results := make([]CreateResult, len(names))
	var newNames []string
	var newIdx []int

	existingTags, err := s.queries.ListAllTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("load existing tags: %w", err)
	}
	existingMap := make(map[string]database.Tag, len(existingTags))
	for _, t := range existingTags {
		existingMap[t.Name] = t
	}

	for i, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			results[i] = CreateResult{Status: Invalid}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = CreateResult{Tag: existing, Status: Conflict}
			continue
		}

		res, err := s.queries.CreateTag(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("create tag %q: %w", name, err)
		}

		rows, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("rows affected for %q: %w", name, err)
		}

		if rows == 0 {
			existing, err := s.queries.GetTagByName(ctx, name)
			if err != nil {
				return nil, fmt.Errorf("get tag by name after conflict %q: %w", name, err)
			}
			results[i] = CreateResult{Tag: existing, Status: Conflict}
			continue
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("last insert id for %q: %w", name, err)
		}

		results[i] = CreateResult{Tag: database.Tag{ID: id, Name: name}, Status: Created}
		newNames = append(newNames, name)
		newIdx = append(newIdx, i)
	}

	if len(newNames) > 0 {
		s.encodeAndAddBatch(ctx, newNames)
		for j, name := range newNames {
			results[newIdx[j]].Tag.Name = name
		}
	}

	return results, nil
}

func (s *TagService) Update(ctx context.Context, pairs []UpdatePair) ([]UpdateResult, error) {
	results := make([]UpdateResult, len(pairs))
	var newNames []string
	type rename struct {
		idx     int
		oldName string
	}
	var renames []rename

	allTags, err := s.queries.ListAllTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("load all tags: %w", err)
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
			results[i] = UpdateResult{Status: UpdateInvalid}
			continue
		}

		old, ok := tagMap[p.ID]
		if !ok {
			results[i] = UpdateResult{Status: UpdateNotFound}
			continue
		}

		if name == old.Name {
			results[i] = UpdateResult{Tag: old, Status: Noop}
			continue
		}

		if existing, ok := nameMap[name]; ok && existing.ID != p.ID {
			results[i] = UpdateResult{Tag: existing, Status: UpdateConflict}
			continue
		}

		if err := s.queries.UpdateTag(ctx, database.UpdateTagParams{
			Name: name,
			ID:   p.ID,
		}); err != nil {
			return nil, fmt.Errorf("update tag %d: %w", p.ID, err)
		}

		newNames = append(newNames, name)
		renames = append(renames, rename{idx: i, oldName: old.Name})
	}

	for _, r := range renames {
		s.store.Remove(r.oldName)
	}

	if len(newNames) > 0 {
		s.encodeAndAddBatch(ctx, newNames)
	}

	for _, r := range renames {
		updatedTag := database.Tag{
			ID:   pairs[r.idx].ID,
			Name: pairs[r.idx].Name,
		}
		results[r.idx] = UpdateResult{Tag: updatedTag, Status: Updated}
	}

	return results, nil
}

func (s *TagService) Delete(ctx context.Context, ids []int64) ([]DeleteResult, error) {
	results := make([]DeleteResult, len(ids))

	allTags, err := s.queries.ListAllTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("load all tags: %w", err)
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
			return nil, fmt.Errorf("delete tag %d: %w", id, err)
		}

		s.store.Remove(tag.Name)
		results[i] = DeleteResult{ID: id, Status: Deleted}
	}

	return results, nil
}

func (s *TagService) Entries() map[string][]float32 {
	return s.store.Entries()
}

func (s *TagService) encodeAndAddBatch(ctx context.Context, names []string) {
	for i := 0; i < len(names); i += batchSize {
		end := min(i+batchSize, len(names))
		chunk := names[i:end]

		vecs, err := s.encoder.Encode(ctx, nil, chunk)
		if err != nil {
			s.logger.Error(nil, "tag service: encode batch %v: %v", chunk, err)
			continue
		}
		for j, name := range chunk {
			if j < len(vecs) && vecs[j] != nil {
				s.store.Add(name, vecs[j])
			}
		}
	}
}
