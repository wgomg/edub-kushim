package task

// Error is the task-system error: handlers wrap failures in it to attach a
// request id for log correlation and, with PauseBatch, to ask that the
// whole batch pause after the failure.
type Error struct {
	ReqID      string
	Err        error
	PauseBatch bool
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }
