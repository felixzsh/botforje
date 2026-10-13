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
		{"unknown scope", "/api/v1/scopes/nope/bots/demo/chats", http.StatusNotFound},
		{"unknown bot", "/api/v1/scopes/support/bots/nope/chats", http.StatusNotFound},
		{"bad limit", "/api/v1/scopes/support/bots/demo/chats?limit=zero", http.StatusBadRequest},
		{"bad after_id",
			"/api/v1/scopes/support/messages?bot=demo&chat_id=3&after_id=abc",
			http.StatusBadRequest},
		{"missing chat",
			"/api/v1/scopes/support/messages?bot=demo", http.StatusBadRequest},
		{"unknown chat",
			"/api/v1/scopes/support/messages?bot=demo&chat_id=999", http.StatusNotFound},
		{"foreign chat",
			"/api/v1/scopes/support/messages?bot=demo&chat_id=4", http.StatusOK},
		{"no session",
			"/api/v1/scopes/support/bots/demo/graph-runs?chat_id=4",
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
