package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPrepareFreshDatabase(t *testing.T) {
	baseDSN := os.Getenv("TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	uniqueName := func() string {
		return fmt.Sprintf("edub_prepfresh_%d", time.Now().UnixNano())
	}
	dropNamed := func(t *testing.T, name string) {
		t.Helper()
		admin, err := sql.Open("pgx", replaceDBName(baseDSN, "postgres"))
		if err != nil {
			t.Fatalf("open admin connection: %v", err)
		}
		defer admin.Close()
		if _, err := admin.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", quoteIdent(name))); err != nil {
			t.Fatalf("drop database %s: %v", name, err)
		}
	}
	createEmpty := func(t *testing.T, name string) {
		t.Helper()
		admin, err := sql.Open("pgx", replaceDBName(baseDSN, "postgres"))
		if err != nil {
			t.Fatalf("open admin connection: %v", err)
		}
		defer admin.Close()
		if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", quoteIdent(name))); err != nil {
			t.Fatalf("create database %s: %v", name, err)
		}
	}
	targetExists := func(t *testing.T, name string) bool {
		t.Helper()
		admin, err := sql.Open("pgx", replaceDBName(baseDSN, "postgres"))
		if err != nil {
			t.Fatalf("open admin connection: %v", err)
		}
		defer admin.Close()
		var exists bool
		if err := admin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
			t.Fatalf("query database existence: %v", err)
		}
		return exists
	}
	execOn := func(t *testing.T, name, statement string) {
		t.Helper()
		db, err := sql.Open("pgx", replaceDBName(baseDSN, name))
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer db.Close()
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("exec on %s: %v", name, err)
		}
	}

	t.Run("creates a missing database", func(t *testing.T) {
		name := uniqueName()
		t.Cleanup(func() { dropNamed(t, name) })

		if err := PrepareFreshDatabase(replaceDBName(baseDSN, name)); err != nil {
			t.Fatalf("PrepareFreshDatabase: %v", err)
		}
		if !targetExists(t, name) {
			t.Fatal("database was not created")
		}
	})

	t.Run("adopts an existing empty database", func(t *testing.T) {
		name := uniqueName()
		createEmpty(t, name)
		t.Cleanup(func() { dropNamed(t, name) })

		if err := PrepareFreshDatabase(replaceDBName(baseDSN, name)); err != nil {
			t.Fatalf("PrepareFreshDatabase on empty database: %v", err)
		}
	})

	t.Run("refuses a database with a public table", func(t *testing.T) {
		name := uniqueName()
		t.Cleanup(func() { dropNamed(t, name) })

		if err := PrepareFreshDatabase(replaceDBName(baseDSN, name)); err != nil {
			t.Fatalf("PrepareFreshDatabase: %v", err)
		}
		execOn(t, name, "CREATE TABLE blocker (id int)")

		err := PrepareFreshDatabase(replaceDBName(baseDSN, name))
		if !errors.Is(err, ErrDatabaseNotEmpty) {
			t.Fatalf("PrepareFreshDatabase error = %v, want ErrDatabaseNotEmpty", err)
		}
	})

	t.Run("refuses a database with a table outside the public schema", func(t *testing.T) {
		name := uniqueName()
		t.Cleanup(func() { dropNamed(t, name) })

		if err := PrepareFreshDatabase(replaceDBName(baseDSN, name)); err != nil {
			t.Fatalf("PrepareFreshDatabase: %v", err)
		}
		execOn(t, name, "CREATE SCHEMA custom")
		execOn(t, name, "CREATE TABLE custom.blocker (id int)")

		err := PrepareFreshDatabase(replaceDBName(baseDSN, name))
		if !errors.Is(err, ErrDatabaseNotEmpty) {
			t.Fatalf("PrepareFreshDatabase error = %v, want ErrDatabaseNotEmpty", err)
		}
	})
}
