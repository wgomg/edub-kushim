package task

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Task is one claimed task handed to a Handler: the row identity, its type
// and batch, the payload bytes, and the claim token a blocking task needs
// to complete or fail its claim.
type Task struct {
	ID         int64
	TaskID     string
	TaskType   types.TaskType
	BatchID    string
	Payload    json.RawMessage
	ClaimToken uuid.UUID
}
