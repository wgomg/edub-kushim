package queue

import (
	"context"
	"database/sql"

	"github.com/wgomg/edub-kushim/internal/database"
)

type TaskHandler interface {
	Handle(ctx context.Context, task database.Task) (documentID sql.NullInt64, err error)
}
