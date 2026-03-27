package telemetry

import (
	"sync"
	"time"
)

// Rate calculates the per-second rate of events.
type Rate struct {
	mu       sync.Mutex
	count    uint64
	lastTime time.Time
}

// NewRate creates a new Rate tracker.
func NewRate() *Rate {
	return &Rate{
		lastTime: time.Now(),
	}
}

// Inc increments the event count.
func (r *Rate) Inc(delta uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count += delta
}

// Rate returns the calculate rate since the last call.
func (r *Rate) Rate() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	duration := now.Sub(r.lastTime).Seconds()
	if duration <= 0 {
		return 0
	}

	rate := float64(r.count) / duration
	r.count = 0
	r.lastTime = now

	return rate
}
