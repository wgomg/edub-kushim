package task

import (
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/types"
)

type Task struct {
	ID       int64
	TaskID   string
	TaskType types.TaskType
	BatchID  string
	Payload  json.RawMessage
}
