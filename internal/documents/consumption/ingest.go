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

// IngestTaskSpecs returns one file's consume task and its pre-linked waiting children: an enrich task
// always, and a thumbnail task when thumbnailsEnabled. The consume payload names both children by id,
// and each child names the consume task as its parent, so a dedup-skipped consume task skips its own
// children with it. ids are generated per call; the returned consume spec is always the first element.
func IngestTaskSpecs(file IngestFile, thumbnailsEnabled bool) []task.TaskSpec {
	consumeTaskID := uuid.New().String()
	enrichTaskID := uuid.New().String()
	thumbnailTaskID := uuid.New().String()

	consumePayloadMap := map[string]any{
		"file_path":    file.Path,
		"file_index":   file.Index,
		"on_completed": enrichTaskID,
		"document_id":  file.DocumentID,
		"md5":          file.MD5,
	}
	if thumbnailsEnabled {
		consumePayloadMap["on_completed_thumbnail"] = thumbnailTaskID
	}
	consumePayload, _ := json.Marshal(consumePayloadMap)

	enrichPayload, _ := json.Marshal(map[string]any{
		"waiting_for": consumeTaskID,
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
		TaskID:       enrichTaskID,
		ParentTaskID: consumeTaskID,
		Payload:      enrichPayload,
		Status:       types.Task.Status.Waiting,
	}}

	if thumbnailsEnabled {
		thumbnailPayload, _ := json.Marshal(map[string]any{
			"waiting_for": consumeTaskID,
			"file_name":   filepath.Base(file.Path),
			"file_index":  file.Index,
		})
		tasks = append(tasks, task.TaskSpec{
			Type:         types.Task.Type.Thumbnail,
			TaskID:       thumbnailTaskID,
			ParentTaskID: consumeTaskID,
			Payload:      thumbnailPayload,
			Status:       types.Task.Status.Waiting,
		})
	}

	return tasks
}
