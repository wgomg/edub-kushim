package task

import (
	"fmt"

	"github.com/wgomg/edub-kushim/internal/types"
)

// Registry maps task types to their handlers, wired once at the
// composition root. It is read-only after startup and safe for concurrent
// use.
type Registry struct {
	handlers map[types.TaskType]Handler
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[types.TaskType]Handler)}
}

// Register installs a handler for a task type, replacing any previous
// one.
func (r *Registry) Register(taskType types.TaskType, h Handler) {
	r.handlers[taskType] = h
}

// Get returns the handler registered for a task type; unknown types error.
func (r *Registry) Get(taskType types.TaskType) (Handler, error) {
	h, ok := r.handlers[taskType]
	if !ok {
		return nil, fmt.Errorf("unknown task type: %q", taskType)
	}
	return h, nil
}
