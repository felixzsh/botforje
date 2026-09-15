package main

import (
	"testing"
	"time"

	"github.com/felixzsh/botforje/internal/config"
)

func TestWhatsappSettingsMapsPacing(t *testing.T) {
	enabled := true
	got := whatsappSettings(config.BotSettings{
		ReadReceipts:     &enabled,
		ReadReceiptDelay: 700,
		ReplyDelay:       900,
		SimulateTyping:   &enabled,
		Jitter:           250,
	})
	if !got.ReadReceipts || !got.SimulateTyping {
		t.Fatalf("booleans not mapped: %#v", got)
	}
	if got.ReadReceiptDelay != 700*time.Millisecond ||
		got.ReplyDelay != 900*time.Millisecond ||
		got.Jitter != 250*time.Millisecond {
		t.Fatalf("durations not mapped: %#v", got)
	}
}
