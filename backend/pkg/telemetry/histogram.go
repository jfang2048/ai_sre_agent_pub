package telemetry

import (
	"sync"
)

// Histogram tracks the distribution of values.
type Histogram struct {
	mu      sync.RWMutex
	buckets []float64
	counts  []uint64
	sum     float64
	count   uint64
}

// NewHistogram creates a new Histogram with the given buckets.
func NewHistogram(buckets []float64) *Histogram {
	return &Histogram{
		buckets: buckets,
		counts:  make([]uint64, len(buckets)+1),
	}
}

// Observe adds a value to the histogram.
func (h *Histogram) Observe(value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.sum += value
	h.count++

	for i, bound := range h.buckets {
		if value <= bound {
			h.counts[i]++
			return
		}
	}
	h.counts[len(h.buckets)]++
}

// Snapshot returns a snapshot of the current state.
func (h *Histogram) Snapshot() (count uint64, sum float64, buckets []uint64) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	buckets = make([]uint64, len(h.counts))
	copy(buckets, h.counts)
	return h.count, h.sum, buckets
}

// Mean returns the arithmetic mean of the observed values.
func (h *Histogram) Mean() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.count == 0 {
		return 0
	}
	return h.sum / float64(h.count)
}
