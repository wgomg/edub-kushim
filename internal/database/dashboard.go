package database

import (
	"context"
	"fmt"
)

type MimeTypeBreakdownRow struct {
	MimeType   string
	Count      int64
	TotalBytes int64
}

type StorageTrendDailyRow struct {
	Day        string
	Count      int64
	DailyBytes int64
}

func (q *Queries) MimeTypeBreakdown(ctx context.Context) ([]MimeTypeBreakdownRow, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT mime_type, COUNT(*) as count, CAST(COALESCE(SUM(file_size), 0) AS INTEGER) AS total_bytes FROM document GROUP BY mime_type ORDER BY total_bytes DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MimeTypeBreakdownRow
	for rows.Next() {
		var i MimeTypeBreakdownRow
		if err := rows.Scan(&i.MimeType, &i.Count, &i.TotalBytes); err != nil {
			return nil, fmt.Errorf("scan mime type breakdown: %w", err)
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

func (q *Queries) StorageTrendDaily(ctx context.Context) ([]StorageTrendDailyRow, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT date(created_at) as day, COUNT(*) as count, CAST(COALESCE(SUM(file_size), 0) AS INTEGER) AS daily_bytes FROM document GROUP BY day ORDER BY day`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []StorageTrendDailyRow
	for rows.Next() {
		var i StorageTrendDailyRow
		if err := rows.Scan(&i.Day, &i.Count, &i.DailyBytes); err != nil {
			return nil, fmt.Errorf("scan storage trend daily: %w", err)
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

type DocumentAggregatesRow struct {
	TotalFiles int64
	TotalBytes int64
	TotalPages int64
	TotalWords int64
}

func (q *Queries) DocumentAggregates(ctx context.Context) (DocumentAggregatesRow, error) {
	var r DocumentAggregatesRow
	err := q.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
			CAST(COALESCE(SUM(file_size), 0) AS INTEGER),
			CAST(COALESCE(SUM(page_count), 0) AS INTEGER),
			CAST(COALESCE(SUM(word_count), 0) AS INTEGER)
		FROM document`,
	).Scan(&r.TotalFiles, &r.TotalBytes, &r.TotalPages, &r.TotalWords)
	return r, err
}
