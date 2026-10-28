package curation

import (
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TestMaintenanceSpecs_ChainsRoutineOrder pins the maintenance routine's task shape: reconcile is the
// pending root, identity, audit and measure wait on their predecessor in that order, every payload is a
// non-nil JSON object (the enqueue discards a waiting child with a nil payload), and the task ids are
// fresh across calls so a second routine never collides on the unique task-id constraint.
func TestMaintenanceSpecs_ChainsRoutineOrder(t *testing.T) {
	specs := MaintenanceSpecs()
	testutil.AssertEqual(t, len(specs), 4, "one spec per routine pass")

	wantTypes := []types.TaskType{
		types.Task.Type.TagReconcile,
		types.Task.Type.TagIdentity,
		types.Task.Type.TagAudit,
		types.Task.Type.TagMeasure,
	}
	for i, spec := range specs {
		testutil.AssertEqual(t, spec.Type, wantTypes[i], "pass order")
	}

	if specs[0].Status != types.Task.Status.Pending {
		t.Errorf("reconcile status = %s, want pending (the batch's root)", specs[0].Status)
	}
	if specs[0].ParentTaskID != "" {
		t.Errorf("reconcile parent = %q, want empty (root)", specs[0].ParentTaskID)
	}
	for i := 1; i < len(specs); i++ {
		if specs[i].Status != types.Task.Status.Waiting {
			t.Errorf("%s status = %s, want waiting", wantTypes[i], specs[i].Status)
		}
		if specs[i].ParentTaskID != specs[i-1].TaskID {
			t.Errorf("%s parent = %q, want the previous pass %q", wantTypes[i], specs[i].ParentTaskID, specs[i-1].TaskID)
		}
	}

	// every chain parent must carry the explicit id its child names; the trailing leaf may rely on the
	// enqueue to generate one.
	for i, spec := range specs {
		if i < len(specs)-1 && spec.TaskID == "" {
			t.Fatalf("%s carries no task id, but its successor names it as a parent", wantTypes[i])
		}
		var payload map[string]any
		if err := json.Unmarshal(spec.Payload, &payload); err != nil {
			t.Errorf("%s payload %q is not a JSON object: %v", wantTypes[i], spec.Payload, err)
		}
	}

	second := MaintenanceSpecs()
	seen := map[string]bool{}
	for _, spec := range specs {
		if spec.TaskID != "" {
			seen[spec.TaskID] = true
		}
	}
	for _, spec := range second {
		if spec.TaskID == "" {
			continue
		}
		if seen[spec.TaskID] {
			t.Errorf("task id %q reused across calls; the enqueue would abort on the unique constraint", spec.TaskID)
		}
	}
}
