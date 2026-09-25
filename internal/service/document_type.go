package service

import (
	"context"
	"strings"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/log"
)

// CreateDocumentTypeInput is one document type to create.
type CreateDocumentTypeInput struct {
	Name        string
	Description string
}

// DocTypeUpdatePair is one document-type rename: id plus the new name
// and description.
type DocTypeUpdatePair struct {
	ID          int64
	Name        string
	Description string
}

// DocumentType is the document-type taxonomy CRUD.
type DocumentType struct {
	queries *database.Queries
	logger  *log.Logger
}

// NewDocumentType wires the document-type service.
func NewDocumentType(queries *database.Queries, logger *log.Logger) *DocumentType {
	return &DocumentType{queries: queries, logger: logger}
}

// Get returns one document type by id.
func (s *DocumentType) Get(ctx context.Context, id int64) (database.DocumentType, error) {
	dt, err := s.queries.GetDocumentType(ctx, id)
	if err != nil {
		return database.DocumentType{}, errs.FromDB(err, "get document type")
	}
	return dt, nil
}

// GetByName returns one document type by exact name.
func (s *DocumentType) GetByName(ctx context.Context, name string) (database.DocumentType, error) {
	dt, err := s.queries.GetDocumentTypeByName(ctx, name)
	if err != nil {
		return database.DocumentType{}, errs.FromDB(err, "get document type by name")
	}
	return dt, nil
}

// ListWithDocumentCount pages the types with their document counts.
func (s *DocumentType) ListWithDocumentCount(ctx context.Context, limit, offset int32) ([]database.ListDocumentTypesWithDocumentCountRow, error) {
	dts, err := s.queries.ListDocumentTypesWithDocumentCount(ctx, database.ListDocumentTypesWithDocumentCountParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list document types with document count")
	}
	return dts, nil
}

// SearchByNameWithDocumentCount returns the types whose names start with
// the prefix, with document counts.
func (s *DocumentType) SearchByNameWithDocumentCount(ctx context.Context, prefix string, limit int32) ([]database.SearchDocumentTypeByNameWithDocumentCountRow, error) {
	dts, err := s.queries.SearchDocumentTypeByNameWithDocumentCount(ctx, database.SearchDocumentTypeByNameWithDocumentCountParams{
		Name:  prefix + "%",
		Limit: limit,
	})
	if err != nil {
		return nil, errs.FromDB(err, "search document types with document count")
	}
	return dts, nil
}

// ListAllWithDocumentCount returns every type with document counts.
func (s *DocumentType) ListAllWithDocumentCount(ctx context.Context) ([]database.ListAllDocumentTypesWithDocumentCountRow, error) {
	dts, err := s.queries.ListAllDocumentTypesWithDocumentCount(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all document types with document count")
	}
	return dts, nil
}

// Create creates document types in bulk, deduplicating by normalized
// name: conflicts resolve to the existing type, empty names are invalid.
func (s *DocumentType) Create(ctx context.Context, inputs []CreateDocumentTypeInput) ([]CreateResult[database.DocumentType], error) {
	results := make([]CreateResult[database.DocumentType], len(inputs))

	existing, err := s.queries.ListAllDocumentTypes(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load existing document types")
	}
	existingMap := make(map[string]database.DocumentType, len(existing))
	for _, dt := range existing {
		existingMap[dt.Name] = dt
	}

	for i, input := range inputs {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			results[i] = CreateResult[database.DocumentType]{Status: Invalid}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = CreateResult[database.DocumentType]{Entity: existing, Status: Conflict}
			continue
		}

		id, err := s.queries.CreateDocumentTypeFull(ctx, database.CreateDocumentTypeFullParams{
			Name:        name,
			Description: strings.TrimSpace(input.Description),
		})
		if err != nil {
			if errs.IsConstraint(err) {
				existing, err := s.queries.GetDocumentTypeByName(ctx, name)
				if err != nil {
					return nil, errs.FromDB(err, "get document type by name after conflict "+name)
				}
				results[i] = CreateResult[database.DocumentType]{Entity: existing, Status: Conflict}
				continue
			}
			return nil, errs.FromDB(err, "create document type "+name)
		}

		results[i] = CreateResult[database.DocumentType]{
			Entity: database.DocumentType{ID: id, Name: name, Description: strings.TrimSpace(input.Description)},
			Status: Created,
		}
	}

	return results, nil
}

// Update renames document types in bulk, one result per pair.
func (s *DocumentType) Update(ctx context.Context, pairs []DocTypeUpdatePair) ([]UpdateResult[database.DocumentType], error) {
	results := make([]UpdateResult[database.DocumentType], len(pairs))

	all, err := s.queries.ListAllDocumentTypes(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load all document types")
	}
	itemMap := make(map[int64]database.DocumentType, len(all))
	nameMap := make(map[string]database.DocumentType, len(all))
	for _, dt := range all {
		itemMap[dt.ID] = dt
		nameMap[dt.Name] = dt
	}

	for i, p := range pairs {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			results[i] = UpdateResult[database.DocumentType]{Status: UpdateInvalid}
			continue
		}

		old, ok := itemMap[p.ID]
		if !ok {
			results[i] = UpdateResult[database.DocumentType]{Status: UpdateNotFound}
			continue
		}

		desc := strings.TrimSpace(p.Description)

		if name == old.Name && desc == old.Description {
			results[i] = UpdateResult[database.DocumentType]{Entity: old, Status: Noop}
			continue
		}

		if existing, ok := nameMap[name]; ok && existing.ID != p.ID {
			results[i] = UpdateResult[database.DocumentType]{Entity: existing, Status: UpdateConflict}
			continue
		}

		if err := s.queries.UpdateDocumentTypeFull(ctx, database.UpdateDocumentTypeFullParams{
			Name:        name,
			Description: desc,
			ID:          p.ID,
		}); err != nil {
			if errs.IsConstraint(err) {
				var existing database.DocumentType
				if e, ok := nameMap[name]; ok {
					existing = e
				}
				results[i] = UpdateResult[database.DocumentType]{Entity: existing, Status: UpdateConflict}
				continue
			}
			return nil, errs.FromDB(err, "update document type")
		}

		results[i] = UpdateResult[database.DocumentType]{
			Entity: database.DocumentType{ID: p.ID, Name: name, Description: desc},
			Status: Updated,
		}
	}

	return results, nil
}

// Delete removes document types in bulk, reporting per id (deleted, not
// found, or conflicting with referenced documents).
func (s *DocumentType) Delete(ctx context.Context, ids []int64) ([]DeleteResult, error) {
	results := make([]DeleteResult, len(ids))

	all, err := s.queries.ListAllDocumentTypes(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load all document types")
	}
	itemMap := make(map[int64]database.DocumentType, len(all))
	for _, dt := range all {
		itemMap[dt.ID] = dt
	}

	for i, id := range ids {
		if _, ok := itemMap[id]; !ok {
			results[i] = DeleteResult{ID: id, Status: DeleteNotFound}
			continue
		}

		if err := s.queries.DeleteDocumentType(ctx, id); err != nil {
			if errs.IsForeignKey(err) {
				results[i] = DeleteResult{ID: id, Status: DeleteConflict}
				continue
			}
			return nil, errs.FromDB(err, "delete document type")
		}

		results[i] = DeleteResult{ID: id, Status: Deleted}
	}

	return results, nil
}
