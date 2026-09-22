package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// TestPrintPairImport covers the result `bot pair` answers with (ADR 0038):
// a switch that is off prints no line at all.
func TestPrintPairImport(t *testing.T) {
	on, off := true, false
	settings := func(history, contacts *bool) *spec.BotSettings {
		return &spec.BotSettings{HistorySync: history, ImportContacts: contacts}
	}
	cases := []struct {
		name     string
		settings *spec.BotSettings
		result   whatsapp.PairResult
		want     []string
		absent   []string
	}{
		{
			name:     "history and contacts",
			settings: settings(&on, &on),
			result: whatsapp.PairResult{
				History: ingest.ImportTotals{
					Chunks: 706, Conversations: 184, Imported: 2727,
					Duplicate: 31, Skipped: 58,
				},
				Contacts: 312,
				Elapsed:  2*time.Minute + 10*time.Second,
			},
			want: []string{
				"history: 184 chats, 2727 messages (31 duplicate, 58 skipped) in 2m10s",
				"contacts: 312 imported",
			},
		},
		{
			name:     "nothing arrived",
			settings: settings(nil, nil),
			result:   whatsapp.PairResult{},
			want:     []string{"history: nothing arrived"},
			absent:   []string{"contacts:"},
		},
		{
			name:     "history disabled",
			settings: settings(&off, &on),
			result:   whatsapp.PairResult{Contacts: 5},
			want:     []string{"contacts: 5 imported"},
			absent:   []string{"history:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buffer bytes.Buffer
			printPairImport(&buffer, tc.settings, tc.result)
			out := buffer.String()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output %q is missing %q", out, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(out, absent) {
					t.Errorf("output %q should not contain %q", out, absent)
				}
			}
		})
	}
}

// TestListBotsReadsTheAPI covers `bot list` as an instance query: it never
// opens the database, it maps what the API answers (ADR 0039).
func TestListBotsReadsTheAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/bots" {
				t.Errorf("path = %q, want /api/v1/bots", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{"bots":[
				{"scope":"support","bot":"demo","phone":"5215500000000",
				 "pairing":"paired","last_change":"2026-09-19T12:00:00Z"},
				{"scope":"ventas","bot":"other","pairing":"not paired"}
			]}`))
		}))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}

	entries, err := listBots(context.Background(), client)
	if err != nil {
		t.Fatalf("listBots: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want two", entries)
	}
	first := entries[0]
	if first.Scope != "support" || first.Bot != "demo" ||
		first.Phone != "5215500000000" || first.Status != "paired" {
		t.Fatalf("first = %#v", first)
	}
	if first.LastChange == nil || !first.LastChange.Equal(testBotListTime) {
		t.Fatalf("last change = %v, want %v", first.LastChange, testBotListTime)
	}
	second := entries[1]
	if second.Status != string(botflota.PairingNotPaired) ||
		second.Phone != "" || second.LastChange != nil {
		t.Fatalf("second = %#v", second)
	}
}

var testBotListTime = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

// TestPairThroughAPI covers `bot pair` under a live instance: it drives the
// pairing session over HTTP and never opens the database (ADR 0039).
func TestPairThroughAPI(t *testing.T) {
	bots := `{"bots":[{"scope":"dev","bot":"alpha","phone":"","pairing":"not paired"}]}`
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/bots":
				_, _ = w.Write([]byte(bots))
			case r.Method == http.MethodPost &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/pairing":
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"state":"paired","result":{` +
					`"phone":"5215500000001","chunks":9,"chats":2,` +
					`"messages":30,"duplicate":1,"skipped":0,"contacts":4,` +
					`"elapsed_seconds":65}}`))
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
	defer server.Close()
	clearAPIEnv(t)
	t.Setenv("BOTFLOTA_API_URL", server.URL)
	t.Setenv("BOTFLOTA_TOKEN", "tok")
	cfg := &config.Config{DataDir: t.TempDir()}

	var out bytes.Buffer
	importContacts := true
	settings := &spec.BotSettings{ImportContacts: &importContacts}
	code := pairThroughAPI(&out, cfg, "dev", "alpha", settings, true)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	printed := out.String()
	for _, want := range []string{
		`bot "alpha" (scope "dev") is paired as 5215500000001`,
		"history: 2 chats, 30 messages (1 duplicate, 0 skipped) in 1m5s",
		"contacts: 4 imported",
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("output %q is missing %q", printed, want)
		}
	}
}

// TestPairThroughAPIReportsAPairedBot covers the idempotent answer: a bot the
// instance already knows is reported, not paired again.
func TestPairThroughAPIReportsAPairedBot(t *testing.T) {
	bots := `{"bots":[{"scope":"dev","bot":"alpha",` +
		`"phone":"5215500000009","pairing":"paired"}]}`
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/bots" {
				t.Errorf("path = %s, want only the bots list", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(bots))
		}))
	defer server.Close()
	clearAPIEnv(t)
	t.Setenv("BOTFLOTA_API_URL", server.URL)
	t.Setenv("BOTFLOTA_TOKEN", "tok")

	var out bytes.Buffer
	code := pairThroughAPI(&out, &config.Config{DataDir: t.TempDir()},
		"dev", "alpha", nil, false)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(out.String(),
		"is already paired as 5215500000009") {
		t.Fatalf("output = %q", out.String())
	}
}
