package task

import (
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/types"
)

// BatchSpec is the requester metadata every enqueue carries: the entry-point origin and, when scheduled,
// the name of the schedule that produced the run. Its fields stay unexported so only NewBatchSpec can set
// them; the constructor and the enqueue share one validation, so a zero-value spec is rejected too.
type BatchSpec struct {
	origin   types.BatchOrigin
	schedule string
}

// NewBatchSpec validates the origin and schedule supplied by an entry-point boundary and returns the
// spec its enqueue call carries. An empty or unknown origin is rejected rather than defaulted, and a
// schedule name is required with origin schedule and rejected with every other origin, so that only the
// scheduler can produce a scheduled batch.
func NewBatchSpec(origin types.BatchOrigin, schedule string) (BatchSpec, error) {
	spec := BatchSpec{origin: origin, schedule: schedule}
	if err := spec.validate(); err != nil {
		return BatchSpec{}, err
	}
	return spec, nil
}

// validate re-checks the invariant the constructor enforces, so the enqueue can reject a zero-value spec
// from a caller that skipped NewBatchSpec with the same check the constructor applies.
func (s BatchSpec) validate() error {
	if !s.origin.Valid() {
		return fmt.Errorf("batch origin %q is not declared", s.origin)
	}
	if s.origin == types.Batch.Origin.Schedule && s.schedule == "" {
		return fmt.Errorf("batch origin %q requires a schedule name", s.origin)
	}
	if s.origin != types.Batch.Origin.Schedule && s.schedule != "" {
		return fmt.Errorf("batch origin %q carries no schedule, got %q", s.origin, s.schedule)
	}
	return nil
}

// Origin returns the entry point that requested the batch.
func (s BatchSpec) Origin() types.BatchOrigin { return s.origin }

// Schedule returns the schedule that produced the batch, empty for non-scheduled work.
func (s BatchSpec) Schedule() string { return s.schedule }

// TaskSpec describes one task row an enqueue creates. The dedup key is not part of it: the task type
// definition derives the key from Payload, so a task cannot be created with a key its type does not
// own. An empty TaskID asks the enqueue to generate one; an explicit id keeps linked waiting children
// addressable. An empty Status means pending, and waiting creates a pre-linked child that activates
// when its parent completes.
type TaskSpec struct {
	Type    types.TaskType
	TaskID  string
	Payload json.RawMessage
	Status  types.TaskStatus
}
