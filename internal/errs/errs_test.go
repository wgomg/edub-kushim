package errs

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestErrorConstructors(t *testing.T) {
	inner := fmt.Errorf("root cause")

	tests := []struct {
		name string
		fn   func(string, error) *Error
		op   string
		kind Kind
	}{
		{"ENotFound", ENotFound, "get user", KindNotFound},
		{"EConflict", EConflict, "create tag", KindConflict},
		{"EInvalid", EInvalid, "validate input", KindInvalid},
		{"EInternal", EInternal, "query db", KindInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := tt.fn(tt.op, inner)
			if e.Kind != tt.kind {
				t.Errorf("Kind = %d, want %d", e.Kind, tt.kind)
			}
			if e.Op != tt.op {
				t.Errorf("Op = %q, want %q", e.Op, tt.op)
			}
			if e.Cause != inner {
				t.Errorf("Cause not preserved")
			}
		})
	}
}

func TestErrorString(t *testing.T) {
	e := ENotFound("get user", fmt.Errorf("sql: no rows"))
	got := e.Error()
	if got != "get user: not found: sql: no rows" {
		t.Errorf("Error() = %q", got)
	}
}

func TestUnwrap(t *testing.T) {
	inner := fmt.Errorf("root")
	e := EConflict("op", inner)
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner error")
	}
}

func TestKindOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{"nil error", nil, KindInternal},
		{"ENotFound", ENotFound("op", fmt.Errorf("x")), KindNotFound},
		{"EConflict", EConflict("op", fmt.Errorf("x")), KindConflict},
		{"EInvalid", EInvalid("op", fmt.Errorf("x")), KindInvalid},
		{"EInternal", EInternal("op", fmt.Errorf("x")), KindInternal},
		{"wrapped ENotFound", fmt.Errorf("outer: %w", ENotFound("op", fmt.Errorf("x"))), KindNotFound},
		{"plain error", fmt.Errorf("plain"), KindInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := KindOf(tt.err)
			if got != tt.want {
				t.Errorf("KindOf() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFromDB(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		if err := FromDB(nil, "op"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("sql.ErrNoRows returns NotFound", func(t *testing.T) {
		err := FromDB(sql.ErrNoRows, "get doc")
		var e *Error
		if !errors.As(err, &e) {
			t.Fatal("expected *Error")
		}
		if e.Kind != KindNotFound {
			t.Errorf("Kind = %d, want KindNotFound", e.Kind)
		}
		if e.Op != "get doc" {
			t.Errorf("Op = %q, want %q", e.Op, "get doc")
		}
	})

	t.Run("unique violation returns Conflict", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: pgerrcode.UniqueViolation}
		err := FromDB(pgErr, "create tag")
		var e *Error
		if !errors.As(err, &e) {
			t.Fatal("expected *Error")
		}
		if e.Kind != KindConflict {
			t.Errorf("Kind = %d, want KindConflict", e.Kind)
		}
	})

	t.Run("other error returns Internal", func(t *testing.T) {
		err := FromDB(fmt.Errorf("connection refused"), "query")
		var e *Error
		if !errors.As(err, &e) {
			t.Fatal("expected *Error")
		}
		if e.Kind != KindInternal {
			t.Errorf("Kind = %d, want KindInternal", e.Kind)
		}
	})
}

func TestPgErrorPredicates(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		constraint  bool
		foreign     bool
		busy        bool
	}{
		{
			name:       "unique violation",
			err:        &pgconn.PgError{Code: pgerrcode.UniqueViolation},
			constraint: true, foreign: false, busy: false,
		},
		{
			name:       "foreign key violation",
			err:        &pgconn.PgError{Code: pgerrcode.ForeignKeyViolation},
			constraint: false, foreign: true, busy: false,
		},
		{
			name:       "deadlock detected",
			err:        &pgconn.PgError{Code: pgerrcode.DeadlockDetected},
			constraint: false, foreign: false, busy: true,
		},
		{
			name:       "serialization failure",
			err:        &pgconn.PgError{Code: pgerrcode.SerializationFailure},
			constraint: false, foreign: false, busy: true,
		},
		{
			name:       "plain error",
			err:        fmt.Errorf("not a pg error"),
			constraint: false, foreign: false, busy: false,
		},
		{
			name:       "wrapped pg error",
			err:        fmt.Errorf("outer: %w", &pgconn.PgError{Code: pgerrcode.UniqueViolation}),
			constraint: true, foreign: false, busy: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsConstraint(tt.err); got != tt.constraint {
				t.Errorf("IsConstraint() = %v, want %v", got, tt.constraint)
			}
			if got := IsForeignKey(tt.err); got != tt.foreign {
				t.Errorf("IsForeignKey() = %v, want %v", got, tt.foreign)
			}
			if got := IsBusy(tt.err); got != tt.busy {
				t.Errorf("IsBusy() = %v, want %v", got, tt.busy)
			}
		})
	}
}
