package task

import (
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/types"
)

// Task is one claimed task handed to a Handler: the row identity, its type and batch, and the payload bytes.
// The execution guard belongs to the runner, which owns the transition and settle writes.
type Task struct {
	ID       int64
	TaskID   string
	TaskType types.TaskType
	BatchID  string
	Payload  json.RawMessage
}
