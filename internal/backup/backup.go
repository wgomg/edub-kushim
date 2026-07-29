package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/version"
)

const insertBatchSize = 100

type Manifest struct {
	Version           int    `json:"version"`
	Format            string `json:"format"`
	Timestamp         string `json:"timestamp"`
	AppVersion        string `json:"app_version"`
	DbSizeBytes       int64  `json:"db_size_bytes"`
	StorageFilesCount int64  `json:"storage_files_count"`
	StorageSizeBytes  int64  `json:"storage_size_bytes"`
	ConfigHash        string `json:"config_hash"`
}

type BackupResult struct {
	Path        string    `json:"path"`
	SizeBytes   int64     `json:"size_bytes"`
	FilesCount  int64     `json:"files_count"`
	DbSizeBytes int64     `json:"db_size_bytes"`
	Manifest    *Manifest `json:"manifest"`
}

func Create(ctx context.Context, db *sql.DB, schemaFS embed.FS, backupDir, configPath, storageDir string) (*BackupResult, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	tmpDir, err := os.MkdirTemp("", "edub-backup-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	sqlPath := filepath.Join(tmpDir, "edub.sql")
	if err := writeSQLDump(ctx, db, schemaFS, sqlPath); err != nil {
		return nil, err
	}

	dbInfo, err := os.Stat(sqlPath)
	if err != nil {
		return nil, fmt.Errorf("stat sql dump: %w", err)
	}

	configHash, err := fileHash(configPath)
	if err != nil {
		return nil, fmt.Errorf("hash config: %w", err)
	}

	storageFilesCount := int64(0)
	storageSizeBytes := int64(0)
	filepath.Walk(storageDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			storageFilesCount++
			storageSizeBytes += info.Size()
		}
		return nil
	})

	configCopyPath := filepath.Join(tmpDir, "config.yaml")
	if err := copyFile(configPath, configCopyPath); err != nil {
		return nil, fmt.Errorf("copy config: %w", err)
	}

	manifest := &Manifest{
		Version:           1,
		Format:            "sql-dump",
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		AppVersion:        version.Version,
		DbSizeBytes:       dbInfo.Size(),
		StorageFilesCount: storageFilesCount,
		StorageSizeBytes:  storageSizeBytes,
		ConfigHash:        fmt.Sprintf("sha256:%x", configHash),
	}

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), manifestData, 0644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	backupName := fmt.Sprintf("edub-backup-%s.tar.gz", time.Now().UTC().Format("2006-01-02T15-04-05"))
	backupPath := filepath.Join(backupDir, backupName)

	if err := createTarGz(backupPath, tmpDir, storageDir); err != nil {
		os.Remove(backupPath)
		return nil, fmt.Errorf("create tar.gz: %w", err)
	}

	archiveInfo, err := os.Stat(backupPath)
	if err != nil {
		return nil, fmt.Errorf("stat archive: %w", err)
	}

	return &BackupResult{
		Path:        backupPath,
		SizeBytes:   archiveInfo.Size(),
		FilesCount:  storageFilesCount,
		DbSizeBytes: dbInfo.Size(),
		Manifest:    manifest,
	}, nil
}

func writeSQLDump(ctx context.Context, db *sql.DB, schemaFS embed.FS, sqlPath string) error {
	f, err := os.Create(sqlPath)
	if err != nil {
		return fmt.Errorf("create sql file: %w", err)
	}
	defer f.Close()

	write := func(s string) error {
		_, err := io.WriteString(f, s)
		return err
	}

	if err := write("BEGIN;\n"); err != nil {
		return err
	}
	if err := write("SET session_replication_role = 'replica';\n\n"); err != nil {
		return err
	}

	schemaPreamble, err := extractSchemaPreamble(schemaFS)
	if err != nil {
		return fmt.Errorf("extract schema: %w", err)
	}
	if err := write(schemaPreamble); err != nil {
		return err
	}
	if err := write("\n"); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	tables, err := getTableNames(ctx, tx)
	if err != nil {
		return fmt.Errorf("get table names: %w", err)
	}

	for _, table := range tables {
		if err := writeIdentityDrop(ctx, tx, table, f); err != nil {
			return fmt.Errorf("drop identities for %s: %w", table, err)
		}
	}

	for _, table := range tables {
		if err := dumpTableData(ctx, tx, table, f); err != nil {
			return fmt.Errorf("dump %s: %w", table, err)
		}
	}

	for _, table := range tables {
		if err := writeIdentityRestore(ctx, tx, table, f); err != nil {
			return fmt.Errorf("restore identities for %s: %w", table, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	if err := write("SET session_replication_role = 'origin';\n"); err != nil {
		return err
	}
	if err := write("COMMIT;\n"); err != nil {
		return err
	}

	return nil
}

func writeIdentityDrop(ctx context.Context, tx *sql.Tx, tableName string, w io.Writer) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		  AND is_identity = 'YES'
	`, tableName)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "ALTER TABLE %s ALTER COLUMN %s DROP IDENTITY IF EXISTS;\n",
			quoteIdent(tableName), quoteIdent(col))
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func writeIdentityRestore(ctx context.Context, tx *sql.Tx, tableName string, w io.Writer) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		  AND is_identity = 'YES'
	`, tableName)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "ALTER TABLE %s ALTER COLUMN %s ADD GENERATED ALWAYS AS IDENTITY;\n",
			quoteIdent(tableName), quoteIdent(col)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "SELECT setval(pg_get_serial_sequence(%s, %s), COALESCE((SELECT MAX(%s) FROM %s), 0) + 1);\n",
			quoteLiteral(tableName), quoteLiteral(col),
			quoteIdent(col), quoteIdent(tableName)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func getTableNames(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT tablename FROM pg_catalog.pg_tables
		WHERE schemaname = 'public' AND tablename NOT IN ('goose_db_version', 'backup_lock')
		ORDER BY tablename
	`)
	if err != nil {
		return nil, fmt.Errorf("query tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan table name: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}
	return tables, nil
}

func extractSchemaPreamble(schemaFS embed.FS) (string, error) {
	var buf strings.Builder

	buf.WriteString("DROP SCHEMA IF EXISTS public CASCADE;\n")
	buf.WriteString("CREATE SCHEMA public;\n\n")

	entries, err := schemaFS.ReadDir("sql/schema/migrations")
	if err != nil {
		return "", fmt.Errorf("read migrations dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		path := fmt.Sprintf("sql/schema/migrations/%s", entry.Name())
		data, err := schemaFS.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		section, err := extractGooseUp(string(data))
		if err != nil {
			return "", fmt.Errorf("extract up from %s: %w", entry.Name(), err)
		}

		section = strings.ReplaceAll(section, "CREATE INDEX CONCURRENTLY", "CREATE INDEX")
		buf.WriteString(section)
		buf.WriteString("\n")
	}

	return buf.String(), nil
}

func extractGooseUp(content string) (string, error) {
	lines := strings.Split(content, "\n")
	var up []string
	inUp := false
	inDown := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "-- +goose Down" {
			inDown = true
			continue
		}
		if inDown {
			continue
		}

		if trimmed == "-- +goose Up" {
			inUp = true
			continue
		}

		if !inUp {
			continue
		}

		if trimmed == "-- +goose StatementBegin" || trimmed == "-- +goose StatementEnd" {
			continue
		}
		if strings.HasPrefix(trimmed, "-- +goose") {
			continue
		}

		up = append(up, line)
	}

	return strings.Join(up, "\n"), nil
}

func dumpTableData(ctx context.Context, tx *sql.Tx, tableName string, w io.Writer) error {
	colRows, err := tx.QueryContext(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1
		  AND is_generated = 'NEVER'
		ORDER BY ordinal_position
	`, tableName)
	if err != nil {
		return fmt.Errorf("query columns: %w", err)
	}

	var cols []string
	for colRows.Next() {
		var col string
		if err := colRows.Scan(&col); err != nil {
			colRows.Close()
			return fmt.Errorf("scan column: %w", err)
		}
		cols = append(cols, col)
	}
	colRows.Close()

	if len(cols) == 0 {
		return nil
	}

	quotedCols := make([]string, len(cols))
	for i, c := range cols {
		quotedCols[i] = quoteIdent(c)
	}
	colList := strings.Join(quotedCols, ", ")
	qTable := quoteIdent(tableName)

	q := fmt.Sprintf("SELECT %s FROM %s ORDER BY 1", colList, qTable)
	dataRows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("query %s: %w", tableName, err)
	}
	defer dataRows.Close()

	scanTargets := make([]any, len(cols))
	scanPtrs := make([]any, len(cols))
	for i := range scanTargets {
		scanPtrs[i] = &scanTargets[i]
	}

	batchCount := 0
	write := func(s string) error {
		_, err := io.WriteString(w, s)
		return err
	}

	for dataRows.Next() {
		if err := dataRows.Scan(scanPtrs...); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}

		if batchCount == 0 {
			if err := write(fmt.Sprintf("INSERT INTO %s (%s) VALUES\n", qTable, colList)); err != nil {
				return err
			}
		} else {
			if err := write(",\n"); err != nil {
				return err
			}
		}

		if err := write("("); err != nil {
			return err
		}
		for i, v := range scanTargets {
			if i > 0 {
				if err := write(", "); err != nil {
					return err
				}
			}
			if err := write(formatValue(v)); err != nil {
				return err
			}
		}
		if err := write(")"); err != nil {
			return err
		}

		batchCount++
		if batchCount >= insertBatchSize {
			if err := write(";\n"); err != nil {
				return err
			}
			batchCount = 0
		}
	}

	if batchCount > 0 {
		if err := write(";\n"); err != nil {
			return err
		}
	}

	if err := dataRows.Err(); err != nil {
		return fmt.Errorf("rows iteration: %w", err)
	}

	return nil
}

func formatValue(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case int64:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%f", val)
	case bool:
		return fmt.Sprintf("%t", val)
	case string:
		return quoteLiteral(val)
	case []byte:
		return quoteLiteral(string(val))
	case time.Time:
		return quoteLiteral(val.Format(time.RFC3339))
	default:
		return quoteLiteral(fmt.Sprintf("%v", val))
	}
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}

func ApplyRetention(backupDir string, keep int) error {
	if keep <= 0 {
		return nil
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return fmt.Errorf("read backup dir: %w", err)
	}

	var backups []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "edub-backup-") && strings.HasSuffix(e.Name(), ".tar.gz") {
			backups = append(backups, filepath.Join(backupDir, e.Name()))
		}
	}

	if len(backups) <= keep {
		return nil
	}

	slices.Sort(backups)

	toRemove := backups[:len(backups)-keep]
	for _, p := range toRemove {
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("remove old backup %s: %w", p, err)
		}
	}

	return nil
}

func createTarGz(dstPath, tmpDir, storageDir string) error {
	f, err := os.OpenFile(dstPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer f.Close()

	gw, err := gzip.NewWriterLevel(f, gzip.BestSpeed)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return fmt.Errorf("read tmp dir: %w", err)
	}

	for _, entry := range entries {
		path := filepath.Join(tmpDir, entry.Name())
		if err := addToTar(tw, path, entry.Name()); err != nil {
			return fmt.Errorf("add %s to tar: %w", entry.Name(), err)
		}
	}

	if _, err := os.Stat(storageDir); err == nil {
		if err := addToTar(tw, storageDir, "storage"); err != nil {
			return fmt.Errorf("add storage to tar: %w", err)
		}
	}

	return nil
}

func addToTar(tw *tar.Writer, srcPath, arcPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.Walk(srcPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(srcPath, path)
			if err != nil {
				return err
			}
			relPath := filepath.Join(arcPath, rel)
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = relPath
			if info.IsDir() {
				header.Name += "/"
			}
			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if !info.IsDir() {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				if _, err := io.Copy(tw, f); err != nil {
					return err
				}
			}
			return nil
		})
	}

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = arcPath
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()

	if _, err := io.Copy(d, s); err != nil {
		return err
	}

	if info, err := os.Stat(src); err == nil {
		os.Chmod(dst, info.Mode())
	}

	return nil
}

func fileHash(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
