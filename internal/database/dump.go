package database

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const insertBatchSize = 100

func DumpSchemaAndData(ctx context.Context, db *sql.DB, schemaFS embed.FS, w io.Writer) error {
	write := func(s string) error {
		_, err := io.WriteString(w, s)
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

	// The preamble drops the whole public schema, including the goose version table.
	if err := dumpGooseVersion(ctx, tx, w); err != nil {
		return fmt.Errorf("dump goose version: %w", err)
	}
	if err := write("\n"); err != nil {
		return err
	}

	for _, table := range tables {
		if err := writeIdentityDrop(ctx, tx, table, w); err != nil {
			return fmt.Errorf("drop identities for %s: %w", table, err)
		}
	}

	for _, table := range tables {
		if err := dumpTableData(ctx, tx, table, w); err != nil {
			return fmt.Errorf("dump %s: %w", table, err)
		}
	}

	for _, table := range tables {
		if err := writeIdentityRestore(ctx, tx, table, w); err != nil {
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

func SQLDumpToFile(ctx context.Context, db *sql.DB, schemaFS embed.FS, destPath string) error {
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create sql file: %w", err)
	}

	gz := gzip.NewWriter(f)
	err = DumpSchemaAndData(ctx, db, schemaFS, gz)
	if gzErr := gz.Close(); err == nil && gzErr != nil {
		err = gzErr
	}
	if fErr := f.Close(); err == nil && fErr != nil {
		err = fErr
	}
	if err != nil {
		os.Remove(destPath)
		return fmt.Errorf("write sql dump %s: %w", destPath, err)
	}
	return nil
}

func ExecuteDumpFile(ctx context.Context, db *sql.DB, dumpPath string) error {
	f, err := os.Open(dumpPath)
	if err != nil {
		return fmt.Errorf("open dump file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 128*1024*1024)
	splitter := &dumpSplitter{}
	scanner.Split(splitter.split)

	idx := 0
	for scanner.Scan() {
		stmt := strings.TrimSpace(scanner.Text())
		if stmt == "" {
			continue
		}
		idx++
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("execute dump statement %d: %w", idx, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read dump: %w", err)
	}
	return nil
}

type dumpSplitter struct {
	inSingle     bool
	inDollar     bool
	dollarTag    string
	lineComment  bool
	blockComment bool
}

func (s *dumpSplitter) split(data []byte, atEOF bool) (advance int, token []byte, err error) {
	i := 0
	for i < len(data) {
		switch {
		case s.inSingle:
			if data[i] == '\'' {
				if i+1 < len(data) && data[i+1] == '\'' {
					i += 2
					continue
				}
				s.inSingle = false
			}
			i++
		case s.lineComment:
			if data[i] == '\n' {
				s.lineComment = false
			}
			i++
		case s.blockComment:
			if data[i] == '*' && i+1 < len(data) && data[i+1] == '/' {
				s.blockComment = false
				i += 2
			} else {
				i++
			}
		case s.inDollar:
			if strings.HasPrefix(string(data[i:]), s.dollarTag) {
				s.inDollar = false
				i += len(s.dollarTag)
			} else {
				i++
			}
		case data[i] == '\'':
			s.inSingle = true
			i++
		case data[i] == '-' && i+1 < len(data) && data[i+1] == '-':
			s.lineComment = true
			i += 2
		case data[i] == '/' && i+1 < len(data) && data[i+1] == '*':
			s.blockComment = true
			i += 2
		case data[i] == '$':
			if tag, ok := dollarQuoteTag(data[i:]); ok {
				s.inDollar = true
				s.dollarTag = tag
				i += len(tag)
			} else {
				i++
			}
		case data[i] == ';':
			return i + 1, data[:i+1], nil
		default:
			i++
		}
	}

	if atEOF {
		if s.inSingle || s.inDollar || s.lineComment || s.blockComment {
			return 0, nil, fmt.Errorf("dump ends with an unterminated statement")
		}
		if strings.TrimSpace(string(data)) == "" {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("dump ends with an unterminated SQL statement")
	}
	return 0, nil, nil
}

func dollarQuoteTag(s []byte) (string, bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '$' {
			return string(s[:i+1]), true
		}
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return "", false
		}
	}
	return "", false
}

func dumpGooseVersion(ctx context.Context, tx *sql.Tx, w io.Writer) error {
	ddl, err := gooseVersionTableDDL(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, ddl+"\n"); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx,
		"SELECT version_id, is_applied, tstamp FROM goose_db_version ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()

	first := true
	for rows.Next() {
		var versionID int64
		var applied bool
		var ts time.Time
		if err := rows.Scan(&versionID, &applied, &ts); err != nil {
			return err
		}
		if first {
			if _, err := io.WriteString(w, "INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES\n"); err != nil {
				return err
			}
			first = false
		} else {
			if _, err := io.WriteString(w, ",\n"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "(%d, %t, %s)", versionID, applied, quoteLiteral(ts.Format(time.RFC3339))); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !first {
		if _, err := io.WriteString(w, ";\n"); err != nil {
			return err
		}
	}
	return nil
}

func gooseVersionTableDDL(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT column_name, data_type, is_nullable, column_default, is_identity, identity_generation
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'goose_db_version'
		ORDER BY ordinal_position`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name, dataType, nullable, identity string
		var def, identityGen sql.NullString
		if err := rows.Scan(&name, &dataType, &nullable, &def, &identity, &identityGen); err != nil {
			return "", err
		}
		col := fmt.Sprintf("\t%s %s", quoteIdent(name), dataType)
		if identity == "YES" {
			if identityGen.String == "ALWAYS" {
				col += " GENERATED ALWAYS AS IDENTITY"
			} else {
				col += " GENERATED BY DEFAULT AS IDENTITY"
			}
		} else if def.Valid && def.String != "" {
			col += " DEFAULT " + def.String
		}
		if nullable == "NO" {
			col += " NOT NULL"
		}
		cols = append(cols, col)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("goose_db_version table not found in source database")
	}

	if pk, err := primaryKeyColumns(ctx, tx, "goose_db_version"); err != nil {
		return "", err
	} else if len(pk) > 0 {
		cols = append(cols, "\tPRIMARY KEY ("+strings.Join(pk, ", ")+")")
	}

	return "CREATE TABLE goose_db_version (\n" + strings.Join(cols, ",\n") + "\n);", nil
}

func primaryKeyColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT a.attname
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY(con.conkey)
		WHERE n.nspname = 'public' AND c.relname = $1 AND con.contype = 'p'
		ORDER BY array_position(con.conkey, a.attnum)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pk []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		pk = append(pk, quoteIdent(name))
	}
	return pk, rows.Err()
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
	defer colRows.Close()

	var cols []string
	for colRows.Next() {
		var col string
		if err := colRows.Scan(&col); err != nil {
			return fmt.Errorf("scan column: %w", err)
		}
		cols = append(cols, col)
	}
	if err := colRows.Err(); err != nil {
		return fmt.Errorf("columns iteration: %w", err)
	}

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

func quoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}
