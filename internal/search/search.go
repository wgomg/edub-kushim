package search

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Result struct {
	DocumentID     int64
	Title          string
	MD5Checksum    string
	SHA512Checksum string
	MimeType       string
	FileSize       int64
	CreatedAt      time.Time
	ModifiedAt     time.Time
	OriginalPath   string
	StoragePath    string
	Snippet        string
	Rank           float64
}

type Engine struct {
	logger  *utils.Logger
	queries *database.Queries
}

func NewEngine(logger *utils.Logger, db *sql.DB) *Engine {
	return &Engine{
		logger:  logger,
		queries: database.NewQueries(db),
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
			DocumentID:     r.ID,
			Title:          r.Title,
			MD5Checksum:    r.Md5Checksum,
			SHA512Checksum: r.Sha512Checksum,
			MimeType:       r.MimeType,
			FileSize:       r.FileSize,
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

func sanitizeQuery(q string) string {
	if q == "" {
		return q
	}
	escaped := strings.ReplaceAll(q, `"`, `""`)
	return `"` + escaped + `"`
}
