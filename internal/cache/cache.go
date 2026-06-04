package cache

import (
	"maps"
	"sync"
)

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

type Cache struct {
	mu     sync.RWMutex
	stores map[string]Store
}

func New() *Cache {
	return &Cache{stores: make(map[string]Store)}
}

func (c *Cache) Set(name string, store Store) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stores[name] = store
}

func (c *Cache) Get(name string) (Store, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.stores[name]
	return s, ok
}
