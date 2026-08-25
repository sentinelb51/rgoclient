// Package cache holds the client's bounded caches: decoded images (memory plus
// disk), recent messages per channel, and text-attachment previews. Each is
// owned by the app controller and handed to widgets through ui.Deps.
package cache

import (
	"container/list"
	"sync"
)

/* Recency */

// LRU tracks string keys from least- to most-recently used with O(1) touch and
// eviction. It backs every bounded cache here. Not safe for concurrent use;
// callers hold their own locks.
type LRU struct {
	order *list.List               // front = least recently used
	index map[string]*list.Element // key -> its element in order
}

// newLRU creates an empty recency tracker.
func newLRU() *LRU {
	return &LRU{order: list.New(), index: make(map[string]*list.Element)}
}

// Touch marks key most recently used, inserting it if new.
func (l *LRU) Touch(key string) {
	if e, ok := l.index[key]; ok {
		l.order.MoveToBack(e)
		return
	}

	l.index[key] = l.order.PushBack(key)
}

// Len returns the number of tracked keys.
func (l *LRU) Len() int { return l.order.Len() }

// EvictOldest removes and returns the least recently used key. Only valid while
// Len is above zero.
func (l *LRU) EvictOldest() string {
	key := l.order.Remove(l.order.Front()).(string)
	delete(l.index, key)
	return key
}

/* Text previews */

// TextCache memoises text-attachment previews by URL. Message widgets are
// rebuilt on every channel revisit, so without it each rebuild re-downloads
// every text attachment. Safe for concurrent use.
type TextCache struct {
	mu      sync.Mutex // guards text and recency
	text    map[string]string
	recency *LRU
	limit   int
}

// NewTextCache creates a cache holding at most limit previews.
func NewTextCache(limit int) *TextCache {
	return &TextCache{text: make(map[string]string), recency: newLRU(), limit: limit}
}

// Get returns the memoised preview for url, if any.
func (c *TextCache) Get(url string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	text, ok := c.text[url]
	if ok {
		c.recency.Touch(url)
	}

	return text, ok
}

// Set memoises a preview, evicting the least recently used past the limit.
func (c *TextCache) Set(url, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.text[url] = text
	c.recency.Touch(url)

	for c.recency.Len() > c.limit {
		delete(c.text, c.recency.EvictOldest())
	}
}
