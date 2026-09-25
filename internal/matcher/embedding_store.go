package matcher

// TagEmbeddingStore holds tag-name to embedding entries in memory under a
// read-write lock, plus diagnostic attributes (dimension, model,
// normalization). It is the EmbeddingStore the daemon bootstraps with
// BuildTagCache and mutates through AddToStore and RemoveFromStore. Entries
// returns a deep copy.
type TagEmbeddingStore struct {
	storeBase
	entries map[string][]float32
}

// NewTagEmbeddingStore builds a store from the given entries and
// attributes; nil maps become empty ones.
func NewTagEmbeddingStore(entries map[string][]float32, attrs map[string]any) *TagEmbeddingStore {
	if attrs == nil {
		attrs = make(map[string]any)
	}
	if entries == nil {
		entries = make(map[string][]float32)
	}
	return &TagEmbeddingStore{
		storeBase: storeBase{attrs: attrs},
		entries:   entries,
	}
}

// Keys lists the stored tag names.
func (s *TagEmbeddingStore) Keys() []string {
	s.myu.RLock()
	defer s.myu.RUnlock()
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	return keys
}

// Len counts the stored entries.
func (s *TagEmbeddingStore) Len() int {
	s.myu.RLock()
	defer s.myu.RUnlock()
	return len(s.entries)
}

// Remove drops a tag name from the store.
func (s *TagEmbeddingStore) Remove(key string) {
	s.myu.Lock()
	defer s.myu.Unlock()
	delete(s.entries, key)
}

// Add stores an embedding under a tag name, replacing any previous
// one.
func (s *TagEmbeddingStore) Add(key string, embedding []float32) {
	s.myu.Lock()
	defer s.myu.Unlock()
	s.entries[key] = embedding
}

// Entries returns a deep copy of all stored embeddings.
func (s *TagEmbeddingStore) Entries() map[string][]float32 {
	s.myu.RLock()
	defer s.myu.RUnlock()
	cp := make(map[string][]float32, len(s.entries))
	for k, v := range s.entries {
		vec := make([]float32, len(v))
		copy(vec, v)
		cp[k] = vec
	}
	return cp
}
