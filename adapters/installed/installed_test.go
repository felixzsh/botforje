package installed_test

import (
	"context"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota/adapters/installed"
	"github.com/felixzsh/botflota/adapters/testchannel"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// testchannelBot is one bot on the in-memory adapter, with the authored
// options a config file would carry.
func testchannelBot(t *testing.T, options string) spec.Bot {
	t.Helper()
	var selection spec.Channel
	document := "provider: " + testchannel.Provider +
		"\nmode: " + testchannel.Mode + "\n"
	if options != "" {
		document += "options:\n" + options
	}
	if err := yaml.Unmarshal([]byte(document), &selection); err != nil {
		t.Fatalf("parse channel: %v", err)
	}
	return spec.Bot{Graph: "main", Channel: selection}
}

func testchannelConfig(t *testing.T, options string) *config.Config {
	t.Helper()
	return &config.Config{Scopes: map[string]spec.Scope{"dev": {
		Actions: map[string]spec.Action{"reply": {
			Steps: []spec.Step{{Message: &spec.Message{Body: spec.Text{"hi"}}}},
		}},
		Graphs: map[string]spec.Graph{"main": {
			Root:  "root",
			Nodes: map[string]spec.Node{"root": {OnEnter: []string{"reply"}}},
		}},
		Bots: map[string]spec.Bot{"bot": testchannelBot(t, options)},
	}}}
}

func installationFor(t *testing.T, provider string) channels.Installation {
	t.Helper()
	for _, installation := range installed.Channels() {
		if installation.Definition.Provider == provider {
			return installation
		}
	}
	t.Fatalf("provider %q is not registered", provider)
	return channels.Installation{}
}

func pureDefinitions() []channels.Definition {
	installations := installed.Channels()
	defs := make([]channels.Definition, 0, len(installations))
	for _, installation := range installations {
		defs = append(defs, installation.Definition)
	}
	return defs
}

// TestRegisteredChannelsAreInstalled proves every shipped adapter reaches the
// catalog as a declaration without a runtime hook, so config-only validation
// opens nothing (ADR 0052, ADR 0054).
func TestRegisteredChannelsAreInstalled(t *testing.T) {
	installations := installed.Channels()
	if len(installations) == 0 {
		t.Fatal("no adapter is registered")
	}
	catalog, err := channels.New(pureDefinitions()...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, installation := range installations {
		def := installation.Definition
		if def.Provider == "" || len(def.Modes) == 0 {
			t.Fatalf("installation without a provider or mode: %+v", def)
		}
		for _, mode := range def.Modes {
			resolved, _, err := catalog.Resolve(
				spec.Channel{Provider: def.Provider, Mode: mode})
			if err != nil {
				t.Fatalf("Resolve %s/%s: %v", def.Provider, mode, err)
			}
			if resolved.Build != nil || resolved.Prepare != nil ||
				resolved.Setup != nil {
				t.Fatalf("provider %q: the installed declaration carries a hook",
					def.Provider)
			}
		}
	}
}

// TestTestchannelInstallationBindsItsRuntime proves the adapter's own
// registration entry supplies its builder, readiness, and setup, so the
// composition root needs no adapter knowledge (ADR 0052, ADR 0053).
func TestTestchannelInstallationBindsItsRuntime(t *testing.T) {
	installation := installationFor(t, testchannel.Provider)
	if installation.Definition.Build != nil {
		t.Fatal("the registered declaration carries a runtime hook")
	}
	runtime, err := installation.Install(context.Background(), channels.Host{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if runtime.Definition.Build == nil || runtime.Definition.Prepare == nil ||
		runtime.Definition.Setup == nil {
		t.Fatal("the runtime definition is missing an assembly hook")
	}
	if runtime.Validate != nil {
		t.Fatal("a stateless adapter reports a durable-state check it does not need")
	}
}

// TestConfigValidationUsesTheRegisteredTestchannel proves the catalog's own
// decoder validates the adapter's options, through real config validation
// (ADR 0052, ADR 0054).
func TestConfigValidationUsesTheRegisteredTestchannel(t *testing.T) {
	catalog, err := channels.New(pureDefinitions()...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	valid := testchannelConfig(t, "    namespace: profile-a\n")
	if errs := config.Validate(valid, catalog); len(errs) > 0 {
		t.Fatalf("valid config errors: %v", errs)
	}
	invalid := testchannelConfig(t, "    typo: x\n")
	if errs := config.Validate(invalid, catalog); len(errs) == 0 {
		t.Fatal("an unknown option passed validation")
	}
}
