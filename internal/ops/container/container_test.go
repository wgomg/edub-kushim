package container

import (
	"database/sql"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/testutil"
)

// TestSchemaRoleSplitsTheBootstrap pins the two container roles against one fresh, package-named
// database: a forked batch executor only opens a pool — the launcher owns the schema — while the
// owner creates the database and applies migrations, seeders and backfills before returning the handle.
func TestSchemaRoleSplitsTheBootstrap(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	cfg := testRoleConfig(t)
	dsn := config.BuildPostgresDSN(cfg.Db)
	dropRoleTestDatabase(t, dsn, cfg.Db.Database)

	t.Run("the executor neither creates the database nor applies schema", func(t *testing.T) {
		c := NewBatchExecutor(cfg, testutil.NewTestLogger())
		defer c.Close()
		if _, err := c.GetDB(); err == nil {
			t.Fatal("the executor must fail against a database its launcher never created")
		}
		if roleTestDatabaseExists(t, dsn, cfg.Db.Database) {
			t.Fatal("the executor must not create the database")
		}
	})

	t.Run("the owner creates the database and applies the schema", func(t *testing.T) {
		c := NewContainer(cfg, testutil.NewTestLogger())
		defer c.Close()
		db, err := c.GetDB()
		if err != nil {
			t.Fatalf("owner GetDB: %v", err)
		}
		var history int64
		if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version`).Scan(&history); err != nil {
			t.Fatalf("read goose history: %v", err)
		}
		if history == 0 {
			t.Fatal("the owner must have applied the schema before returning the handle")
		}
	})

	dropRoleTestDatabase(t, dsn, cfg.Db.Database)
}

// testRoleConfig derives the container configuration from the package test DSN; the database the two
// subtests race over is absent until the owner subtest creates it.
func testRoleConfig(t *testing.T) *config.Config {
	t.Helper()
	u, err := url.Parse(database.TestDSN(t))
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	port := 5432
	if raw := u.Port(); raw != "" {
		if port, err = strconv.Atoi(raw); err != nil {
			t.Fatalf("parse test DSN port: %v", err)
		}
	}
	password, _ := u.User.Password()
	return &config.Config{Db: config.DatabaseConfig{
		Host:     u.Hostname(),
		Port:     port,
		User:     u.User.Username(),
		Password: password,
		Database: strings.TrimPrefix(u.Path, "/"),
		SSLMode:  u.Query().Get("sslmode"),
	}}
}

// dropRoleTestDatabase removes the role test's database through the admin connection, with FORCE so a
// live pool from a previous run cannot block the fresh start the two subtests require.
func dropRoleTestDatabase(t *testing.T, dsn, name string) {
	t.Helper()
	db, err := sql.Open("pgx", roleAdminDSN(t, dsn))
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`); err != nil {
		t.Fatalf("drop role test database: %v", err)
	}
}

// roleTestDatabaseExists reports whether the role test's database is present, read through the admin
// connection the same way the connection layer's ensure step does.
func roleTestDatabaseExists(t *testing.T, dsn, name string) bool {
	t.Helper()
	db, err := sql.Open("pgx", roleAdminDSN(t, dsn))
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("check role test database: %v", err)
	}
	return exists
}

// roleAdminDSN retargets the test DSN at the shared postgres database the admin commands need.
func roleAdminDSN(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	u.Path = "/postgres"
	return u.String()
}
