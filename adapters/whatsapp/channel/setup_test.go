package channel

// The P3 adapter-assembly gate: one WhatsApp installation exposes its
// neutral account lifecycle and setup runner through the bot's definition,
// with no invented state when a dependency is missing.

import (
	"context"
	"log/slog"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/channels"
)

func testSetupDeps(t *testing.T) Deps {
	t.Helper()
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	devices, err := whatsapp.OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	manager := whatsapp.NewPairManager(whatsapp.PairManagerOptions{
		Build: func(string, string, bool) (whatsapp.PairOptions, error) {
			return whatsapp.PairOptions{}, nil
		},
	})
	if manager == nil {
		t.Fatal("NewPairManager returned nil")
	}
	// Close cancels any session a test starts and waits for its goroutine, so
	// no pairing worker outlives the test that built the manager.
	t.Cleanup(manager.Close)
	return Deps{
		Devices: devices, Bindings: db.Bindings(),
		Peers: db.Peers(), Chats: db.Chats(), Messages: db.Messages(),
		Directory: db.Directory(), Outbox: db.Outbox(), Pairs: manager,
	}
}

func TestDefinitionDeclaresSetupMethods(t *testing.T) {
	def := Definition()
	if len(def.SetupMethods) != 2 || def.SetupMethods[0] != whatsapp.SetupMethodQR ||
		def.SetupMethods[1] != whatsapp.SetupMethodCode {
		t.Fatalf("SetupMethods = %v, want the qr and code methods", def.SetupMethods)
	}
	if def.Setup != nil {
		t.Fatal("the pure definition must not bind a setup runner without deps")
	}
}

func TestSetupReturnsLifecycleAndRunner(t *testing.T) {
	deps := testSetupDeps(t)
	hook := Setup(deps)
	lifecycle, runner, err := hook(channels.Bind{Scope: "dev", Bot: "alpha"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if lifecycle == nil || runner == nil {
		t.Fatal("Setup returned a nil endpoint")
	}
	status, err := lifecycle.InspectBinding(context.Background(), "dev", "alpha")
	if err != nil {
		t.Fatalf("InspectBinding: %v", err)
	}
	if status.Condition != botflota.ReadinessUnconfigured {
		t.Fatalf("status = %#v, want the unconfigured bot inspectable", status)
	}
	methods, err := runner.SetupMethods(context.Background(), "dev", "alpha")
	if err != nil {
		t.Fatalf("SetupMethods: %v", err)
	}
	if len(methods) != 2 || methods[0] != whatsapp.SetupMethodQR ||
		methods[1] != whatsapp.SetupMethodCode {
		t.Fatalf("methods = %v, want the qr and code methods", methods)
	}
}

func TestSetupRefusesMissingDependencies(t *testing.T) {
	full := testSetupDeps(t)
	cases := map[string]Deps{
		"no device store": func() Deps { deps := full; deps.Devices = nil; return deps }(),
		"no bindings":     func() Deps { deps := full; deps.Bindings = nil; return deps }(),
		"no outbox":       func() Deps { deps := full; deps.Outbox = nil; return deps }(),
		"no runner":       func() Deps { deps := full; deps.Pairs = nil; return deps }(),
	}
	for name, deps := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Setup(deps)(channels.Bind{Scope: "dev", Bot: "alpha"}); err == nil {
				t.Fatal("Setup assembled endpoints without its dependencies")
			}
		})
	}
}
