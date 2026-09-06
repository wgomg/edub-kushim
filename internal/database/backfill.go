package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/wgomg/edub-kushim/internal/utils"
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

func BackfillTextHash(ctx context.Context, db *sql.DB) error {
	var pending bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM document WHERE text_hash IS NULL AND text_content IS NOT NULL LIMIT 1)`,
	).Scan(&pending); err != nil {
		return fmt.Errorf("probe pending text-hash rows: %w", err)
	}
	if !pending {
		return nil
	}

	const batchSize = 500
	var lastID int64
	for batch := 1; ; batch++ {
		rows, err := db.QueryContext(ctx,
			`SELECT id, text_content FROM document
			WHERE text_hash IS NULL AND text_content IS NOT NULL AND id > $1
			ORDER BY id LIMIT $2`, lastID, batchSize)
		if err != nil {
			return fmt.Errorf("select documents pending text-hash backfill: %w", err)
		}

		type pendingRow struct {
			id   int64
			text string
		}
		var pending []pendingRow
		for rows.Next() {
			var r pendingRow
			if err := rows.Scan(&r.id, &r.text); err != nil {
				rows.Close()
				return fmt.Errorf("scan pending text-hash row: %w", err)
			}
			pending = append(pending, r)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin text-hash batch tx: %w", err)
		}
		var sb strings.Builder
		sb.WriteString("UPDATE document SET text_hash = v.hash FROM (VALUES ")
		args := make([]any, 0, len(pending)*2)
		for j, r := range pending {
			if j > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("($%d::bigint, $%d::text)", j*2+1, j*2+2))
			args = append(args, r.id, utils.SHA256Hex(r.text))
		}
		sb.WriteString(") AS v(id, hash) WHERE document.id = v.id AND document.text_hash IS NULL")
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			tx.Rollback()
			return fmt.Errorf("backfill text_hash batch: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit text-hash batch: %w", err)
		}

		lastID = pending[len(pending)-1].id
		slog.Default().Info("backfill text_hash", "batch", batch, "rows", len(pending), "last_id", lastID)
	}
}
