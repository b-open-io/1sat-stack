package ordfs

import (
	"container/list"
	"context"
	"sync"
)

type lruCacheEntry struct {
	key   string
	value []byte
}

// LRUCache is an in-memory Cache backed by an LRU eviction policy.
type LRUCache struct {
	maxSize int
	items   map[string]*list.Element
	lru     *list.List
	mu      sync.RWMutex
}

// NewLRUCache creates an LRU cache that evicts the oldest entry when maxSize items are stored.
func NewLRUCache(maxSize int) *LRUCache {
	return &LRUCache{
		maxSize: maxSize,
		items:   make(map[string]*list.Element, maxSize),
		lru:     list.New(),
	}
}

func (c *LRUCache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return nil, nil
	}
	c.lru.MoveToFront(elem)
	return elem.Value.(*lruCacheEntry).value, nil
}

func (c *LRUCache) Set(_ context.Context, key string, value []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.lru.MoveToFront(elem)
		elem.Value.(*lruCacheEntry).value = value
		return nil
	}

	elem := c.lru.PushFront(&lruCacheEntry{key: key, value: value})
	c.items[key] = elem

	for c.lru.Len() > c.maxSize {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		delete(c.items, oldest.Value.(*lruCacheEntry).key)
		c.lru.Remove(oldest)
	}

	return nil
}

func (c *LRUCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		delete(c.items, key)
		c.lru.Remove(elem)
	}
	return nil
}
