package curation

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// MaintenanceSpecs returns the task set one maintenance routine enqueues: the split re-ask as the batch's
// pending root and reconciliation, identity, coverage audit and measurement chained behind it as waiting
// successors, so each pass activates only after its predecessor completes and the whole routine runs in one
// batch. The pass order is re-ask, reconciliation, identity, coverage audit and measurement: re-asking first
// refreshes the abstentions the reconciliation sweep reads, so the compounds it releases are re-disposed in
// the same routine, and measurement closes the cycle on fresh evidence. Every task id is a fresh UUID,
// because task ids are unique across the task table, and the payloads are non-nil JSON objects, because the
// enqueue discards a waiting child whose payload is nil.
func MaintenanceSpecs() []task.TaskSpec {
	reaskID := uuid.New().String()
	reconcileID := uuid.New().String()
	identityID := uuid.New().String()
	auditID := uuid.New().String()
	return []task.TaskSpec{
		{
			Type:    types.Task.Type.TagSplitReask,
			TaskID:  reaskID,
			Payload: json.RawMessage("{}"),
			Status:  types.Task.Status.Pending,
		},
		{
			Type:         types.Task.Type.TagReconcile,
			TaskID:       reconcileID,
			ParentTaskID: reaskID,
			Payload:      json.RawMessage("{}"),
			Status:       types.Task.Status.Waiting,
		},
		{
			Type:         types.Task.Type.TagIdentity,
			TaskID:       identityID,
			ParentTaskID: reconcileID,
			Payload:      json.RawMessage("{}"),
			Status:       types.Task.Status.Waiting,
		},
		{
			Type:         types.Task.Type.TagAudit,
			TaskID:       auditID,
			ParentTaskID: identityID,
			Payload:      json.RawMessage("{}"),
			Status:       types.Task.Status.Waiting,
		},
		{
			Type:         types.Task.Type.TagMeasure,
			ParentTaskID: auditID,
			Payload:      json.RawMessage("{}"),
			Status:       types.Task.Status.Waiting,
		},
	}
}
