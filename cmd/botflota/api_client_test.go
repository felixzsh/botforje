package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixzsh/botflota/config"
)

func clearAPIEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BOTFLOTA_API_URL", "")
	t.Setenv("BOTFLOTA_TOKEN", "")
}

func TestNewAPIClientFromConfig(t *testing.T) {
	clearAPIEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "owner.token")
	if err := os.WriteFile(path, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := newAPIClient(&config.Config{Port: 4000, DataDir: dir})
	if err != nil {
		t.Fatalf("newAPIClient: %v", err)
	}
	if client.base != "http://127.0.0.1:4000" {
		t.Fatalf("base = %q, want the config address and port", client.base)
	}
	if client.token != "secret" {
		t.Fatalf("token = %q, want the file contents trimmed", client.token)
	}
}

func TestNewAPIClientEnvironmentWins(t *testing.T) {
	t.Setenv("BOTFLOTA_API_URL", "http://example.test:9000/")
	t.Setenv("BOTFLOTA_TOKEN", "tok")
	client, err := newAPIClient(&config.Config{Port: 4000, DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("newAPIClient: %v", err)
	}
	if client.base != "http://example.test:9000" {
		t.Fatalf("base = %q, want the environment URL without a trailing slash",
			client.base)
	}
	if client.token != "tok" {
		t.Fatalf("token = %q, want the environment token", client.token)
	}
}

func TestNewAPIClientNeedsCredential(t *testing.T) {
	clearAPIEnv(t)
	_, err := newAPIClient(&config.Config{Port: 4000, DataDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "BOTFLOTA_TOKEN") {
		t.Fatalf("want a credential error naming BOTFLOTA_TOKEN, got %v", err)
	}
}

func TestNewAPIClientNeedsPort(t *testing.T) {
	clearAPIEnv(t)
	_, err := newAPIClient(&config.Config{DataDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "port is 0") {
		t.Fatalf("want a disabled-API error, got %v", err)
	}
}

func TestAPIClientErrorShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Errorf("Authorization = %q, want the bearer token", got)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(
				`{"error":{"code":"not_found","message":"unknown sender"}}`))
		}))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}
	err := client.get(context.Background(), "/api/v1/anything", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown sender") {
		t.Fatalf("want the API message in the error, got %v", err)
	}
}
