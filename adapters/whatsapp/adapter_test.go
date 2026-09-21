package whatsapp

import (
	"context"
	"log/slog"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
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
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
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
