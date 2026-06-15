package task

import (
	"context"
	"encoding/json"
)

type Handler interface {
	Handle(ctx context.Context, task Task) (json.RawMessage, error)
}

type Dedupable interface {
	DedupKey(payload json.RawMessage) string
}
