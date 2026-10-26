package curation

import (
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// MeasureSpecs returns the task set a tag measure run enqueues.
func MeasureSpecs() []task.TaskSpec {
	return []task.TaskSpec{{Type: types.Task.Type.TagMeasure, Payload: json.RawMessage("{}")}}
}
