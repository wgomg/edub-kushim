package task

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

func TestNewBatchSpec(t *testing.T) {
	ok := []struct {
		name     string
		origin   types.BatchOrigin
		schedule string
	}{
		{"cli without schedule", types.Batch.Origin.CLI, ""},
		{"api without schedule", types.Batch.Origin.API, ""},
		{"polling without schedule", types.Batch.Origin.Polling, ""},
		{"setup without schedule", types.Batch.Origin.Setup, ""},
		{"wizard without schedule", types.Batch.Origin.Wizard, ""},
		{"scheduler with schedule name", types.Batch.Origin.Schedule, "nightly-backup"},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			spec, err := NewBatchSpec(c.origin, c.schedule)
			if err != nil {
				t.Fatalf("NewBatchSpec(%q, %q) error: %v", c.origin, c.schedule, err)
			}
			if spec.Origin() != c.origin {
				t.Errorf("Origin() = %q, want %q", spec.Origin(), c.origin)
			}
			if spec.Schedule() != c.schedule {
				t.Errorf("Schedule() = %q, want %q", spec.Schedule(), c.schedule)
			}
		})
	}

	bad := []struct {
		name     string
		origin   types.BatchOrigin
		schedule string
	}{
		{"empty origin", "", ""},
		{"unknown origin", "cron", ""},
		{"schedule origin without a name", types.Batch.Origin.Schedule, ""},
		{"cli origin with a schedule name", types.Batch.Origin.CLI, "nightly-backup"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewBatchSpec(c.origin, c.schedule); err == nil {
				t.Errorf("NewBatchSpec(%q, %q) expected an error", c.origin, c.schedule)
			}
		})
	}
}

// TestWithFollowUp pins the declaration constructor: the settle could not turn an empty, id-carrying,
// parent-linked, non-pending or payload-less task into work, so each is refused, and the accepted spec
// stores a copy the caller later mutations cannot reach.
func TestWithFollowUp(t *testing.T) {
	spec, err := NewBatchSpec(types.Batch.Origin.CLI, "")
	testutil.AssertNoError(t, err, "spec")

	measure := []TaskSpec{{Type: types.Task.Type.TagMeasure, Payload: json.RawMessage(`{}`)}}
	declared, err := WithFollowUp(spec, measure)
	testutil.AssertNoError(t, err, "declare")
	testutil.AssertEqual(t, len(declared.followUp), 1, "one declared task")
	testutil.AssertEqual(t, declared.followUp[0].Type, types.Task.Type.TagMeasure, "declared type")

	measure[0].Type = types.Task.Type.Consume
	testutil.AssertEqual(t, declared.followUp[0].Type, types.Task.Type.TagMeasure, "the spec stores a copy, not the caller's slice")

	bad := []struct {
		name  string
		tasks []TaskSpec
	}{
		{"empty declaration", nil},
		{"task naming an id", []TaskSpec{{Type: types.Task.Type.TagMeasure, Payload: json.RawMessage(`{}`), TaskID: "fixed"}}},
		{"task with a parent link", []TaskSpec{{Type: types.Task.Type.TagMeasure, Payload: json.RawMessage(`{}`), ParentTaskID: "parent"}}},
		{"waiting status", []TaskSpec{{Type: types.Task.Type.TagMeasure, Payload: json.RawMessage(`{}`), Status: types.Task.Status.Waiting}}},
		{"failed status", []TaskSpec{{Type: types.Task.Type.TagMeasure, Payload: json.RawMessage(`{}`), Status: types.Task.Status.Failed}}},
		{"nil payload", []TaskSpec{{Type: types.Task.Type.TagMeasure}}},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, err := WithFollowUp(spec, c.tasks); err == nil {
				t.Errorf("WithFollowUp(%v) expected an error", c.tasks)
			}
		})
	}
}

// TestEnqueueBatchFollowUpPersistence pins the storage half of the link: a follow-up declared through
// WithFollowUp rides the batch row in the wire form the settle later decodes.
func TestEnqueueBatchFollowUpPersistence(t *testing.T) {
	_, _, client := setupTaskTestClient(t)
	ctx := context.Background()

	catalog, err := NewCatalog(map[types.TaskType]TypeWiring{
		types.Task.Type.Consume:    {DedupKey: ConstantDedupKey("consume-persist")},
		types.Task.Type.TagMeasure: {DedupKey: ConstantDedupKey("tagmeasure-persist")},
	})
	testutil.AssertNoError(t, err, "catalog")

	spec, err := NewBatchSpec(types.Batch.Origin.API, "")
	testutil.AssertNoError(t, err, "spec")
	spec, err = WithFollowUp(spec, []TaskSpec{{Type: types.Task.Type.TagMeasure, Payload: json.RawMessage(`{}`)}})
	testutil.AssertNoError(t, err, "declare follow-up")

	result, err := NewEnqueuer(client, catalog).EnqueueBatch(ctx, spec,
		[]TaskSpec{{Type: types.Task.Type.Consume, Payload: json.RawMessage(`{}`)}})
	testutil.AssertNoError(t, err, "enqueue")
	if result.BatchID == "" {
		t.Fatal("enqueue created no batch")
	}

	batch, err := client.Queries.GetBatch(ctx, result.BatchID)
	testutil.AssertNoError(t, err, "read batch")
	if batch.FollowUp == nil {
		t.Fatal("declaration not persisted on the batch row")
	}
	var stored []followUpTask
	testutil.AssertNoError(t, json.Unmarshal(*batch.FollowUp, &stored), "decode stored declaration")
	testutil.AssertEqual(t, len(stored), 1, "one declared task")
	testutil.AssertEqual(t, stored[0].Type, types.Task.Type.TagMeasure, "declared type survives the round trip")
}
