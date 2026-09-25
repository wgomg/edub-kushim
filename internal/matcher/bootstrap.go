package matcher

import (
	"context"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
)

// Declared in this untagged file, not in the cgo-tagged hugot.go, so
// CGO_ENABLED=0 builds still see it; hugot.go shares the value from here.
const embedBatchSize = 32

// BuildTagCache replaces the store's contents with embeddings of every
// promoted tag name, encoded in batches. An empty vocabulary leaves an
// empty store. Intended at daemon startup, before serving.
func BuildTagCache(ctx context.Context, queries *database.Queries, enc Embedder, store *TagEmbeddingStore) error {
	tagNames, err := queries.ListPromotedTagNames(ctx)
	if err != nil {
		return fmt.Errorf("list tags: %w", err)
	}

	if len(tagNames) == 0 {
		return nil
	}

	embeddings := make(map[string][]float32, len(tagNames))
	for i := 0; i < len(tagNames); i += embedBatchSize {
		end := min(i+embedBatchSize, len(tagNames))
		batch := tagNames[i:end]

		out, err := enc.Encode(ctx, "", batch)
		if err != nil {
			return fmt.Errorf("build tag cache encoding failed: %w", err)
		}

		for j, name := range batch {
			embeddings[name] = out[j]
		}
	}

	store.mu.Lock()
	store.entries = embeddings
	store.mu.Unlock()

	return nil
}
