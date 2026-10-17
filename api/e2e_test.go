package api

// P4 end-to-end gate (ADR 0053, ADR 0044): a scoped HTTP client completes
// each fake setup mode and reauthorization without filesystem access, then
// reads a conversation, holds/releases it, and queues a text send through
// the same API. The client only speaks HTTP to Server.Handler; the server
// fixture's temp config file is never touched by the client. The fakes in
// e2e_fakes_test.go speak genuinely distinct modes — a QR display
// challenge, a typed token input, a two-step approval, and a
// managed-profile selection — and success commits through the binding
// store: initial setup on an unbound bot establishes its binding, while a
// later setup reauthorizes the same identity with its namespace and
// generation kept.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

type e2eBinding struct {
	Provider   string
	Subject    string
	Namespace  string
	Generation int64
}

// e2eStepView is the wire shape of a setup step the test asserts on.
type e2eStepView struct {
	Kind    string `json:"kind"`
	Display *struct {
		Format string `json:"format"`
	} `json:"display,omitempty"`
	Fields []struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"fields,omitempty"`
	Approval *struct {
		Destination string `json:"destination"`
	} `json:"approval,omitempty"`
	Profile *struct {
		Options []string `json:"options"`
	} `json:"profile,omitempty"`
}

// e2eSession is the wire shape of a setup session the gate asserts on:
// terminal success is read here, never inferred from the status code.
type e2eSession struct {
	ID       string       `json:"id"`
	Revision int64        `json:"revision"`
	State    string       `json:"state"`
	Step     *e2eStepView `json:"step,omitempty"`
}

func decodeE2ESession(t *testing.T, raw []byte) e2eSession {
	t.Helper()
	var session e2eSession
	if err := json.Unmarshal(raw, &session); err != nil {
		t.Fatalf("decode session: %v: %s", err, raw)
	}
	return session
}

func requireE2EState(t *testing.T, session e2eSession, want string) {
	t.Helper()
	if session.State != want {
		t.Fatalf("state = %q, want %q", session.State, want)
	}
}

// requireSameE2ESession checks a read-back against the session the write
// returned: same id, revision, and state (steps compare by kind elsewhere,
// since each decode allocates fresh pointers).
func requireSameE2ESession(t *testing.T, read, written e2eSession) {
	t.Helper()
	if read.ID != written.ID || read.Revision != written.Revision ||
		read.State != written.State {
		t.Fatalf("read = %+v, want %+v", read, written)
	}
}

func beginE2ESetup(
	t *testing.T, handler http.Handler, bot, method, key string,
) e2eSession {
	t.Helper()
	recorder := doJSON(t, handler, "POST",
		"/api/v1/scopes/support/bots/"+bot+"/binding/setups",
		testAccessToken, `{"method":"`+method+`","request_key":"`+key+`"}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("begin status = %d, want 202: %s",
			recorder.Code, recorder.Body.String())
	}
	if cache := recorder.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cache)
	}
	return decodeE2ESession(t, recorder.Body.Bytes())
}

func readE2ESetup(
	t *testing.T, handler http.Handler, bot, id string,
) e2eSession {
	t.Helper()
	recorder := get(t, handler,
		"/api/v1/scopes/support/bots/"+bot+"/binding/setups/"+id,
		testAccessToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200", recorder.Code)
	}
	return decodeE2ESession(t, recorder.Body.Bytes())
}

// inputE2ESetup answers one step and returns the raw response, so the
// caller asserts both the status and the resulting session state.
func inputE2ESetup(
	t *testing.T, handler http.Handler, bot, id string, revision int64,
	key, values string,
) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, handler, "POST",
		"/api/v1/scopes/support/bots/"+bot+"/binding/setups/"+id+"/inputs",
		testAccessToken,
		`{"revision":`+strconv.FormatInt(revision, 10)+`,"request_key":"`+key+
			`","values":`+values+`}`)
}

func cancelE2ESetup(t *testing.T, handler http.Handler, bot, id string) {
	t.Helper()
	recorder := doJSON(t, handler, "DELETE",
		"/api/v1/scopes/support/bots/"+bot+"/binding/setups/"+id,
		testAccessToken, "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204", recorder.Code)
	}
}

func TestScopedEndToEndSetupReadControlSend(t *testing.T) {
	methods := []string{"qr", "token", "approval", "profile"}
	bindings := &e2eBindings{accounts: map[string]botflota.Binding{
		"support/demo": {
			Scope: "support", Bot: "demo",
			Provider: "whatsapp", Adapter: "whatsmeow", Mode: "native",
			Identity:   waKey("5215500000000"),
			Generation: 1,
		},
	}}
	runner := &e2eRunner{
		methods: methods, bindings: bindings,
		sessions: map[string]*botflota.SetupSession{},
	}
	fleet := &chatControlFleet{fakeFleet: fakeFleet{statuses: []botflota.BotStatus{
		{Scope: "support", Bot: "demo", State: "connected", OutboxDepth: 2},
	}}}
	sends := &fakeSends{}
	server := testServerWithOptions(t, fleet,
		func(opts *Options) {
			opts.Setups = &e2eSetups{
				lifecycle: &e2eLifecycle{bindings: bindings},
				runner:    runner,
			}
			opts.Bindings = bindings
			opts.Sends = sends
		}, func(cfg *config.Config) {
			// Fresh is the unbound bot initial setup establishes.
			cfg.Scopes["support"].Bots["fresh"] = spec.Bot{
				Graph: "main", Channel: testChannel(),
			}
		})
	handler := server.Handler()

	recorder := get(t, handler,
		"/api/v1/scopes/support/bots/demo/binding/methods", testAccessToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("methods status = %d, want 200: %s",
			recorder.Code, recorder.Body.String())
	}
	var discovered struct {
		Methods []string `json:"methods"`
	}
	decodeSetupResponse(t, recorder.Body.Bytes(), &discovered)
	for _, method := range methods {
		if !slices.Contains(discovered.Methods, method) {
			t.Fatalf("methods = %v, want every fake mode", discovered.Methods)
		}
	}

	demoBefore := inspectE2EBinding(t, handler, "demo")
	if demoBefore == nil {
		t.Fatal("demo starts without the committed binding")
	}
	if inspectE2EBinding(t, handler, "fresh") != nil {
		t.Fatal("fresh starts bound, want it unbound for initial setup")
	}

	// Initial setup on the unbound bot: each single-step mode completes
	// over HTTP, and the first completion establishes the binding.
	cases := []struct {
		name   string
		method string
		values string
	}{
		{"qr display challenge", "qr", `{}`},
		{"typed token input", "token", `{"token":"e2e-secret-token"}`},
		{"managed-profile selection", "profile", `{"profile":"main"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			begun := beginE2ESetup(t, handler, "fresh",
				tc.method, "e2e-"+tc.method)
			if begun.Step == nil {
				t.Fatalf("%s opened with no step", tc.method)
			}
			assertE2EStep(t, tc.method, *begun.Step)
			requireSameE2ESession(t,
				readE2ESetup(t, handler, "fresh", begun.ID), begun)
			stale := inputE2ESetup(t, handler, "fresh", begun.ID,
				9, "e2e-stale", `{}`)
			if stale.Code != http.StatusConflict {
				t.Fatalf("stale input status = %d, want 409", stale.Code)
			}
			answered := inputE2ESetup(t, handler, "fresh", begun.ID,
				1, "e2e-input-"+tc.method, tc.values)
			if answered.Code != http.StatusOK {
				t.Fatalf("input status = %d, want 200: %s",
					answered.Code, answered.Body.String())
			}
			if body := answered.Body.String(); strings.Contains(body, "e2e-secret") {
				t.Fatalf("response echoes the submitted secret: %s", body)
			}
			done := decodeE2ESession(t, answered.Body.Bytes())
			requireE2EState(t, done, string(botflota.SetupSucceeded))
			if done.Step != nil {
				t.Fatalf("succeeded with a pending step: %+v", done.Step)
			}
			cancelE2ESetup(t, handler, "fresh", begun.ID)
		})
	}

	established := inspectE2EBinding(t, handler, "fresh")
	if established == nil {
		t.Fatal("initial setup left no committed binding")
	}
	if established.Generation != 1 {
		t.Fatalf("generation = %d, want the established 1", established.Generation)
	}

	// A genuine multi-step approval: the external confirmation advances
	// to a code step instead of succeeding, with each state read back.
	t.Run("multi-step approval", func(t *testing.T) {
		begun := beginE2ESetup(t, handler, "fresh", "approval", "e2e-approval")
		requireE2EState(t, begun, string(botflota.SetupAwaitingApproval))
		if begun.Step == nil {
			t.Fatal("approval opened with no step")
		}
		assertE2EStep(t, "approval", *begun.Step)
		requireSameE2ESession(t,
			readE2ESetup(t, handler, "fresh", begun.ID), begun)
		stale := inputE2ESetup(t, handler, "fresh", begun.ID,
			9, "e2e-stale", `{}`)
		if stale.Code != http.StatusConflict {
			t.Fatalf("stale input status = %d, want 409", stale.Code)
		}
		confirmed := inputE2ESetup(t, handler, "fresh", begun.ID,
			1, "e2e-approve", `{}`)
		if confirmed.Code != http.StatusOK {
			t.Fatalf("confirm status = %d, want 200: %s",
				confirmed.Code, confirmed.Body.String())
		}
		next := decodeE2ESession(t, confirmed.Body.Bytes())
		requireE2EState(t, next, string(botflota.SetupAwaitingInput))
		if next.Revision != 2 || next.Step == nil ||
			next.Step.Kind != botflota.SetupStepInput {
			t.Fatalf("second step = %+v, want the code input", next)
		}
		requireSameE2ESession(t,
			readE2ESetup(t, handler, "fresh", begun.ID), next)
		finished := inputE2ESetup(t, handler, "fresh", begun.ID,
			2, "e2e-code", `{"code":"123456"}`)
		if finished.Code != http.StatusOK {
			t.Fatalf("code status = %d, want 200: %s",
				finished.Code, finished.Body.String())
		}
		done := decodeE2ESession(t, finished.Body.Bytes())
		requireE2EState(t, done, string(botflota.SetupSucceeded))
		if done.Step != nil {
			t.Fatalf("succeeded with a pending step: %+v", done.Step)
		}
		cancelE2ESetup(t, handler, "fresh", begun.ID)
	})
	if renewed := inspectE2EBinding(t, handler, "fresh"); renewed == nil ||
		*renewed != *established {
		t.Fatalf("renewal moved %+v from %+v", renewed, established)
	}

	// Reauthorization on the already bound bot preserves its binding.
	t.Run("renewal preserves the binding", func(t *testing.T) {
		begun := beginE2ESetup(t, handler, "demo", "token", "e2e-demo-token")
		answered := inputE2ESetup(t, handler, "demo", begun.ID,
			1, "e2e-demo-input", `{"token":"e2e-secret-demo"}`)
		if answered.Code != http.StatusOK {
			t.Fatalf("input status = %d, want 200: %s",
				answered.Code, answered.Body.String())
		}
		done := decodeE2ESession(t, answered.Body.Bytes())
		requireE2EState(t, done, string(botflota.SetupSucceeded))
		if done.Step != nil {
			t.Fatalf("succeeded with a pending step: %+v", done.Step)
		}
		cancelE2ESetup(t, handler, "demo", begun.ID)
	})
	if after := inspectE2EBinding(t, handler, "demo"); after == nil ||
		*after != *demoBefore {
		t.Fatalf("renewal moved %+v from %+v", after, demoBefore)
	}

	recorder = get(t, handler,
		"/api/v1/scopes/support/bots/demo/chats", testAccessToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("chats status = %d, want 200: %s",
			recorder.Code, recorder.Body.String())
	}
	var chats struct {
		Chats []struct {
			ID int64 `json:"id"`
		} `json:"chats"`
	}
	decodeSetupResponse(t, recorder.Body.Bytes(), &chats)
	if len(chats.Chats) == 0 {
		t.Fatal("no conversation to read")
	}

	recorder = get(t, handler,
		"/api/v1/scopes/support/messages?bot=demo&chat_id=3&limit=10",
		testAccessToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("messages status = %d, want 200: %s",
			recorder.Code, recorder.Body.String())
	}

	held := setChatControl(t, server, "support", testAccessToken, 3,
		string(botflota.ChatHuman))
	if held.Code != http.StatusOK {
		t.Fatalf("hold status = %d, want 200: %s",
			held.Code, held.Body.String())
	}
	if body := held.Body.String(); !strings.Contains(body, `"mode":"human"`) {
		t.Fatalf("hold body %s does not report human", body)
	}
	released := setChatControl(t, server, "support", testAccessToken, 3,
		string(botflota.ChatAuto))
	if released.Code != http.StatusOK {
		t.Fatalf("release status = %d, want 200: %s",
			released.Code, released.Body.String())
	}

	recorder = postJSON(t, handler, "/api/v1/scopes/support/messages",
		testAccessToken, map[string]any{
			"bot": "demo", "chat_id": 3, "body": "tu pedido está listo",
			"kind": "text", "client_id": "e2e-1",
		})
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("send status = %d, want 202: %s",
			recorder.Code, recorder.Body.String())
	}
	if len(sends.sends) != 1 {
		t.Fatalf("committed sends = %d, want 1", len(sends.sends))
	}
	send := sends.sends[0]
	if send.Scope != "support" || send.Bot != "demo" ||
		send.Target != waTarget("5215500000000") {
		t.Fatalf("send = %#v, want the chat's target", send)
	}
	if body := botflota.ContentText(send.Content); body != "tu pedido está listo" {
		t.Fatalf("body = %q", body)
	}
}

// assertE2EStep checks the distinct step each fake mode opens with: the QR
// display challenge, the typed credential input, the external approval, or
// the managed-profile selection.
func assertE2EStep(t *testing.T, method string, step e2eStepView) {
	t.Helper()
	switch method {
	case "qr":
		if step.Kind != botflota.SetupStepDisplay ||
			step.Display == nil || step.Display.Format != "qr" {
			t.Fatalf("qr step = %+v, want a qr display challenge", step)
		}
	case "token":
		if step.Kind != botflota.SetupStepInput || len(step.Fields) != 1 ||
			step.Fields[0].Name != "token" ||
			step.Fields[0].Kind != botflota.SetupFieldCredential {
			t.Fatalf("token step = %+v, want one credential field", step)
		}
	case "approval":
		if step.Kind != botflota.SetupStepApproval ||
			step.Approval == nil || step.Approval.Destination == "" {
			t.Fatalf("approval step = %+v, want an external wait", step)
		}
	case "profile":
		if step.Kind != botflota.SetupStepProfile ||
			step.Profile == nil || !slices.Contains(step.Profile.Options, "main") {
			t.Fatalf("profile step = %+v, want a managed selection", step)
		}
	}
}

// inspectE2EBinding reads the committed binding with its identity,
// namespace, and generation, or nil when the bot has none.
func inspectE2EBinding(
	t *testing.T, handler http.Handler, bot string,
) *e2eBinding {
	t.Helper()
	recorder := get(t, handler,
		"/api/v1/scopes/support/bots/"+bot+"/binding", testAccessToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("binding status = %d, want 200: %s",
			recorder.Code, recorder.Body.String())
	}
	var status struct {
		Binding *struct {
			Provider  string `json:"provider"`
			Namespace string `json:"namespace,omitempty"`
			Identity  struct {
				Provider string `json:"provider"`
				Subject  string `json:"subject"`
			} `json:"identity"`
			Generation int64 `json:"generation"`
		} `json:"binding"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode binding: %v", err)
	}
	if status.Binding == nil {
		return nil
	}
	return &e2eBinding{
		Provider:   status.Binding.Provider,
		Subject:    status.Binding.Identity.Subject,
		Namespace:  status.Binding.Namespace,
		Generation: status.Binding.Generation,
	}
}
