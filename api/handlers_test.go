package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

type fakeSyncFleet struct {
	fakeFleet
	scope, bot string
	target     botflota.ChatTarget
	count      int
	timeout    time.Duration
	err        error
}

func (f *fakeSyncFleet) HistorySync(
	_ context.Context, scope, bot string, target botflota.ChatTarget,
	count int, timeout time.Duration,
) (botflota.HistoryResult, error) {
	f.scope, f.bot, f.target = scope, bot, target
	f.count, f.timeout = count, timeout
	if f.err != nil {
		return botflota.HistoryResult{}, f.err
	}
	return botflota.HistoryResult{
		Received: 75, Rounds: 2, Imported: 73,
		Duplicate: 2, Stable: true,
	}, nil
}

func TestChatSyncAsksTheLiveFleetForHistory(t *testing.T) {
	live := &fakeSyncFleet{}
	server := testServerWithFleet(t, live)
	recorder := post(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/chats/sync?chat_id=3&count=75&timeout=30",
		testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if live.scope != "support" || live.bot != "demo" ||
		live.target != waTarget("5215500000000") {
		t.Fatalf("addressed %s/%s/%s", live.scope, live.bot, live.target.Thread)
	}
	if live.count != 75 || live.timeout != 30*time.Second {
		t.Fatalf("count = %d, timeout = %s", live.count, live.timeout)
	}
	var body struct {
		Received  int  `json:"received"`
		Rounds    int  `json:"rounds"`
		Duplicate int  `json:"duplicate"`
		Stable    bool `json:"stable"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Received != 75 || body.Rounds != 2 || body.Duplicate != 2 || !body.Stable {
		t.Fatalf("body = %+v", body)
	}
}

func TestChatSyncReportsAFleetWithoutHistory(t *testing.T) {
	server := testServer(t)
	recorder := post(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/chats/sync?chat_id=3",
		testOwnerToken)
	requireUnsupportedOperation(t, recorder.Code, recorder.Body.String())
}

func TestChatSyncRejectsAnUnknownChat(t *testing.T) {
	live := &fakeSyncFleet{}
	server := testServerWithFleet(t, live)
	recorder := post(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/chats/sync?chat_id=999", testOwnerToken)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

// TestChatSyncHidesUpstreamFailure: the caller learns that the sync failed,
// not why — the detail names the account, the device, or the provider and
// belongs in the server's log (ROADMAP Phase 13).
func TestChatSyncHidesUpstreamFailure(t *testing.T) {
	live := &fakeSyncFleet{
		err: fmt.Errorf("whatsapp: device store rejected 5215500000000"),
	}
	server := testServerWithFleet(t, live)
	recorder := post(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/chats/sync?chat_id=3",
		testOwnerToken)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "device store") || strings.Contains(body, "5215500000000") {
		t.Fatalf("upstream detail leaked to the caller: %s", body)
	}
	if !strings.Contains(body, "sync_failed") {
		t.Fatalf("body = %s, want the public error code", body)
	}
}

// TestChatSyncNoAnchorIsAState: a chat with nothing to page from answers its
// own code, so a batch sync can skip it instead of counting a failure, and the
// upstream detail still stays in the log.
func TestChatSyncNoAnchorIsAState(t *testing.T) {
	live := &fakeSyncFleet{
		err: fmt.Errorf("whatsapp: chat with 5215500000000 has no message to "+
			"page from yet: %w", botflota.ErrNoAnchor),
	}
	server := testServerWithFleet(t, live)
	recorder := post(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/chats/sync?chat_id=3",
		testOwnerToken)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "no_anchor") {
		t.Fatalf("body = %s, want the no_anchor code", body)
	}
	if strings.Contains(body, "5215500000000") {
		t.Fatalf("upstream detail leaked to the caller: %s", body)
	}
}

func TestConfigEndpointMasksSecrets(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(), "/api/v1/config", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "sk-live-secret") {
		t.Fatal("config response leaked the model API key")
	}
	if !strings.Contains(body, `"***"`) {
		t.Fatalf("masked secret missing from %s", body)
	}
	if strings.Contains(body, "sk-live-header") {
		t.Fatal("config response leaked the request header secret")
	}
	if !strings.Contains(body, `"Bearer ${API_TOKEN}"`) {
		t.Fatalf("header placeholder missing from %s", body)
	}
}

func TestChatsEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(), "/api/v1/scopes/support/bots/demo/chats", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Chats []struct {
			ID    int64  `json:"id"`
			Scope string `json:"scope"`
			Bot   string `json:"bot"`
			Peer  struct {
				Provider string `json:"provider"`
				Subject  string `json:"subject"`
				Name     string `json:"name"`
			} `json:"peer"`
			Messages int64 `json:"messages"`
			Sync     int64 `json:"sync"`
			Live     int64 `json:"live"`
		} `json:"chats"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Chats) != 2 {
		t.Fatalf("chats = %#v, want two", body.Chats)
	}
	chat := body.Chats[0]
	if chat.ID != 3 || chat.Scope != "support" || chat.Bot != "demo" ||
		chat.Peer.Provider != "whatsapp" ||
		chat.Peer.Subject != "5215500000000" || chat.Peer.Name != "Ana" ||
		chat.Messages != 5 || chat.Sync != 3 || chat.Live != 2 {
		t.Fatalf("chat = %#v", chat)
	}
	// A zero limit asks for every chat, which `chat sync --all` walks.
	all := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/chats?limit=0", testOwnerToken)
	if all.Code != http.StatusOK {
		t.Fatalf("limit=0 status = %d, want 200", all.Code)
	}
	var every struct {
		Chats []struct {
			ID int64 `json:"id"`
		} `json:"chats"`
	}
	if err := json.Unmarshal(all.Body.Bytes(), &every); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(every.Chats) != 2 {
		t.Fatalf("limit=0 chats = %#v, want every chat", every.Chats)
	}
}

// The ?after_id= cursor is the polling contract of `message tail --follow`.
func TestMessagesAfterCursor(t *testing.T) {
	server := testServer(t)
	base := "/api/v1/scopes/support/messages?bot=demo&chat_id=3"
	var body struct {
		Messages []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		} `json:"messages"`
	}
	recorder := get(t, server.Handler(), base+"&after_id=1", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Messages) != 1 || body.Messages[0].ID != 2 {
		t.Fatalf("after_id=1 = %#v, want message 2", body.Messages)
	}

	recorder = get(t, server.Handler(), base+"&after_id=2", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Messages) != 0 {
		t.Fatalf("after_id=2 = %#v, want none", body.Messages)
	}
}

func TestRetiredContactsEndpointUnknownBot(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/ghost/contacts", testOwnerToken)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}
