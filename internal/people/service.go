package people

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/database"
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

var (
	ErrNotFound = errors.New("people: not found")
)

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
		if errors.Is(err, sql.ErrNoRows) {
			return database.People{}, ErrNotFound
		}
		return database.People{}, fmt.Errorf("get people: %w", err)
	}
	return p, nil
}

func (s *PeopleService) GetByName(ctx context.Context, name string) (database.People, error) {
	p, err := s.queries.GetPeopleByName(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return database.People{}, ErrNotFound
		}
		return database.People{}, fmt.Errorf("get people by name: %w", err)
	}
	return p, nil
}

func (s *PeopleService) List(ctx context.Context, limit, offset int64) ([]database.People, error) {
	people, err := s.queries.ListPeople(ctx, database.ListPeopleParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	return people, nil
}

func (s *PeopleService) ListAll(ctx context.Context) ([]database.People, error) {
	people, err := s.queries.ListAllPeople(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all people: %w", err)
	}
	return people, nil
}

func (s *PeopleService) Search(ctx context.Context, prefix string, limit int64) ([]database.People, error) {
	people, err := s.queries.SearchPeopleByName(ctx, database.SearchPeopleByNameParams{
		Name:  prefix + "%",
		Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("search people: %w", err)
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
		return nil, fmt.Errorf("load existing people: %w", err)
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
			return nil, fmt.Errorf("create people %q: %w", name, err)
		}

		rows, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("rows affected for %q: %w", name, err)
		}

		if rows == 0 {
			existing, err := s.queries.GetPeopleByName(ctx, name)
			if err != nil {
				return nil, fmt.Errorf("get people by name after conflict %q: %w", name, err)
			}
			results[i] = CreateResult{People: existing, Status: Conflict}
			continue
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("last insert id for %q: %w", name, err)
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
		return nil, fmt.Errorf("load all people: %w", err)
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
			return nil, fmt.Errorf("update people %d: %w", p.ID, err)
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
		return nil, fmt.Errorf("load all people: %w", err)
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
			return nil, fmt.Errorf("delete people %d: %w", id, err)
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
		if errors.Is(err, sql.ErrNoRows) {
			return database.PeopleType{}, ErrNotFound
		}
		return database.PeopleType{}, fmt.Errorf("get people type: %w", err)
	}
	return pt, nil
}

func (s *PeopleTypeService) GetByName(ctx context.Context, name string) (database.PeopleType, error) {
	pt, err := s.queries.GetPeopleTypeByName(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return database.PeopleType{}, ErrNotFound
		}
		return database.PeopleType{}, fmt.Errorf("get people type by name: %w", err)
	}
	return pt, nil
}

func (s *PeopleTypeService) List(ctx context.Context, limit, offset int64) ([]database.PeopleType, error) {
	pts, err := s.queries.ListPeopleTypes(ctx, database.ListPeopleTypesParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("list people types: %w", err)
	}
	return pts, nil
}

func (s *PeopleTypeService) ListAll(ctx context.Context) ([]database.PeopleType, error) {
	pts, err := s.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all people types: %w", err)
	}
	return pts, nil
}

func (s *PeopleTypeService) Search(ctx context.Context, prefix string, limit int64) ([]database.PeopleType, error) {
	pts, err := s.queries.SearchPeopleTypeByName(ctx, database.SearchPeopleTypeByNameParams{
		Name:  prefix + "%",
		Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("search people types: %w", err)
	}
	return pts, nil
}

func (s *PeopleTypeService) Create(ctx context.Context, inputs []CreatePeopleTypeInput) ([]PeopleTypeCreateResult, error) {
	results := make([]PeopleTypeCreateResult, len(inputs))

	existing, err := s.queries.ListAllPeopleTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("load existing people types: %w", err)
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
			return nil, fmt.Errorf("create people type %q: %w", name, err)
		}

		id, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("last insert id for %q: %w", name, err)
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
		return nil, fmt.Errorf("load all people types: %w", err)
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
			return nil, fmt.Errorf("update people type %d: %w", p.ID, err)
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
		return nil, fmt.Errorf("load all people types: %w", err)
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
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				results[i] = PeopleTypeDeleteResult{ID: id, Status: PeopleTypeDeleteConflict}
				continue
			}
			return nil, fmt.Errorf("delete people type %d: %w", id, err)
		}

		results[i] = PeopleTypeDeleteResult{ID: id, Status: PeopleTypeDeleted}
	}

	return results, nil
}
