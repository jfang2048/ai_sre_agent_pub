package ring

import "sync"

// Ring is a fixed-capacity circular buffer.
//
// It is intentionally small and allocation-free after construction:
// - Push is O(1)
// - Iteration is O(n) in logical order (oldest -> newest)
//
// This is a common systems pattern used to cap memory and avoid slice shifting.
type Ring[T any] struct {
	mu   sync.RWMutex
	buf  []T
	head int // index of the oldest element
	size int // number of live elements (<= len(buf))
}

// New returns a ring with the given capacity.
// If capacity <= 0, New returns nil.
func New[T any](capacity int) *Ring[T] {
	if capacity <= 0 {
		return nil
	}
	return &Ring[T]{buf: make([]T, capacity)}
}

func (r *Ring[T]) Cap() int {
	if r == nil {
		return 0
	}
	return len(r.buf)
}

func (r *Ring[T]) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}

// Oldest returns the oldest logical element currently stored.
func (r *Ring[T]) Oldest() (T, bool) {
	var zero T
	if r == nil {
		return zero, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return zero, false
	}
	return r.buf[r.head], true
}

// Newest returns the newest logical element currently stored.
func (r *Ring[T]) Newest() (T, bool) {
	var zero T
	if r == nil {
		return zero, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return zero, false
	}
	idx := (r.head + r.size - 1) % len(r.buf)
	return r.buf[idx], true
}

// Push appends v to the buffer, overwriting the oldest value when full.
func (r *Ring[T]) Push(v T) {
	if r == nil || len(r.buf) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size < len(r.buf) {
		r.buf[(r.head+r.size)%len(r.buf)] = v
		r.size++
		return
	}
	// Full: overwrite the oldest and advance head.
	r.buf[r.head] = v
	r.head = (r.head + 1) % len(r.buf)
}

// ForEachOldest calls fn for each element in logical order, from oldest to newest.
func (r *Ring[T]) ForEachOldest(fn func(v T)) {
	if r == nil || fn == nil {
		return
	}
	for _, value := range r.SliceOldest() {
		fn(value)
	}
}

// SliceOldest returns a copy of elements in logical order (oldest -> newest).
func (r *Ring[T]) SliceOldest() []T {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return nil
	}
	out := make([]T, 0, r.size)
	for i := 0; i < r.size; i++ {
		out = append(out, r.buf[(r.head+i)%len(r.buf)])
	}
	return out
}

// SliceLastN returns the last n elements (newest tail) in logical order.
func (r *Ring[T]) SliceLastN(n int) []T {
	if r == nil || n <= 0 {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return nil
	}
	if n > r.size {
		n = r.size
	}
	start := r.size - n
	out := make([]T, 0, n)
	for i := start; i < r.size; i++ {
		out = append(out, r.buf[(r.head+i)%len(r.buf)])
	}
	return out
}
