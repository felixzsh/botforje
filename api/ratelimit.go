package api

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/felixzsh/botflota/config"
)

const (
	// One fixed one-minute window per key; the budgets themselves come from
	// config.RateBudgets, where zero turns a budget off.
	rateLimitWindow = time.Minute
	// pruneAbove keeps the window map from growing without bound as source
	// addresses come and go.
	pruneAbove = 4096
)

type limitWindow struct {
	count int
	reset time.Time
}

// rateLimiter counts fixed windows per key. The zero value is not usable;
// newRateLimiter builds one.
type rateLimiter struct {
	mu            sync.Mutex
	now           func() time.Time
	windows       map[string]limitWindow
	credential    int
	failedAttempt int
}

// newRateLimiter builds the budgets from config; a nil value uses the
// built-in defaults, and fleet run always passes the resolved ones.
func newRateLimiter(budgets *config.RateBudgets) *rateLimiter {
	limiter := &rateLimiter{
		now:           time.Now,
		windows:       map[string]limitWindow{},
		credential:    config.DefaultCredentialPerMinute,
		failedAttempt: config.DefaultFailedPerMinute,
	}
	if budgets != nil {
		limiter.credential = budgets.CredentialPerMinute
		limiter.failedAttempt = budgets.FailedPerMinute
	}
	return limiter
}

// allow consumes one unit for key and reports whether the caller may
// proceed; after a refusal it says how long the window has left. A budget
// of zero or less turns the limit off (config: rate_limit).
func (l *rateLimiter) allow(key string, limit int) (bool, time.Duration) {
	if limit <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.windows) > pruneAbove {
		for windowKey, window := range l.windows {
			if now.After(window.reset) {
				delete(l.windows, windowKey)
			}
		}
	}
	window, ok := l.windows[key]
	if !ok || now.After(window.reset) {
		window = limitWindow{count: 0, reset: now.Add(rateLimitWindow)}
	}
	if window.count >= limit {
		return false, window.reset.Sub(now)
	}
	window.count++
	l.windows[key] = window
	return true, 0
}

// requestAddress is the source of a request as the server sees it; a
// connection without a usable address keeps its raw RemoteAddr.
func requestAddress(r *http.Request) string {
	address, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return address
}

// writeRateLimited answers with one shape and the wait, so a client can back
// off instead of retrying in a loop.
func writeRateLimited(w http.ResponseWriter, retry time.Duration) {
	seconds := int((retry + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, http.StatusTooManyRequests, "rate_limited",
		"too many requests; retry later")
}
