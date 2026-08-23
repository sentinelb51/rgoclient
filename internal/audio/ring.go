package audio

import "sync/atomic"

// ring is a wait-free single-producer single-consumer queue.
//
// It is what every hand-off across the device callback is made of: one
// goroutine writes, the callback reads, and neither ever waits on the other. A
// lock would do the same job correctly and is exactly what must not be here —
// a callback blocked on a mutex held by a descheduled goroutine is a dropout.
//
// The two counters only ever grow, so a wrap of uint64 is not a case anyone has
// to have thought about, and the index is a mask rather than a modulo. Capacity
// is rounded up to a power of two for that reason.
//
// Correctness rests on one thing: the producer publishes its slot with a Store
// that the consumer's Load pairs with, so everything written to the slot before
// the Store is visible to the consumer after the Load. Go's atomics are
// sequentially consistent, which is stronger than the release/acquire this
// needs.
type ring[T any] struct {
	buf  []T
	mask uint64

	write atomic.Uint64 // producer's own; read by the consumer
	read  atomic.Uint64 // consumer's own; read by the producer

	_ [56]byte // keeps the two counters off one cache line
}

// newRing returns a ring holding at least capacity items.
func newRing[T any](capacity int) *ring[T] {
	size := 1
	for size < capacity {
		size <<= 1
	}

	return &ring[T]{buf: make([]T, size), mask: uint64(size - 1)}
}

// Len is how many items are waiting. Safe from either end, and exact from
// neither — the other end may be mid-write — so it is a level, not a fence.
func (r *ring[T]) Len() int { return int(r.write.Load() - r.read.Load()) }

// Cap is how many items fit.
func (r *ring[T]) Cap() int { return len(r.buf) }

// Push adds one item, reporting false when the ring is full. Producer side
// only. A full ring is dropped rather than waited on: whatever the consumer is
// behind on, adding to the backlog cannot help it.
func (r *ring[T]) Push(item T) bool {
	write := r.write.Load()
	if write-r.read.Load() >= uint64(len(r.buf)) {
		return false
	}

	r.buf[write&r.mask] = item
	r.write.Store(write + 1)

	return true
}

// PushAll adds as many of items as fit and reports how many that was. Producer
// side only.
func (r *ring[T]) PushAll(items []T) int {
	write := r.write.Load()
	free := uint64(len(r.buf)) - (write - r.read.Load())

	n := min(uint64(len(items)), free)
	for i := uint64(0); i < n; i++ {
		r.buf[(write+i)&r.mask] = items[i]
	}
	r.write.Store(write + n)

	return int(n)
}

// Pop takes one item, reporting false when the ring is empty. Consumer side
// only.
func (r *ring[T]) Pop() (T, bool) {
	read := r.read.Load()
	if read == r.write.Load() {
		var zero T
		return zero, false
	}

	item := r.buf[read&r.mask]
	r.read.Store(read + 1)

	return item, true
}

// PopAll fills out with as many items as are waiting and reports how many that
// was. Consumer side only.
func (r *ring[T]) PopAll(out []T) int {
	read := r.read.Load()

	n := min(uint64(len(out)), r.write.Load()-read)
	for i := uint64(0); i < n; i++ {
		out[i] = r.buf[(read+i)&r.mask]
	}
	r.read.Store(read + n)

	return int(n)
}

// Discard drops up to n items. Consumer side only, and the one thing a lane
// that has fallen too far behind does about it: playing a stale backlog out is
// latency nobody asked for, where dropping it costs one audible seam.
func (r *ring[T]) Discard(n int) {
	read := r.read.Load()

	drop := min(uint64(n), r.write.Load()-read)
	r.read.Store(read + drop)
}
