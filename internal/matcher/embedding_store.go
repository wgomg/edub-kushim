package matcher

type TagEmbeddingStore struct {
	storeBase
	entries map[string][]float32
}

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

func (s *TagEmbeddingStore) Keys() []string {
	s.myu.RLock()
	defer s.myu.RUnlock()
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	return keys
}

func (s *TagEmbeddingStore) Len() int {
	s.myu.RLock()
	defer s.myu.RUnlock()
	return len(s.entries)
}

func (s *TagEmbeddingStore) Remove(key string) {
	s.myu.Lock()
	defer s.myu.Unlock()
	delete(s.entries, key)
}

func (s *TagEmbeddingStore) Add(key string, embedding []float32) {
	s.myu.Lock()
	defer s.myu.Unlock()
	s.entries[key] = embedding
}

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
