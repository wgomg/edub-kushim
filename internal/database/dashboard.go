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

type DistributionRow struct {
	Label string
	Count int64
}

type MissingCountsRow struct {
	MissingLanguage int64
	MissingType     int64
	MissingTags     int64
}

func (q *Queries) LanguageDistribution(ctx context.Context) ([]DistributionRow, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT language, COUNT(*) as count FROM document WHERE language != 'und' AND language != '' GROUP BY language ORDER BY count DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []DistributionRow
	for rows.Next() {
		var i DistributionRow
		if err := rows.Scan(&i.Label, &i.Count); err != nil {
			return nil, fmt.Errorf("scan language distribution: %w", err)
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

func (q *Queries) DocumentTypeDistribution(ctx context.Context) ([]DistributionRow, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT dt.name, COUNT(*) as count FROM document d JOIN document_type dt ON d.document_type_id = dt.id WHERE d.document_type_id != 1 GROUP BY dt.id, dt.name ORDER BY count DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []DistributionRow
	for rows.Next() {
		var i DistributionRow
		if err := rows.Scan(&i.Label, &i.Count); err != nil {
			return nil, fmt.Errorf("scan document type distribution: %w", err)
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

func (q *Queries) TagFrequency(ctx context.Context) ([]DistributionRow, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT t.name, COUNT(*) as count FROM document_tag dt JOIN tag t ON dt.tag_id = t.id GROUP BY t.id, t.name ORDER BY count DESC LIMIT 10`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []DistributionRow
	for rows.Next() {
		var i DistributionRow
		if err := rows.Scan(&i.Label, &i.Count); err != nil {
			return nil, fmt.Errorf("scan tag frequency: %w", err)
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

func (q *Queries) MissingCounts(ctx context.Context) (MissingCountsRow, error) {
	var r MissingCountsRow
	err := q.db.QueryRowContext(ctx,
		`SELECT
			(SELECT COUNT(*) FROM document WHERE language = 'und' OR language = '') AS missing_language,
			(SELECT COUNT(*) FROM document WHERE document_type_id = 1) AS missing_type,
			(SELECT COUNT(*) FROM document d WHERE NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.document_id = d.id)) AS missing_tags`,
	).Scan(&r.MissingLanguage, &r.MissingType, &r.MissingTags)
	return r, err
}

type TaskSuccessRateRow struct {
	Completed int64
	Failed    int64
}

type AvgTaskDurationMsRow struct {
	AvgDurationMs int64
}

func (q *Queries) TaskSuccessRate(ctx context.Context) (TaskSuccessRateRow, error) {
	var r TaskSuccessRateRow
	err := q.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0) AS completed,
			COALESCE(SUM(CASE WHEN status = 'failed'    THEN 1 ELSE 0 END), 0) AS failed
		FROM task
		WHERE completed_at >= datetime('now', '-7 days')
	`).Scan(&r.Completed, &r.Failed)
	return r, err
}

func (q *Queries) AvgTaskDurationMs(ctx context.Context) (AvgTaskDurationMsRow, error) {
	var r AvgTaskDurationMsRow
	err := q.db.QueryRowContext(ctx, `
		SELECT CAST(COALESCE(AVG(
			(julianday(completed_at) - julianday(started_at)) * 86400000.0
		), 0.0) AS INTEGER) AS avg_duration_ms
		FROM task
		WHERE status = 'completed'
		  AND started_at IS NOT NULL
		  AND completed_at IS NOT NULL
		  AND completed_at >= datetime('now', '-7 days')
	`).Scan(&r.AvgDurationMs)
	return r, err
}

func (q *Queries) ActiveBatchIDs(ctx context.Context) ([]string, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT DISTINCT batch_id
		FROM task
		WHERE batch_id IS NOT NULL
		  AND status IN ('pending', 'processing')
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan active batch id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
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
