package task

import (
	"context"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/types"
)

// BuildInput is the typed input a task-set builder accepts. Each task type declares its own input struct
// implementing this interface next to its definition; a boundary parses all its raw surface input (CLI
// arguments, API fields, schedule settings) into that concrete struct and passes the struct, never the
// raw arguments. The interface cannot seal its implementers, because TaskType is exported; a builder must
// verify TaskType and then assert its own struct before reading any field, rejecting a mismatch.
type BuildInput interface {
	TaskType() types.TaskType
}

// Precondition checks one environment requirement of a task type before any work is created, such as
// an external tool being installed. The batch enqueue evaluates the preconditions of every selected task
// type definition before anything is created; one failure aborts the enqueue with a typed error and
// writes nothing.
type Precondition func(ctx context.Context) error

// LockPolicy is a task type's queued lock declaration: the named locks its execution holds for the whole
// batch run, and the locks a claim must see free before it may start. Both slices are unordered sets;
// an empty slice declares no lock in that direction.
type LockPolicy struct {
	Holds    []types.LockName
	WaitsFor []types.LockName
}

// TypeDefinition is the declaration of one task type. It is a composition of concern-specific values,
// not one struct owning every concern: identity, the queued and direct lock policy, dedup derivation,
// preconditions and the typed task-set builder. Fields are added only by the stage that consumes them
// (CLI surface, hosts, concurrency, schedules), into this same type. It never carries a handler, an
// attach pass, handler construction, a legacy source, a pool interval or worker count, and a pooling
// predicate or a lifecycle-ownership flag; handlers stay in the registry.
type TypeDefinition struct {
	Type  types.TaskType
	Locks LockPolicy

	// DirectLocks overrides the lock policy for direct CLI runs; nil uses the queued Locks set.
	DirectLocks   *LockPolicy
	DedupKey      func(json.RawMessage) string
	Preconditions []Precondition
	BuildTasks    func(ctx context.Context, input BuildInput) ([]TaskSpec, error)
}

// LocksForDirectMode returns the lock policy a direct CLI run of the type uses: its declared direct
// policy when one is set, otherwise the queued lock set.
func (d TypeDefinition) LocksForDirectMode() LockPolicy {
	if d.DirectLocks != nil {
		return *d.DirectLocks
	}
	return d.Locks
}

// TypeCatalog looks up the task type definitions built by the composition root. Enqueue, dispatch and
// scheduling read definitions through this interface; they do not build or attach them.
type TypeCatalog interface {
	// Definition returns the entry for a task type; an unknown type is an error.
	Definition(taskType types.TaskType) (TypeDefinition, error)
}
