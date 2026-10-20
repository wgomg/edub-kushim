package tags

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
)

// TestOpenStoreSignal pins the opening-gate protocol every consuming pass shares: a set flag reads as
// changed and clears in one committed step, a clear flag stays clear without another write, a failed
// run re-arms it, and a missing row counts as changed rather than letting a gate skip.
func TestOpenStoreSignal(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	ctx := context.Background()
	name := SignalMeasureChanged

	writeFlag := func(value string) {
		t.Helper()
		if _, err := client.DB().ExecContext(ctx, `
			INSERT INTO system_signal (name, type, value)
			VALUES ($1, 'bool', $2)
			ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value, modified_at = CURRENT_TIMESTAMP`,
			name, value); err != nil {
			t.Fatalf("write the flag as %q: %v", value, err)
		}
	}
	readFlag := func() string {
		t.Helper()
		value, err := client.Queries.GetTagStoreSignal(ctx, name)
		if err != nil {
			t.Fatalf("read the flag: %v", err)
		}
		return value
	}

	writeFlag(SignalSet)
	changed, err := OpenStoreSignal(ctx, client, name)
	if err != nil {
		t.Fatalf("open with a set flag: %v", err)
	}
	if !changed {
		t.Error("open with a set flag reported unchanged")
	}
	if got := readFlag(); got != SignalClear {
		t.Errorf("flag after open = %q, want %q", got, SignalClear)
	}

	changed, err = OpenStoreSignal(ctx, client, name)
	if err != nil {
		t.Fatalf("open with a clear flag: %v", err)
	}
	if changed {
		t.Error("open with a clear flag reported changed")
	}

	cause := errors.New("run failed")
	if err := RearmStoreSignal(ctx, client, name, cause); !errors.Is(err, cause) {
		t.Errorf("rearm returned %v, want the cause itself", err)
	}
	if got := readFlag(); got != SignalSet {
		t.Errorf("flag after rearm = %q, want %q", got, SignalSet)
	}

	if _, err := client.DB().ExecContext(ctx, "DELETE FROM system_signal WHERE name = $1", name); err != nil {
		t.Fatalf("delete the flag row: %v", err)
	}
	changed, err = OpenStoreSignal(ctx, client, name)
	if err != nil {
		t.Fatalf("open with the flag absent: %v", err)
	}
	if !changed {
		t.Error("an absent flag reported unchanged: a gate must never skip on missing state")
	}
	writeFlag(SignalClear)
}
