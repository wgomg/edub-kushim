package matcher

import (
	"context"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
)

const (
	defaultDim = 384
	batchSize  = 32
)

// BuildTagCache replaces the store's contents with embeddings of every
// promoted tag name, encoded in batches, and records the cache metadata
// (dimension, model, normalization) as attributes. An empty vocabulary
// leaves an empty store. Intended at daemon startup, before serving.
func BuildTagCache(ctx context.Context, queries *database.Queries, logger *log.Logger, enc Embedder, store *TagEmbeddingStore) error {
	tagNames, err := queries.ListPromotedTagNames(ctx)
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
