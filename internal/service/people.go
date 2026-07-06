package service

import (
	"context"
	"database/sql"
	"strings"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type CreatePersonInput struct {
	Name       string
	NameNative string
}

type PeopleUpdatePair struct {
	ID         int64
	Name       string
	NameNative string
}

type People struct {
	queries *database.Queries
	logger  *utils.Logger
}

func NewPeople(queries *database.Queries, logger *utils.Logger) *People {
	return &People{queries: queries, logger: logger}
}

func (s *People) Get(ctx context.Context, id int64) (database.People, error) {
	p, err := s.queries.GetPeople(ctx, id)
	if err != nil {
		return database.People{}, errs.FromDB(err, "get people")
	}
	return p, nil
}

func (s *People) GetByName(ctx context.Context, name string) (database.People, error) {
	p, err := s.queries.GetPeopleByName(ctx, name)
	if err != nil {
		return database.People{}, errs.FromDB(err, "get people by name")
	}
	return p, nil
}

func (s *People) ListAll(ctx context.Context) ([]database.People, error) {
	people, err := s.queries.ListAllPeople(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all people")
	}
	return people, nil
}

func (s *People) ListWithDocumentCount(ctx context.Context, limit, offset int64) ([]database.ListPeopleWithDocumentCountRow, error) {
	people, err := s.queries.ListPeopleWithDocumentCount(ctx, database.ListPeopleWithDocumentCountParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list people with document count")
	}
	return people, nil
}

func (s *People) SearchByNameWithDocumentCount(ctx context.Context, prefix string, limit int64) ([]database.SearchPeopleByNameWithDocumentCountRow, error) {
	people, err := s.queries.SearchPeopleByNameWithDocumentCount(ctx, database.SearchPeopleByNameWithDocumentCountParams{
		Name:  prefix + "%",
		Limit: limit,
	})
	if err != nil {
		return nil, errs.FromDB(err, "search people with document count")
	}
	return people, nil
}

func toNullString(s string) sql.NullString {
	if trimmed := strings.TrimSpace(s); trimmed != "" {
		return sql.NullString{String: trimmed, Valid: true}
	}
	return sql.NullString{}
}

func (s *People) Create(ctx context.Context, inputs []CreatePersonInput) ([]CreateResult[database.People], error) {
	results := make([]CreateResult[database.People], len(inputs))

	existing, err := s.queries.ListAllPeople(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load existing people")
	}
	existingMap := make(map[string]database.People, len(existing))
	for _, p := range existing {
		existingMap[p.Name] = p
	}

	for i, input := range inputs {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			results[i] = CreateResult[database.People]{Status: Invalid}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = CreateResult[database.People]{Entity: existing, Status: Conflict}
			continue
		}

		nameNative := toNullString(input.NameNative)

		res, err := s.queries.CreatePeople(ctx, database.CreatePeopleParams{
			Name:       name,
			NameNative: nameNative,
		})
		if err != nil {
			return nil, errs.FromDB(err, "create people "+name)
		}

		rows, err := res.RowsAffected()
		if err != nil {
			return nil, errs.FromDB(err, "rows affected for "+name)
		}

		if rows == 0 {
			existing, err := s.queries.GetPeopleByName(ctx, name)
			if err != nil {
				return nil, errs.FromDB(err, "get people by name after conflict "+name)
			}
			results[i] = CreateResult[database.People]{Entity: existing, Status: Conflict}
			continue
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, errs.FromDB(err, "last insert id for "+name)
		}

		results[i] = CreateResult[database.People]{
			Entity: database.People{ID: id, Name: name, NameNative: nameNative},
			Status: Created,
		}
	}

	return results, nil
}

func (s *People) Update(ctx context.Context, pairs []PeopleUpdatePair) ([]UpdateResult[database.People], error) {
	results := make([]UpdateResult[database.People], len(pairs))

	allPeople, err := s.queries.ListAllPeople(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load all people")
	}
	peopleMap := make(map[int64]database.People, len(allPeople))
	nameMap := make(map[string]database.People, len(allPeople))
	for _, p := range allPeople {
		peopleMap[p.ID] = p
		nameMap[p.Name] = p
	}

	for i, p := range pairs {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			results[i] = UpdateResult[database.People]{Status: UpdateInvalid}
			continue
		}

		old, ok := peopleMap[p.ID]
		if !ok {
			results[i] = UpdateResult[database.People]{Status: UpdateNotFound}
			continue
		}

		nameNative := toNullString(p.NameNative)

		if name == old.Name && nameNative == old.NameNative {
			results[i] = UpdateResult[database.People]{Entity: old, Status: Noop}
			continue
		}

		if existing, ok := nameMap[name]; ok && existing.ID != p.ID {
			results[i] = UpdateResult[database.People]{Entity: existing, Status: UpdateConflict}
			continue
		}

		if err := s.queries.UpdatePeopleFull(ctx, database.UpdatePeopleFullParams{
			Name:       name,
			NameNative: nameNative,
			ID:         p.ID,
		}); err != nil {
			if errs.IsConstraint(err) {
				var existing database.People
				if e, ok := nameMap[name]; ok {
					existing = e
				}
				results[i] = UpdateResult[database.People]{Entity: existing, Status: UpdateConflict}
				continue
			}
			return nil, errs.FromDB(err, "update people")
		}

		results[i] = UpdateResult[database.People]{
			Entity: database.People{ID: p.ID, Name: name, NameNative: nameNative},
			Status: Updated,
		}
	}

	return results, nil
}

func (s *People) Delete(ctx context.Context, ids []int64) ([]DeleteResult, error) {
	results := make([]DeleteResult, len(ids))

	allPeople, err := s.queries.ListAllPeople(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load all people")
	}
	peopleMap := make(map[int64]database.People, len(allPeople))
	for _, p := range allPeople {
		peopleMap[p.ID] = p
	}

	for i, id := range ids {
		if _, ok := peopleMap[id]; !ok {
			results[i] = DeleteResult{ID: id, Status: DeleteNotFound}
			continue
		}

		if err := s.queries.DeletePeople(ctx, id); err != nil {
			return nil, errs.FromDB(err, "delete people")
		}

		results[i] = DeleteResult{ID: id, Status: Deleted}
	}

	return results, nil
}
