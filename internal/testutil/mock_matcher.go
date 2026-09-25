package testutil

import "context"

import (
	"sync"

)

// MockEmbedder implements matcher.Embedder for testing: a simple map
// store, fixed encode results, and an injectable consolidation behavior.
type MockEmbedder struct {
	mu            sync.RWMutex
	store         map[string][]float32
	consolidateFn func(ctx context.Context, docId string, queries []string) ([]string, error)
}

// NewMockEmbedder builds an empty mock store.
func NewMockEmbedder() *MockEmbedder {
	return &MockEmbedder{
		store: make(map[string][]float32),
	}
}

// Encode returns a fixed vector per input text.
func (m *MockEmbedder) Encode(ctx context.Context, docId *string, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = []float32{0.1, 0.2, 0.3}
	}
	return result, nil
}

// Consolidate delegates to the injected behavior or echoes the queries.
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

// AddToStore stores the mock vector under each name.
func (m *MockEmbedder) AddToStore(ctx context.Context, names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, name := range names {
		m.store[name] = []float32{0.1, 0.2, 0.3}
	}
	return nil
}

// RemoveFromStore drops the names from the mock store.
func (m *MockEmbedder) RemoveFromStore(ctx context.Context, names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, name := range names {
		delete(m.store, name)
	}
	return nil
}

// Close is a no-op.
func (m *MockEmbedder) Close() {}

// Name identifies the mock.
func (m *MockEmbedder) Name() string { return "mock-embedder" }

// Has reports whether a name is currently in the mock store.
func (m *MockEmbedder) Has(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.store[name]
	return ok
}

// SetConsolidateFn replaces the default consolidation behavior.
func (m *MockEmbedder) SetConsolidateFn(fn func(ctx context.Context, docId string, queries []string) ([]string, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.consolidateFn = fn
}

