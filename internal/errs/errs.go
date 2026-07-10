package errs

import (
	"database/sql"
	"errors"
	"fmt"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type Kind int

const (
	KindNotFound Kind = iota
	KindConflict
	KindInvalid
	KindInternal
)

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

func ENotFound(op string, cause error) *Error {
	return &Error{Kind: KindNotFound, Op: op, Cause: cause}
}

func EConflict(op string, cause error) *Error {
	return &Error{Kind: KindConflict, Op: op, Cause: cause}
}

func EInvalid(op string, cause error) *Error {
	return &Error{Kind: KindInvalid, Op: op, Cause: cause}
}

func EInternal(op string, cause error) *Error {
	return &Error{Kind: KindInternal, Op: op, Cause: cause}
}

func KindOf(err error) Kind {
	if err == nil {
		return KindInternal
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

func IsConstraint(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
	}
	return false
}

func IsForeignKey(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
	}
	return false
}

func IsBusy(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()&0xff == sqlite3.SQLITE_BUSY || sqliteErr.Code()&0xff == sqlite3.SQLITE_LOCKED
	}
	return false
}

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
