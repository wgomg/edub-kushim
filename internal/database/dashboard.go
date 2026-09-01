package database

import (
	"context"
	"fmt"
)

type OriginalTypeBreakdownRow struct {
	OriginalType string
	Count        int64
	TotalBytes   int64
}

type StorageTrendDailyRow struct {
	Day        string
	Count      int64
	DailyBytes int64
}

func (q *Queries) OriginalTypeBreakdown(ctx context.Context) ([]OriginalTypeBreakdownRow, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT original_type, COUNT(*) as count, CAST(COALESCE(SUM(file_size), 0) AS BIGINT) AS total_bytes FROM document WHERE deleted_at IS NULL GROUP BY original_type ORDER BY total_bytes DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []OriginalTypeBreakdownRow
	for rows.Next() {
		var i OriginalTypeBreakdownRow
		if err := rows.Scan(&i.OriginalType, &i.Count, &i.TotalBytes); err != nil {
			return nil, fmt.Errorf("scan original type breakdown: %w", err)
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
		`SELECT date(created_at) as day, COUNT(*) as count, CAST(COALESCE(SUM(file_size), 0) AS BIGINT) AS daily_bytes FROM document WHERE deleted_at IS NULL GROUP BY day ORDER BY day`,
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
	TotalFiles     int64
	TotalBytes     int64
	ProcessedBytes int64
	TotalPages     int64
	TotalWords     int64
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
		`SELECT language, COUNT(*) as count FROM document WHERE language != 'und' AND language != '' AND deleted_at IS NULL GROUP BY language ORDER BY count DESC LIMIT 10`,
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
		`SELECT dt.name, COUNT(*) as count FROM document d JOIN document_type dt ON d.document_type_id = dt.id WHERE d.document_type_id != 1 AND d.deleted_at IS NULL GROUP BY dt.id, dt.name ORDER BY count DESC`,
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
		`SELECT t.name, COUNT(*) as count FROM document_tag dt JOIN tag t ON dt.tag_id = t.id JOIN document d ON dt.document_id = d.id AND d.deleted_at IS NULL GROUP BY t.id, t.name ORDER BY count DESC LIMIT 10`,
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
			(SELECT COUNT(*) FROM document WHERE (language = 'und' OR language = '') AND deleted_at IS NULL) AS missing_language,
			(SELECT COUNT(*) FROM document WHERE document_type_id = 1 AND deleted_at IS NULL) AS missing_type,
			(SELECT COUNT(*) FROM document d WHERE d.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM document_tag dt WHERE dt.document_id = d.id)) AS missing_tags`,
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
		WHERE completed_at >= CURRENT_TIMESTAMP - INTERVAL '7 days'
	`).Scan(&r.Completed, &r.Failed)
	return r, err
}

func (q *Queries) AvgTaskDurationMs(ctx context.Context) (AvgTaskDurationMsRow, error) {
	var r AvgTaskDurationMsRow
	err := q.db.QueryRowContext(ctx, `
		SELECT CAST(COALESCE(AVG(
			EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000
		), 0.0) AS INTEGER) AS avg_duration_ms
		FROM task
		WHERE status = 'completed'
		  AND started_at IS NOT NULL
		  AND completed_at IS NOT NULL
		  AND completed_at >= CURRENT_TIMESTAMP - INTERVAL '7 days'
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

func (q *Queries) DistinctLanguages(ctx context.Context) ([]string, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT DISTINCT language FROM document WHERE language != '' AND language != 'und' AND deleted_at IS NULL ORDER BY language`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("scan distinct language: %w", err)
		}
		items = append(items, s)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (q *Queries) DocumentAggregates(ctx context.Context) (DocumentAggregatesRow, error) {
	var r DocumentAggregatesRow
	err := q.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
			CAST(COALESCE(SUM(file_size), 0) AS BIGINT),
			CAST(COALESCE(SUM(GREATEST(processed_size, 0)), 0) AS BIGINT),
			CAST(COALESCE(SUM(page_count), 0) AS BIGINT),
			CAST(COALESCE(SUM(word_count), 0) AS BIGINT)
		FROM document WHERE deleted_at IS NULL`,
	).Scan(&r.TotalFiles, &r.TotalBytes, &r.ProcessedBytes, &r.TotalPages, &r.TotalWords)
	return r, err
}
