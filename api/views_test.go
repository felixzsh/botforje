package api

import (
	"encoding/json"
	"net/http"
	"strings"
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
			State   string `json:"state"`
			Outbox  int    `json:"outbox"`
			Binding *struct {
				Provider   string `json:"provider"`
				Generation int64  `json:"generation"`
				Identity   struct {
					Provider string `json:"provider"`
					Subject  string `json:"subject"`
				} `json:"identity"`
			} `json:"binding"`
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
		bot.State != "connected" || bot.Outbox != 2 {
		t.Fatalf("bot = %#v", bot)
	}
	if bot.Binding == nil || bot.Binding.Provider != "whatsapp" ||
		bot.Binding.Identity.Subject != "5215500000000" {
		t.Fatalf("binding = %#v, want the committed one", bot.Binding)
	}
	if body.Bots[1].Binding != nil {
		t.Fatalf("unbound bot = %#v, want a null binding", body.Bots[1])
	}
	if raw := recorder.Body.String(); strings.Contains(raw, "phone") ||
		strings.Contains(raw, "pairing") {
		t.Fatalf("body %s retains the retired pairing projection", raw)
	}
}

func TestMessagesEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/messages?bot=demo&chat_id=3&limit=1",
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
		"/api/v1/scopes/support/sessions?bot=demo&chat_id=3", testOwnerToken)
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

func TestDirectoryEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/directory", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Directory []struct {
			Provider       string `json:"provider"`
			Subject        string `json:"subject"`
			SourceProvider string `json:"source_provider"`
			SourceSubject  string `json:"source_subject"`
			DisplayName    string `json:"display_name"`
		} `json:"directory"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Directory) != 2 {
		t.Fatalf("directory = %+v, want the bot's two", body.Directory)
	}
	if raw := recorder.Body.String(); strings.Contains(raw, "phone") {
		t.Fatalf("directory entry retains a phone field: %s", raw)
	}
}
