package main

import (
	"context"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// TestOfflineChannelCatalogSelectsTheBotChannel proves the offline path opens
// only the adapter the bot selects and yields its neutral setup runner
// (ADR 0053, ADR 0054).
func TestOfflineChannelCatalogSelectsTheBotChannel(t *testing.T) {
	db := testDB(t)
	runner := &stubOfflineRunner{session: &botflota.SetupSession{
		Bot:      botflota.BotRef{Scope: "dev", Bot: "alpha"},
		ID:       "s1",
		State:    botflota.SetupSucceeded,
		Revision: 1,
	}}
	chosen := &fakeAdapter{provider: "fake", mode: "test", setup: runner}
	other := &fakeAdapter{provider: "other", mode: "remote"}
	cfg := &config.Config{}
	botSpec := spec.Bot{Channel: spec.Channel{Provider: "fake", Mode: "test"}}
	catalog, runtimes, err := offlineChannelCatalog(context.Background(), cfg, db,
		botSpec, botflota.DiscardLogger(),
		[]channels.Installation{chosen.installation(), other.installation()})
	if err != nil {
		t.Fatalf("offlineChannelCatalog: %v", err)
	}
	defer runtimes.Close()
	if chosen.opened != 1 || other.opened != 0 {
		t.Fatalf("installs = %d/%d, want the selected adapter only",
			chosen.opened, other.opened)
	}
	_, gotRunner, err := resolveOfflineSetup(
		catalog, "dev", "alpha", botSpec, botflota.DiscardLogger())
	if err != nil {
		t.Fatalf("resolveOfflineSetup: %v", err)
	}
	methods, err := gotRunner.SetupMethods(context.Background(), "dev", "alpha")
	if err != nil {
		t.Fatalf("SetupMethods: %v", err)
	}
	if len(methods) != 1 || methods[0] != "token" {
		t.Fatalf("methods = %v, want the fake runner's", methods)
	}
}

// TestOfflineChannelCatalogDoesNotOpenAnUninstalledChannel proves an
// uninstalled selection opens nothing and still reports the missing entry
// (ADR 0054).
func TestOfflineChannelCatalogDoesNotOpenAnUninstalledChannel(t *testing.T) {
	db := testDB(t)
	adapter := &fakeAdapter{}
	cfg := &config.Config{}
	botSpec := spec.Bot{Channel: spec.Channel{Provider: "ghost", Mode: "remote"}}
	catalog, runtimes, err := offlineChannelCatalog(context.Background(), cfg, db,
		botSpec, botflota.DiscardLogger(), []channels.Installation{adapter.installation()})
	if err != nil {
		t.Fatalf("offlineChannelCatalog: %v", err)
	}
	defer runtimes.Close()
	if adapter.opened != 0 {
		t.Fatalf("installs = %d, want 0", adapter.opened)
	}
	if _, _, err := catalog.Resolve(botSpec.Channel); err == nil {
		t.Fatal("an uninstalled selection resolved")
	}
}
