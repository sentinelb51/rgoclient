package cache

import "container/list"

// lruKeys tracks string keys from least- to most-recently used. It replaces
// the linear slice scans both caches used to do on every touch with O(1)
// operations. Not safe for concurrent use; callers hold their own locks.
type lruKeys struct {
	order *list.List               // front = least recently used
	index map[string]*list.Element // key -> its element in order
}

func newLRUKeys() *lruKeys {
	return &lruKeys{order: list.New(), index: make(map[string]*list.Element)}
}

// Touch marks key most recently used, inserting it if new.
func (l *lruKeys) Touch(key string) {
	if e, ok := l.index[key]; ok {
		l.order.MoveToBack(e)
		return
	}
	l.index[key] = l.order.PushBack(key)
}

// Len returns the number of tracked keys.
func (l *lruKeys) Len() int { return l.order.Len() }

// EvictOldest removes and returns the least recently used key. It must only be
// called when Len() > 0.
func (l *lruKeys) EvictOldest() string {
	key := l.order.Remove(l.order.Front()).(string)
	delete(l.index, key)
	return key
}
