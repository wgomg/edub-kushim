package tagmatcher

import "context"

type Matcher interface {
	Match(ctx context.Context, docId, input string) ([]string, error)
	Close()
	Name() string
}

type Embedder interface {
	Encode(ctx context.Context, docId *string, texts []string) ([][]float32, error)
	Consolidate(ctx context.Context, docId string, queries []string) ([]string, error)
	AddToStore(ctx context.Context, names []string) error
	RemoveFromStore(ctx context.Context, names []string) error
	Close()
	Name() string
}

type EmbeddingStore interface {
	Add(key string, embedding []float32)
	Remove(key string)
	Entries() map[string][]float32
}
