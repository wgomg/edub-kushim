package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Enricher struct {
	config  *config.Config
	logger  *utils.Logger
	queries *database.Queries
}

func NewEnricher(cfg *config.Config, logger *utils.Logger, db *sql.DB) (*Enricher, error) {
	return &Enricher{
		config:  cfg,
		logger:  logger,
		queries: database.NewQueries(db),
	}, nil
}

func (e *Enricher) Enrich(ctx context.Context, documentID int64) (json.RawMessage, error) {
	e.logger.Debug(nil, "enrich stub: document_id=%d (LLM pipeline not yet implemented)", documentID)

	return json.Marshal(struct {
		DocumentID int64  `json:"document_id"`
		Status     string `json:"status"`
	}{
		DocumentID: documentID,
		Status:     "skipped",
	})
}
