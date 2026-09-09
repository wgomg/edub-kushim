package task

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/types"
)

type Task struct {
	ID         int64
	TaskID     string
	TaskType   types.TaskType
	BatchID    string
	Payload    json.RawMessage
	ClaimToken uuid.UUID
}
