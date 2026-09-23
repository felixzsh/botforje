package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota/config"
)

// TestRateLimitIsPerCredential: one token spends its own budget, is refused
// with a retry hint, and another credential is unaffected (ROADMAP Phase 13).
func TestRateLimitIsPerCredential(t *testing.T) {
	server := testServer(t)
	server.limiter.credential = 2

	for i := 0; i < 2; i++ {
		recorder := get(t, server.Handler(), "/api/v1/status", testOwnerToken)
		if recorder.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", i+1, recorder.Code)
		}
	}
	over := get(t, server.Handler(), "/api/v1/status", testOwnerToken)
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("third request = %d, want 429", over.Code)
	}
	if over.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After header on the refusal")
	}
	if body := over.Body.String(); !strings.Contains(body, "rate_limited") {
		t.Fatalf("body = %s, want the public error code", body)
	}

	scoped := get(t, server.Handler(), "/api/v1/status", testAccessToken)
	if scoped.Code != http.StatusOK {
		t.Fatalf("scoped request = %d, want 200 on its own budget", scoped.Code)
	}
}

// status is one request's HTTP status, keeping the budget tests readable.
func status(t *testing.T, server *Server, path, token string) int {
	t.Helper()
	return get(t, server.Handler(), path, token).Code
}

func TestRateLimitDefaultsMatchTheConfig(t *testing.T) {
	server := testServer(t)
	if server.limiter.credential != config.DefaultCredentialPerMinute ||
		server.limiter.failedAttempt != config.DefaultFailedPerMinute {
		t.Fatalf("budgets = %d/%d, want the built-in defaults",
			server.limiter.credential, server.limiter.failedAttempt)
	}
}

func TestRateLimitCanBeTurnedOff(t *testing.T) {
	server := testServer(t)
	server.limiter.credential = 0
	server.limiter.failedAttempt = 0

	for i := 0; i < 5; i++ {
		if got := status(t, server, "/api/v1/status", testOwnerToken); got != http.StatusOK {
			t.Fatalf("request %d = %d, want 200 with the budget off", i+1, got)
		}
	}
	for i := 0; i < 5; i++ {
		if got := status(t, server, "/api/v1/status", "wrong"); got != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d = %d, want 401 with the budget off", i+1, got)
		}
	}
}

func TestRateLimitWindowResets(t *testing.T) {
	server := testServer(t)
	server.limiter.credential = 1

	if got := status(t, server, "/api/v1/status", testOwnerToken); got != http.StatusOK {
		t.Fatalf("first request = %d, want 200", got)
	}
	if got := status(t, server, "/api/v1/status", testOwnerToken); got != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", got)
	}

	epoch := server.limiter.now()
	server.limiter.now = func() time.Time { return epoch.Add(2 * time.Minute) }
	if got := status(t, server, "/api/v1/status", testOwnerToken); got != http.StatusOK {
		t.Fatalf("request after the window = %d, want 200", got)
	}
}

// TestRateLimitBoundsFailedAttempts: attempts with no usable credential are
// bounded per source address, and a valid credential still works afterwards.
func TestRateLimitBoundsFailedAttempts(t *testing.T) {
	server := testServer(t)
	server.limiter.failedAttempt = 2

	for _, key := range []string{"", "wrong"} {
		if got := status(t, server, "/api/v1/status", key); got != http.StatusUnauthorized {
			t.Fatalf("attempt with key %q = %d, want 401", key, got)
		}
	}
	over := get(t, server.Handler(), "/api/v1/status", "wrong")
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("third failed attempt = %d, want 429", over.Code)
	}
	if got := status(t, server, "/api/v1/status", testOwnerToken); got != http.StatusOK {
		t.Fatalf("valid credential = %d, want 200 outside the failed budget", got)
	}
}
