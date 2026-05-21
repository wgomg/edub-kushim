package task

import (
	"context"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/database"
)

type Handler interface {
	Handle(ctx context.Context, task database.Task) (json.RawMessage, error)
}

type Dedupable interface {
	DedupKey(payload json.RawMessage) string
}
