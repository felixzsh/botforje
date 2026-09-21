package main

import (
	"net"
	"testing"
	"time"

	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

func TestWhatsappSettingsMapsPacing(t *testing.T) {
	enabled := true
	got := whatsappSettings(spec.BotSettings{
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

// TestFleetLooksRunning covers the guard that keeps bot pair and bot unpair
// from changing the local device under a running fleet.
func TestFleetLooksRunning(t *testing.T) {
	if fleetLooksRunning(&config.Config{}) {
		t.Fatal("a port of zero turns the API off, so nothing can answer")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if !fleetLooksRunning(&config.Config{Address: "127.0.0.1", Port: port}) {
		t.Fatal("a listening address must report a running fleet")
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if fleetLooksRunning(&config.Config{Address: "127.0.0.1", Port: port}) {
		t.Fatal("a closed port must not report a running fleet")
	}
}
