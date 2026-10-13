package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

func TestPeersListPages(t *testing.T) {
	peers := &fakePeers{listPage: botflota.PeerPage{
		Peers: []botflota.Peer{{
			ID: 7, Scope: "support", Key: waKey("5215500000000"),
			DisplayName: "Ana", FirstSeen: testTime,
			LastSeen: testTime.Add(time.Minute), AddressBookImported: true,
			HasChat: true, HasHistory: true,
		}},
		NextCursor: "next-page",
	}}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Peers = peers }, nil)
	since := testTime.UnixMilli() + 1234
	path := fmt.Sprintf(
		"/api/v1/scopes/support/peers?since=%d&limit=1&cursor=abc", since)
	recorder := get(t, server.Handler(), path, testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Scope      string     `json:"scope"`
		Peers      []peerView `json:"peers"`
		NextCursor string     `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Scope != "support" || body.NextCursor != "next-page" {
		t.Fatalf("body = %s", recorder.Body)
	}
	if len(body.Peers) != 1 {
		t.Fatalf("peers = %#v", body.Peers)
	}
	peer := body.Peers[0]
	if peer.ID != 7 || peer.Provider != "whatsapp" ||
		peer.Subject != "5215500000000" || peer.DisplayName != "Ana" ||
		!peer.DirectoryImported || !peer.HasChat || !peer.HasHistory {
		t.Fatalf("peer = %#v", peer)
	}
	if raw := recorder.Body.String(); strings.Contains(raw, "phone") {
		t.Fatalf("peer view retains a phone field: %s", raw)
	}
	if !peers.lastSince.Equal(time.UnixMilli(since)) {
		t.Fatalf("since = %v, want %v", peers.lastSince, time.UnixMilli(since))
	}
	if peers.lastLimit != 1 || peers.lastCursor != "abc" {
		t.Fatalf("limit/cursor = %d/%q", peers.lastLimit, peers.lastCursor)
	}
}

func TestPeersListOmitsTheCursorOnTheLastPage(t *testing.T) {
	peers := &fakePeers{listPage: botflota.PeerPage{
		Peers: []botflota.Peer{{
			ID: 7, Scope: "support", Key: waKey("5215500000000"), FirstSeen: testTime,
		}},
	}}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Peers = peers }, nil)
	recorder := get(t, server.Handler(), "/api/v1/scopes/support/peers", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if got := recorder.Body.String(); strings.Contains(got, "next_cursor") {
		t.Fatalf("a last page must not carry a cursor: %s", got)
	}
}

func TestPeersListRejectsBadParameters(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		store *fakePeers
	}{
		{"bad since", "/api/v1/scopes/support/peers?since=yesterday", &fakePeers{}},
		{"bad cursor", "/api/v1/scopes/support/peers?cursor=zzz",
			&fakePeers{listErr: botflota.ErrInvalidCursor}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := testServerWithOptions(t, &fakeFleet{},
				func(opts *Options) { opts.Peers = tc.store }, nil)
			recorder := get(t, server.Handler(), tc.path, testOwnerToken)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s",
					recorder.Code, recorder.Body)
			}
		})
	}
}

// TestPeerRead resolves the scope-owned internal id, never a subject alone
// (ADR 0044).
func TestPeerRead(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/peers/7", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var view peerView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if view.ID != 7 || view.Subject != "5215500000000" || view.DisplayName != "Ana" {
		t.Fatalf("view = %#v", view)
	}

	missing := get(t, server.Handler(),
		"/api/v1/scopes/support/peers/999", testOwnerToken)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown peer status = %d, want 404", missing.Code)
	}

	out := get(t, server.Handler(),
		"/api/v1/scopes/ventas/peers/7", testAccessToken)
	if out.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope status = %d, want 404", out.Code)
	}
}

func TestScopeBotRead(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var view botView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if view.Scope != "support" || view.Bot != "demo" ||
		view.Binding == nil ||
		view.Binding.Identity.Subject != "5215500000000" {
		t.Fatalf("view = %#v", view)
	}

	missing := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/ghost", testOwnerToken)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown bot status = %d, want 404", missing.Code)
	}

	out := get(t, server.Handler(),
		"/api/v1/scopes/ventas/bots/other", testAccessToken)
	if out.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope status = %d, want 404", out.Code)
	}
}
