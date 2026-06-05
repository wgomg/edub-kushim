package cache

import (
	"sync"
	"testing"
)

func TestNewEmbeddingStore(t *testing.T) {
	entries := map[string][]float32{
		"tag1": {0.1, 0.2},
		"tag2": {0.3, 0.4},
	}
	attrs := map[string]any{
		"dim":        2,
		"model":      "test-model",
		"normalized": true,
	}

	s := NewEmbeddingStore(entries, attrs)

	if s.Len() != 2 {
		t.Errorf("Len = %d, want 2", s.Len())
	}

	dim, ok := s.Attr("dim")
	if !ok {
		t.Fatal("Attr('dim') not found")
	}
	if dim.(int) != 2 {
		t.Errorf("Attr('dim') = %d, want 2", dim.(int))
	}

	model, ok := s.Attr("model")
	if !ok {
		t.Fatal("Attr('model') not found")
	}
	if model.(string) != "test-model" {
		t.Errorf("Attr('model') = %q, want 'test-model'", model.(string))
	}
}

func TestNewEmbeddingStore_NilMaps(t *testing.T) {
	s := NewEmbeddingStore(nil, nil)

	if s.Len() != 0 {
		t.Errorf("Len = %d, want 0", s.Len())
	}
	if s.Attrs() == nil {
		t.Fatal("Attrs() returned nil, want empty map")
	}
	if s.Entries() == nil {
		t.Fatal("Entries() returned nil, want empty map")
	}
}

func TestEmbeddingStoreGet(t *testing.T) {
	s := NewEmbeddingStore(map[string][]float32{
		"alpha": {1.0, 2.0},
	}, nil)

	val, ok := s.Get("alpha")
	if !ok {
		t.Fatal("Get('alpha') not found")
	}
	if len(val) != 2 || val[0] != 1.0 || val[1] != 2.0 {
		t.Errorf("Get('alpha') = %v, want [1.0, 2.0]", val)
	}

	_, ok = s.Get("missing")
	if ok {
		t.Fatal("Get('missing') should not be found")
	}

	_, ok = s.Get("")
	if ok {
		t.Fatal("Get('') should not be found")
	}
}

func TestEmbeddingStoreKeys(t *testing.T) {
	s := NewEmbeddingStore(map[string][]float32{
		"z": {1.0},
		"a": {2.0},
	}, nil)

	keys := s.Keys()
	if len(keys) != 2 {
		t.Errorf("len(Keys) = %d, want 2", len(keys))
	}

	seen := make(map[string]bool)
	for _, k := range keys {
		seen[k] = true
	}
	if !seen["z"] || !seen["a"] {
		t.Errorf("Keys() = %v, want both 'z' and 'a'", keys)
	}
}

func TestEmbeddingStoreKeys_Empty(t *testing.T) {
	s := NewEmbeddingStore(nil, nil)
	keys := s.Keys()
	if len(keys) != 0 {
		t.Errorf("len(Keys) = %d, want 0", len(keys))
	}
}

func TestEmbeddingStoreEntries(t *testing.T) {
	original := map[string][]float32{
		"x": {0.5, 0.6},
	}
	s := NewEmbeddingStore(original, nil)

	cp := s.Entries()
	if len(cp) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(cp))
	}

	cp["x"][0] = 99.0

	val, _ := s.Get("x")
	if val[0] != 0.5 {
		t.Errorf("Get('x')[0] = %v, want 0.5 (store was mutated through Entries copy)", val[0])
	}
}

func TestEmbeddingStoreAdd(t *testing.T) {
	s := NewEmbeddingStore(nil, nil)

	s.Add("new", []float32{7.0, 8.0})

	if s.Len() != 1 {
		t.Errorf("Len = %d, want 1", s.Len())
	}

	val, ok := s.Get("new")
	if !ok {
		t.Fatal("Get('new') not found after Add")
	}
	if val[0] != 7.0 || val[1] != 8.0 {
		t.Errorf("Get('new') = %v, want [7.0, 8.0]", val)
	}

	s.Add("new", []float32{9.0})

	val, _ = s.Get("new")
	if len(val) != 1 || val[0] != 9.0 {
		t.Errorf("Get('new') after overwrite = %v, want [9.0]", val)
	}
}

func TestEmbeddingStoreRemove(t *testing.T) {
	s := NewEmbeddingStore(map[string][]float32{
		"keep":   {1.0},
		"remove": {2.0},
	}, nil)

	s.Remove("remove")

	if s.Len() != 1 {
		t.Errorf("Len = %d, want 1", s.Len())
	}

	_, ok := s.Get("remove")
	if ok {
		t.Fatal("Get('remove') should not be found after Remove")
	}

	_, ok = s.Get("keep")
	if !ok {
		t.Fatal("Get('keep') should still be found")
	}

	s.Remove("nonexistent")
	if s.Len() != 1 {
		t.Errorf("Len = %d, want 1 (removing nonexistent key should be no-op)", s.Len())
	}
}

func TestEmbeddingStoreConcurrentReadWrite(t *testing.T) {
	s := NewEmbeddingStore(nil, nil)

	var wg sync.WaitGroup
	n := 100

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.Add("key", []float32{float32(idx)})
		}(i)
	}

	for range n {
		wg.Go(func() {
			s.Get("key")
			s.Keys()
			s.Len()
			s.Entries()
		})
	}

	wg.Wait()
}

func TestStoreInterface(t *testing.T) {
	s := NewEmbeddingStore(map[string][]float32{
		"a": {1.0},
	}, map[string]any{"dim": 3})

	var iface Store = s

	if iface.Len() != 1 {
		t.Errorf("Store.Len() = %d, want 1", iface.Len())
	}

	keys := iface.Keys()
	if len(keys) != 1 || keys[0] != "a" {
		t.Errorf("Store.Keys() = %v, want ['a']", keys)
	}

	val, ok := iface.Attr("dim")
	if !ok || val.(int) != 3 {
		t.Errorf("Store.Attr('dim') = (%v, %v), want (3, true)", val, ok)
	}

	iface.Remove("a")
	if iface.Len() != 0 {
		t.Errorf("Store.Len() after Remove = %d, want 0", iface.Len())
	}
}

func TestStoreAttr_Missing(t *testing.T) {
	s := NewEmbeddingStore(nil, nil)
	_, ok := s.Attr("nonexistent")
	if ok {
		t.Fatal("Attr('nonexistent') should not be found")
	}
}

func TestStoreAttrs(t *testing.T) {
	attrs := map[string]any{"dim": 384, "model": "all-MiniLM-L6-v2"}
	s := NewEmbeddingStore(nil, attrs)

	cp := s.Attrs()
	if len(cp) != 2 {
		t.Errorf("len(Attrs) = %d, want 2", len(cp))
	}

	cp["dim"] = 999

	val, _ := s.Attr("dim")
	if val.(int) != 384 {
		t.Errorf("Attr('dim') = %d, want 384 (store was mutated through Attrs copy)", val.(int))
	}
}

func TestNewCache(t *testing.T) {
	c := New()
	if c == nil {
		t.Fatal("New() returned nil")
	}
	_, ok := c.Get("nonexistent")
	if ok {
		t.Fatal("Get('nonexistent') should not be found in empty cache")
	}
}

func TestCacheSetAndGet(t *testing.T) {
	c := New()
	s := NewEmbeddingStore(map[string][]float32{"tag": {1.0}}, map[string]any{"dim": 1})

	c.Set("tags", s)

	got, ok := c.Get("tags")
	if !ok {
		t.Fatal("Get('tags') not found")
	}

	typed, ok := got.(*EmbeddingStore)
	if !ok {
		t.Fatalf("Get('tags') type assertion failed, got %T", got)
	}

	if typed.Len() != 1 {
		t.Errorf("Len = %d, want 1", typed.Len())
	}

	_, ok = c.Get("missing")
	if ok {
		t.Fatal("Get('missing') should not be found")
	}
}

func TestCacheSet_Overwrite(t *testing.T) {
	c := New()
	first := NewEmbeddingStore(map[string][]float32{"a": {1.0}}, nil)
	second := NewEmbeddingStore(map[string][]float32{"b": {2.0}}, nil)

	c.Set("store", first)
	c.Set("store", second)

	got, _ := c.Get("store")
	typed := got.(*EmbeddingStore)

	if typed.Len() != 1 {
		t.Errorf("Len = %d, want 1", typed.Len())
	}
	_, ok := typed.Get("b")
	if !ok {
		t.Fatal("Get('b') should be found (second store should have replaced first)")
	}
}

func TestCacheConcurrentGet(t *testing.T) {
	c := New()
	s := NewEmbeddingStore(map[string][]float32{"k": {1.0}}, nil)
	c.Set("s", s)

	var wg sync.WaitGroup
	n := 100

	for range n {
		wg.Go(func() {
			got, ok := c.Get("s")
			if !ok {
				t.Error("Get('s') not found")
				return
			}
			_ = got.(*EmbeddingStore)
		})
	}

	wg.Wait()
}
