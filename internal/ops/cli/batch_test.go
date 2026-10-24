package cli

import (
	"strings"
	"testing"
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
