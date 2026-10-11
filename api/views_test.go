package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

func TestStatusEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(), "/api/v1/status", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		UptimeSeconds int `json:"uptime_seconds"`
		Bots          []struct {
			Scope    string `json:"scope"`
			Bot      string `json:"bot"`
			State    string `json:"state"`
			Outbox   int    `json:"outbox"`
			Sessions int    `json:"sessions"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Bots) != 2 {
		t.Fatalf("bots = %#v, want two", body.Bots)
	}
	bot := body.Bots[0]
	if bot.Scope != "support" || bot.Bot != "demo" ||
		bot.State != "connected" || bot.Outbox != 2 || bot.Sessions != 1 {
		t.Fatalf("bot = %#v", bot)
	}
	if body.UptimeSeconds < 0 {
		t.Fatalf("uptime_seconds = %d", body.UptimeSeconds)
	}
}

func TestBotsEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(), "/api/v1/bots", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Bots []struct {
			Scope      string    `json:"scope"`
			Bot        string    `json:"bot"`
			Phone      string    `json:"phone"`
			Pairing    string    `json:"pairing"`
			LastChange time.Time `json:"last_change"`
			State      string    `json:"state"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Bots) != 2 {
		t.Fatalf("bots = %#v, want two", body.Bots)
	}
	bot := body.Bots[0]
	if bot.Scope != "support" || bot.Bot != "demo" ||
		bot.Phone != "5215500000000" ||
		bot.Pairing != string(botflota.PairingPaired) || bot.State != "connected" {
		t.Fatalf("bot = %#v", bot)
	}
	if !bot.LastChange.Equal(testTime) {
		t.Fatalf("last_change = %v, want %v", bot.LastChange, testTime)
	}
	if body.Bots[1].Pairing != string(botflota.PairingNotPaired) ||
		body.Bots[1].Phone != "" {
		t.Fatalf("unpaired bot = %#v, want not paired with no phone", body.Bots[1])
	}
}

// fakePresence reports local credentials per account, so the bots view can be
// checked without the device store.
type fakePresence struct{ present map[string]bool }

func (f fakePresence) HasCredentials(
	_ context.Context, account *botflota.Binding,
) (bool, error) {
	return f.present[account.Scope+"/"+account.Bot], nil
}

// TestBotsEndpointPrefersLivePresence proves the live credential check wins
// over the stored state: a stored "paired" account whose local credentials are
// gone reports unpaired and hides its phone (ADR 0011, ADR 0039).
func TestBotsEndpointPrefersLivePresence(t *testing.T) {
	server := testServer(t)
	server.presence = fakePresence{present: map[string]bool{
		"support/demo": false,
	}}
	recorder := get(t, server.Handler(), "/api/v1/bots", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Bots []struct {
			Phone      string     `json:"phone"`
			Pairing    string     `json:"pairing"`
			LastChange *time.Time `json:"last_change"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Bots) != 2 {
		t.Fatalf("bots = %#v, want two", body.Bots)
	}
	bot := body.Bots[0]
	if bot.Pairing != string(botflota.PairingUnpaired) || bot.Phone != "" {
		t.Fatalf("bot = %#v, want unpaired with no phone", bot)
	}
	if bot.LastChange != nil {
		t.Fatalf("last_change = %v, want none for a state that changed", bot.LastChange)
	}
}

func TestMessagesEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/messages?bot=demo&contact=5215500000000&limit=1",
		testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Scope    string `json:"scope"`
		Bot      string `json:"bot"`
		ChatID   int64  `json:"chat_id"`
		Messages []struct {
			ID         int64  `json:"id"`
			Direction  string `json:"direction"`
			Kind       string `json:"kind"`
			IngestKind string `json:"ingest_kind"`
			Origin     string `json:"origin"`
			Routing    string `json:"routing"`
			Body       string `json:"body"`
			Content    struct {
				Body string `json:"body"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Scope != "support" || body.Bot != "demo" || body.ChatID != 3 {
		t.Fatalf("messages envelope = %#v", body)
	}
	if len(body.Messages) != 1 || body.Messages[0].Body != "hi" ||
		body.Messages[0].Direction != string(botflota.Outbound) {
		t.Fatalf("messages = %#v, want the newest one", body.Messages)
	}
	got := body.Messages[0]
	if got.Kind != string(botflota.KindText) || got.Content.Body != "hi" {
		t.Fatalf("kind/content = %#v, want the structured text payload", got)
	}
	if got.IngestKind != string(botflota.IngestLive) ||
		got.Origin != string(botflota.OriginPreset) ||
		got.Routing != string(botflota.RoutingTriggerExact) {
		t.Fatalf("provenance = %#v", got)
	}
}

func TestSessionsEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/sessions?bot=demo&contact=5215500000000", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Session struct {
			ID   int64  `json:"id"`
			Node string `json:"node"`
		} `json:"session"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Session.ID != 9 || body.Session.Node != "menu" {
		t.Fatalf("session = %#v", body.Session)
	}
}

func TestAddressBookEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/contacts", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Contacts []struct {
			Phone    string `json:"phone"`
			Name     string `json:"name"`
			PushName string `json:"push_name"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Contacts) != 2 {
		t.Fatalf("contacts = %+v, want the bot's two", body.Contacts)
	}
}
