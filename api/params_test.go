package api

import (
	"net/http"
	"testing"
)

func TestQueryErrors(t *testing.T) {
	server := testServer(t)
	cases := []struct {
		name string
		path string
		want int
	}{
		{"unknown scope", "/api/v1/scopes/nope/chats", http.StatusNotFound},
		{"unknown bot", "/api/v1/scopes/support/chats?bot=nope", http.StatusNotFound},
		{"bad limit", "/api/v1/scopes/support/chats?limit=zero", http.StatusBadRequest},
		{"bad after_id",
			"/api/v1/scopes/support/messages?bot=demo&sender=5215500000000&after_id=abc",
			http.StatusBadRequest},
		{"missing sender",
			"/api/v1/scopes/support/messages?bot=demo", http.StatusBadRequest},
		{"unknown sender",
			"/api/v1/scopes/support/messages?bot=demo&sender=000", http.StatusNotFound},
		{"no session",
			"/api/v1/scopes/support/sessions?bot=demo&sender=5215599999999",
			http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := get(t, server.Handler(), tc.path, testOwnerToken)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.want)
			}
		})
	}
}
