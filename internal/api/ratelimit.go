package api

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Rate limiting: a token bucket per client, stdlib only. It exists to keep
// one wedged script or one misbehaving integration from starving everyone
// else — not to be a WAF. The bucket key is the bearer when there is one
// (per-principal fairness) and the remote IP when there is not (the
// unauthenticated surface is where junk arrives).

const (
	// ratePerSecond and burst are deliberately generous: the UI polls logs
	// every 2.5s and metrics every 15s per open drawer, agents long-poll, and
	// none of that should ever see a 429. The target is runaway loops.
	ratePerSecond = 25
	rateBurst     = 100
	// bucketTTL evicts idle buckets so the map is bounded by active clients,
	// not by every client ever seen.
	bucketTTL = 10 * time.Minute
)

type bucket struct {
	mu       sync.Mutex
	tokens   float64
	lastFill time.Time
	lastSeen time.Time
}

func (b *bucket) take(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	elapsed := now.Sub(b.lastFill).Seconds()
	b.tokens += elapsed * ratePerSecond
	if b.tokens > rateBurst {
		b.tokens = rateBurst
	}
	b.lastFill = now
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// limiter is the middleware state.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

func newLimiter(now func() time.Time) *limiter {
	return &limiter{buckets: map[string]*bucket{}, now: now}
}

func (l *limiter) allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	entry, ok := l.buckets[key]
	if !ok {
		entry = &bucket{tokens: rateBurst, lastFill: now, lastSeen: now}
		l.buckets[key] = entry
		// Piggybacked eviction keeps the map bounded without a goroutine.
		if len(l.buckets)%256 == 0 {
			for existing, b := range l.buckets {
				b.mu.Lock()
				idle := now.Sub(b.lastSeen) > bucketTTL
				b.mu.Unlock()
				if idle {
					delete(l.buckets, existing)
				}
			}
		}
	}
	l.mu.Unlock()
	return entry.take(now)
}

// RateLimit wraps a handler. Health and readiness stay unlimited — a load
// balancer that gets 429 from /healthz takes the instance out exactly when
// it is busiest, which is the opposite of what anyone wants.
func (s *Server) RateLimit(next http.Handler) http.Handler {
	l := newLimiter(s.now)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("Authorization")
		if key == "" {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			key = "ip:" + host
		}
		if !l.allow(key) {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"code": "HD0429", "message": "rate limited; slow down and retry",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
