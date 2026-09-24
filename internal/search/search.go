package search

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/textnorm"
)

type Result struct {
	ID             int64
	DocumentID     string
	Title          string
	MD5Checksum    string
	SHA512Checksum string
	FileSize       int64
	PageCount      int32
	WordCount      int32
	CharCount      int32
	Language       string
	DocumentTypeID int64
	CreatedAt      time.Time
	ModifiedAt     time.Time
	OriginalPath   string
	StoragePath    string
	HasThumbnail   bool
	Snippet        string
	Rank           float64
}

type PersonFilter struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type DateRange struct {
	From *string `json:"from"`
	To   *string `json:"to"`
}

type FileSizeRange struct {
	Min *int64 `json:"min"`
	Max *int64 `json:"max"`
}

type Filter struct {
	Query           string         `json:"query"`
	Tags            []string       `json:"tags"`
	People          []PersonFilter `json:"people"`
	DocumentType    string         `json:"document_type"`
	Language        string         `json:"language"`
	DateCreated     *DateRange     `json:"date_created"`
	DateModified    *DateRange     `json:"date_modified"`
	FileSize        *FileSizeRange `json:"file_size"`
	SortBy          string         `json:"sort_by"`
	SortOrder       string         `json:"sort_order"`
	Limit           int32          `json:"limit"`
	Offset          int32          `json:"offset"`
	MissingLanguage bool           `json:"missing_language"`
	MissingType     bool           `json:"missing_type"`
	Untagged        bool           `json:"untagged"`
}

type Engine struct {
	logger  *log.Logger
	queries *database.Queries
}

func NewEngine(logger *log.Logger, queries *database.Queries) *Engine {
	return &Engine{
		logger:  logger,
		queries: queries,
	}
}

func (e *Engine) resolveTagAliases(ctx context.Context, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		n := textnorm.NormalizeTag(name)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		normalized = append(normalized, n)
	}
	if len(normalized) == 0 {
		return nil, nil
	}

	aliases, err := tags.ResolveAliases(ctx, e.queries, normalized)
	if err != nil {
		return nil, fmt.Errorf("resolve tag aliases: %w", err)
	}

	out := make([]string, 0, len(normalized))
	resolved := make(map[string]struct{}, len(normalized))
	for _, name := range normalized {
		if canonical, ok := aliases[name]; ok {
			name = canonical
		}
		if _, ok := resolved[name]; ok {
			continue
		}
		resolved[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

func (e *Engine) Search(ctx context.Context, query string, limit, offset int32) ([]Result, error) {
	query = sanitizeQuery(query)
	if query == "" {
		return nil, nil
	}

	if offset > 0 {
		total, err := e.queries.CountDocumentsStructured(ctx, database.SearchFilter{
			Query:  query,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("tsvector search count: %w", err)
		}
		if int64(offset) >= total {
			return []Result{}, nil
		}
	}

	rows, err := e.queries.SearchDocumentsStructured(ctx, database.SearchFilter{
		Query:  query,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("tsvector search: %w", err)
	}

	results := make([]Result, len(rows))
	for i, r := range rows {
		results[i] = Result{
			ID:             r.ID,
			DocumentID:     r.DocumentID,
			Title:          r.Title,
			MD5Checksum:    r.Md5Checksum,
			SHA512Checksum: r.Sha512Checksum,
			FileSize:       r.FileSize,
			PageCount:      int32(r.PageCount),
			WordCount:      int32(r.WordCount),
			CharCount:      int32(r.CharCount),
			Language:       r.Language,
			DocumentTypeID: r.DocumentTypeID,
			CreatedAt:      r.CreatedAt.Time,
			ModifiedAt:     r.ModifiedAt.Time,
			OriginalPath:   r.OriginalPath,
			StoragePath:    r.StoragePath,
			HasThumbnail:   r.HasThumbnail,
			Snippet:        r.Snippet,
			Rank:           r.Rank,
		}
	}

	return results, nil
}

func (e *Engine) SearchStructured(ctx context.Context, filter Filter) ([]Result, int64, error) {
	tags, err := e.resolveTagAliases(ctx, filter.Tags)
	if err != nil {
		return nil, 0, err
	}

	dbFilter := database.SearchFilter{
		Query:           sanitizeQuery(filter.Query),
		Tags:            tags,
		DocumentType:    filter.DocumentType,
		Language:        filter.Language,
		SortBy:          filter.SortBy,
		SortOrder:       filter.SortOrder,
		Limit:           filter.Limit,
		Offset:          filter.Offset,
		MissingLanguage: filter.MissingLanguage,
		MissingType:     filter.MissingType,
		Untagged:        filter.Untagged,
	}

	for _, p := range filter.People {
		dbFilter.People = append(dbFilter.People, struct{ Name, Type string }{p.Name, p.Type})
	}

	if filter.DateCreated != nil {
		dbFilter.DateCreated = &struct{ From, To *string }{filter.DateCreated.From, filter.DateCreated.To}
	}
	if filter.DateModified != nil {
		dbFilter.DateModified = &struct{ From, To *string }{filter.DateModified.From, filter.DateModified.To}
	}
	if filter.FileSize != nil {
		dbFilter.FileSize = &struct{ Min, Max *int64 }{filter.FileSize.Min, filter.FileSize.Max}
	}

	total, err := e.queries.CountDocumentsStructured(ctx, dbFilter)
	if err != nil {
		return nil, 0, fmt.Errorf("structured search count: %w", err)
	}

	if int64(dbFilter.Offset) >= total {
		return []Result{}, total, nil
	}

	rows, err := e.queries.SearchDocumentsStructured(ctx, dbFilter)
	if err != nil {
		return nil, 0, fmt.Errorf("structured search: %w", err)
	}

	results := make([]Result, len(rows))
	for i, r := range rows {
		results[i] = Result{
			ID:             r.ID,
			DocumentID:     r.DocumentID,
			Title:          r.Title,
			MD5Checksum:    r.Md5Checksum,
			SHA512Checksum: r.Sha512Checksum,
			FileSize:       r.FileSize,
			PageCount:      int32(r.PageCount),
			WordCount:      int32(r.WordCount),
			CharCount:      int32(r.CharCount),
			Language:       r.Language,
			DocumentTypeID: r.DocumentTypeID,
			CreatedAt:      r.CreatedAt.Time,
			ModifiedAt:     r.ModifiedAt.Time,
			OriginalPath:   r.OriginalPath,
			StoragePath:    r.StoragePath,
			HasThumbnail:   r.HasThumbnail,
			Snippet:        r.Snippet,
			Rank:           r.Rank,
		}
	}

	return results, total, nil
}

func sanitizeQuery(q string) string {
	return strings.TrimSpace(q)
}
