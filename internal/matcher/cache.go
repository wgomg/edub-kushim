package matcher

import (
	"maps"
	"sync"
)

// Store is the attribute-bag contract shared by the daemon's caches: a few
// string-keyed diagnostic values (dimension, model name) alongside the
// entries a concrete store manages.
type Store interface {
	Attr(key string) (any, bool)
	Attrs() map[string]any
	Keys() []string
	Len() int
	Remove(key string)
}

type storeBase struct {
	myu   sync.RWMutex
	attrs map[string]any
}

func (b *storeBase) Attr(key string) (any, bool) {
	b.myu.RLock()
	defer b.myu.RUnlock()
	val, ok := b.attrs[key]
	return val, ok
}

func (b *storeBase) Attrs() map[string]any {
	b.myu.RLock()
	defer b.myu.RUnlock()
	cp := make(map[string]any, len(b.attrs))
	maps.Copy(cp, b.attrs)
	return cp
}

// Cache is a named registry of stores, used by the daemon wiring to expose
// several caches under stable names. All methods are safe for concurrent
// use.
type Cache struct {
	mu     sync.RWMutex
	stores map[string]Store
}

// NewCache returns an empty registry.
func NewCache() *Cache {
	return &Cache{stores: make(map[string]Store)}
}

// Set installs a store under a name, replacing any previous one.
func (c *Cache) Set(name string, store Store) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stores[name] = store
}

// Get returns the store registered under name.
func (c *Cache) Get(name string) (Store, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.stores[name]
	return s, ok
}
