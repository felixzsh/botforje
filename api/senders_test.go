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

func TestSendersListPages(t *testing.T) {
	senders := &fakeSenders{listPage: botflota.SenderPage{
		Senders: []botflota.Sender{{
			ID: 7, Scope: "support", Phone: "5215500000000",
			DisplayName: "Ana", FirstSeen: testTime,
			LastSeen: testTime.Add(time.Minute),
		}},
		NextCursor: "next-page",
	}}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Senders = senders }, nil)
	since := testTime.UnixMilli() + 1234
	path := fmt.Sprintf(
		"/api/v1/scopes/support/senders?since=%d&limit=1&cursor=abc", since)
	recorder := get(t, server.Handler(), path, testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Scope      string              `json:"scope"`
		Senders    []senderProfileView `json:"senders"`
		NextCursor string              `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Scope != "support" || body.NextCursor != "next-page" {
		t.Fatalf("body = %s", recorder.Body)
	}
	if len(body.Senders) != 1 || body.Senders[0].Phone != "5215500000000" ||
		body.Senders[0].DisplayName != "Ana" {
		t.Fatalf("senders = %#v", body.Senders)
	}
	if !senders.lastSince.Equal(time.UnixMilli(since)) {
		t.Fatalf("since = %v, want %v", senders.lastSince, time.UnixMilli(since))
	}
	if senders.lastLimit != 1 || senders.lastCursor != "abc" {
		t.Fatalf("limit/cursor = %d/%q", senders.lastLimit, senders.lastCursor)
	}
}

func TestSendersListOmitsTheCursorOnTheLastPage(t *testing.T) {
	senders := &fakeSenders{listPage: botflota.SenderPage{
		Senders: []botflota.Sender{{
			ID: 7, Scope: "support", Phone: "5215500000000", FirstSeen: testTime,
		}},
	}}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Senders = senders }, nil)
	recorder := get(t, server.Handler(), "/api/v1/scopes/support/senders", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if got := recorder.Body.String(); strings.Contains(got, "next_cursor") {
		t.Fatalf("a last page must not carry a cursor: %s", got)
	}
}

func TestSendersListRejectsBadParameters(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		store *fakeSenders
	}{
		{"bad since", "/api/v1/scopes/support/senders?since=yesterday", &fakeSenders{}},
		{"bad cursor", "/api/v1/scopes/support/senders?cursor=zzz",
			&fakeSenders{listErr: botflota.ErrInvalidCursor}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := testServerWithOptions(t, &fakeFleet{},
				func(opts *Options) { opts.Senders = tc.store }, nil)
			recorder := get(t, server.Handler(), tc.path, testOwnerToken)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s",
					recorder.Code, recorder.Body)
			}
		})
	}
}

func TestSenderRead(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/senders/5215500000000", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var view senderProfileView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if view.Phone != "5215500000000" || view.DisplayName != "Ana" {
		t.Fatalf("view = %#v", view)
	}

	missing := get(t, server.Handler(),
		"/api/v1/scopes/support/senders/5215500000099", testOwnerToken)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown sender status = %d, want 404", missing.Code)
	}

	out := get(t, server.Handler(),
		"/api/v1/scopes/ventas/senders/5215500000000", testAccessToken)
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
