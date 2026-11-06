package channel

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/egress"
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
	want := []string{whatsapp.SetupMethodQR, whatsapp.SetupMethodCode}
	if len(methods) != len(want) || methods[0] != want[0] || methods[1] != want[1] {
		t.Fatalf("methods = %v, want the adapter's own", methods)
	}
}

// TestInstallationCarriesTheInstanceOutboundClient proves the installed adapter
// fetches media through the client the instance's egress policy manages, not
// through the standard client (ADR 0048).
func TestInstallationCarriesTheInstanceOutboundClient(t *testing.T) {
	client, err := egress.Client(egress.PublicOnly)
	if err != nil {
		t.Fatalf("egress client: %v", err)
	}
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "botflota.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	bot := spec.Bot{Channel: spec.Channel{Provider: Provider, Mode: Mode}}
	host := channels.Host{
		DB:         db.DB,
		Bindings:   db.Bindings(),
		Peers:      db.Peers(),
		Chats:      db.Chats(),
		Messages:   db.Messages(),
		Directory:  db.Directory(),
		Outbox:     db.Outbox(),
		Log:        botflota.DiscardLogger(),
		HTTPClient: client,
		Bot: func(scope, name string) (spec.Bot, error) {
			return bot, nil
		},
	}
	runtime, err := Installation().Install(context.Background(), host)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	defer runtime.Close()
	value, reasons := Definition().Decode(bot)
	if len(reasons) > 0 {
		t.Fatalf("decode: %v", reasons)
	}
	built, err := runtime.Definition.Build(channels.Bind{
		Scope: "dev", Bot: "alpha", Options: value,
		Log: botflota.DiscardLogger(),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	adapter, ok := built.(*Adapter)
	if !ok {
		t.Fatalf("built %T, want the adapter", built)
	}
	if adapter.http != client {
		t.Fatal("the adapter does not fetch through the instance client")
	}
}
