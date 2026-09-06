package task

import "encoding/json"

type Task struct {
	ID       int64
	TaskID   string
	TaskType string
	BatchID  string
	Payload  json.RawMessage
}
