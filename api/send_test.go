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

func TestSendMessageQueues(t *testing.T) {
	sends := &fakeSends{}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Sends = sends }, nil)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/support/messages",
		testOwnerToken, map[string]any{
			"bot": "demo", "to": "5215599999999", "body": "tu pedido está listo",
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
	if send.Scope != "support" || send.Bot != "demo" ||
		send.Target != waTarget("5215599999999") ||
		send.Recipient == nil || *send.Recipient != waKey("5215599999999") ||
		send.ClientID != "order-1" {
		t.Fatalf("send = %#v", send)
	}
	if body := botflota.ContentText(send.Content); body != "tu pedido está listo" {
		t.Fatalf("body = %q", body)
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
			"bot": "demo", "to": "5215599999999", "body": "aviso",
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
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"unknown bot", map[string]any{
			"bot": "ghost", "to": "5215599999999", "body": "x"}, http.StatusNotFound},
		{"missing bot", map[string]any{
			"to": "5215599999999", "body": "x"}, http.StatusBadRequest},
		{"missing destination", map[string]any{
			"bot": "demo", "to": "", "body": "x"}, http.StatusBadRequest},
		{"missing to", map[string]any{
			"bot": "demo", "body": "x"}, http.StatusBadRequest},
		{"empty body", map[string]any{
			"bot": "demo", "to": "5215599999999", "body": ""}, http.StatusBadRequest},
		{"unsupported kind", map[string]any{
			"bot": "demo", "to": "5215599999999", "body": "x",
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
			"bot": "demo", "to": "5215599999999", "body": "x",
		})
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body = %s", recorder.Code, recorder.Body)
	}
}

// A scoped token cannot send in another scope: the target is indistinguishable
// from an unknown one (ADR 0039, ROADMAP Phase 13).
func TestSendMessageOutOfScopeIsNotFound(t *testing.T) {
	server := testServer(t)
	recorder := postJSON(t, server.Handler(), "/api/v1/scopes/ventas/messages",
		testAccessToken, map[string]any{
			"bot": "other", "to": "5215599999999", "body": "x",
		})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", recorder.Code, recorder.Body)
	}
	if strings.Contains(recorder.Body.String(), "other") {
		t.Fatalf("out-of-scope response leaked a bot name: %s", recorder.Body)
	}
}
