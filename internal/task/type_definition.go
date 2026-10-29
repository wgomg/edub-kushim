package task

import (
	"context"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/types"
)

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

// ExecutorFlag is the reserved argument that a launcher prepends to a forked batch executor's argv.
// It declares that the child's schema lifecycle belongs to the launcher, so the CLI builds a connect-only
// container instead of bootstrapping; it is internal to the fork contract and never appears in verb help.
const ExecutorFlag = "--executor"

// TypeDefinition is the declaration of one task type. It is a composition of concern-specific values,
// not one struct owning every concern: identity, the queued and direct lock policy, dedup derivation,
// preconditions, the CLI surface, hosting eligibility and the worker count. Fields are added only by the
// stage that consumes them, into this same type. It never carries a handler, an attach pass, handler
// construction, a legacy source, a pool interval, a pooling predicate or any lifecycle-ownership flag;
// handlers stay in the registry. It carries no task-set builder: the producers that own the work build
// their own []TaskSpec and hand it to the enqueue surface.
type TypeDefinition struct {
	Type  types.TaskType
	Locks LockPolicy

	// DirectLocks overrides the lock policy for direct CLI runs; nil uses the queued Locks set.
	DirectLocks *LockPolicy

	// CLI names the command that executes this type's tasks in batch mode, such as "consume"; an empty
	// value means the type has no batch-mode task CLI: an in-process registration executes it, and a fork-only
	// dispatcher refuses its batches.
	CLI string
	// BatchSelector is the flag CLI takes the batch id on, such as "--from-batch"; the launcher always
	// forks ExecutorFlag CLI BatchSelector <batch-id> --exec-token <token>, so the marker, the selector
	// and the token form one contract.
	BatchSelector string
	// Hosts lists the processes allowed to dispatch this type's batches.
	Hosts []types.Host

	// Concurrency is the number of workers the shared execution library runs for this task type inside a
	// batch execution; a value below 1 means one worker.
	Concurrency int

	// DedupKey derives the type's dedup key from a task payload; the owning producer package supplies it
	// when the process constructs the catalog. A pending task of a type without a hook in the process's
	// catalog is refused by the enqueue surface instead of being created without deduplication.
	DedupKey      DedupKeyFunc
	Preconditions []Precondition
}

// TypeCatalog looks up the task type definitions built at a process boundary. Enqueue, dispatch and
// scheduling read definitions through this interface; they do not build or attach them.
type TypeCatalog interface {
	// Definition returns the entry for a task type; an unknown type is an error.
	Definition(taskType types.TaskType) (TypeDefinition, error)
}

// DedupKeyFunc derives the dedup key of one task from its payload.
type DedupKeyFunc func(json.RawMessage) string
