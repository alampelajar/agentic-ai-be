package httpguard

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// maxTrackedKeys bounds memory. Past it, idle entries are dropped, and keys that
// are still new share one overflow bucket instead of growing the table.
const maxTrackedKeys = 50000

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a token bucket per key, kept in memory. That is right for a single
// API process; several instances behind a load balancer would each keep their own
// count and need a shared store (see docs/phase7-public-readiness.md).
type Limiter struct {
	rate  float64 // tokens per second
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

// NewLimiter allows burst requests at once and perMinute on average.
func NewLimiter(perMinute, burst int) *Limiter {
	if perMinute < 1 {
		perMinute = 1
	}
	if burst < 1 {
		burst = 1
	}
	return &Limiter{rate: float64(perMinute) / 60, burst: float64(burst), now: time.Now, buckets: map[string]*bucket{}}
}

// Allow takes one token for key. When none is left it says how long to wait.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	current, known := l.buckets[key]
	if !known {
		if len(l.buckets) >= maxTrackedKeys {
			l.sweep(now)
		}
		if len(l.buckets) >= maxTrackedKeys {
			key = "overflow"
			current, known = l.buckets[key]
		}
		if !known {
			current = &bucket{tokens: l.burst, last: now}
			l.buckets[key] = current
		}
	}
	current.tokens = math.Min(l.burst, current.tokens+now.Sub(current.last).Seconds()*l.rate)
	current.last = now
	if current.tokens >= 1 {
		current.tokens--
		return true, 0
	}
	return false, time.Duration((1 - current.tokens) / l.rate * float64(time.Second))
}

// sweep forgets buckets that have been idle long enough to be full again.
func (l *Limiter) sweep(now time.Time) {
	idle := time.Duration(l.burst / l.rate * float64(time.Second))
	for key, b := range l.buckets {
		if now.Sub(b.last) >= idle {
			delete(l.buckets, key)
		}
	}
}

// ClientKey identifies a caller by address. An IPv6 address is reduced to its /64,
// because one customer usually holds a whole /64 and could otherwise rotate
// through addresses to avoid a limit.
func ClientKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// ByIP keys a limit on the caller's address.
func ByIP(c *gin.Context) string { return ClientKey(c.ClientIP()) }

// ByUserOrIP keys a limit on the signed-in user, falling back to the address.
// It must run after the authentication middleware to see the user.
func ByUserOrIP(c *gin.Context) string {
	if id, ok := c.Get("user_id"); ok {
		return fmt.Sprintf("user:%v", id)
	}
	return ByIP(c)
}

// RateLimit answers 429 with Retry-After when the caller is over the limit.
func RateLimit(limiter *Limiter, key func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if ok, wait := limiter.Allow(key(c)); !ok {
			TooManyRequests(c, wait)
			return
		}
		c.Next()
	}
}

// TooManyRequests writes the standard 429 answer and stops the request.
func TooManyRequests(c *gin.Context, wait time.Duration) {
	seconds := int(math.Ceil(wait.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"ok":          false,
		"message":     "Terlalu banyak permintaan. Coba lagi nanti.",
		"retry_after": seconds,
	})
}
