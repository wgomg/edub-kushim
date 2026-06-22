package tagmatcher

import "context"

type Matcher interface {
	Match(ctx context.Context, docId, input string, candidateTags []string) ([]string, error)
	Close()
	Name() string
}

type Embedder interface {
	Encode(ctx context.Context, docId *string, texts []string) ([][]float32, error)
	Consolidate(ctx context.Context, docId string, queries []string) ([]string, error)
	Close()
	Name() string
}

type EmbeddingStore interface {
	Get(key string) ([]float32, bool)
	Entries() map[string][]float32
}
