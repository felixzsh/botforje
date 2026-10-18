package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
)

// fakePauser records the pauses and resumes a history operation performs, and
// can refuse one so the failure path is covered.
type fakePauser struct {
	stopped   []string
	discarded []string
	started   []string
	err       error
}

func (f *fakePauser) StopBot(scope, bot string) error {
	if f.err != nil {
		return f.err
	}
	f.stopped = append(f.stopped, scope+"/"+bot)
	return nil
}

func (f *fakePauser) StopBotForUnpair(scope, bot string) error {
	if f.err != nil {
		return f.err
	}
	f.discarded = append(f.discarded, scope+"/"+bot)
	return nil
}

func (f *fakePauser) StartBot(scope, bot string) error {
	f.started = append(f.started, scope+"/"+bot)
	return nil
}

// cloneBot gives a scope a second bot with the first one's definition, so a
// migration test has a real destination.
func cloneBot(t *testing.T, server *Server, scope, from, to string) {
	t.Helper()
	bots := server.config().Scopes[scope].Bots
	bots[to] = bots[from]
}

func historyOp(
	t *testing.T, server *Server, method, path, token string, body any,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(encoded))
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func deleteHistory(
	t *testing.T, server *Server, scope, token, confirm string,
) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/scopes/" + scope + "/bots/demo/history"
	if confirm != "" {
		path += "?confirm=" + confirm
	}
	return historyOp(t, server, http.MethodDelete, path, token, nil)
}

func migrateHistory(
	t *testing.T, server *Server, scope, token, to string,
) *httptest.ResponseRecorder {
	t.Helper()
	return historyOp(t, server, http.MethodPost,
		"/api/v1/scopes/"+scope+"/bots/demo/history/migrations", token,
		map[string]any{"to": to})
}

func TestHistoryDeleteNeedsConfirmation(t *testing.T) {
	history := &fakeHistory{}
	server := newHistoryServer(t, history)

	t.Run("a missing confirmation is refused", func(t *testing.T) {
		recorder := deleteHistory(t, server, "support", testOwnerToken, "")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
		}
		if len(history.deleted) != 0 {
			t.Fatal("a refused deletion ran anyway")
		}
	})
	t.Run("a confirmation for another bot is refused", func(t *testing.T) {
		recorder := deleteHistory(t, server, "support", testOwnerToken, "support/otro")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
		}
		if len(history.deleted) != 0 {
			t.Fatal("a misdirected deletion ran anyway")
		}
	})
}

func TestHistoryDeleteReportsWhatItRemoved(t *testing.T) {
	history := &fakeHistory{}
	server := newHistoryServer(t, history)
	recorder := deleteHistory(t, server, "support", testOwnerToken, "support/demo")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Scope   string           `json:"scope"`
		Bot     string           `json:"bot"`
		Removed map[string]int64 `json:"removed"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Scope != "support" || body.Bot != "demo" {
		t.Fatalf("body = %+v, want the target", body)
	}
	if body.Removed["chats"] != 2 || body.Removed["directory"] != 4 {
		t.Fatalf("removed = %v, want the counts", body.Removed)
	}
	if len(history.deleted) != 1 || history.deleted[0] != "support/demo" {
		t.Fatalf("deleted = %v", history.deleted)
	}
}

func TestHistoryDeleteAuthority(t *testing.T) {
	server := newHistoryServer(t, &fakeHistory{})
	recorder := deleteHistory(t, server, "ventas", testAccessToken, "ventas/demo")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("another scope status = %d, want 404", recorder.Code)
	}
	recorder = get(t, server.Handler(),
		"/api/v1/scopes/support/bots/nope/history", testOwnerToken)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unknown bot status = %d, want 405 for GET", recorder.Code)
	}
	recorder = historyOp(t, server, http.MethodDelete,
		"/api/v1/scopes/support/bots/nope/history?confirm=support/nope",
		testOwnerToken, nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown bot status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

func TestHistoryMigrateMovesToAnotherBotOfTheScope(t *testing.T) {
	history := &fakeHistory{}
	server := newHistoryServer(t, history)
	cloneBot(t, server, "support", "demo", "replacement")
	server.channels = compatibleCatalog(t, testCompatibleBindings)
	addMigrationBinding(t, server, botflota.Binding{
		Scope: "support", Bot: "replacement",
		Provider: "whatsapp", Adapter: "whatsmeow", Mode: "native",
		Identity: waKey("5215599999999"),
	})

	recorder := migrateHistory(t, server, "support", testOwnerToken, "replacement")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Bot   string           `json:"bot"`
		To    string           `json:"to"`
		Moved map[string]int64 `json:"moved"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Bot != "demo" || body.To != "replacement" {
		t.Fatalf("body = %+v, want the move", body)
	}
	if body.Moved["chats"] != 2 || body.Moved["messages"] != 3 {
		t.Fatalf("moved = %v, want the counts", body.Moved)
	}
	if history.at.IsZero() {
		t.Fatal("the move was not given a time to expire sessions with")
	}
}

func TestHistoryMigrateRefusals(t *testing.T) {
	t.Run("a missing destination is refused", func(t *testing.T) {
		server := newHistoryServer(t, &fakeHistory{})
		recorder := migrateHistory(t, server, "support", testOwnerToken, "")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
		}
		if body := recorder.Body.String(); !strings.Contains(body, "invalid_request") {
			t.Fatalf("body %s does not report invalid_request", body)
		}
	})
	t.Run("the destination must exist in the same scope", func(t *testing.T) {
		server := newHistoryServer(t, &fakeHistory{})
		recorder := migrateHistory(t, server, "support", testOwnerToken, "nope")
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", recorder.Code)
		}
	})
	t.Run("a bot cannot move its history to itself", func(t *testing.T) {
		server := newHistoryServer(t, &fakeHistory{})
		recorder := migrateHistory(t, server, "support", testOwnerToken, "demo")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
		}
		if body := recorder.Body.String(); !strings.Contains(body, "invalid_request") {
			t.Fatalf("body %s does not report invalid_request", body)
		}
	})
	t.Run("a destination that holds history is a conflict", func(t *testing.T) {
		history := &fakeHistory{occupancy: map[string]bool{"support/busy": true}}
		server := newHistoryServer(t, history)
		server.config().Scopes["support"].Bots["busy"] = server.config().Scopes["support"].Bots["demo"]
		server.channels = compatibleCatalog(t, testCompatibleBindings)
		addMigrationBinding(t, server, botflota.Binding{
			Scope: "support", Bot: "busy",
			Provider: "whatsapp", Adapter: "whatsmeow", Mode: "native",
			Identity: waKey("5215599999999"),
		})
		recorder := migrateHistory(t, server, "support", testOwnerToken, "busy")
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
		}
		if body := recorder.Body.String(); !strings.Contains(body, "destination_not_empty") {
			t.Fatalf("body %s does not report destination_not_empty", body)
		}
	})
	t.Run("a destination outside the caller's scope is unknown", func(t *testing.T) {
		server := newHistoryServer(t, &fakeHistory{})
		recorder := migrateHistory(t, server, "ventas", testAccessToken, "replacement")
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", recorder.Code)
		}
	})
}

// compatibleCatalog is the installed set a migration proves itself against:
// fake definitions with the test's own compatibility hook, so the check runs
// against declared adapter spaces rather than a provider switch (ADR 0042,
// ADR 0051). A nil hook build models an adapter with unknown compatibility.
func compatibleCatalog(
	t *testing.T, hook func(botflota.Binding, botflota.Binding) []string,
) *channels.Catalog {
	t.Helper()
	catalog, err := channels.New(channels.Definition{
		Provider:           "whatsapp",
		Kind:               "fake",
		Modes:              []string{"native"},
		CompatibleBindings: hook,
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return catalog
}

// testCompatibleBindings accepts only the same provider and account mode,
// mirroring the WhatsApp rule without importing the adapter.
func testCompatibleBindings(from, to botflota.Binding) []string {
	if from.Provider != to.Provider {
		return []string{"different providers cannot share history"}
	}
	if from.Mode != to.Mode {
		return []string{"different account modes cannot share history"}
	}
	return nil
}

// addMigrationBinding commits one binding to the test server's fake store,
// so a migration test controls exactly which bots hold one.
func addMigrationBinding(t *testing.T, server *Server, binding botflota.Binding) {
	t.Helper()
	accounts, ok := server.bindings.(*fakeAccounts)
	if !ok {
		t.Fatalf("bindings = %T, want *fakeAccounts", server.bindings)
	}
	accounts.accounts[binding.Scope+"/"+binding.Bot] = binding
}

// requireIncompatibleMigration checks the refusal and that no rows moved.
func requireIncompatibleMigration(
	t *testing.T, recorder *httptest.ResponseRecorder, history *fakeHistory,
) {
	t.Helper()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "incompatible_binding") {
		t.Fatalf("body %s does not report incompatible_binding", body)
	}
	if len(history.moved) != 0 {
		t.Fatalf("moved = %v, want no rows moved", history.moved)
	}
}

// TestHistoryMigrateEnforcesBindingCompatibility proves the move is refused
// before any mutation when the two bindings cannot share history: a
// different provider, a different mode, a missing binding on either side, or
// unknown compatibility (ADR 0042, ADR 0051).
func TestHistoryMigrateEnforcesBindingCompatibility(t *testing.T) {
	destination := func(provider, mode string) *botflota.Binding {
		return &botflota.Binding{
			Scope: "support", Bot: "replacement",
			Provider: provider, Adapter: "fake", Mode: mode,
			Identity: botflota.IdentityKey{
				Provider: provider, Subject: "9",
			},
		}
	}
	cases := []struct {
		name string
		// hook builds the installed definition; nil models unknown
		// compatibility, and noCatalog a server without the catalog.
		hook      func(botflota.Binding, botflota.Binding) []string
		noCatalog bool
		// dropSource removes the source's committed binding; a nil
		// destination leaves the destination without one.
		dropSource bool
		dest       *botflota.Binding
	}{
		{
			name: "incompatible provider",
			hook: testCompatibleBindings,
			dest: destination("telegram", "native"),
		},
		{
			name: "incompatible mode",
			hook: testCompatibleBindings,
			dest: destination("whatsapp", "cloud"),
		},
		{
			name: "missing source binding", hook: testCompatibleBindings,
			dropSource: true, dest: destination("whatsapp", "native"),
		},
		{
			name: "missing destination binding",
			hook: testCompatibleBindings,
		},
		{
			name: "unknown compatibility",
			dest: destination("whatsapp", "native"),
		},
		{
			name:      "a server without the catalog cannot prove compatibility",
			noCatalog: true, dest: destination("whatsapp", "native"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			history := &fakeHistory{}
			server := newHistoryServer(t, history)
			cloneBot(t, server, "support", "demo", "replacement")
			if tc.noCatalog {
				if server.channels != nil {
					t.Fatal("the fixture must leave the catalog unset")
				}
			} else {
				server.channels = compatibleCatalog(t, tc.hook)
			}
			if tc.dest != nil {
				addMigrationBinding(t, server, *tc.dest)
			}
			if tc.dropSource {
				accounts, ok := server.bindings.(*fakeAccounts)
				if !ok {
					t.Fatalf("bindings = %T, want *fakeAccounts", server.bindings)
				}
				delete(accounts.accounts, "support/demo")
			}
			recorder := migrateHistory(t, server, "support",
				testOwnerToken, "replacement")
			requireIncompatibleMigration(t, recorder, history)
		})
	}
}

func TestHistoryOperationsPauseAndResumeTheBots(t *testing.T) {
	t.Run("a running bot is stopped and put back", func(t *testing.T) {
		server := newHistoryServer(t, &fakeHistory{})
		pauser := &fakePauser{}
		server.pauser = pauser
		// The fixture's fleet reports support/demo as connected.
		recorder := deleteHistory(t, server, "support", testOwnerToken, "support/demo")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
		if len(pauser.discarded) != 1 || pauser.discarded[0] != "support/demo" {
			t.Fatalf("discarded = %v, want the target stopped with its queue dropped",
				pauser.discarded)
		}
		if len(pauser.started) != 1 || pauser.started[0] != "support/demo" {
			t.Fatalf("started = %v, want the bot put back", pauser.started)
		}
	})

	t.Run("a stopped bot is left alone", func(t *testing.T) {
		server := newHistoryServer(t, &fakeHistory{})
		pauser := &fakePauser{}
		server.pauser = pauser
		server.fleet = &fakeFleet{statuses: nil}
		recorder := deleteHistory(t, server, "support", testOwnerToken, "support/demo")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
		if len(pauser.discarded) != 0 || len(pauser.started) != 0 {
			t.Fatalf("a stopped bot was paused: %v / %v", pauser.discarded, pauser.started)
		}
	})

	t.Run("a migration pauses the source and the destination", func(t *testing.T) {
		server := newHistoryServer(t, &fakeHistory{})
		pauser := &fakePauser{}
		server.pauser = pauser
		server.channels = compatibleCatalog(t, testCompatibleBindings)
		addMigrationBinding(t, server, botflota.Binding{
			Scope: "support", Bot: "replacement",
			Provider: "whatsapp", Adapter: "whatsmeow", Mode: "native",
			Identity: waKey("5215599999999"),
		})
		server.fleet = &fakeFleet{statuses: []botflota.BotStatus{
			{Scope: "support", Bot: "demo", State: "connected"},
			{Scope: "support", Bot: "replacement", State: "connected"},
		}}
		cloneBot(t, server, "support", "demo", "replacement")

		recorder := migrateHistory(t, server, "support", testOwnerToken, "replacement")
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
		if len(pauser.discarded) != 1 || pauser.discarded[0] != "support/demo" {
			t.Fatalf("discarded = %v, want the source dropped", pauser.discarded)
		}
		if len(pauser.stopped) != 1 || pauser.stopped[0] != "support/replacement" {
			t.Fatalf("stopped = %v, want the destination drained", pauser.stopped)
		}
		if len(pauser.started) != 2 {
			t.Fatalf("started = %v, want both put back", pauser.started)
		}
	})

	t.Run("a pause that fails stops the operation", func(t *testing.T) {
		// The work is only sound with the bot stopped, so a bot that cannot be
		// stopped is reported instead of raced (ADR 0042).
		history := &fakeHistory{}
		server := newHistoryServer(t, history)
		server.pauser = &fakePauser{err: errors.New("no channel")}
		recorder := deleteHistory(t, server, "support", testOwnerToken, "support/demo")
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want the operation refused: %s",
				recorder.Code, recorder.Body.String())
		}
		if len(history.deleted) != 0 {
			t.Fatalf("deleted = %v, want no history touched", history.deleted)
		}
	})
}

func TestHistoryOperationsWithoutAStoreSaySo(t *testing.T) {
	server := testServer(t)
	recorder := deleteHistory(t, server, "support", testOwnerToken, "support/demo")
	requireUnsupportedOperation(t, recorder.Code, recorder.Body.String())
	// The destination must be a real bot of the scope, or the request is refused
	// before the missing store is even reached.
	server.config().Scopes["support"].Bots["replacement"] =
		server.config().Scopes["support"].Bots["demo"]
	recorder = migrateHistory(t, server, "support", testOwnerToken, "replacement")
	requireUnsupportedOperation(t, recorder.Code, recorder.Body.String())
}

// TestHistoryOperationsHoldTheBotLock keeps one bot's write operations from
// overlapping: a history request waits for the lock pairing and unbinding take,
// so neither can restart the bot the other is still working on (ADR 0042).
// blockingHistory holds the move open once it starts, so a test can prove
// an overlapping bot operation waits for the migration instead of slipping
// between its compatibility proof and its move.
type blockingHistory struct {
	*fakeHistory
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *blockingHistory) MoveBot(
	ctx context.Context, scope, from, to string, at time.Time,
) (botflota.HistoryCounts, error) {
	f.once.Do(func() { close(f.started) })
	select {
	case <-f.release:
	case <-ctx.Done():
		return botflota.HistoryCounts{}, ctx.Err()
	}
	return f.fakeHistory.MoveBot(ctx, scope, from, to, at)
}

// TestHistoryMigrateSerializesWithDetaches proves the compatibility proof
// and the move hold both bots' locks together: a detach of the source waits
// until the migration commits instead of invalidating the proof mid-move
// (ADR 0042).
func TestHistoryMigrateSerializesWithDetaches(t *testing.T) {
	history := &blockingHistory{
		fakeHistory: &fakeHistory{},
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	server := newHistoryServer(t, history)
	cloneBot(t, server, "support", "demo", "replacement")
	server.channels = compatibleCatalog(t, testCompatibleBindings)
	addMigrationBinding(t, server, botflota.Binding{
		Scope: "support", Bot: "replacement",
		Provider: "whatsapp", Adapter: "whatsmeow", Mode: "native",
		Identity: waKey("5215599999999"),
	})
	server.setups = testBindingSetup(committedLifecycle(),
		&stubSetupRunner{sessions: map[string]*botflota.SetupSession{}})

	migrateDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		migrateDone <- migrateHistory(t, server, "support",
			testOwnerToken, "replacement")
	}()
	<-history.started
	detachDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		detachDone <- doJSON(t, server.Handler(), "DELETE",
			"/api/v1/scopes/support/bots/demo/binding", testOwnerToken, "")
	}()
	select {
	case <-detachDone:
		close(history.release)
		t.Fatal("the detach ran while the migration held the bot lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(history.release)
	if recorder := <-migrateDone; recorder.Code != http.StatusOK {
		t.Fatalf("migrate status = %d, want 200: %s",
			recorder.Code, recorder.Body.String())
	}
	if recorder := <-detachDone; recorder.Code != http.StatusNoContent {
		t.Fatalf("detach status = %d, want 204: %s",
			recorder.Code, recorder.Body.String())
	}
	if len(history.moved) != 1 {
		t.Fatalf("moved = %v, want the single move", history.moved)
	}
}

func TestHistoryOperationsHoldTheBotLock(t *testing.T) {
	history := &fakeHistory{}
	server := newHistoryServer(t, history)
	unlock := server.lockBot("support", "demo")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodDelete,
			"/api/v1/scopes/support/bots/demo/history?confirm=support/demo", nil)
		request.Header.Set("Authorization", "Bearer "+testOwnerToken)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		done <- recorder
	}()
	select {
	case <-done:
		unlock()
		t.Fatal("the delete ran while the bot's operation lock was held")
	case <-time.After(30 * time.Millisecond):
	}
	unlock()
	select {
	case recorder := <-done:
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the delete never ran")
	}
	if len(history.deleted) != 1 {
		t.Fatalf("deleted = %v, want exactly one", history.deleted)
	}
}
