package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
)

func BackfillProcessedSizes(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT id, storage_path FROM document WHERE processed_size = 0 AND deleted_at IS NULL`)
	if err != nil {
		return fmt.Errorf("select documents pending processed-size backfill: %w", err)
	}

	type pendingRow struct {
		id   int64
		path string
	}
	var pending []pendingRow
	for rows.Next() {
		var r pendingRow
		if err := rows.Scan(&r.id, &r.path); err != nil {
			rows.Close()
			return fmt.Errorf("scan pending processed-size row: %w", err)
		}
		pending = append(pending, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	type resolvedRow struct {
		id   int64
		size int64
	}
	resolved := make([]resolvedRow, 0, len(pending))
	for _, r := range pending {
		info, err := os.Stat(r.path)
		if err != nil {
			// File gone: mark as attempted so it is not re-statted on
			// every boot. The aggregate excludes negative sizes.
			resolved = append(resolved, resolvedRow{id: r.id, size: -1})
			continue
		}
		resolved = append(resolved, resolvedRow{id: r.id, size: info.Size()})
	}

	const batchSize = 500
	for i := 0; i < len(resolved); i += batchSize {
		end := min(i+batchSize, len(resolved))
		chunk := resolved[i:end]

		var sb strings.Builder
		sb.WriteString("UPDATE document SET processed_size = v.size FROM (VALUES ")
		args := make([]any, 0, len(chunk)*2)
		for j, r := range chunk {
			if j > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("($%d::bigint, $%d::bigint)", j*2+1, j*2+2))
			args = append(args, r.id, r.size)
		}
		sb.WriteString(") AS v(id, size) WHERE document.id = v.id")

		if _, err := db.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("backfill processed_size batch: %w", err)
		}
	}
	return nil
}
