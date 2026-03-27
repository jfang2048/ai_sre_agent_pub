package middleware

import (
	"net/http"

	"golang.org/x/time/rate"
)

// RateLimitConfig configures a coarse-grained HTTP token bucket.
type RateLimitConfig struct {
	Enabled  bool
	RPS      float64
	Burst    int
	Skip     func(*http.Request) bool
	OnReject func(http.ResponseWriter, *http.Request)
}

// RateLimit wraps an HTTP handler with a single shared token bucket. This is
// intentionally simple and explicit: it protects the controller API surface
// without introducing per-principal state on the hot path.
func RateLimit(cfg RateLimitConfig, next http.Handler) http.Handler {
	if next == nil || !cfg.Enabled {
		return next
	}
	if cfg.RPS <= 0 {
		cfg.RPS = 1
	}
	if cfg.Burst <= 0 {
		cfg.Burst = 1
	}
	limiter := rate.NewLimiter(rate.Limit(cfg.RPS), cfg.Burst)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.Skip != nil && cfg.Skip(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !limiter.Allow() {
			if cfg.OnReject != nil {
				cfg.OnReject(w, r)
			} else {
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}
