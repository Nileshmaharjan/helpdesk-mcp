package mcpserver

import (
	"sync"

	"golang.org/x/time/rate"
)

// limiter keeps one token bucket per principal, so one noisy agent cannot starve the others.
type limiter struct {
	mu      sync.Mutex
	perSec  rate.Limit
	burst   int
	buckets map[string]*rate.Limiter
}

func newLimiter(perSec float64, burst int) *limiter {
	return &limiter{perSec: rate.Limit(perSec), burst: burst, buckets: map[string]*rate.Limiter{}}
}

func (l *limiter) allow(principal string) bool {
	if l == nil || l.perSec <= 0 {
		return true
	}
	l.mu.Lock()
	b, ok := l.buckets[principal]
	if !ok {
		b = rate.NewLimiter(l.perSec, l.burst)
		l.buckets[principal] = b
	}
	l.mu.Unlock()
	return b.Allow()
}
