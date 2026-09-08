package task

import (
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/types"
)

type Registry struct {
	handlers map[types.TaskType]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[types.TaskType]Handler)}
}

func (r *Registry) Register(taskType types.TaskType, h Handler) {
	r.handlers[taskType] = h
}

func (r *Registry) Get(taskType types.TaskType) (Handler, error) {
	h, ok := r.handlers[taskType]
	if !ok {
		return nil, fmt.Errorf("unknown task type: %q", taskType)
	}
	return h, nil
}

func (r *Registry) DedupKey(taskType types.TaskType, payload json.RawMessage) string {
	h, err := r.Get(taskType)
	if err != nil {
		return ""
	}
	if dd, ok := h.(Dedupable); ok {
		return dd.DedupKey(payload)
	}
	return ""
}
