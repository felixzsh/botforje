package main

import (
	"context"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/adapters/testchannel"
	whatsappchannel "github.com/felixzsh/botflota/adapters/whatsapp/channel"
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

// TestPureCatalogInstallsTestchannel proves one catalog entry onboards the
// adapter for every config-only command (ADR 0052, ADR 0054).
func TestPureCatalogInstallsTestchannel(t *testing.T) {
	catalog, err := pureCatalog()
	if err != nil {
		t.Fatalf("pureCatalog: %v", err)
	}
	def, key, err := catalog.Resolve(spec.Channel{
		Provider: testchannel.Provider, Mode: testchannel.Mode,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if key.Provider != testchannel.Provider || key.Mode != testchannel.Mode {
		t.Fatalf("resolved %+v", key)
	}
	if !def.Capabilities.SupportsSend(botflota.KindText) {
		t.Fatal("the registered definition does not declare text")
	}
	if def.Build != nil || def.Setup != nil {
		t.Fatal("the pure catalog must carry no runtime hooks")
	}
}

// TestConfigValidationUsesTheRegisteredTestchannel proves the catalog's own
// decoder validates the adapter's options, through real config validation.
func TestConfigValidationUsesTheRegisteredTestchannel(t *testing.T) {
	catalog, err := pureCatalog()
	if err != nil {
		t.Fatalf("pureCatalog: %v", err)
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

func testchannelConfig(t *testing.T, options string) *config.Config {
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

// TestRuntimeCatalogBindsTestchannel proves the runtime entry supplies the
// adapter's builder, readiness, and setup without a WhatsApp device store.
func TestRuntimeCatalogBindsTestchannel(t *testing.T) {
	catalog, err := runtimeCatalog(whatsappchannel.Deps{})
	if err != nil {
		t.Fatalf("runtimeCatalog: %v", err)
	}
	def, _, err := catalog.Resolve(spec.Channel{
		Provider: testchannel.Provider, Mode: testchannel.Mode,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if def.Build == nil || def.Prepare == nil || def.Setup == nil {
		t.Fatal("the runtime definition is missing an assembly hook")
	}
}

// TestOfflineAssemblyServesTestchannel proves the adapter's offline bind entry
// resolves its setup runner through the installed registry, never a
// provider-specific command (ADR 0053).
func TestOfflineAssemblyServesTestchannel(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "botflota.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	cfg := &config.Config{}
	botSpec := testchannelBot(t, "    namespace: profile-a\n")
	catalog, cleanup, err := offlineChannelCatalog(
		cfg, db, botSpec, botflota.DiscardLogger(), installedOfflineAssemblies())
	if err != nil {
		t.Fatalf("offlineChannelCatalog: %v", err)
	}
	defer cleanup()
	_, runner, err := resolveOfflineSetup(
		catalog, "dev", "bot", botSpec, botflota.DiscardLogger())
	if err != nil {
		t.Fatalf("resolveOfflineSetup: %v", err)
	}
	methods, err := runner.SetupMethods(context.Background(), "dev", "bot")
	if err != nil {
		t.Fatalf("SetupMethods: %v", err)
	}
	if len(methods) != 1 || methods[0] != testchannel.SetupMethodCredential {
		t.Fatalf("methods = %v", methods)
	}
}
