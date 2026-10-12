package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/felixzsh/botflota"
)

// chatControlFleet is a fleet that can hold chats, which the plain fake cannot.
// Keeping the capability out of fakeFleet leaves the route's unsupported path
// reachable in the test below.
type chatControlFleet struct {
	fakeFleet
	control map[botflota.ChatTarget]botflota.ChatControl
	held    []string
	err     error
}

func (f *chatControlFleet) SetChatHuman(
	_ context.Context, scope, bot string, target botflota.ChatTarget, human bool,
) error {
	if f.err != nil {
		return f.err
	}
	mode := botflota.ChatAuto
	if human {
		mode = botflota.ChatHuman
	}
	if f.control == nil {
		f.control = map[botflota.ChatTarget]botflota.ChatControl{}
	}
	f.control[target] = botflota.ChatControl{Mode: mode}
	f.held = append(f.held, scope+"/"+bot+"/"+target.Thread)
	return nil
}

func (f *chatControlFleet) ChatControl(
	_ string, _ string, target botflota.ChatTarget,
) (botflota.ChatControl, bool) {
	state, ok := f.control[target]
	return state, ok
}

func setChatControl(
	t *testing.T, server *Server, scope, token, sender, mode string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"mode": mode})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := "/api/v1/scopes/" + scope + "/chats/control?bot=demo&contact=" + sender
	request := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func decodeControl(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Scope   string         `json:"scope"`
		Bot     string         `json:"bot"`
		Contact string         `json:"contact"`
		ChatID  int64          `json:"chat_id"`
		Control map[string]any `json:"control"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v: %s", err, recorder.Body.String())
	}
	if body.Scope != "support" || body.Bot != "demo" || body.ChatID == 0 {
		t.Fatalf("body = %+v, want the addressed chat", body)
	}
	mode, _ := body.Control["mode"].(string)
	return mode
}

// newControlServer is a server whose fleet can hold chats.
func newControlServer(t *testing.T, fleet *chatControlFleet) *Server {
	t.Helper()
	server := testServer(t)
	server.fleet = fleet
	return server
}

func TestChatControlHoldsAndReleases(t *testing.T) {
	fleet := &chatControlFleet{}
	server := newControlServer(t, fleet)

	recorder := setChatControl(t, server, "support", testOwnerToken,
		"5215500000000", "human")
	if recorder.Code != http.StatusOK {
		t.Fatalf("hold status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if got := decodeControl(t, recorder); got != "human" {
		t.Fatalf("mode = %q, want human", got)
	}
	if len(fleet.held) != 1 || fleet.held[0] != "support/demo/5215500000000" {
		t.Fatalf("held = %v, want the addressed chat", fleet.held)
	}

	recorder = setChatControl(t, server, "support", testOwnerToken,
		"5215500000000", "auto")
	if recorder.Code != http.StatusOK {
		t.Fatalf("release status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if got := decodeControl(t, recorder); got != "auto" {
		t.Fatalf("mode = %q, want auto", got)
	}
}

func TestChatControlRefusals(t *testing.T) {
	cases := []struct {
		name   string
		fleet  *chatControlFleet
		scope  string
		token  string
		sender string
		mode   string
		want   int
		code   string
	}{
		{
			name: "a mode that is neither human nor auto", fleet: &chatControlFleet{},
			scope: "support", token: testOwnerToken, sender: "5215500000000",
			mode: "paused", want: http.StatusUnprocessableEntity,
		},
		{
			name: "an empty mode", fleet: &chatControlFleet{},
			scope: "support", token: testOwnerToken, sender: "5215500000000",
			mode: "", want: http.StatusUnprocessableEntity,
		},
		{
			name: "a bot that is not running", fleet: &chatControlFleet{
				err: botflota.ErrBotNotRunning,
			},
			scope: "support", token: testOwnerToken, sender: "5215500000000",
			mode: "human", want: http.StatusConflict, code: "bot_not_running",
		},
		{
			name: "an unknown sender", fleet: &chatControlFleet{},
			scope: "support", token: testOwnerToken, sender: "0000000000",
			mode: "human", want: http.StatusNotFound,
		},
		{
			name: "another scope, for a scoped token", fleet: &chatControlFleet{},
			scope: "ventas", token: testAccessToken, sender: "5215500000000",
			mode: "human", want: http.StatusNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := setChatControl(t, newControlServer(t, tc.fleet),
				tc.scope, tc.token, tc.sender, tc.mode)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s",
					recorder.Code, tc.want, recorder.Body.String())
			}
			if tc.code == "" {
				return
			}
			var body errorBody
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error.Code != tc.code {
				t.Fatalf("code = %q, want %q", body.Error.Code, tc.code)
			}
		})
	}
}

func TestChatControlWithoutACapableFleetSaysSo(t *testing.T) {
	// The plain fixture fleet has no control capability.
	recorder := setChatControl(t, testServer(t), "support", testOwnerToken,
		"5215500000000", "human")
	requireUnsupportedOperation(t, recorder.Code, recorder.Body.String())
}

func TestChatListReportsControlOfAHeldChat(t *testing.T) {
	fleet := &chatControlFleet{control: map[botflota.ChatTarget]botflota.ChatControl{
		waTarget("5215500000000"): {Mode: botflota.ChatHuman},
	}}
	server := newControlServer(t, fleet)

	recorder := get(t, server.Handler(), "/api/v1/scopes/support/chats", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Chats []struct {
			Contact struct {
				Phone string `json:"phone"`
			} `json:"contact"`
			Control string `json:"control"`
		} `json:"chats"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, chat := range body.Chats {
		if chat.Contact.Phone != "5215500000000" {
			continue
		}
		found = true
		if chat.Control != "human" {
			t.Fatalf("control = %q, want human", chat.Control)
		}
	}
	if !found {
		t.Fatalf("the fixture chat is missing from %+v", body.Chats)
	}
}

func TestChatListOmitsControlWithoutACapableFleet(t *testing.T) {
	recorder := get(t, testServer(t).Handler(),
		"/api/v1/scopes/support/chats", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Chats []map[string]any `json:"chats"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, chat := range body.Chats {
		if _, ok := chat["control"]; ok {
			t.Fatalf("chat %v reports control without a capable fleet", chat)
		}
	}
}
