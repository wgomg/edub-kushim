package testutil

import "context"

import (
	"sync"

)

// MockEmbedder implements tagmatcher.Embedder for testing.
// It stores embeddings in a simple map and returns fixed results.
type MockEmbedder struct {
	mu            sync.RWMutex
	store         map[string][]float32
	consolidateFn func(ctx context.Context, docId string, queries []string) ([]string, error)
}

func NewMockEmbedder() *MockEmbedder {
	return &MockEmbedder{
		store: make(map[string][]float32),
	}
}

func (m *MockEmbedder) Encode(ctx context.Context, docId *string, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = []float32{0.1, 0.2, 0.3}
	}
	return result, nil
}

func (m *MockEmbedder) Consolidate(ctx context.Context, docId string, queries []string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.consolidateFn != nil {
		return m.consolidateFn(ctx, docId, queries)
	}
	result := make([]string, len(queries))
	copy(result, queries)
	return result, nil
}

func (m *MockEmbedder) AddToStore(ctx context.Context, names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, name := range names {
		m.store[name] = []float32{0.1, 0.2, 0.3}
	}
	return nil
}

func (m *MockEmbedder) RemoveFromStore(ctx context.Context, names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, name := range names {
		delete(m.store, name)
	}
	return nil
}

func (m *MockEmbedder) Close() {}

func (m *MockEmbedder) Name() string { return "mock-embedder" }

// SetConsolidateFn replaces the default consolidation behavior.
func (m *MockEmbedder) SetConsolidateFn(fn func(ctx context.Context, docId string, queries []string) ([]string, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.consolidateFn = fn
}

