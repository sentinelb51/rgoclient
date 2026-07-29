package cache

import "container/list"

// LRU tracks string keys from least- to most-recently used with O(1) touch and
// eviction. It is the shared recency primitive behind every bounded cache in the
// client (images, messages, text previews). Not safe for concurrent use;
// callers hold their own locks.
type LRU struct {
	order *list.List               // front = least recently used
	index map[string]*list.Element // key -> its element in order
}

// NewLRU creates an empty recency tracker.
func NewLRU() *LRU {
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

// EvictOldest removes and returns the least recently used key. It must only be
// called when Len() > 0.
func (l *LRU) EvictOldest() string {
	key := l.order.Remove(l.order.Front()).(string)
	delete(l.index, key)
	return key
}
