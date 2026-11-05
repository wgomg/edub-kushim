package curation

import (
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TagSplitReaskDedupKey is the active-dedup key of the split re-ask pass, shared by the scheduler's
// enqueue path and the task handlers so one standing batch is never duplicated.
const TagSplitReaskDedupKey = "tagsplitreask"

// SplitReaskSpecs returns the task set a split re-ask pass enqueues: one task re-asking the cached
// abstentions under the current split-gate policy versions.
func SplitReaskSpecs() []task.TaskSpec {
	return []task.TaskSpec{{Type: types.Task.Type.TagSplitReask, Payload: json.RawMessage("{}")}}
}
