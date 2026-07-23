package task

type Error struct {
	ReqID      string
	Err        error
	PauseBatch bool
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }
