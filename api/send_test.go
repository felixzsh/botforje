package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
)

// fakeSends records what the handler asked to store; a test that needs an
// error or a specific accepted id sets it before the request.
type fakeSends struct {
	sends  []botflota.SendRecord
	result botflota.SendResult
	err    error
}

func (f *fakeSends) CommitSend(
	_ context.Context, send botflota.SendRecord,
) (botflota.SendResult, error) {
	f.sends = append(f.sends, send)
	if f.err != nil {
		return f.result, f.err
	}
	if f.result.MessageID == 0 {
		return botflota.SendResult{MessageID: 123, OutboxID: 456}, nil
	}
	return f.result, nil
}

// FindSend reconciles a client id against the sends this fake already
// recorded, mirroring the store's accepted-send lookup (ADR 0044).
func (f *fakeSends) FindSend(
	_ context.Context, scope, bot, clientID string,
) (botflota.SendResult, error) {
	if clientID == "" {
		return botflota.SendResult{}, botflota.ErrNotFound
	}
	for i := len(f.sends) - 1; i >= 0; i-- {
		send := f.sends[i]
		if send.Scope == scope && send.Bot == bot && send.ClientID == clientID {
			if f.result.MessageID == 0 {
				return botflota.SendResult{MessageID: 123, OutboxID: 456}, nil
			}
			return f.result, nil
		}
	}
	return botflota.SendResult{}, botflota.ErrNotFound
}

func postJSON(
	t *testing.T, handler http.Handler, path, token string, body any,
) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return postRaw(t, handler, path, token, data)
}

func postRaw(
	t *testing.T, handler http.Handler, path, token string, data []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestSendMessageQueuesByChatID(t *testing.T) {
	sends := &fakeSends{}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = sends }, nil)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/support/messages",
		testOwnerToken, map[string]any{
			"bot": "demo", "chat_id": 3, "body": "tu pedido está listo",
			"kind": "text", "client_id": "order-1",
		})
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 123 || response.Status != "queued" {
		t.Fatalf("response = %#v, want id 123 queued", response)
	}
	if len(sends.sends) != 1 {
		t.Fatalf("committed sends = %d, want 1", len(sends.sends))
	}
	send := sends.sends[0]
	// A chat id selects the stored conversation target, never the sender:
	// no recipient is fabricated for the commit.
	if send.Scope != "support" || send.Bot != "demo" ||
		send.Target != waTarget("5215500000000") ||
		send.Recipient != nil || send.ClientID != "order-1" {
		t.Fatalf("send = %#v", send)
	}
	if body := botflota.ContentText(send.Content); body != "tu pedido está listo" {
		t.Fatalf("body = %q", body)
	}
}

func TestSendMessageQueuesByTarget(t *testing.T) {
	sends := &fakeSends{}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = sends }, nil)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/support/messages",
		testOwnerToken, map[string]any{
			"bot": "demo",
			"target": map[string]any{
				"provider": "whatsapp", "kind": "direct",
				"thread": "5215599999999",
			},
			"recipient": map[string]any{
				"provider": "whatsapp", "subject": "5215599999999",
			},
			"body": "aviso",
		})
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if len(sends.sends) != 1 {
		t.Fatalf("committed sends = %d, want 1", len(sends.sends))
	}
	send := sends.sends[0]
	if send.Target != waTarget("5215599999999") ||
		send.Recipient == nil || *send.Recipient != waKey("5215599999999") {
		t.Fatalf("send = %#v", send)
	}
}

// TestSendMessageSnapshotsBindingGeneration: the committed send carries the
// generation the binding had at admission, so delivery can refuse a later
// identity (ADR 0040).
func TestSendMessageSnapshotsBindingGeneration(t *testing.T) {
	sends := &fakeSends{}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = sends }, nil)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/support/messages",
		testOwnerToken, map[string]any{
			"bot": "demo", "chat_id": 3, "body": "aviso",
		})
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if len(sends.sends) != 1 {
		t.Fatalf("committed sends = %d, want 1", len(sends.sends))
	}
	if sends.sends[0].BindingGeneration != 1 {
		t.Fatalf("generation = %d, want the admitted 1", sends.sends[0].BindingGeneration)
	}
}

// TestSendMessageRejectsForeignProviderTarget: a target outside the bot's
// installed definition is refused before queue admission (ADR 0044).
func TestSendMessageRejectsForeignProviderTarget(t *testing.T) {
	sends := &fakeSends{}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = sends; opts.Channels = testCatalog(t) }, nil)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/support/messages",
		testOwnerToken, map[string]any{
			"bot": "demo",
			"target": map[string]any{
				"provider": "pager", "kind": "direct", "thread": "x",
			},
			"body": "aviso",
		})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", recorder.Code, recorder.Body)
	}
	if len(sends.sends) != 0 {
		t.Fatalf("committed sends = %d, want none admitted", len(sends.sends))
	}
}

func TestSendMessageRepeatsAnswerTheAcceptedID(t *testing.T) {
	sends := &fakeSends{
		result: botflota.SendResult{MessageID: 77, OutboxID: 78},
		err:    botflota.ErrDuplicate,
	}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = sends }, nil)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/support/messages",
		testOwnerToken, map[string]any{
			"bot": "demo", "chat_id": 3, "body": "aviso",
			"client_id": "order-1",
		})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "duplicate" || body.ID != 77 {
		t.Fatalf("body = %s, want duplicate and the accepted id 77", recorder.Body)
	}
}

func TestSendMessageValidatesTheRequest(t *testing.T) {
	validTarget := map[string]any{
		"provider": "whatsapp", "kind": "direct", "thread": "5215599999999",
	}
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"unknown bot", map[string]any{
			"bot": "ghost", "target": validTarget, "body": "x"},
			http.StatusNotFound},
		{"missing bot", map[string]any{
			"target": validTarget, "body": "x"}, http.StatusBadRequest},
		{"both selectors", map[string]any{
			"bot": "demo", "chat_id": 3, "target": validTarget,
			"body": "x"}, http.StatusBadRequest},
		{"no selector", map[string]any{
			"bot": "demo", "body": "x"}, http.StatusBadRequest},
		{"retired to", map[string]any{
			"bot": "demo", "to": "5215599999999", "body": "x"},
			http.StatusBadRequest},
		{"unknown chat", map[string]any{
			"bot": "demo", "chat_id": 999, "body": "x"},
			http.StatusNotFound},
		{"target without thread", map[string]any{
			"bot": "demo", "target": map[string]any{
				"provider": "whatsapp", "kind": "direct",
			}, "body": "x"}, http.StatusBadRequest},
		{"target with bad kind", map[string]any{
			"bot": "demo", "target": map[string]any{
				"provider": "whatsapp", "kind": "smoke",
				"thread": "5215599999999",
			}, "body": "x"}, http.StatusBadRequest},
		{"recipient without subject", map[string]any{
			"bot": "demo", "target": validTarget, "body": "x",
			"recipient": map[string]any{"provider": "whatsapp"}},
			http.StatusBadRequest},
		{"empty body", map[string]any{
			"bot": "demo", "target": validTarget, "body": ""},
			http.StatusBadRequest},
		{"unsupported kind", map[string]any{
			"bot": "demo", "target": validTarget, "body": "x",
			"kind": "image"}, http.StatusBadRequest},
	}
	server := testServer(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := postJSON(t, server.Handler(),
				"/api/v1/scopes/support/messages", testOwnerToken, tc.body)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d; body = %s",
					recorder.Code, tc.want, recorder.Body)
			}
		})
	}
}

func TestSendMessageRejectsAMalformedBody(t *testing.T) {
	server := testServer(t)
	recorder := postRaw(t, server.Handler(), "/api/v1/scopes/support/messages",
		testOwnerToken, []byte("not json"))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", recorder.Code, recorder.Body)
	}
}

func TestSendMessageUnavailableWithoutTheStore(t *testing.T) {
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = nil }, nil)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/support/messages",
		testOwnerToken, map[string]any{
			"bot": "demo", "chat_id": 3, "body": "x",
		})
	requireUnsupportedOperation(t, recorder.Code, recorder.Body.String())
}

// A scoped token cannot send in another scope: the target is indistinguishable
// from an unknown one (ADR 0039, ROADMAP Phase 13).
func TestSendMessageOutOfScopeIsNotFound(t *testing.T) {
	server := testServer(t)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/ventas/messages",
		testAccessToken, map[string]any{
			"bot": "other", "chat_id": 3, "body": "x",
		})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", recorder.Code, recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), "other") {
		t.Fatalf("out-of-scope response leaked a bot name: %s", recorder.Body)
	}
}

// sendCatalog is the installed set send admission resolves against: the
// test channel with configurable send support and target rules, so
// admission is checked against a declared definition rather than a
// provider switch (ADR 0044, ADR 0052).
func sendCatalog(
	t *testing.T, send []botflota.MessageKind,
	validate func(botflota.ChatTarget) []string,
) *channels.Catalog {
	t.Helper()
	catalog, err := channels.New(channels.Definition{
		Provider: "whatsapp",
		Kind:     "fake",
		Modes:    []string{"native"},
		Capabilities: channels.Capabilities{
			Send: send,
		},
		ValidateTarget: validate,
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return catalog
}

func sendTargetBody(thread string) map[string]any {
	return map[string]any{
		"provider": "whatsapp", "kind": "direct", "thread": thread,
	}
}

// TestSendMessageForeignChatIsNotFound covers scope and bot resolution: a
// chat id owned by another bot answers 404, never 403 or a cross-bot send
// (ADR 0044).
func TestSendMessageForeignChatIsNotFound(t *testing.T) {
	sends := &fakeSends{}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = sends }, nil)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/ventas/messages",
		testOwnerToken, map[string]any{
			"bot": "other", "chat_id": 3, "body": "x",
		})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", recorder.Code, recorder.Body)
	}
	if len(sends.sends) != 0 {
		t.Fatalf("committed sends = %d, want none", len(sends.sends))
	}
}

// TestSendMessageRequiresABinding refuses an unbound send before any peer,
// chat, message, or outbox row exists; a disconnected bound bot is not
// unbound, so the check reads the committed binding only (ADR 0044).
func TestSendMessageRequiresABinding(t *testing.T) {
	sends := &fakeSends{}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = sends }, nil)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/ventas/messages",
		testOwnerToken, map[string]any{
			"bot": "other", "target": sendTargetBody("5215500000003"),
			"body": "x",
		})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "binding_required" {
		t.Fatalf("body = %s, want binding_required", recorder.Body)
	}
	if len(sends.sends) != 0 {
		t.Fatalf("committed sends = %d, want none stored", len(sends.sends))
	}
}

// TestSendMessageReconcilesDuplicateWithoutABinding covers the retry path: a
// repeat of an accepted client id reports the accepted message without a new
// admission, so reconciliation survives even after the binding changed
// (ADR 0044).
func TestSendMessageReconcilesDuplicateWithoutABinding(t *testing.T) {
	sends := &fakeSends{sends: []botflota.SendRecord{{
		Scope: "ventas", Bot: "other", ClientID: "retry-1",
		Target: waTarget("5215500000003"),
	}}}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = sends }, nil)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/ventas/messages",
		testOwnerToken, map[string]any{
			"bot": "other", "target": sendTargetBody("5215500000003"),
			"body": "x", "client_id": "retry-1",
		})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", recorder.Code, recorder.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "duplicate" || body.ID != 123 {
		t.Fatalf("body = %s, want duplicate and the accepted id", recorder.Body)
	}
	if len(sends.sends) != 1 {
		t.Fatalf("committed sends = %d, want no new admission", len(sends.sends))
	}
}

// TestSendMessageAdapterAdmission applies the selected definition before
// queue admission: a mismatched provider, an unsupported target, or
// undeclared content is refused before persistence (ADR 0044, ADR 0052).
func TestSendMessageAdapterAdmission(t *testing.T) {
	directOnly := func(target botflota.ChatTarget) []string {
		if target.Kind != botflota.ChatDirect {
			return []string{"shared conversations are not supported"}
		}
		if target.Topic != "" {
			return []string{"topics are not supported"}
		}
		return nil
	}
	cases := []struct {
		name     string
		send     []botflota.MessageKind
		validate func(botflota.ChatTarget) []string
		body     map[string]any
		want     int
	}{
		{"mismatched provider",
			[]botflota.MessageKind{botflota.KindText}, directOnly,
			map[string]any{
				"bot": "demo", "target": map[string]any{
					"provider": "telegram", "kind": "direct",
					"thread": "1",
				}, "body": "x",
			}, http.StatusUnprocessableEntity},
		{"unsupported target kind",
			[]botflota.MessageKind{botflota.KindText}, directOnly,
			map[string]any{
				"bot": "demo", "target": map[string]any{
					"provider": "whatsapp", "kind": "group",
					"thread": "g1",
				}, "body": "x",
			}, http.StatusUnprocessableEntity},
		{"unsupported topic",
			[]botflota.MessageKind{botflota.KindText}, directOnly,
			map[string]any{
				"bot": "demo", "target": map[string]any{
					"provider": "whatsapp", "kind": "direct",
					"thread": "5215599999999", "topic": "t",
				}, "body": "x",
			}, http.StatusUnprocessableEntity},
		{"undeclared content",
			[]botflota.MessageKind{botflota.KindImage}, nil,
			map[string]any{
				"bot": "demo", "target": sendTargetBody("5215599999999"),
				"body": "x",
			}, http.StatusUnprocessableEntity},
		{"admitted",
			[]botflota.MessageKind{botflota.KindText}, directOnly,
			map[string]any{
				"bot": "demo", "target": sendTargetBody("5215599999999"),
				"body": "x",
			}, http.StatusAccepted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sends := &fakeSends{}
			server := testServerWithOptions(t, &fakeFleet{},
				func(opts *Options) {
					opts.Sends = sends
					opts.Channels = sendCatalog(t, tc.send, tc.validate)
				}, nil)
			recorder := postJSON(t, server.Handler(),
				"/api/v1/scopes/support/messages", testOwnerToken, tc.body)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d; body = %s",
					recorder.Code, tc.want, recorder.Body)
			}
			if tc.want != http.StatusAccepted && len(sends.sends) != 0 {
				t.Fatalf("committed sends = %d, want none stored", len(sends.sends))
			}
			if tc.want == http.StatusAccepted {
				var response struct {
					ID     int64  `json:"id"`
					Status string `json:"status"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if response.Status != "queued" {
					t.Fatalf("response = %#v, want queued", response)
				}
			}
		})
	}
}

// TestSendMessageRecipientContradiction covers the association rule: a
// supplied recipient that is not the chat's known participant is rejected,
// while an unknown identity is left for the store to register (ADR 0044).
func TestSendMessageRecipientContradiction(t *testing.T) {
	sends := &fakeSends{}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = sends }, nil)
	handler := server.Handler()
	recorder := postJSON(t, handler, "/api/v1/scopes/support/messages",
		testOwnerToken, map[string]any{
			"bot": "demo", "chat_id": 3, "body": "x",
			"recipient": map[string]any{
				"provider": "whatsapp", "subject": "5215599999999",
			},
		})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", recorder.Code, recorder.Body)
	}
	if len(sends.sends) != 0 {
		t.Fatalf("committed sends = %d, want none stored", len(sends.sends))
	}

	recorder = postJSON(t, handler, "/api/v1/scopes/support/messages",
		testOwnerToken, map[string]any{
			"bot": "demo", "chat_id": 3, "body": "x",
			"recipient": map[string]any{
				"provider": "whatsapp", "subject": "5215500000099",
			},
		})
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", recorder.Code, recorder.Body)
	}
	if len(sends.sends) != 1 || sends.sends[0].Recipient == nil ||
		sends.sends[0].Recipient.Subject != "5215500000099" {
		t.Fatalf("sends = %#v, want the unknown recipient kept", sends.sends)
	}
}
