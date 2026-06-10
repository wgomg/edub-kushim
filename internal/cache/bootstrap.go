package cache

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	defaultDim = 384
	batchSize  = 32
)

func BuildTagCache(ctx context.Context, db *sql.DB, logger *utils.Logger, tmCfg config.TagMatcherConfig) (*Cache, error) {
	queries := database.NewQueries(db)
	tagNames, err := queries.ListAllTagsNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}

	attrs := map[string]any{
		"dim":        defaultDim,
		"model":      tmCfg.Hugot.Model,
		"normalized": true,
	}

	c := New()
	c.Set("tags", NewEmbeddingStore(nil, attrs))

	if len(tagNames) == 0 {
		return c, nil
	}

	hugot, err := tagmatcher.NewHugot(logger, tmCfg, "bootstrap")
	if err != nil {
		return c, nil
	}
	defer hugot.Close()

	embeddings := make(map[string][]float32, len(tagNames))

	for i := 0; i < len(tagNames); i += batchSize {
		end := min(i+batchSize, len(tagNames))
		batch := tagNames[i:end]

		out, err := hugot.Encode(ctx, nil, batch)
		if err != nil {
			return nil, fmt.Errorf("build tag cache encoding failed: %w", err)
		}

		for j, name := range batch {
			embeddings[name] = out[j]
		}
	}

	c.Set("tags", NewEmbeddingStore(embeddings, attrs))
	return c, nil
}
