package service

import (
	"context"
	"strings"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type CreatePeopleTypeInput struct {
	Name        string
	Description string
}

type PeopleTypeUpdatePair struct {
	ID          int64
	Name        string
	Description string
}

type PeopleType struct {
	queries *database.Queries
	logger  *utils.Logger
}

func NewPeopleType(queries *database.Queries, logger *utils.Logger) *PeopleType {
	return &PeopleType{queries: queries, logger: logger}
}

func (s *PeopleType) Get(ctx context.Context, id int64) (database.PeopleType, error) {
	pt, err := s.queries.GetPeopleType(ctx, id)
	if err != nil {
		return database.PeopleType{}, errs.FromDB(err, "get people type")
	}
	return pt, nil
}

func (s *PeopleType) GetByName(ctx context.Context, name string) (database.PeopleType, error) {
	pt, err := s.queries.GetPeopleTypeByName(ctx, name)
	if err != nil {
		return database.PeopleType{}, errs.FromDB(err, "get people type by name")
	}
	return pt, nil
}

func (s *PeopleType) List(ctx context.Context, limit, offset int32) ([]database.PeopleType, error) {
	pts, err := s.queries.ListPeopleTypes(ctx, database.ListPeopleTypesParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, errs.FromDB(err, "list people types")
	}
	return pts, nil
}

func (s *PeopleType) ListAll(ctx context.Context) ([]database.PeopleType, error) {
	pts, err := s.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all people types")
	}
	return pts, nil
}

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

func (s *PeopleType) Create(ctx context.Context, inputs []CreatePeopleTypeInput) ([]CreateResult[database.PeopleType], error) {
	results := make([]CreateResult[database.PeopleType], len(inputs))

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
			results[i] = CreateResult[database.PeopleType]{Status: Invalid}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = CreateResult[database.PeopleType]{Entity: existing, Status: Conflict}
			continue
		}

		res, err := s.queries.CreatePeopleType(ctx, database.CreatePeopleTypeParams{
			Name:        name,
			Description: strings.TrimSpace(input.Description),
		})
		if err != nil {
			if errs.IsConstraint(err) {
				existing, err := s.queries.GetPeopleTypeByName(ctx, name)
				if err != nil {
					return nil, errs.FromDB(err, "get people type by name after conflict "+name)
				}
				results[i] = CreateResult[database.PeopleType]{Entity: existing, Status: Conflict}
				continue
			}
			return nil, errs.FromDB(err, "create people type "+name)
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, errs.FromDB(err, "last insert id for "+name)
		}

		results[i] = CreateResult[database.PeopleType]{
			Entity: database.PeopleType{ID: id, Name: name, Description: strings.TrimSpace(input.Description)},
			Status: Created,
		}
	}

	return results, nil
}

func (s *PeopleType) Update(ctx context.Context, pairs []PeopleTypeUpdatePair) ([]UpdateResult[database.PeopleType], error) {
	results := make([]UpdateResult[database.PeopleType], len(pairs))

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
			results[i] = UpdateResult[database.PeopleType]{Status: UpdateInvalid}
			continue
		}

		old, ok := itemMap[p.ID]
		if !ok {
			results[i] = UpdateResult[database.PeopleType]{Status: UpdateNotFound}
			continue
		}

		desc := strings.TrimSpace(p.Description)

		if name == old.Name && desc == old.Description {
			results[i] = UpdateResult[database.PeopleType]{Entity: old, Status: Noop}
			continue
		}

		if existing, ok := nameMap[name]; ok && existing.ID != p.ID {
			results[i] = UpdateResult[database.PeopleType]{Entity: existing, Status: UpdateConflict}
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
				results[i] = UpdateResult[database.PeopleType]{Entity: existing, Status: UpdateConflict}
				continue
			}
			return nil, errs.FromDB(err, "update people type")
		}

		results[i] = UpdateResult[database.PeopleType]{
			Entity: database.PeopleType{ID: p.ID, Name: name, Description: desc},
			Status: Updated,
		}
	}

	return results, nil
}

func (s *PeopleType) Delete(ctx context.Context, ids []int64) ([]DeleteResult, error) {
	results := make([]DeleteResult, len(ids))

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
			results[i] = DeleteResult{ID: id, Status: DeleteNotFound}
			continue
		}

		if err := s.queries.DeletePeopleType(ctx, id); err != nil {
			if errs.IsForeignKey(err) {
				results[i] = DeleteResult{ID: id, Status: DeleteConflict}
				continue
			}
			return nil, errs.FromDB(err, "delete people type")
		}

		results[i] = DeleteResult{ID: id, Status: Deleted}
	}

	return results, nil
}
