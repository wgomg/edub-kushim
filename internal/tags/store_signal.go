package tags

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
)

// SignalMeasureChanged and its siblings name the store-changed flags of system_signal: one per consuming
// pass, each read and cleared by that pass's opening gate.
const (
	SignalMeasureChanged   = "tag_measure_changed"
	SignalReconcileChanged = "tag_reconcile_changed"
	SignalAuditChanged     = "tag_audit_changed"
)

// SignalSet and SignalClear are the store-changed flags' two values.
const (
	SignalSet   = "true"
	SignalClear = "false"
)

// OpenStoreSignal reads the named store-changed flag and, when it is set, clears it in one committed
// transaction; the clear commits before the pass reads anything, so a mutation landing mid-run re-arms the
// flag. It reports whether the store changed since the flag was last cleared, and an absent row counts as
// changed, so a gate can never skip a store the record does not describe.
func OpenStoreSignal(ctx context.Context, client *database.Client, name string) (bool, error) {
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("open store-changed signal %q: %w", name, err)
	}
	defer tx.Rollback()

	q := client.WithTx(tx)
	value, err := q.GetTagStoreSignal(ctx, name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read store-changed signal %q: %w", name, err)
	}
	changed := err != nil || value != SignalClear

	if changed {
		if err := q.SetTagStoreSignal(ctx, database.SetTagStoreSignalParams{
			Name:  name,
			Value: SignalClear,
		}); err != nil {
			return false, fmt.Errorf("clear store-changed signal %q: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit store-changed signal %q: %w", name, err)
	}
	return changed, nil
}

// RearmStoreSignal restores the named flag after a failed run and returns cause, wrapping it when the
// re-arm itself fails: a failed run must not be read as an unchanged store.
func RearmStoreSignal(ctx context.Context, client *database.Client, name string, cause error) error {
	if err := client.Queries.SetTagStoreSignal(ctx, database.SetTagStoreSignalParams{
		Name:  name,
		Value: SignalSet,
	}); err != nil {
		return fmt.Errorf("%w (re-arming the store-changed signal failed: %v)", cause, err)
	}
	return cause
}
