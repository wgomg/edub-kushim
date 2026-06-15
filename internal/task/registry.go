package task

import (
	"encoding/json"
	"fmt"
)

type Registry struct {
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

func (r *Registry) Register(taskType string, h Handler) {
	r.handlers[taskType] = h
}

func (r *Registry) Get(taskType string) (Handler, error) {
	h, ok := r.handlers[taskType]
	if !ok {
		return nil, fmt.Errorf("unknown task type: %q", taskType)
	}
	return h, nil
}

func (r *Registry) DedupKey(taskType string, payload json.RawMessage) string {
	h, err := r.Get(taskType)
	if err != nil {
		return ""
	}
	if dd, ok := h.(Dedupable); ok {
		return dd.DedupKey(payload)
	}
	return ""
}
