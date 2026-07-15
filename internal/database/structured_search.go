package database

import (
	"context"
	"fmt"
	"strings"
)

type SearchFilter struct {
	Query           string
	Tags            []string
	People          []struct{ Name, Type string }
	DocumentType    string
	Language        string
	MimeType        string
	DateCreated     *struct{ From, To *string }
	DateModified    *struct{ From, To *string }
	FileSize        *struct{ Min, Max *int64 }
	SortBy          string
	SortOrder       string
	Limit           int32
	Offset          int32
	MissingLanguage bool
	MissingType     bool
	Untagged        bool
}

type queryBuilder struct {
	clauses []string
	args    []any
}

func (b *queryBuilder) nextIndex() int {
	return len(b.args) + 1
}

func (b *queryBuilder) add(clause string, args ...any) {
	b.clauses = append(b.clauses, clause)
	b.args = append(b.args, args...)
}

func (b *queryBuilder) eq(col, val string) {
	if val == "" {
		return
	}
	b.add(fmt.Sprintf("AND d.%s = $%d", col, b.nextIndex()), val)
}

func (b *queryBuilder) subqueryIn(col, subquery string, values []string) {
	if len(values) == 0 {
		return
	}
	placeholders := make([]string, len(values))
	for i := range values {
		placeholders[i] = fmt.Sprintf("$%d", b.nextIndex()+i)
	}
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	b.add(fmt.Sprintf("AND d.%s IN (%s)", col, fmt.Sprintf(subquery, strings.Join(placeholders, ","))), args...)
}

func (b *queryBuilder) addMissingFilters(filter SearchFilter) {
	if filter.MissingLanguage {
		b.add(`AND (d.language = 'und' OR d.language = '')`)
	}
	if filter.MissingType {
		b.add(`AND d.document_type_id = 1`)
	}
	if filter.Untagged {
		b.add(`AND NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.document_id = d.id)`)
	}
}

func (b *queryBuilder) rangeClause(col string, min, max *int64) {
	if min != nil {
		b.add(fmt.Sprintf("AND d.%s >= $%d", col, b.nextIndex()), *min)
	}
	if max != nil {
		b.add(fmt.Sprintf("AND d.%s <= $%d", col, b.nextIndex()), *max)
	}
}

func (b *queryBuilder) dateRange(col string, r *struct{ From, To *string }) {
	if r == nil {
		return
	}
	if r.From != nil {
		b.add(fmt.Sprintf("AND d.%s >= $%d", col, b.nextIndex()), *r.From)
	}
	if r.To != nil {
		b.add(fmt.Sprintf("AND d.%s <= $%d", col, b.nextIndex()), *r.To)
	}
}

func (q *Queries) SearchDocumentsStructured(ctx context.Context, filter SearchFilter) ([]FTSDocumentRow, error) {
	b := &queryBuilder{}

	selectCols := `SELECT d.id, d.document_id, d.title, d.md5_checksum, d.sha512_checksum,
		d.mime_type, d.file_size, d.page_count, d.word_count, d.char_count,
		d.language, d.created_at, d.modified_at, d.document_type_id,
		d.original_path, d.storage_path, d.text_content`

	if filter.Query != "" {
		b.add(fmt.Sprintf(`%s,
			bm25(document_fts) as rank,
			snippet(document_fts, 1, '<b>', '</b>', '...', 64) as snippet
			FROM document d
			JOIN document_fts ON d.id = document_fts.document_id
			WHERE document_fts MATCH $%d`, selectCols, b.nextIndex()), filter.Query)
	} else {
		b.add(selectCols + `,
			0.0 as rank,
			'' as snippet
			FROM document d
			WHERE 1=1`)
	}

	b.subqueryIn("id",
		`SELECT dt.document_id FROM document_tag dt
		JOIN tag t ON dt.tag_id = t.id
		WHERE t.name IN (%s)`,
		filter.Tags)

	for _, p := range filter.People {
		nameIdx := b.nextIndex()
		typeIdx := b.nextIndex() + 1
		b.add(fmt.Sprintf(`AND d.id IN (SELECT dp.document_id FROM document_people dp
			JOIN people pe ON dp.people_id = pe.id
			JOIN people_type pt ON dp.people_type_id = pt.id
			WHERE pe.name = $%d AND pt.name = $%d)`, nameIdx, typeIdx), p.Name, p.Type)
	}

	if filter.DocumentType != "" {
		b.add(fmt.Sprintf(`AND d.document_type_id = (SELECT id FROM document_type WHERE name = $%d)`, b.nextIndex()), filter.DocumentType)
	}

	b.eq("language", filter.Language)
	b.eq("mime_type", filter.MimeType)
	b.dateRange("created_at", filter.DateCreated)
	b.dateRange("modified_at", filter.DateModified)
	if filter.FileSize != nil {
		b.rangeClause("file_size", filter.FileSize.Min, filter.FileSize.Max)
	}

	b.addMissingFilters(filter)

	if filter.Query != "" {
		b.add(`ORDER BY rank`)
	} else {
		sortCol := "created_at"
		sortDir := "DESC"
		switch filter.SortBy {
		case "title", "mime_type", "file_size", "created_at":
			sortCol = filter.SortBy
		}
		if strings.EqualFold(filter.SortOrder, "asc") {
			sortDir = "ASC"
		}
		b.add(fmt.Sprintf("ORDER BY d.%s %s", sortCol, sortDir))
	}

	b.add(fmt.Sprintf("LIMIT $%d OFFSET $%d", b.nextIndex(), b.nextIndex()+1), filter.Limit, filter.Offset)

	query := strings.Join(b.clauses, " ")

	rows, err := q.db.QueryContext(ctx, query, b.args...)
	if err != nil {
		return nil, fmt.Errorf("structured search: %w", err)
	}
	defer rows.Close()

	var items []FTSDocumentRow
	for rows.Next() {
		var i FTSDocumentRow
		if err := rows.Scan(
			&i.ID, &i.DocumentID, &i.Title,
			&i.Md5Checksum, &i.Sha512Checksum, &i.MimeType,
			&i.FileSize, &i.PageCount, &i.WordCount, &i.CharCount,
			&i.Language, &i.CreatedAt, &i.ModifiedAt,
			&i.DocumentTypeID, &i.OriginalPath, &i.StoragePath,
			&i.TextContent, &i.Rank, &i.Snippet,
		); err != nil {
			return nil, fmt.Errorf("scan structured search row: %w", err)
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

func (q *Queries) CountDocumentsStructured(ctx context.Context, filter SearchFilter) (int64, error) {
	b := &queryBuilder{}

	if filter.Query != "" {
		b.add(fmt.Sprintf(`SELECT COUNT(*) FROM document d
			JOIN document_fts ON d.id = document_fts.document_id
			WHERE document_fts MATCH $%d`, b.nextIndex()), filter.Query)
	} else {
		b.add(`SELECT COUNT(*) FROM document d WHERE 1=1`)
	}

	b.subqueryIn("id",
		`SELECT dt.document_id FROM document_tag dt
		JOIN tag t ON dt.tag_id = t.id
		WHERE t.name IN (%s)`,
		filter.Tags)

	for _, p := range filter.People {
		nameIdx := b.nextIndex()
		typeIdx := b.nextIndex() + 1
		b.add(fmt.Sprintf(`AND d.id IN (SELECT dp.document_id FROM document_people dp
			JOIN people pe ON dp.people_id = pe.id
			JOIN people_type pt ON dp.people_type_id = pt.id
			WHERE pe.name = $%d AND pt.name = $%d)`, nameIdx, typeIdx), p.Name, p.Type)
	}

	if filter.DocumentType != "" {
		b.add(fmt.Sprintf(`AND d.document_type_id = (SELECT id FROM document_type WHERE name = $%d)`, b.nextIndex()), filter.DocumentType)
	}

	b.eq("language", filter.Language)
	b.eq("mime_type", filter.MimeType)
	b.dateRange("created_at", filter.DateCreated)
	b.dateRange("modified_at", filter.DateModified)
	if filter.FileSize != nil {
		b.rangeClause("file_size", filter.FileSize.Min, filter.FileSize.Max)
	}

	b.addMissingFilters(filter)

	query := strings.Join(b.clauses, " ")
	var count int64
	err := q.db.QueryRowContext(ctx, query, b.args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count structured search: %w", err)
	}
	return count, nil
}
