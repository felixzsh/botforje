package api

import (
	"net/http"
	"strings"
	"testing"
	"time"
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

func TestRateLimitWindowResets(t *testing.T) {
	server := testServer(t)
	server.limiter.credential = 1

	if recorder := get(t, server.Handler(), "/api/v1/status", testOwnerToken); recorder.Code != http.StatusOK {
		t.Fatalf("first request = %d, want 200", recorder.Code)
	}
	if recorder := get(t, server.Handler(), "/api/v1/status", testOwnerToken); recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("second request = %d, want 429", recorder.Code)
	}

	epoch := server.limiter.now()
	server.limiter.now = func() time.Time { return epoch.Add(2 * time.Minute) }
	if recorder := get(t, server.Handler(), "/api/v1/status", testOwnerToken); recorder.Code != http.StatusOK {
		t.Fatalf("request after the window = %d, want 200", recorder.Code)
	}
}

// TestRateLimitBoundsFailedAttempts: attempts with no usable credential are
// bounded per source address, and a valid credential still works afterwards.
func TestRateLimitBoundsFailedAttempts(t *testing.T) {
	server := testServer(t)
	server.limiter.failedAttempt = 2

	for _, key := range []string{"", "wrong"} {
		if recorder := get(t, server.Handler(), "/api/v1/status", key); recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt with key %q = %d, want 401", key, recorder.Code)
		}
	}
	over := get(t, server.Handler(), "/api/v1/status", "wrong")
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("third failed attempt = %d, want 429", over.Code)
	}
	if recorder := get(t, server.Handler(), "/api/v1/status", testOwnerToken); recorder.Code != http.StatusOK {
		t.Fatalf("valid credential = %d, want 200 outside the failed budget", recorder.Code)
	}
}
