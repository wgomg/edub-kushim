package cache

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	defaultDim = 384
	batchSize  = 32
)

func BuildTagCache(ctx context.Context, db *sql.DB, logger *utils.Logger, enc tagmatcher.Embedder, store *EmbeddingStore) error {
	queries := database.NewQueries(db)
	tagNames, err := queries.ListAllTagsNames(ctx)
	if err != nil {
		return fmt.Errorf("list tags: %w", err)
	}

	store.attrs = map[string]any{
		"dim":        defaultDim,
		"model":      "bge-m3",
		"normalized": true,
	}

	if len(tagNames) == 0 {
		return nil
	}

	embeddings := make(map[string][]float32, len(tagNames))
	for i := 0; i < len(tagNames); i += batchSize {
		end := min(i+batchSize, len(tagNames))
		batch := tagNames[i:end]

		out, err := enc.Encode(ctx, nil, batch)
		if err != nil {
			return fmt.Errorf("build tag cache encoding failed: %w", err)
		}

		for j, name := range batch {
			embeddings[name] = out[j]
		}
	}

	store.myu.Lock()
	store.entries = embeddings
	store.myu.Unlock()

	return nil
}
