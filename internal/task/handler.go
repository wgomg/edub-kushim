// Package task is the task system: the lifecycle store, the claim and settle protocol under the execution
// lease, batch ownership with heartbeats, the registry of task-type handlers, the runner that claims tasks
// and owns their lifecycle, the worker sets of one batch execution, the transactional enqueue, the task type
// definition model and its enqueue contract, and the progress tracker. The lifecycle, batch-ownership and
// queue-semantics contracts are documented in docs/developer-guide/task-system.md; this package implements
// them.
package task

import (
	"context"
	"encoding/json"
)

// Handler is the per-task-type seam: the domain packages implement it and each command boundary's registry
// registers the implementations it runs. Handle receives one claimed task and returns the result stored on
// the task row; an error fails the task, and a wrapped *Error adds a request id for log correlation and,
// with PauseBatch, pauses the whole batch. Handlers are pure work functions: they perform no batch-status
// write, lock acquisition, drain or settle.
type Handler interface {
	// Handle performs the task's work. The runner fails the task on panic, so handlers should report failures
	// through the returned error instead.
	Handle(ctx context.Context, task Task) (json.RawMessage, error)
}
