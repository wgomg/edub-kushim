package matcher

import "sync"

// TagEmbeddingStore holds tag-name to embedding entries in memory under a
// read-write lock. It is the EmbeddingStore the daemon bootstraps with
// BuildTagCache and mutates through Add and Remove; Entries returns a deep
// copy for readers.
type TagEmbeddingStore struct {
	mu      sync.RWMutex
	entries map[string][]float32
}

// NewTagEmbeddingStore builds a store from the given entries; a nil map
// becomes an empty one.
func NewTagEmbeddingStore(entries map[string][]float32) *TagEmbeddingStore {
	if entries == nil {
		entries = make(map[string][]float32)
	}
	return &TagEmbeddingStore{entries: entries}
}

// Remove drops a tag name from the store.
func (s *TagEmbeddingStore) Remove(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}

// Add stores an embedding under a tag name, replacing any previous
// one.
func (s *TagEmbeddingStore) Add(key string, embedding []float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = embedding
}

// Entries returns a deep copy of all stored embeddings.
func (s *TagEmbeddingStore) Entries() map[string][]float32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string][]float32, len(s.entries))
	for k, v := range s.entries {
		vec := make([]float32, len(v))
		copy(vec, v)
		cp[k] = vec
	}
	return cp
}
