package channel

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// TestInstallationBindsTheAdapterToItsOwnResources proves the registration
// entry opens the device store and setup runner, returns the runtime hooks,
// and reports the durable state the adapter needs (ADR 0052, ADR 0053).
func TestInstallationBindsTheAdapterToItsOwnResources(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "botflota.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	installation := Installation()
	if installation.Definition.Build != nil || installation.Definition.Setup != nil {
		t.Fatal("the registered declaration carries a runtime hook")
	}
	bot := spec.Bot{Channel: spec.Channel{Provider: Provider, Mode: Mode}}
	host := channels.Host{
		DB:        db.DB,
		Bindings:  db.Bindings(),
		Peers:     db.Peers(),
		Chats:     db.Chats(),
		Messages:  db.Messages(),
		Directory: db.Directory(),
		Outbox:    db.Outbox(),
		Log:       botflota.DiscardLogger(),
		Bot: func(scope, name string) (spec.Bot, error) {
			return bot, nil
		},
	}
	runtime, err := installation.Install(context.Background(), host)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	defer runtime.Close()
	if runtime.Definition.Build == nil || runtime.Definition.Prepare == nil ||
		runtime.Definition.Setup == nil {
		t.Fatal("the runtime definition is missing an assembly hook")
	}
	if runtime.Close == nil {
		t.Fatal("the runtime resources have no close")
	}
	if err := runtime.Validate(context.Background()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	value, reasons := Definition().Decode(bot)
	if len(reasons) > 0 {
		t.Fatalf("decode: %v", reasons)
	}
	_, runner, err := runtime.Definition.Setup(
		channels.Bind{Scope: "dev", Bot: "alpha", Options: value})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	methods, err := runner.SetupMethods(context.Background(), "dev", "alpha")
	if err != nil {
		t.Fatalf("SetupMethods: %v", err)
	}
	if len(methods) != 1 || methods[0] != whatsapp.SetupMethodQR {
		t.Fatalf("methods = %v, want the adapter's own", methods)
	}
}
