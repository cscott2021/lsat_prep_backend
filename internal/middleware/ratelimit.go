package middleware

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rateLimiter is a lightweight in-memory, per-user fixed-window request limiter.
// It is DEFENSE-IN-DEPTH — not the primary paywall or free-tier control. The
// limit is set well above normal interactive use so a real user never trips it,
// but a runaway client or a scripted abuse loop (e.g. hammering the answer
// endpoint) is capped. State is per-process: fine on a single instance; if this
// service is ever scaled out each instance keeps its own counters, which only
// makes the effective limit more lenient, never stricter.
type rateLimiter struct {
	mu      sync.Mutex
	windows map[int64]*rlWindow
	limit   int
	window  time.Duration
}

type rlWindow struct {
	count int
	reset time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		windows: make(map[int64]*rlWindow),
		limit:   limit,
		window:  window,
	}
	go rl.gc()
	return rl
}

// allow reports whether userID may make another request in the current window,
// and if not, how long until the window resets.
func (rl *rateLimiter) allow(userID int64) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	w := rl.windows[userID]
	if w == nil || now.After(w.reset) {
		rl.windows[userID] = &rlWindow{count: 1, reset: now.Add(rl.window)}
		return true, 0
	}
	if w.count >= rl.limit {
		return false, time.Until(w.reset)
	}
	w.count++
	return true, 0
}

// gc periodically drops expired windows so the map cannot grow without bound
// (one entry per distinct active user id otherwise lingers forever).
func (rl *rateLimiter) gc() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for uid, w := range rl.windows {
			if now.After(w.reset) {
				delete(rl.windows, uid)
			}
		}
		rl.mu.Unlock()
	}
}

// RateLimit builds a per-user-id rate-limiting middleware. It MUST be layered on
// top of AuthMiddleware (which populates user_id in the request context):
// requests without a user_id are passed through untouched, so it never throttles
// anonymous/public traffic (that would risk penalizing many users behind a
// single shared/NAT IP). Over-limit requests get a 429 with a Retry-After hint.
func RateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	rl := newRateLimiter(limit, window)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := r.Context().Value("user_id").(int64)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			allowed, retryAfter := rl.allow(userID)
			if !allowed {
				secs := int(retryAfter.Seconds()) + 1
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate_limit_exceeded"})
				log.Printf("[ratelimit] user %d exceeded %d requests per %s", userID, rl.limit, rl.window)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
