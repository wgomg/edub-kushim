package consumption

import (
	"encoding/json"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// IngestFile identifies one file entering the ingest pipeline. MD5 is the content checksum the consume
// task's dedup key derives from, and DocumentID is the id the consume task will create for it.
type IngestFile struct {
	Path       string
	Index      int
	MD5        string
	DocumentID string
}

// IngestTaskSpecs returns one file's consume task and its pre-linked waiting successors: an enrich task
// always, and a thumbnail task when thumbnailsEnabled. Each successor names the consume task as its parent
// and carries everything it can know at enqueue; the thumbnail declares the storage path it inherits from
// the consume result, because only the run produces it. The task system activates them when the consume
// completes and discards them with it when it fails. The returned consume spec is always the first element.
func IngestTaskSpecs(file IngestFile, thumbnailsEnabled bool) []task.TaskSpec {
	consumeTaskID := uuid.New().String()

	consumePayload, _ := json.Marshal(map[string]any{
		"file_path":   file.Path,
		"file_index":  file.Index,
		"document_id": file.DocumentID,
		"md5":         file.MD5,
	})

	enrichPayload, _ := json.Marshal(map[string]any{
		"file_name":   filepath.Base(file.Path),
		"file_index":  file.Index,
		"document_id": file.DocumentID,
	})

	tasks := []task.TaskSpec{{
		Type:    types.Task.Type.Consume,
		TaskID:  consumeTaskID,
		Payload: consumePayload,
		Status:  types.Task.Status.Pending,
	}, {
		Type:         types.Task.Type.Enrich,
		ParentTaskID: consumeTaskID,
		Payload:      enrichPayload,
		Status:       types.Task.Status.Waiting,
	}}

	if thumbnailsEnabled {
		thumbnailPayload, _ := json.Marshal(map[string]any{
			"file_name":            filepath.Base(file.Path),
			"file_index":           file.Index,
			"document_id":          file.DocumentID,
			task.InheritPayloadKey: []string{"storage_path"},
		})
		tasks = append(tasks, task.TaskSpec{
			Type:         types.Task.Type.Thumbnail,
			ParentTaskID: consumeTaskID,
			Payload:      thumbnailPayload,
			Status:       types.Task.Status.Waiting,
		})
	}

	return tasks
}
