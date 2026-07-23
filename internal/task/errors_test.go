package task

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorDelegatesToWrapped(t *testing.T) {
	inner := fmt.Errorf("something broke")
	e := &Error{ReqID: "doc-1", Err: inner}

	if e.Error() != "something broke" {
		t.Fatalf("Error(): got %q, want %q", e.Error(), "something broke")
	}
}

func TestErrorUnwrap(t *testing.T) {
	inner := fmt.Errorf("root cause")
	e := &Error{ReqID: "doc-1", Err: inner}

	if !errors.Is(e, inner) {
		t.Fatal("errors.Is should find inner error through Error.Unwrap")
	}
}

func TestErrorAsExtractsReqID(t *testing.T) {
	e := &Error{ReqID: "doc-123", Err: fmt.Errorf("something broke")}

	var tErr *Error
	if !errors.As(e, &tErr) {
		t.Fatal("errors.As should extract *Error")
	}
	if tErr.ReqID != "doc-123" {
		t.Fatalf("ReqID: got %q, want %q", tErr.ReqID, "doc-123")
	}
}

func TestErrorAsThroughWrappedError(t *testing.T) {
	inner := &Error{ReqID: "doc-456", Err: fmt.Errorf("root")}
	wrapped := fmt.Errorf("outer: %w", inner)

	var tErr *Error
	if !errors.As(wrapped, &tErr) {
		t.Fatal("errors.As should extract through fmt.Errorf wrapping")
	}
	if tErr.ReqID != "doc-456" {
		t.Fatalf("ReqID: got %q, want %q", tErr.ReqID, "doc-456")
	}
}

func TestErrorAsReturnsFalseForBareError(t *testing.T) {
	err := fmt.Errorf("plain error")

	var tErr *Error
	if errors.As(err, &tErr) {
		t.Fatal("errors.As should not match plain error")
	}
}

func TestErrorAsEmptyReqID(t *testing.T) {
	e := &Error{ReqID: "", Err: fmt.Errorf("no req id")}

	var tErr *Error
	if !errors.As(e, &tErr) {
		t.Fatal("errors.As should extract even with empty ReqID")
	}
	if tErr.ReqID != "" {
		t.Fatalf("ReqID: got %q, want empty", tErr.ReqID)
	}
}

func TestErrorPauseBatch(t *testing.T) {
	e := &Error{ReqID: "doc-1", Err: fmt.Errorf("credit error"), PauseBatch: true}

	var tErr *Error
	if !errors.As(e, &tErr) {
		t.Fatal("errors.As should extract *Error with PauseBatch")
	}
	if !tErr.PauseBatch {
		t.Fatal("PauseBatch should be true")
	}
}

func TestErrorPauseBatchDefaultFalse(t *testing.T) {
	e := &Error{ReqID: "doc-1", Err: fmt.Errorf("some error")}
	if e.PauseBatch {
		t.Fatal("PauseBatch should default to false")
	}
}
