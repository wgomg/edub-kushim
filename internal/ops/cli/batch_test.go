package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/types"
)

func TestBatchHandler_Subcommands(t *testing.T) {
	c, _ := newTestContainer(t)

	t.Run("no arguments and --help print the help", func(t *testing.T) {
		if err := batchHandler(c, nil); err != nil {
			t.Fatalf("bare batch returned an error: %v", err)
		}
		if err := batchHandler(c, []string{"--help"}); err != nil {
			t.Fatalf("batch --help returned an error: %v", err)
		}
	})

	t.Run("an unknown subcommand is refused", func(t *testing.T) {
		err := batchHandler(c, []string{"frobnicate"})
		if err == nil || !strings.Contains(err.Error(), "unknown batch subcommand") {
			t.Fatalf("want the unknown-subcommand refusal, got %v", err)
		}
	})

	t.Run("the argument-taking subcommands require their id before touching the database", func(t *testing.T) {
		cases := []struct {
			name    string
			handler func(args []string) error
			want    string
		}{
			{name: "run", handler: func(args []string) error { return batchRunHandler(c, args) }, want: "requires a batch id"},
			{name: "stop", handler: func(args []string) error { return batchStopHandler(c, args) }, want: "requires a batch id"},
			{name: "resume", handler: func(args []string) error { return batchResumeHandler(c, args, false) }, want: "requires a batch id"},
			{name: "restart", handler: func(args []string) error { return batchResumeHandler(c, args, true) }, want: "requires a batch id"},
			{name: "status", handler: func(args []string) error { return batchStatusHandler(c, args) }, want: "requires a batch id"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := tc.handler(nil)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want an error mentioning %q, got %v", tc.want, err)
				}
			})
		}
	})
}

// TestBatchListValidatesFlags pins the pagination contract: out-of-range and malformed values are
// refused instead of silently falling back to another page.
func TestBatchListValidatesFlags(t *testing.T) {
	c, _ := newTestContainer(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "limit above the bound", args: []string{"--limit", "999"}, want: "between 1 and 100"},
		{name: "malformed limit", args: []string{"--limit", "abc"}, want: "invalid integer"},
		{name: "negative offset", args: []string{"--offset", "-5"}, want: "between 0 and 1000000"},
		{name: "malformed offset", args: []string{"--offset", "abc"}, want: "invalid integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := batchListHandler(c, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("batch list %v = %v, want an error mentioning %q", tc.args, err, tc.want)
			}
		})
	}
}

// TestBatchCreateConsumePausedGuard pins the no-bypass rule on the CLI ingest creation path: while a
// credit-paused batch exists the consume creation refuses with the paused ids, and once none remains the
// same call proceeds — the guard the polling scan enforces independently on its own path.
func TestBatchCreateConsumePausedGuard(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	configDir := t.TempDir()
	yaml := "database:\n  dsn: " + os.Getenv("TEST_DATABASE_URL") + "\n" +
		"consumer:\n  ocr:\n    languages:\n      - eng\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "inbox"), 0o755); err != nil {
		t.Fatalf("create inbox: %v", err)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	c := container.NewContainer(cfg, log.NewLogger("error"))
	t.Cleanup(func() { c.Close() })

	ctx := context.Background()
	client, err := c.GetClient()
	if err != nil {
		t.Fatalf("container client: %v", err)
	}
	// a crashed previous run must not leave a stale row that breaks the creation path below
	if _, err := client.DB().ExecContext(ctx, "DELETE FROM batch WHERE id = 'guard-paused'"); err != nil {
		t.Fatalf("clear a previous run's row: %v", err)
	}
	if err := client.Queries.CreateBatch(ctx, database.CreateBatchParams{
		ID: "guard-paused", Origin: types.Batch.Origin.CLI, Status: types.Batch.Status.Paused,
	}); err != nil {
		t.Fatalf("seed paused batch: %v", err)
	}

	err = batchCreateHandler(c, []string{"consume"})
	if err == nil || !strings.Contains(err.Error(), "paused batch(es) exist") {
		t.Fatalf("want the paused-batch refusal, got %v", err)
	}

	if _, err := client.DB().ExecContext(ctx, "DELETE FROM batch WHERE id = 'guard-paused'"); err != nil {
		t.Fatalf("resolve the paused batch: %v", err)
	}
	if err := batchCreateHandler(c, []string{"consume"}); err != nil {
		t.Fatalf("creation proceeds once no paused batch remains: %v", err)
	}
}

// TestBatchCreateRejectsUnknownVerb pins that the verb is validated before any database access.
func TestBatchCreateRejectsUnknownVerb(t *testing.T) {
	c, _ := newTestContainer(t)
	err := batchCreateHandler(c, []string{"frobnicate"})
	if err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Fatalf("want the unknown-task refusal, got %v", err)
	}
}

// TestParseCreateSelection pins the selection contract: live-data flags produce a deferred enqueue,
// verbs whose targets arrive as arguments select nothing, and conflicting or malformed flags are
// refused with an error and without a selection.
func TestParseCreateSelection(t *testing.T) {
	t.Run("verbs that read their targets from the arguments select nothing", func(t *testing.T) {
		cases := []struct {
			task string
			args []string
		}{
			{task: "enrich"}, // document ids arrive as arguments
			{task: "enrich", args: []string{"doc-1", "doc-2"}}, // no selection flag present
			{task: "thumbnails"}, // the bare form enqueues every document
			{task: "consume"},    // not a selection verb
			{task: "frobnicate"}, // unknown task, refused before parsing
		}
		for _, tc := range cases {
			sel, err := parseCreateSelection(tc.task, tc.args, &config.Config{})
			if err != nil {
				t.Fatalf("parseCreateSelection(%q, %v) = %v, want no error", tc.task, tc.args, err)
			}
			if sel.run != nil {
				t.Fatalf("parseCreateSelection(%q, %v) selected an enqueue, want the argument path", tc.task, tc.args)
			}
		}
	})

	t.Run("live-data flags select a deferred enqueue", func(t *testing.T) {
		cases := []struct {
			task string
			args []string
		}{
			{task: "enrich", args: []string{"--all"}},
			{task: "enrich", args: []string{"--limit", "3"}},
			{task: "thumbnails", args: []string{"--document", "doc-1"}},
			{task: "thumbnails", args: []string{"--from-batch", "batch-1"}},
		}
		for _, tc := range cases {
			sel, err := parseCreateSelection(tc.task, tc.args, &config.Config{})
			if err != nil {
				t.Fatalf("parseCreateSelection(%q, %v) = %v, want no error", tc.task, tc.args, err)
			}
			if sel.run == nil {
				t.Fatalf("parseCreateSelection(%q, %v) selected nothing, want a deferred enqueue", tc.task, tc.args)
			}
		}
	})

	t.Run("conflicting or malformed flags are refused", func(t *testing.T) {
		cases := []struct {
			task string
			args []string
			want string
		}{
			{task: "enrich", args: []string{"--limit", "abc"}, want: "invalid integer"},
			{task: "enrich", args: []string{"--all", "--limit", "5"}, want: "mutually exclusive"},
			{task: "enrich", args: []string{"doc-1", "--all"}, want: "cannot be combined"},
			{task: "thumbnails", args: []string{"--from-batch", "b", "--document", "d"}, want: "mutually exclusive"},
			{task: "thumbnails", args: []string{"--document", "d", "extra"}, want: "unknown argument"},
		}
		for _, tc := range cases {
			sel, err := parseCreateSelection(tc.task, tc.args, &config.Config{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseCreateSelection(%q, %v) = %v, want an error mentioning %q", tc.task, tc.args, err, tc.want)
			}
			if sel.run != nil {
				t.Fatalf("parseCreateSelection(%q, %v) refused the flags but kept a selection", tc.task, tc.args)
			}
		}
	})
}

// TestBatchCreateValidatesSelectionFlagsBeforeDatabase pins the argument-validation order on the
// live-data selection verbs: with an unreachable database, malformed or conflicting selection flags
// must still fail on the arguments themselves instead of surfacing a connection error.
func TestBatchCreateValidatesSelectionFlagsBeforeDatabase(t *testing.T) {
	configDir := t.TempDir()
	yaml := "database:\n  dsn: postgres://edub:edub@127.0.0.1:1/edub?sslmode=disable\n" +
		"consumer:\n  ocr:\n    languages:\n      - eng\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	c := container.NewContainer(cfg, log.NewLogger("error"))
	t.Cleanup(func() { c.Close() })

	// the ordering only has teeth when the database is genuinely unreachable
	if _, err := c.GetClient(); err == nil {
		t.Fatal("want the refused DSN to block the client, got a connection")
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "malformed enrich limit", args: []string{"enrich", "--limit", "abc"}, want: "invalid integer"},
		{name: "enrich all with a limit", args: []string{"enrich", "--all", "--limit", "5"}, want: "mutually exclusive"},
		{name: "thumbnails with both selectors", args: []string{"thumbnails", "--from-batch", "b", "--document", "d"}, want: "mutually exclusive"},
		{name: "thumbnail trailing argument", args: []string{"thumbnails", "--document", "d", "extra"}, want: "unknown argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := batchCreateHandler(c, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("batch create %v = %v, want an error mentioning %q", tc.args, err, tc.want)
			}
			if strings.Contains(err.Error(), "database") {
				t.Fatalf("batch create %v reached the database before validating its arguments: %v", tc.args, err)
			}
		})
	}
}
