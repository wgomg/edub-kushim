package people

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

type CreateStatus int

const (
	Created CreateStatus = iota
	Conflict
	Invalid
)

type CreateResult struct {
	People database.People
	Status CreateStatus
}

type UpdatePair struct {
	ID         int64
	Name       string
	NameNative string
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
	People database.People
	Status UpdateStatus
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

type PeopleService struct {
	queries *database.Queries
	logger  *utils.Logger
}

func NewPeopleService(queries *database.Queries, logger *utils.Logger) *PeopleService {
	return &PeopleService{queries: queries, logger: logger}
}

func (s *PeopleService) Get(ctx context.Context, id int64) (database.People, error) {
	p, err := s.queries.GetPeople(ctx, id)
	if err != nil {
		return database.People{}, errs.FromDB(err, "get people")
	}
	return p, nil
}

func (s *PeopleService) GetByName(ctx context.Context, name string) (database.People, error) {
	p, err := s.queries.GetPeopleByName(ctx, name)
	if err != nil {
		return database.People{}, errs.FromDB(err, "get people by name")
	}
	return p, nil
}

func (s *PeopleService) List(ctx context.Context, limit, offset int64) ([]database.People, error) {
	people, err := s.queries.ListPeople(ctx, database.ListPeopleParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, errs.FromDB(err, "list people")
	}
	return people, nil
}

func (s *PeopleService) ListAll(ctx context.Context) ([]database.People, error) {
	people, err := s.queries.ListAllPeople(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all people")
	}
	return people, nil
}

func (s *PeopleService) Search(ctx context.Context, prefix string, limit int64) ([]database.People, error) {
	people, err := s.queries.SearchPeopleByName(ctx, database.SearchPeopleByNameParams{
		Name:  prefix + "%",
		Limit: limit,
	})
	if err != nil {
		return nil, errs.FromDB(err, "search people")
	}
	return people, nil
}

func toNullString(s string) sql.NullString {
	if trimmed := strings.TrimSpace(s); trimmed != "" {
		return sql.NullString{String: trimmed, Valid: true}
	}
	return sql.NullString{}
}

func (s *PeopleService) Create(ctx context.Context, inputs []CreatePersonInput) ([]CreateResult, error) {
	results := make([]CreateResult, len(inputs))

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
			results[i] = CreateResult{Status: Invalid}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = CreateResult{People: existing, Status: Conflict}
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
			results[i] = CreateResult{People: existing, Status: Conflict}
			continue
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, errs.FromDB(err, "last insert id for "+name)
		}

		results[i] = CreateResult{
			People: database.People{ID: id, Name: name, NameNative: nameNative},
			Status: Created,
		}
	}

	return results, nil
}

func (s *PeopleService) Update(ctx context.Context, pairs []UpdatePair) ([]UpdateResult, error) {
	results := make([]UpdateResult, len(pairs))

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
			results[i] = UpdateResult{Status: UpdateInvalid}
			continue
		}

		old, ok := peopleMap[p.ID]
		if !ok {
			results[i] = UpdateResult{Status: UpdateNotFound}
			continue
		}

		nameNative := toNullString(p.NameNative)

		if name == old.Name && nameNative == old.NameNative {
			results[i] = UpdateResult{People: old, Status: Noop}
			continue
		}

		if existing, ok := nameMap[name]; ok && existing.ID != p.ID {
			results[i] = UpdateResult{People: existing, Status: UpdateConflict}
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
				results[i] = UpdateResult{People: existing, Status: UpdateConflict}
				continue
			}
			return nil, errs.FromDB(err, "update people")
		}

		results[i] = UpdateResult{
			People: database.People{ID: p.ID, Name: name, NameNative: nameNative},
			Status: Updated,
		}
	}

	return results, nil
}

func (s *PeopleService) Delete(ctx context.Context, ids []int64) ([]DeleteResult, error) {
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

type PeopleTypeService struct {
	queries *database.Queries
	logger  *utils.Logger
}

type CreatePeopleTypeInput struct {
	Name        string
	Description string
}

type PeopleTypeCreateStatus int

const (
	PeopleTypeCreated PeopleTypeCreateStatus = iota
	PeopleTypeConflict
	PeopleTypeInvalid
)

type PeopleTypeCreateResult struct {
	PeopleType database.PeopleType
	Status     PeopleTypeCreateStatus
}

type PeopleTypeUpdatePair struct {
	ID          int64
	Name        string
	Description string
}

type PeopleTypeUpdateStatus int

const (
	PeopleTypeUpdated PeopleTypeUpdateStatus = iota
	PeopleTypeUpdateConflict
	PeopleTypeUpdateNotFound
	PeopleTypeUpdateInvalid
	PeopleTypeNoop
)

type PeopleTypeUpdateResult struct {
	PeopleType database.PeopleType
	Status     PeopleTypeUpdateStatus
}

type PeopleTypeDeleteStatus int

const (
	PeopleTypeDeleted PeopleTypeDeleteStatus = iota
	PeopleTypeDeleteNotFound
	PeopleTypeDeleteConflict
)

type PeopleTypeDeleteResult struct {
	ID     int64
	Status PeopleTypeDeleteStatus
}

func NewPeopleTypeService(queries *database.Queries, logger *utils.Logger) *PeopleTypeService {
	return &PeopleTypeService{queries: queries, logger: logger}
}

func (s *PeopleTypeService) Get(ctx context.Context, id int64) (database.PeopleType, error) {
	pt, err := s.queries.GetPeopleType(ctx, id)
	if err != nil {
		return database.PeopleType{}, errs.FromDB(err, "get people type")
	}
	return pt, nil
}

func (s *PeopleTypeService) GetByName(ctx context.Context, name string) (database.PeopleType, error) {
	pt, err := s.queries.GetPeopleTypeByName(ctx, name)
	if err != nil {
		return database.PeopleType{}, errs.FromDB(err, "get people type by name")
	}
	return pt, nil
}

func (s *PeopleTypeService) List(ctx context.Context, limit, offset int64) ([]database.PeopleType, error) {
	pts, err := s.queries.ListPeopleTypes(ctx, database.ListPeopleTypesParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, errs.FromDB(err, "list people types")
	}
	return pts, nil
}

func (s *PeopleTypeService) ListAll(ctx context.Context) ([]database.PeopleType, error) {
	pts, err := s.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all people types")
	}
	return pts, nil
}

func (s *PeopleTypeService) Search(ctx context.Context, prefix string, limit int64) ([]database.PeopleType, error) {
	pts, err := s.queries.SearchPeopleTypeByName(ctx, database.SearchPeopleTypeByNameParams{
		Name:  prefix + "%",
		Limit: limit,
	})
	if err != nil {
		return nil, errs.FromDB(err, "search people types")
	}
	return pts, nil
}

func (s *PeopleTypeService) Create(ctx context.Context, inputs []CreatePeopleTypeInput) ([]PeopleTypeCreateResult, error) {
	results := make([]PeopleTypeCreateResult, len(inputs))

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
			results[i] = PeopleTypeCreateResult{Status: PeopleTypeInvalid}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = PeopleTypeCreateResult{PeopleType: existing, Status: PeopleTypeConflict}
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
				results[i] = PeopleTypeCreateResult{PeopleType: existing, Status: PeopleTypeConflict}
				continue
			}
			return nil, errs.FromDB(err, "create people type "+name)
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, errs.FromDB(err, "last insert id for "+name)
		}

		results[i] = PeopleTypeCreateResult{
			PeopleType: database.PeopleType{ID: id, Name: name, Description: strings.TrimSpace(input.Description)},
			Status:     PeopleTypeCreated,
		}
	}

	return results, nil
}

func (s *PeopleTypeService) Update(ctx context.Context, pairs []PeopleTypeUpdatePair) ([]PeopleTypeUpdateResult, error) {
	results := make([]PeopleTypeUpdateResult, len(pairs))

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
			results[i] = PeopleTypeUpdateResult{Status: PeopleTypeUpdateInvalid}
			continue
		}

		old, ok := itemMap[p.ID]
		if !ok {
			results[i] = PeopleTypeUpdateResult{Status: PeopleTypeUpdateNotFound}
			continue
		}

		desc := strings.TrimSpace(p.Description)

		if name == old.Name && desc == old.Description {
			results[i] = PeopleTypeUpdateResult{PeopleType: old, Status: PeopleTypeNoop}
			continue
		}

		if existing, ok := nameMap[name]; ok && existing.ID != p.ID {
			results[i] = PeopleTypeUpdateResult{PeopleType: existing, Status: PeopleTypeUpdateConflict}
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
				results[i] = PeopleTypeUpdateResult{PeopleType: existing, Status: PeopleTypeUpdateConflict}
				continue
			}
			return nil, errs.FromDB(err, "update people type")
		}

		results[i] = PeopleTypeUpdateResult{
			PeopleType: database.PeopleType{ID: p.ID, Name: name, Description: desc},
			Status:     PeopleTypeUpdated,
		}
	}

	return results, nil
}

func (s *PeopleTypeService) Delete(ctx context.Context, ids []int64) ([]PeopleTypeDeleteResult, error) {
	results := make([]PeopleTypeDeleteResult, len(ids))

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
			results[i] = PeopleTypeDeleteResult{ID: id, Status: PeopleTypeDeleteNotFound}
			continue
		}

		if err := s.queries.DeletePeopleType(ctx, id); err != nil {
			if errs.IsForeignKey(err) {
				results[i] = PeopleTypeDeleteResult{ID: id, Status: PeopleTypeDeleteConflict}
				continue
			}
			return nil, errs.FromDB(err, "delete people type")
		}

		results[i] = PeopleTypeDeleteResult{ID: id, Status: PeopleTypeDeleted}
	}

	return results, nil
}
