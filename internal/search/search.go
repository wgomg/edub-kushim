package search

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Result struct {
	ID             int64
	DocumentID     string
	Title          string
	MD5Checksum    string
	SHA512Checksum string
	MimeType       string
	FileSize       int64
	PageCount      int64
	WordCount      int64
	CharCount      int64
	Language       string
	DocumentTypeID int64
	CreatedAt      time.Time
	ModifiedAt     time.Time
	OriginalPath   string
	StoragePath    string
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
	MimeType        string         `json:"mime_type"`
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
	logger  *utils.Logger
	queries *database.Queries
}

func NewEngine(logger *utils.Logger, queries *database.Queries) *Engine {
	return &Engine{
		logger:  logger,
		queries: queries,
	}
}

func (e *Engine) Search(ctx context.Context, query string, limit, offset int32) ([]Result, error) {
	query = sanitizeQuery(query)
	if query == "" {
		return nil, nil
	}

	rows, err := e.queries.SearchDocumentsFTS(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("fts5 search: %w", err)
	}

	results := make([]Result, len(rows))
	for i, r := range rows {
		results[i] = Result{
			ID:             r.ID,
			DocumentID:     r.DocumentID,
			Title:          r.Title,
			MD5Checksum:    r.Md5Checksum,
			SHA512Checksum: r.Sha512Checksum,
			MimeType:       r.MimeType,
			FileSize:       r.FileSize,
			PageCount:      r.PageCount,
			WordCount:      r.WordCount,
			CharCount:      r.CharCount,
			Language:       r.Language,
			DocumentTypeID: r.DocumentTypeID,
			CreatedAt:      r.CreatedAt.Time,
			ModifiedAt:     r.ModifiedAt.Time,
			OriginalPath:   r.OriginalPath,
			StoragePath:    r.StoragePath,
			Snippet:        r.Snippet,
			Rank:           r.Rank,
		}
	}

	return results, nil
}

func (e *Engine) SearchStructured(ctx context.Context, filter Filter) ([]Result, int64, error) {
	dbFilter := database.SearchFilter{
		Query:           sanitizeQuery(filter.Query),
		Tags:            filter.Tags,
		DocumentType:    filter.DocumentType,
		Language:        filter.Language,
		MimeType:        filter.MimeType,
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
			MimeType:       r.MimeType,
			FileSize:       r.FileSize,
			PageCount:      r.PageCount,
			WordCount:      r.WordCount,
			CharCount:      r.CharCount,
			Language:       r.Language,
			DocumentTypeID: r.DocumentTypeID,
			CreatedAt:      r.CreatedAt.Time,
			ModifiedAt:     r.ModifiedAt.Time,
			OriginalPath:   r.OriginalPath,
			StoragePath:    r.StoragePath,
			Snippet:        r.Snippet,
			Rank:           r.Rank,
		}
	}

	return results, total, nil
}

func sanitizeQuery(q string) string {
	if q == "" {
		return q
	}
	escaped := strings.ReplaceAll(q, `"`, `""`)
	return `"` + escaped + `"`
}
