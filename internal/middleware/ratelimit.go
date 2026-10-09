package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	limit   int
	window  time.Duration
}

func NewRateLimiter(limit int) *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string][]time.Time),
		limit:   limit,
		window:  time.Minute,
	}
}

func (r *rateLimiter) Middleware(keyFn func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := keyFn(c)
		now := time.Now()
		r.mu.Lock()
		times := r.buckets[key]
		filtered := times[:0]
		for _, t := range times {
			if now.Sub(t) < r.window {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) >= r.limit {
			r.mu.Unlock()
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"detail": "操作过于频繁，请稍后重试"})
			return
		}
		filtered = append(filtered, now)
		r.buckets[key] = filtered
		r.mu.Unlock()
		c.Next()
	}
}
