// Package errs is the error-kind vocabulary the API maps to HTTP status
// codes: not found, conflict, invalid and internal, with the Postgres
// error mapping the services use at their boundaries.
package errs

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// Kind is the error class a caller can branch on.
type Kind int

// The kinds, in API-response order: 404, 409, 400, 500.
const (
	KindNotFound Kind = iota
	KindConflict
	KindInvalid
	KindInternal
)

// Error carries the kind, the operation that failed, and the cause; it
// unwraps to the cause and renders "op: kind: cause".
type Error struct {
	Kind  Kind
	Op    string
	Cause error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s: %v", e.Op, e.kindString(), e.Cause)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (e *Error) kindString() string {
	switch e.Kind {
	case KindNotFound:
		return "not found"
	case KindConflict:
		return "conflict"
	case KindInvalid:
		return "invalid"
	case KindInternal:
		return "internal"
	default:
		return "unknown"
	}
}

// ENotFound builds the not-found-kind error; EConflict, EInvalid and
// EInternal build the other kinds, all wrapping the cause with the
// operation name.
func ENotFound(op string, cause error) *Error {
	return &Error{Kind: KindNotFound, Op: op, Cause: cause}
}

// EConflict builds a conflict-kind error.
func EConflict(op string, cause error) *Error {
	return &Error{Kind: KindConflict, Op: op, Cause: cause}
}

// EInvalid builds an invalid-input-kind error.
func EInvalid(op string, cause error) *Error {
	return &Error{Kind: KindInvalid, Op: op, Cause: cause}
}

// EInternal builds an internal-kind error.
func EInternal(op string, cause error) *Error {
	return &Error{Kind: KindInternal, Op: op, Cause: cause}
}

// KindOf resolves the error's kind; anything that is not an *Error, or
// nil, is internal.
func KindOf(err error) Kind {
	if err == nil {
		return KindInternal
	}
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Kind
	}
	return KindInternal
}

// IsConstraint reports a unique-violation from Postgres.
func IsConstraint(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == pgerrcode.UniqueViolation
	}
	return false
}

// IsConstraintOn reports a unique violation from Postgres raised by the named constraint or index, so a
// caller that treats one conflict as a skip can tell its own index apart from every other unique rule.
func IsConstraintOn(err error, name string) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == pgerrcode.UniqueViolation && pgErr.ConstraintName == name
	}
	return false
}

// IsForeignKey reports a foreign-key violation from Postgres.
func IsForeignKey(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == pgerrcode.ForeignKeyViolation
	}
	return false
}

// IsBusy reports a deadlock or serialization failure from Postgres, the
// transient errors callers retry.
func IsBusy(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == pgerrcode.DeadlockDetected || pgErr.Code == pgerrcode.SerializationFailure
	}
	return false
}

// FromDB maps a persistence-layer error into the vocabulary: no rows is
// not found, a unique violation is a conflict, anything else is internal.
// nil passes through.
func FromDB(err error, op string) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, sql.ErrNoRows) {
		return ENotFound(op, err)
	}

	if IsConstraint(err) {
		return EConflict(op, err)
	}

	return EInternal(op, err)
}
