package service

import (
	"context"
	"strings"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/types"
)

// CreatePeopleTypeInput is one people type to create.
type CreatePeopleTypeInput struct {
	Name        string
	Description string
}

// PeopleTypeUpdatePair is one people-type rename: id plus the new name
// and description.
type PeopleTypeUpdatePair struct {
	ID          int64
	Name        string
	Description string
}

// PeopleType is the people-type taxonomy CRUD.
type PeopleType struct {
	queries *database.Queries
}

// NewPeopleType wires the people-type service.
func NewPeopleType(queries *database.Queries) *PeopleType {
	return &PeopleType{queries: queries}
}

// Get returns one people type by id.
func (s *PeopleType) Get(ctx context.Context, id int64) (database.PeopleType, error) {
	pt, err := s.queries.GetPeopleType(ctx, id)
	if err != nil {
		return database.PeopleType{}, errs.FromDB(err, "get people type")
	}
	return pt, nil
}

// GetByName returns one people type by exact name.
func (s *PeopleType) GetByName(ctx context.Context, name string) (database.PeopleType, error) {
	pt, err := s.queries.GetPeopleTypeByName(ctx, name)
	if err != nil {
		return database.PeopleType{}, errs.FromDB(err, "get people type by name")
	}
	return pt, nil
}

// List pages the people types.
func (s *PeopleType) List(ctx context.Context, limit, offset int32) ([]database.PeopleType, error) {
	pts, err := s.queries.ListPeopleTypes(ctx, database.ListPeopleTypesParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, errs.FromDB(err, "list people types")
	}
	return pts, nil
}

// ListAll returns every people type.
func (s *PeopleType) ListAll(ctx context.Context) ([]database.PeopleType, error) {
	pts, err := s.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all people types")
	}
	return pts, nil
}

// Search returns the people types whose names start with the prefix.
func (s *PeopleType) Search(ctx context.Context, prefix string, limit int32) ([]database.PeopleType, error) {
	pts, err := s.queries.SearchPeopleTypeByName(ctx, database.SearchPeopleTypeByNameParams{
		Name:  prefix + "%",
		Limit: limit,
	})
	if err != nil {
		return nil, errs.FromDB(err, "search people types")
	}
	return pts, nil
}

// Create creates people types in bulk, deduplicating by normalized name:
// conflicts resolve to the existing type, empty names are invalid.
func (s *PeopleType) Create(ctx context.Context, inputs []CreatePeopleTypeInput) ([]types.CreateResult[database.PeopleType], error) {
	results := make([]types.CreateResult[database.PeopleType], len(inputs))

	existing, err := s.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load existing people types")
	}
	existingMap := make(map[string]database.PeopleType, len(existing))
	for _, pt := range existing {
		existingMap[pt.Name] = pt
	}

	for i, input := range inputs {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			results[i] = types.CreateResult[database.PeopleType]{Status: types.Invalid}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = types.CreateResult[database.PeopleType]{Entity: existing, Status: types.Conflict}
			continue
		}

		id, err := s.queries.CreatePeopleType(ctx, database.CreatePeopleTypeParams{
			Name:        name,
			Description: strings.TrimSpace(input.Description),
		})
		if err != nil {
			if errs.IsConstraint(err) {
				existing, err := s.queries.GetPeopleTypeByName(ctx, name)
				if err != nil {
					return nil, errs.FromDB(err, "get people type by name after conflict "+name)
				}
				results[i] = types.CreateResult[database.PeopleType]{Entity: existing, Status: types.Conflict}
				continue
			}
			return nil, errs.FromDB(err, "create people type "+name)
		}

		results[i] = types.CreateResult[database.PeopleType]{
			Entity: database.PeopleType{ID: id, Name: name, Description: strings.TrimSpace(input.Description)},
			Status: types.Created,
		}
	}

	return results, nil
}

// Update renames people types in bulk, one result per pair (conflict,
// not found, no-op, or updated).
func (s *PeopleType) Update(ctx context.Context, pairs []PeopleTypeUpdatePair) ([]types.UpdateResult[database.PeopleType], error) {
	results := make([]types.UpdateResult[database.PeopleType], len(pairs))

	all, err := s.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load all people types")
	}
	itemMap := make(map[int64]database.PeopleType, len(all))
	nameMap := make(map[string]database.PeopleType, len(all))
	for _, pt := range all {
		itemMap[pt.ID] = pt
		nameMap[pt.Name] = pt
	}

	for i, p := range pairs {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			results[i] = types.UpdateResult[database.PeopleType]{Status: types.UpdateInvalid}
			continue
		}

		old, ok := itemMap[p.ID]
		if !ok {
			results[i] = types.UpdateResult[database.PeopleType]{Status: types.UpdateNotFound}
			continue
		}

		desc := strings.TrimSpace(p.Description)

		if name == old.Name && desc == old.Description {
			results[i] = types.UpdateResult[database.PeopleType]{Entity: old, Status: types.Noop}
			continue
		}

		if existing, ok := nameMap[name]; ok && existing.ID != p.ID {
			results[i] = types.UpdateResult[database.PeopleType]{Entity: existing, Status: types.UpdateConflict}
			continue
		}

		if err := s.queries.UpdatePeopleType(ctx, database.UpdatePeopleTypeParams{
			Name:        name,
			Description: desc,
			ID:          p.ID,
		}); err != nil {
			if errs.IsConstraint(err) {
				var existing database.PeopleType
				if e, ok := nameMap[name]; ok {
					existing = e
				}
				results[i] = types.UpdateResult[database.PeopleType]{Entity: existing, Status: types.UpdateConflict}
				continue
			}
			return nil, errs.FromDB(err, "update people type")
		}

		results[i] = types.UpdateResult[database.PeopleType]{
			Entity: database.PeopleType{ID: p.ID, Name: name, Description: desc},
			Status: types.Updated,
		}
	}

	return results, nil
}

// Delete removes people types in bulk, reporting per id (deleted, not
// found, or conflicting with referenced people).
func (s *PeopleType) Delete(ctx context.Context, ids []int64) ([]types.DeleteResult, error) {
	results := make([]types.DeleteResult, len(ids))

	all, err := s.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load all people types")
	}
	itemMap := make(map[int64]database.PeopleType, len(all))
	for _, pt := range all {
		itemMap[pt.ID] = pt
	}

	for i, id := range ids {
		if _, ok := itemMap[id]; !ok {
			results[i] = types.DeleteResult{ID: id, Status: types.DeleteNotFound}
			continue
		}

		if err := s.queries.DeletePeopleType(ctx, id); err != nil {
			if errs.IsForeignKey(err) {
				results[i] = types.DeleteResult{ID: id, Status: types.DeleteConflict}
				continue
			}
			return nil, errs.FromDB(err, "delete people type")
		}

		results[i] = types.DeleteResult{ID: id, Status: types.Deleted}
	}

	return results, nil
}
