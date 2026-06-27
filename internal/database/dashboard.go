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

type ActivityEventRow struct {
	EventType       string
	EventTime       string
	Title           string
	PayloadFilePath string
	RefID           string
	BatchID         string
	TaskID          string
}

func (q *Queries) ListActivityTimeline(ctx context.Context) ([]ActivityEventRow, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT 'document_uploaded' AS event_type,
		       d.created_at AS event_time,
		       d.title AS title,
		       '' AS payload_file_path,
		       d.document_id AS ref_id,
		       '' AS batch_id,
		       '' AS task_id
		FROM document d

		UNION ALL

		SELECT CASE WHEN t.status = 'completed' THEN 'task_completed' ELSE 'task_failed' END,
		       t.completed_at,
		       COALESCE(json_extract(t.payload, '$.file_name'), ''),
		       COALESCE(json_extract(t.payload, '$.file_path'), ''),
		       COALESCE(json_extract(t.payload, '$.document_id'), ''),
		       COALESCE(t.batch_id, ''),
		       t.task_id
		FROM task t
		WHERE t.status IN ('completed', 'failed')
		  AND t.completed_at IS NOT NULL

		UNION ALL

		SELECT 'batch_created',
		       b.created_at,
		       b.source,
		       '',
		       b.id,
		       b.id,
		       ''
		FROM batch b

		ORDER BY event_time DESC
		LIMIT 30
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ActivityEventRow
	for rows.Next() {
		var i ActivityEventRow
		if err := rows.Scan(&i.EventType, &i.EventTime, &i.Title, &i.PayloadFilePath, &i.RefID, &i.BatchID, &i.TaskID); err != nil {
			return nil, fmt.Errorf("scan activity event: %w", err)
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
