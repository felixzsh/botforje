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
	account := &botflota.Binding{
		Scope: "dev", Bot: "botsito", Provider: "whatsapp", Identity: waKey("5215500000000"),
	}
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := whatsapp.OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	a := &Adapter{
		scope: "dev", bot: "botsito", bindings: db.Bindings(),
		devices: devices,
		log:     botflota.DiscardLogger(),
	}
	err = a.Connect(ctx)
	if err == nil || !strings.Contains(err.Error(), "no local device credentials") {
		t.Fatalf("Connect error = %v, want missing-device error", err)
	}
}

// TestOptionalWorkersStartOnlyWhenDeclared: assembling a channel starts the
// history and address-book workers only for an option the bot selected, so no
// optional feature is invoked unconditionally (ADR 0035, ADR 0037, ADR 0052).
func TestOptionalWorkersStartOnlyWhenDeclared(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	devices, err := whatsapp.OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		history  bool
		contacts bool
	}{
		{"neither option", false, false},
		{"history only", true, false},
		{"address book only", false, true},
		{"both options", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, err := New(Options{
				Scope: "dev", Bot: "botsito",
				Devices: devices, Bindings: db.Bindings(),
				Peers: db.Peers(), Chats: db.Chats(),
				Messages: db.Messages(), Directory: db.Directory(),
				History: test.history, ImportContacts: test.contacts,
				Log: botflota.DiscardLogger(),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if (adapter.importer != nil) != test.history {
				t.Fatalf("importer present = %v, want History=%v",
					adapter.importer != nil, test.history)
			}
			if (adapter.historyDone != nil) != test.history {
				t.Fatalf("history worker present = %v, want History=%v",
					adapter.historyDone != nil, test.history)
			}
			if (adapter.addressBook != nil) != test.contacts {
				t.Fatalf("address book present = %v, want ImportContacts=%v",
					adapter.addressBook != nil, test.contacts)
			}
			if err := adapter.Disconnect(ctx); err != nil {
				t.Fatalf("Disconnect: %v", err)
			}
		})
	}
}

// TestDisconnectStopsWorkersAfterConnectFails covers the adapter lifetime:
// New starts its workers before Connect, so Disconnect must stop them even
// when the first connection cannot start.
func TestDisconnectStopsWorkersAfterConnectFails(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.Binding{
		Scope: "dev", Bot: "botsito", Provider: "whatsapp", Identity: waKey("5215500000000"),
	}
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := whatsapp.OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(Options{
		Scope: account.Scope, Bot: account.Bot,
		Devices: devices, Bindings: db.Bindings(),
		Peers: db.Peers(), Chats: db.Chats(),
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
