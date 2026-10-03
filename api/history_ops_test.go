package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
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
	if body.Removed["chats"] != 2 || body.Removed["contacts"] != 4 {
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
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", recorder.Code)
		}
	})
	t.Run("a destination that holds history is a conflict", func(t *testing.T) {
		history := &fakeHistory{occupancy: map[string]bool{"support/busy": true}}
		server := newHistoryServer(t, history)
		server.config().Scopes["support"].Bots["busy"] = server.config().Scopes["support"].Bots["demo"]
		recorder := migrateHistory(t, server, "support", testOwnerToken, "busy")
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
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
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", recorder.Code)
	}
	// The destination must be a real bot of the scope, or the request is refused
	// before the missing store is even reached.
	server.config().Scopes["support"].Bots["replacement"] =
		server.config().Scopes["support"].Bots["demo"]
	recorder = migrateHistory(t, server, "support", testOwnerToken, "replacement")
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", recorder.Code)
	}
}

// TestHistoryOperationsHoldTheBotLock keeps one bot's write operations from
// overlapping: a history request waits for the lock pairing and unpairing take,
// so neither can restart the bot the other is still working on (ADR 0042).
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
