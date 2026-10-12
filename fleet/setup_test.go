package fleet

// The P3 assembly gate (ADR 0053): inspection, bind, and unbind resolve
// through the bot's installed definition, never by scanning every bot
// through one device store.

import (
	"context"
	"errors"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
)

type stubLifecycle struct {
	status botflota.BindingStatus
	err    error
	calls  *[]string
}

func (s stubLifecycle) InspectBinding(
	_ context.Context, scope, bot string,
) (botflota.BindingStatus, error) {
	*s.calls = append(*s.calls, "inspect:"+scope+"/"+bot)
	return s.status, s.err
}

func (s stubLifecycle) DetachBinding(
	_ context.Context, scope, bot string,
) error {
	*s.calls = append(*s.calls, "detach:"+scope+"/"+bot)
	return s.err
}

type stubRunner struct {
	methods []string
	err     error
}

func (s stubRunner) SetupMethods(
	_ context.Context, _, _ string,
) ([]string, error) {
	return s.methods, s.err
}

func (s stubRunner) BeginSetup(
	_ context.Context, _, _ string, _ botflota.SetupBegin,
) (*botflota.SetupSession, error) {
	return nil, s.err
}

func (s stubRunner) ReadSetup(
	_ context.Context, _, _, _ string,
) (*botflota.SetupSession, error) {
	return nil, s.err
}

func (s stubRunner) InputSetup(
	_ context.Context, _, _, _ string, _ botflota.SetupInput,
) (*botflota.SetupSession, error) {
	return nil, s.err
}

func (s stubRunner) CancelSetup(
	_ context.Context, _, _, _ string,
) error {
	return s.err
}

func setupCatalog(
	setup func(channels.Bind) (botflota.AccountLifecycle, botflota.SetupRunner, error),
) *channels.Catalog {
	def := channels.Definition{
		Provider: "whatsapp",
		Kind:     "fake",
		Modes:    []string{"native"},
		Capabilities: channels.Capabilities{Send: []botflota.MessageKind{
			botflota.KindText,
		}},
		Build: func(channels.Bind) (botflota.MessageChannel, error) {
			return newFakeChannel(), nil
		},
		Setup: setup,
	}
	catalog, err := channels.New(def)
	if err != nil {
		panic(err)
	}
	return catalog
}

func buildSetupFleet(
	t *testing.T, catalog *channels.Catalog,
) *Fleet {
	t.Helper()
	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	store := newStore()
	runner, err := New(cfg, Options{
		Store: store, Chats: store, Peers: store, Messages: store,
		Turn: store, Outbox: store,
		Catalog: catalog,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner
}

// TestSetupForSelectsTheBotsDefinition: each bot's lifecycle endpoints come
// from its own installed definition; an unknown bot and a definition
// without setup are errors, never silent fallbacks.
func TestSetupForSelectsTheBotsDefinition(t *testing.T) {
	var calls []string
	want := botflota.BindingStatus{Condition: botflota.ReadinessConfigured}
	runner := buildSetupFleet(t, setupCatalog(
		func(bind channels.Bind) (botflota.AccountLifecycle, botflota.SetupRunner, error) {
			if bind.Scope != "dev" {
				return nil, nil, errors.New("unexpected scope")
			}
			lifecycle := stubLifecycle{status: want, calls: &calls}
			return lifecycle, stubRunner{methods: []string{"qr"}}, nil
		}))

	lifecycle, setup, err := runner.SetupFor("dev", "alpha")
	if err != nil {
		t.Fatalf("SetupFor: %v", err)
	}
	status, err := lifecycle.InspectBinding(context.Background(), "dev", "alpha")
	if err != nil {
		t.Fatalf("InspectBinding: %v", err)
	}
	if status.Condition != botflota.ReadinessConfigured {
		t.Fatalf("status = %#v, want the definition's answer", status)
	}
	methods, err := setup.SetupMethods(context.Background(), "dev", "alpha")
	if err != nil {
		t.Fatalf("SetupMethods: %v", err)
	}
	if len(methods) != 1 || methods[0] != "qr" {
		t.Fatalf("methods = %v, want the definition's methods", methods)
	}
	if len(calls) != 1 || calls[0] != "inspect:dev/alpha" {
		t.Fatalf("calls = %v, want the inspection scoped to its bot", calls)
	}
	if err := lifecycle.(stubLifecycle).DetachBinding(
		context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("DetachBinding: %v", err)
	}

	if _, _, err := runner.SetupFor("dev", "nobody"); err == nil {
		t.Fatal("SetupFor accepted an unknown bot")
	}
	if _, _, err := runner.SetupFor("elsewhere", "alpha"); err == nil {
		t.Fatal("SetupFor accepted an unknown scope")
	}
}

// TestSetupForWithoutHook: a definition that offers no account setup is an
// explicit error, never an invented permission.
func TestSetupForWithoutHook(t *testing.T) {
	def := channels.Definition{
		Provider: "whatsapp",
		Kind:     "fake",
		Modes:    []string{"native"},
		Build: func(channels.Bind) (botflota.MessageChannel, error) {
			return newFakeChannel(), nil
		},
	}
	catalog, err := channels.New(def)
	if err != nil {
		t.Fatal(err)
	}
	runner := buildSetupFleet(t, catalog)
	if _, _, err := runner.SetupFor("dev", "alpha"); err == nil {
		t.Fatal("SetupFor accepted a definition without setup")
	}
}

// TestSetupForPropagatesHookErrors: a definition that cannot assemble its
// endpoints fails the lookup instead of handing out a nil runner.
func TestSetupForPropagatesHookErrors(t *testing.T) {
	boom := errors.New("no device store")
	runner := buildSetupFleet(t, setupCatalog(
		func(channels.Bind) (botflota.AccountLifecycle, botflota.SetupRunner, error) {
			return nil, nil, boom
		}))
	if _, _, err := runner.SetupFor("dev", "alpha"); !errors.Is(err, boom) {
		t.Fatalf("SetupFor = %v, want the hook error", err)
	}
}
