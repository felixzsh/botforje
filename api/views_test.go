package api

import (
	"encoding/json"
	"net/http"
	"testing"

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
			Scope   string `json:"scope"`
			Bot     string `json:"bot"`
			JID     string `json:"jid"`
			Pairing string `json:"pairing"`
			State   string `json:"state"`
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
		bot.JID != "5215500000000@s.whatsapp.net" ||
		bot.Pairing != string(botflota.PairingPaired) || bot.State != "connected" {
		t.Fatalf("bot = %#v", bot)
	}
}

func TestMessagesEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/messages?bot=demo&sender=5215500000000&limit=1",
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
		"/api/v1/scopes/support/sessions?bot=demo&sender=5215500000000", testOwnerToken)
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

func TestContactsEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/contacts?bot=demo", testOwnerToken)
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
