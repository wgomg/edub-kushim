package task

import "encoding/json"

type Task struct {
	ID       int64
	TaskID   string
	TaskType string
	Payload  json.RawMessage
}
