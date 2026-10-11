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

func TestContactsListPages(t *testing.T) {
	contacts := &fakePeers{listPage: botflota.PeerPage{
		Peers: []botflota.Peer{{
			ID: 7, Scope: "support", Key: waKey("5215500000000"),
			DisplayName: "Ana", FirstSeen: testTime,
			LastSeen: testTime.Add(time.Minute), AddressBookImported: true,
			HasChat: true, HasHistory: true,
		}},
		NextCursor: "next-page",
	}}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Peers = contacts }, nil)
	since := testTime.UnixMilli() + 1234
	path := fmt.Sprintf(
		"/api/v1/scopes/support/contacts?since=%d&limit=1&cursor=abc", since)
	recorder := get(t, server.Handler(), path, testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Scope      string               `json:"scope"`
		Contacts   []contactProfileView `json:"contacts"`
		NextCursor string               `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Scope != "support" || body.NextCursor != "next-page" {
		t.Fatalf("body = %s", recorder.Body)
	}
	if len(body.Contacts) != 1 || body.Contacts[0].Phone != "5215500000000" ||
		body.Contacts[0].DisplayName != "Ana" ||
		!body.Contacts[0].AddressBookImported || !body.Contacts[0].HasChat ||
		!body.Contacts[0].HasHistory {
		t.Fatalf("contacts = %#v", body.Contacts)
	}
	if !contacts.lastSince.Equal(time.UnixMilli(since)) {
		t.Fatalf("since = %v, want %v", contacts.lastSince, time.UnixMilli(since))
	}
	if contacts.lastLimit != 1 || contacts.lastCursor != "abc" {
		t.Fatalf("limit/cursor = %d/%q", contacts.lastLimit, contacts.lastCursor)
	}
}

func TestContactsListOmitsTheCursorOnTheLastPage(t *testing.T) {
	contacts := &fakePeers{listPage: botflota.PeerPage{
		Peers: []botflota.Peer{{
			ID: 7, Scope: "support", Key: waKey("5215500000000"), FirstSeen: testTime,
		}},
	}}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Peers = contacts }, nil)
	recorder := get(t, server.Handler(), "/api/v1/scopes/support/contacts", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if got := recorder.Body.String(); strings.Contains(got, "next_cursor") {
		t.Fatalf("a last page must not carry a cursor: %s", got)
	}
}

func TestContactsListRejectsBadParameters(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		store *fakePeers
	}{
		{"bad since", "/api/v1/scopes/support/contacts?since=yesterday", &fakePeers{}},
		{"bad cursor", "/api/v1/scopes/support/contacts?cursor=zzz",
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

func TestContactRead(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/contacts/5215500000000", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var view contactProfileView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if view.Phone != "5215500000000" || view.DisplayName != "Ana" {
		t.Fatalf("view = %#v", view)
	}

	missing := get(t, server.Handler(),
		"/api/v1/scopes/support/contacts/5215500000099", testOwnerToken)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown contact status = %d, want 404", missing.Code)
	}

	out := get(t, server.Handler(),
		"/api/v1/scopes/ventas/contacts/5215500000000", testAccessToken)
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
		view.Phone != "5215500000000" || view.Pairing != "paired" {
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
