package middleware

import (
	"net/http"
	"sync"
	"time"
)

type bucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

type RateLimiter struct {
	rps     float64
	buckets sync.Map
}

func NewRateLimiter(rps int) *RateLimiter {
	rl := &RateLimiter{rps: float64(rps)}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) allow(ip string) bool {
	raw, _ := rl.buckets.LoadOrStore(ip, &bucket{
		tokens:   rl.rps,
		lastSeen: time.Now(),
	})
	b := raw.(*bucket)

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens += elapsed * rl.rps
	if b.tokens > rl.rps {
		b.tokens = rl.rps
	}
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens -= 1
		return true
	}
	return false
}

func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(5 * time.Minute)
		rl.buckets.Range(func(key, value interface{}) bool {
			b := value.(*bucket)
			b.mu.Lock()
			if time.Since(b.lastSeen) > 10*time.Minute {
				rl.buckets.Delete(key)
			}
			b.mu.Unlock()
			return true
		})
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := realIP(r)
		if !rl.allow(ip) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded","code":"RATE_LIMITED"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
