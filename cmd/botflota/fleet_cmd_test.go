package main

import (
	"errors"
	"testing"
	"time"

	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/instance"
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

// TestLockInstance covers the guard that keeps a local write command out of a
// running fleet: ownership comes from the lock, never from the API port
// (ADR 0039).
func TestLockInstance(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	lock, err := lockInstance(cfg)
	if err != nil {
		t.Fatalf("lockInstance: %v", err)
	}
	if _, err := lockInstance(cfg); !errors.Is(err, instance.ErrOwned) {
		t.Fatalf("second lock = %v, want instance.ErrOwned", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	lock, err = lockInstance(cfg)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}
