package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota/adapters/whatsapp"
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
				History: whatsapp.ImportTotals{
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
