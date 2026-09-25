package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/felixzsh/botflota/config"
)

// fakeApplier adopts scopes the way the fleet does, and can be told to hold a
// scope back the way an active session does.
type fakeApplier struct {
	applied []string
	held    map[string][]string
	err     error
	calls   int
}

func (f *fakeApplier) ApplyConfig(
	_ context.Context, cfg *config.Config, scopes []string, source *config.Source,
) ([]string, map[string][]string, error) {
	f.calls++
	if f.err != nil {
		return nil, nil, f.err
	}
	adopted := make([]string, 0, len(scopes))
	pending := map[string][]string{}
	for _, scope := range scopes {
		if reasons, ok := f.held[scope]; ok {
			pending[scope] = reasons
			continue
		}
		adopted = append(adopted, scope)
	}
	f.applied = append(f.applied, adopted...)
	if source != nil {
		source.Replace(config.WithScopes(source.Current(), cfg, adopted))
	}
	return adopted, pending, nil
}

func newAdoptingServer(t *testing.T) (publishServer, *fakeApplier) {
	t.Helper()
	applier := &fakeApplier{}
	server := testServer(t)
	server.applier = applier
	main := server.writer.MainPath()
	return publishServer{Server: server, main: main}, applier
}

func TestPublishBecomesLiveInTheSameRequest(t *testing.T) {
	server, applier := newAdoptingServer(t)
	current := savedScope(t, server.main, "support")
	version, _ := config.ScopeVersion(current)

	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": version,
		"config":  relabelled(t, current, "Support desk"),
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	results := decodeWriteResults(t, recorder)
	if results.Scopes[0].Status != "active" {
		t.Fatalf("status = %q, want active", results.Scopes[0].Status)
	}
	if results.Scopes[0].SavedVersion != results.Scopes[0].ActiveVersion {
		t.Fatal("an adopted scope reported two versions")
	}
	if len(applier.applied) != 1 || applier.applied[0] != "support" {
		t.Fatalf("adopted = %v, want the published scope", applier.applied)
	}
	// A follow-up read sees the change as active, not pending.
	after := readScopeConfig(t, server.Server,
		"/api/v1/scopes/support/config", testOwnerToken)
	if after.Status != "active" {
		t.Fatalf("read status = %q, want active", after.Status)
	}
	if after.Version != results.Scopes[0].SavedVersion {
		t.Fatalf("read version = %q, want %q", after.Version, results.Scopes[0].SavedVersion)
	}
}

func TestPublishReportsAScopeTheFleetCouldNotAdopt(t *testing.T) {
	server, applier := newAdoptingServer(t)
	applier.held = map[string][]string{
		"support": {"graph topology of bot \"demo\" has active sessions"},
	}
	current := savedScope(t, server.main, "support")
	version, _ := config.ScopeVersion(current)

	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": version,
		"config":  relabelled(t, current, "Support desk"),
	})
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", recorder.Code, recorder.Body.String())
	}
	results := decodeWriteResults(t, recorder)
	if results.Scopes[0].Status != "pending" {
		t.Fatalf("status = %q, want pending", results.Scopes[0].Status)
	}
	// Saved on disk even though the fleet could not take it yet.
	if got := savedScope(t, server.main, "support").DisplayName; got != "Support desk" {
		t.Fatalf("display_name = %q, want the change saved", got)
	}
	// The read side says pending too, and keeps the saved document.
	after := readScopeConfig(t, server.Server,
		"/api/v1/scopes/support/config", testOwnerToken)
	if after.Status != "pending" {
		t.Fatalf("read status = %q, want pending", after.Status)
	}
	if after.field(t, "display_name") != "Support desk" {
		t.Fatal("the read side did not serve the saved document")
	}
}

func TestPublishSurvivesAFleetThatCannotAdopt(t *testing.T) {
	server, applier := newAdoptingServer(t)
	applier.err = context.DeadlineExceeded
	current := savedScope(t, server.main, "support")
	version, _ := config.ScopeVersion(current)

	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": version,
		"config":  relabelled(t, current, "Support desk"),
	})
	// The change is saved; the watcher will adopt it on a later poll.
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", recorder.Code, recorder.Body.String())
	}
	if got := savedScope(t, server.main, "support").DisplayName; got != "Support desk" {
		t.Fatalf("display_name = %q, want the change saved", got)
	}
}

func TestPublishWithoutAnApplierLeavesTheChangeSaved(t *testing.T) {
	server := newPublishServer(t)
	current := savedScope(t, server.main, "support")
	version, _ := config.ScopeVersion(current)

	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": version,
		"config":  relabelled(t, current, "Support desk"),
	})
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", recorder.Code, recorder.Body.String())
	}
	results := decodeWriteResults(t, recorder)
	if results.Scopes[0].Status != "pending" {
		t.Fatalf("status = %q, want pending without an applier", results.Scopes[0].Status)
	}
	if got := savedScope(t, server.main, "support").DisplayName; got != "Support desk" {
		t.Fatalf("display_name = %q, want the change saved", got)
	}
}

func TestPublishAdoptsOnlyTheScopesItChanged(t *testing.T) {
	server, applier := newAdoptingServer(t)
	support := savedScope(t, server.main, "support")
	ventas := savedScope(t, server.main, "ventas")
	supportVersion, _ := config.ScopeVersion(support)
	ventasVersion, _ := config.ScopeVersion(ventas)

	recorder := publishScopes(t, server.Server, testOwnerToken, map[string]any{
		"scopes": map[string]any{
			"support": map[string]any{
				"version": supportVersion,
				"config":  relabelled(t, support, "Support desk"),
			},
			"ventas": map[string]any{
				"version": ventasVersion,
				"config":  relabelled(t, ventas, "Sales"),
			},
		},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	results := decodeWriteResults(t, recorder)
	if len(results.Scopes) != 2 {
		t.Fatalf("results = %+v, want both scopes", results.Scopes)
	}
	for _, result := range results.Scopes {
		if result.Status != "active" {
			t.Fatalf("scope %q status = %q, want active", result.Scope, result.Status)
		}
	}
	if len(applier.applied) != 2 {
		t.Fatalf("adopted = %v, want both scopes", applier.applied)
	}
}

func TestPublishOfAnIdenticalScopeAdoptsNothing(t *testing.T) {
	server, applier := newAdoptingServer(t)
	current := savedScope(t, server.main, "support")
	version, _ := config.ScopeVersion(current)

	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": version, "config": current,
	})
	if recorder.Code != http.StatusOK && recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	results := decodeWriteResults(t, recorder)
	// Nothing was written, so the running scope is already what was saved.
	if results.Scopes[0].Status != "active" {
		t.Fatalf("status = %q, want active: nothing changed to adopt",
			results.Scopes[0].Status)
	}
	if applier.calls != 0 {
		t.Fatalf("applier called %d times, want none for an unchanged scope",
			applier.calls)
	}
}
