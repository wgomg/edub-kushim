package database

import (
	"context"
	"fmt"
	"strings"
)

var allowedSortColumns = map[string]string{
	"title":      "title",
	"file_size":  "file_size",
	"created_at": "created_at",
}

type ListDocumentsWithSortParams struct {
	Limit     int64
	Offset    int64
	SortBy    string
	SortOrder string
}

func (q *Queries) ListDocumentsWithSort(ctx context.Context, arg ListDocumentsWithSortParams) ([]ListDocumentsRow, error) {
	col, ok := allowedSortColumns[arg.SortBy]
	if !ok {
		col = "created_at"
	}

	dir := strings.ToUpper(arg.SortOrder)
	if dir != "ASC" && dir != "DESC" {
		dir = "DESC"
	}

	query := fmt.Sprintf(
		`SELECT id, document_id, title, md5_checksum, sha512_checksum, original_type, file_size,
                 page_count, word_count, char_count, language,
                 created_at, modified_at, document_type_id, original_path, storage_path, has_thumbnail
         FROM document WHERE deleted_at IS NULL ORDER BY %s %s LIMIT $1 OFFSET $2`,
		col, dir,
	)

	rows, err := q.db.QueryContext(ctx, query, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ListDocumentsRow
	for rows.Next() {
		var i ListDocumentsRow
		if err := rows.Scan(
			&i.ID,
			&i.DocumentID,
			&i.Title,
			&i.Md5Checksum,
			&i.Sha512Checksum,
			&i.OriginalType,
			&i.FileSize,
			&i.PageCount,
			&i.WordCount,
			&i.CharCount,
			&i.Language,
			&i.CreatedAt,
			&i.ModifiedAt,
			&i.DocumentTypeID,
			&i.OriginalPath,
			&i.StoragePath,
			&i.HasThumbnail,
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
