package matcher

import "context"

type Candidate struct {
	Tag        string  `json:"tag"`
	Similarity float64 `json:"similarity"`
}

type RankResult struct {
	KeptName   string      `json:"kept_name"`
	Candidates []Candidate `json:"candidates"`
}

type SimilarPair struct {
	A          string  `json:"a"`
	B          string  `json:"b"`
	Similarity float64 `json:"similarity"`
}

type Matcher interface {
	Match(ctx context.Context, docId, input string) ([]string, error)
	Rank(ctx context.Context, docId string, queries []string) ([]RankResult, error)
	SimilarPairs(ctx context.Context) ([]SimilarPair, error)
	Close()
	Name() string
}

type Embedder interface {
	Encode(ctx context.Context, docId *string, texts []string) ([][]float32, error)
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
