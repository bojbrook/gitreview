package ctxpane

import (
	"container/list"
	"sync"
)

// lruCache is a small, thread-safe LRU keyed by string. Values are opaque to
// the cache. Bounded by maxEntries; older entries are evicted on insert.
type lruCache struct {
	mu      sync.Mutex
	maxSize int
	ll      *list.List // most-recent at front
	idx     map[string]*list.Element
}

type lruEntry struct {
	key   string
	value any
}

func newLRU(max int) *lruCache {
	if max <= 0 {
		max = 1
	}
	return &lruCache{
		maxSize: max,
		ll:      list.New(),
		idx:     make(map[string]*list.Element, max),
	}
}

func (c *lruCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.idx[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(lruEntry).value, true
}

func (c *lruCache) Put(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.idx[key]; ok {
		el.Value = lruEntry{key, value}
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(lruEntry{key, value})
	c.idx[key] = el
	for c.ll.Len() > c.maxSize {
		old := c.ll.Back()
		if old == nil {
			break
		}
		c.ll.Remove(old)
		delete(c.idx, old.Value.(lruEntry).key)
	}
}
