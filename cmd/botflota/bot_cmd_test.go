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
