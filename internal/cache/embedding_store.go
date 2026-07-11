package cache

type EmbeddingStore struct {
	storeBase
	entries map[string][]float32
}

func NewEmbeddingStore(entries map[string][]float32, attrs map[string]any) *EmbeddingStore {
	if attrs == nil {
		attrs = make(map[string]any)
	}
	if entries == nil {
		entries = make(map[string][]float32)
	}
	return &EmbeddingStore{
		storeBase: storeBase{attrs: attrs},
		entries:   entries,
	}
}

func (s *EmbeddingStore) Keys() []string {
	s.myu.RLock()
	defer s.myu.RUnlock()
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	return keys
}

func (s *EmbeddingStore) Len() int {
	s.myu.RLock()
	defer s.myu.RUnlock()
	return len(s.entries)
}

func (s *EmbeddingStore) Remove(key string) {
	s.myu.Lock()
	defer s.myu.Unlock()
	delete(s.entries, key)
}

func (s *EmbeddingStore) Add(key string, embedding []float32) {
	s.myu.Lock()
	defer s.myu.Unlock()
	s.entries[key] = embedding
}

func (s *EmbeddingStore) Entries() map[string][]float32 {
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
