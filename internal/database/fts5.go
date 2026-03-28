// This file extends the auto-generated Queries with FTS5 full-text search methods.
// sqlc doesn't support FTS5 virtual tables, so we implement these manually.
package database

import (
	"context"
	"database/sql"
)

// FTSDocumentRow represents a row from the FTS5 search results
type FTSDocumentRow struct {
	ID             int64          `json:"id"`
	Title          string         `json:"title"`
	Md5Checksum    string         `json:"md5_checksum"`
	Sha512Checksum string         `json:"sha512_checksum"`
	MimeType       string         `json:"mime_type"`
	FileSize       int64          `json:"file_size"`
	CreatedAt      sql.NullTime   `json:"created_at"`
	ModifiedAt     sql.NullTime   `json:"modified_at"`
	DocumentTypeID sql.NullInt64  `json:"document_type_id"`
	OriginalPath   string         `json:"original_path"`
	StoragePath    string         `json:"storage_path"`
	TextContent    sql.NullString `json:"text_content"`
	Rank           float64        `json:"rank"`
	Snippet        string         `json:"snippet"`
}

// SearchDocumentsFTS performs a full-text search on the document_fts FTS5 table
func (q *Queries) SearchDocumentsFTS(
	ctx context.Context,
	searchQuery string,
	limit, offset int32,
) ([]FTSDocumentRow, error) {
	const query = `
		SELECT
			d.id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size,
			d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path, d.text_content,
			bm25(document_fts) as rank,
			snippet(document_fts, 2, '<b>', '</b>', '...', 64) as snippet
		FROM document_fts
		JOIN document d ON document_fts.document_id = d.id
		WHERE document_fts MATCH ?
		ORDER BY rank
		LIMIT ? OFFSET ?
	`

	rows, err := q.db.QueryContext(ctx, query, searchQuery, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []FTSDocumentRow
	for rows.Next() {
		var i FTSDocumentRow
		if err := rows.Scan(
			&i.ID,
			&i.Title,
			&i.Md5Checksum,
			&i.Sha512Checksum,
			&i.MimeType,
			&i.FileSize,
			&i.CreatedAt,
			&i.ModifiedAt,
			&i.DocumentTypeID,
			&i.OriginalPath,
			&i.StoragePath,
			&i.TextContent,
			&i.Rank,
			&i.Snippet,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}

	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// SearchDocumentsFTSWithFilters performs a full-text search with MIME type filter
func (q *Queries) SearchDocumentsFTSWithFilters(ctx context.Context, arg struct {
	SearchQuery string
	MimeType    string
	Limit       int32
	Offset      int32
}) ([]FTSDocumentRow, error) {
	const query = `
		SELECT
			d.id, d.title, d.md5_checksum, d.sha512_checksum, d.mime_type, d.file_size,
			d.created_at, d.modified_at, d.document_type_id, d.original_path, d.storage_path, d.text_content,
			bm25(document_fts) as rank,
			snippet(document_fts, 2, '<b>', '</b>', '...', 64) as snippet
		FROM document_fts
		JOIN document d ON document_fts.document_id = d.id
		WHERE document_fts MATCH ?
		  AND d.mime_type = ?
		ORDER BY rank
		LIMIT ? OFFSET ?
	`

	rows, err := q.db.QueryContext(ctx, query, arg.SearchQuery, arg.MimeType, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []FTSDocumentRow
	for rows.Next() {
		var i FTSDocumentRow
		if err := rows.Scan(
			&i.ID,
			&i.Title,
			&i.Md5Checksum,
			&i.Sha512Checksum,
			&i.MimeType,
			&i.FileSize,
			&i.CreatedAt,
			&i.ModifiedAt,
			&i.DocumentTypeID,
			&i.OriginalPath,
			&i.StoragePath,
			&i.TextContent,
			&i.Rank,
			&i.Snippet,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}

	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// GetDocumentFTSContent retrieves the FTS content for a document
func (q *Queries) GetDocumentFTSContent(ctx context.Context, documentID int64) (string, error) {
	const query = `SELECT content FROM document_fts WHERE document_id = ?`

	var content string
	err := q.db.QueryRowContext(ctx, query, documentID).Scan(&content)
	return content, err
}

// UpdateDocumentFTS updates or inserts FTS data for a document
func (q *Queries) UpdateDocumentFTS(ctx context.Context, arg struct {
	DocumentID int64
	Title      string
	Content    string
}) error {
	const query = `INSERT OR REPLACE INTO document_fts(document_id, title, content) VALUES (?, ?, ?)`

	_, err := q.db.ExecContext(ctx, query, arg.DocumentID, arg.Title, arg.Content)
	return err
}

// DeleteDocumentFTS removes FTS data for a document
func (q *Queries) DeleteDocumentFTS(ctx context.Context, documentID int64) error {
	const query = `DELETE FROM document_fts WHERE document_id = ?`

	_, err := q.db.ExecContext(ctx, query, documentID)
	return err
}

// RebuildDocumentFTS rebuilds the FTS5 search index by copying all documents into the document_fts table
func (q *Queries) RebuildDocumentFTS(ctx context.Context) error {
	const query = `
		INSERT OR REPLACE INTO document_fts (document_id, title, content)
		SELECT id, title, COALESCE(text_content, '') FROM document
	`

	_, err := q.db.ExecContext(ctx, query)
	return err
}
