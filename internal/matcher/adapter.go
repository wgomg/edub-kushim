// Package matcher is the semantic matching domain: the embedding engine and
// its tag store on the daemon side, the Unix-socket client the API process
// uses instead, and the shared vocabulary of matches, ranks and similar
// pairs. The kushim binary runs the engine behind "kushim hugot"; edub talks
// to it through MatcherClient. Both sides satisfy the Matcher and Embedder
// seams the tools runner consumes.
package matcher

import "context"

// Candidate is one ranked match for a tag query: a stored tag name and its
// cosine similarity to the query embedding.
type Candidate struct {
	Tag        string  `json:"tag"`
	Similarity float64 `json:"similarity"`
}

// RankResult is the outcome for one tag query: the name the query is kept
// under (the query itself, or the consolidation target on fallback paths)
// and the candidate matches that passed the similarity floor, best first.
type RankResult struct {
	KeptName   string      `json:"kept_name"`
	Candidates []Candidate `json:"candidates"`
}

// SimilarPair is a pair of stored tags whose embedding similarity is above
// the consolidation threshold, a candidate for a merge review.
type SimilarPair struct {
	A          string  `json:"a"`
	B          string  `json:"b"`
	Similarity float64 `json:"similarity"`
}

// Matcher is the document-facing seam of semantic matching: finding stored
// tags for a document's text and resolving candidate tags. The daemon-side
// engine (Hugot) and the API-side socket client (MatcherClient) both satisfy
// it; the tools runner holds one behind this interface. Implementations
// must be safe for concurrent use.
type Matcher interface {
	// Match returns the stored tags most similar to a document's reduced
	// text, up to the configured top N and above the document-match
	// similarity floor. No matches is nil with a nil error.
	Match(ctx context.Context, docId, input string) ([]string, error)
	// Rank returns, per query, the candidates above the consolidation
	// similarity, at most two and best first, alongside the name the query
	// is kept under.
	Rank(ctx context.Context, docId string, queries []string) ([]RankResult, error)
	// SimilarPairs returns every stored-tag pair above the consolidation
	// similarity, the raw input of merge suggestions.
	SimilarPairs(ctx context.Context) ([]SimilarPair, error)
	Close()
	Name() string
}

// Embedder maintains the tag vocabulary behind the matcher: Encode turns
// texts into unit-normalized vectors, and AddToStore and RemoveFromStore
// keep the stored tags in sync with the tags table. docId is optional and
// only used for log correlation. Implementations must be safe for
// concurrent use.
type Embedder interface {
	// Encode embeds texts in order; the returned slice has one vector per
	// input, in input order.
	Encode(ctx context.Context, docId *string, texts []string) ([][]float32, error)
	AddToStore(ctx context.Context, names []string) error
	RemoveFromStore(ctx context.Context, names []string) error
	Close()
	Name() string
}

// EmbeddingStore is the tag embedding cache the matcher reads from: a tag
// name to vector map plus an attribute bag for diagnostics. Implementations
// must be safe for concurrent use.
type EmbeddingStore interface {
	// Add stores an embedding under a tag name, replacing any previous one.
	Add(key string, embedding []float32)
	// Remove drops a tag name from the store.
	Remove(key string)
	// Entries returns a deep copy of all stored embeddings.
	Entries() map[string][]float32
}
