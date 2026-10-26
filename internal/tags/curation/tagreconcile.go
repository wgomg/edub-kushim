package curation

import (
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TagReconcileDedupKey is the active-dedup key shared by the enqueue path and the task handler so they agree.
const TagReconcileDedupKey = "tagreconcile"

// ReconcileSpecs returns the task set a tag reconcile run enqueues.
func ReconcileSpecs() []task.TaskSpec {
	return []task.TaskSpec{{Type: types.Task.Type.TagReconcile, Payload: json.RawMessage("{}")}}
}
