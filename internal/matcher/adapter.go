// Package matcher is the semantic matching domain: the embedding engine and its tag store on the daemon
// side, the Unix-socket client the API process uses instead, and the shared vocabulary of matches and
// ranks. The kushim binary runs the engine behind "kushim hugot"; edub talks to it through the
// MatcherClient. Both sides satisfy the Matcher and Embedder seams the tools runner consumes.
package matcher

import "context"

// Candidate is one ranked match for a tag query: a stored tag name and its
// cosine similarity to the query embedding.
type Candidate struct {
	Tag        string  `json:"tag"`
	Similarity float64 `json:"similarity"`
}

// RankResult is the outcome for one tag query: the candidate matches that passed the identity-candidate
// similarity floor, best first. Candidates are the gate's trigger input; a query never takes a name
// from them.
type RankResult struct {
	Candidates []Candidate `json:"candidates"`
}

// Matcher is the document-facing seam of semantic matching: finding stored tags for a document's text
// and resolving candidate tags. The daemon-side engine (Hugot) and the API-side socket client
// (MatcherClient) both satisfy it; the tools runner holds one behind this interface. Implementations
// must be safe for concurrent use.
type Matcher interface {
	// Match returns the stored tags most similar to a document's reduced
	// text, up to the configured top N and above the document-match
	// similarity floor, each carrying its similarity. No matches is nil
	// with a nil error.
	Match(ctx context.Context, docId, input string) ([]Candidate, error)
	// Rank returns, per query, the candidates above the identity-candidate similarity, at most two and
	// best first.
	Rank(ctx context.Context, docId string, queries []string) ([]RankResult, error)
	Close()
	Name() string
}

// Embedder maintains the tag vocabulary behind the matcher: Encode turns
// texts into unit-normalized vectors, and AddToStore and RemoveFromStore
// keep the stored tags in sync with the tags table. docId is used only for
// log correlation and is empty when there is none. Implementations must be
// safe for concurrent use.
type Embedder interface {
	// Encode embeds texts in order; the returned slice has one vector per
	// input, in input order.
	Encode(ctx context.Context, docId string, texts []string) ([][]float32, error)
	AddToStore(ctx context.Context, names []string) error
	RemoveFromStore(ctx context.Context, names []string) error
	Close()
	Name() string
}

// EmbeddingStore is the tag embedding cache the matcher reads from: a tag
// name to vector map. Implementations must be safe for concurrent use.
type EmbeddingStore interface {
	// Add stores an embedding under a tag name, replacing any previous one.
	Add(key string, embedding []float32)
	// Remove drops a tag name from the store.
	Remove(key string)
	// Entries returns a deep copy of all stored embeddings.
	Entries() map[string][]float32
}
