package channel

import (
	"context"
	"log/slog"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp"
)

func TestJitteredBounds(t *testing.T) {
	a := &Adapter{
		settings: Settings{Jitter: 100 * time.Millisecond},
		rnd:      rand.New(rand.NewSource(1)),
	}
	for i := 0; i < 50; i++ {
		got := a.jittered(50 * time.Millisecond)
		if got < 50*time.Millisecond || got > 150*time.Millisecond {
			t.Fatalf("jittered = %s, want within [50ms, 150ms]", got)
		}
	}
	a.settings.Jitter = 0
	if got := a.jittered(50 * time.Millisecond); got != 50*time.Millisecond {
		t.Fatalf("jitter 0 = %s, want the base 50ms", got)
	}
}

func TestConnectReportsMissingDevice(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.LinkedAccount{
		Scope: "dev", Bot: "botsito", JID: "5215500000000:1@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := whatsapp.OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	a := &Adapter{
		scope: "dev", bot: "botsito", accounts: db.LinkedAccounts(),
		pairings: db.Pairings(), devices: devices,
		log: botflota.DiscardLogger(),
	}
	err = a.Connect(ctx)
	if err == nil || !strings.Contains(err.Error(), "no local device credentials") {
		t.Fatalf("Connect error = %v, want missing-device error", err)
	}
}

// TestDisconnectStopsWorkersAfterConnectFails covers the adapter lifetime:
// New starts its workers before Connect, so Disconnect must stop them even
// when the first connection cannot start.
func TestDisconnectStopsWorkersAfterConnectFails(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.LinkedAccount{
		Scope: "dev", Bot: "botsito", JID: "5215500000000:1@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := whatsapp.OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(Options{
		Scope: account.Scope, Bot: account.Bot,
		Devices: devices, Accounts: db.LinkedAccounts(),
		Pairings: db.Pairings(), Senders: db.Senders(), Chats: db.Chats(),
		Messages: db.Messages(), History: true, Log: botflota.DiscardLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Disconnect(ctx) })
	if err := adapter.Connect(ctx); err == nil {
		t.Fatal("Connect succeeded without device credentials")
	}
	if err := adapter.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect after failed Connect: %v", err)
	}
	select {
	case <-adapter.stopped:
	default:
		t.Fatal("adapter worker is still running after Disconnect")
	}
	select {
	case <-adapter.historyDone:
	default:
		t.Fatal("history worker is still running after Disconnect")
	}
}
