package api

// P4 binding/setup gate (ADR 0053): a scoped HTTP client completes the
// neutral lifecycle over the same routes the CLI will use — inspect,
// methods, begin, read, input, cancel, detach — with stale IDs, conflicts,
// cross-scope opacity, and no secret echo.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

type stubBindingLifecycle struct {
	status     botflota.BindingStatus
	inspectErr error
	detachErr  error
	detached   []string
}

func (s *stubBindingLifecycle) InspectBinding(
	_ context.Context, scope, bot string,
) (botflota.BindingStatus, error) {
	if s.inspectErr != nil {
		return botflota.BindingStatus{}, s.inspectErr
	}
	return s.status, nil
}

func (s *stubBindingLifecycle) DetachBinding(
	_ context.Context, scope, bot string,
) error {
	s.detached = append(s.detached, scope+"/"+bot)
	if s.detachErr != nil {
		return s.detachErr
	}
	if s.status.Binding == nil {
		return botflota.ErrNotFound
	}
	s.status.Binding = nil
	s.status.Condition = botflota.ReadinessUnconfigured
	return nil
}

type stubSetupRunner struct {
	methods    []string
	methodsErr error
	sessions   map[string]*botflota.SetupSession
	keys       map[string]string
	params     map[string]string
	values     map[string]map[string]string
	beginErr   error
	nextID     int
}

func (s *stubSetupRunner) SetupMethods(
	_ context.Context, _, _ string,
) ([]string, error) {
	return s.methods, s.methodsErr
}

// discoveryRunner adds the optional requirement report, so a test can pin what
// the discovery route answers for a runner that says more than method names
// (ADR 0053).
type discoveryRunner struct {
	*stubSetupRunner
	requirements []botflota.SetupRequirement
}

func (d *discoveryRunner) SetupRequirements(
	_ context.Context, _, _ string,
) ([]botflota.SetupRequirement, error) {
	return d.requirements, nil
}

func (s *stubSetupRunner) BeginSetup(
	_ context.Context, _, _ string, req botflota.SetupBegin,
) (*botflota.SetupSession, error) {
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	if id, ok := s.keys[req.RequestKey]; ok && req.RequestKey != "" {
		if s.params[req.RequestKey] != req.Method {
			return nil, botflota.ErrConflict
		}
		return s.sessions[id], nil
	}
	s.nextID++
	id := fmt.Sprintf("setup-%d", s.nextID)
	now := time.Now()
	session := &botflota.SetupSession{
		Bot:       botflota.BotRef{Scope: "support", Bot: "demo"},
		ID:        id,
		Method:    req.Method,
		State:     botflota.SetupAwaitingInput,
		Revision:  1,
		StartedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
		Step: &botflota.SetupStep{
			Kind: botflota.SetupStepInput,
			Fields: []botflota.SetupField{
				{Name: "token", Kind: botflota.SetupFieldCredential, Label: "Token"},
			},
		},
	}
	s.sessions[id] = session
	if s.keys == nil {
		s.keys = map[string]string{}
		s.params = map[string]string{}
	}
	if req.RequestKey != "" {
		s.keys[req.RequestKey] = id
		s.params[req.RequestKey] = req.Method
	}
	return session, nil
}

func (s *stubSetupRunner) ReadSetup(
	_ context.Context, _, _, sessionID string,
) (*botflota.SetupSession, error) {
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return session, nil
}

func (s *stubSetupRunner) InputSetup(
	_ context.Context, _, _, sessionID string, input botflota.SetupInput,
) (*botflota.SetupSession, error) {
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	if input.Revision != session.Revision {
		return nil, botflota.ErrConflict
	}
	if s.values == nil {
		s.values = map[string]map[string]string{}
	}
	s.values[sessionID] = input.Values
	session.Revision++
	session.UpdatedAt = time.Now()
	session.State = botflota.SetupSucceeded
	session.Step = nil
	return session, nil
}

func (s *stubSetupRunner) CancelSetup(
	_ context.Context, _, _, sessionID string,
) error {
	session, ok := s.sessions[sessionID]
	if !ok {
		return botflota.ErrNotFound
	}
	session.State = botflota.SetupCancelled
	session.UpdatedAt = time.Now()
	return nil
}

type stubSetups struct {
	lifecycle botflota.AccountLifecycle
	runner    botflota.SetupRunner
	err       error
}

func (s *stubSetups) SetupFor(
	scope, bot string,
) (botflota.AccountLifecycle, botflota.SetupRunner, error) {
	if s.err != nil {
		return nil, nil, s.err
	}
	return s.lifecycle, s.runner, nil
}

func testBindingSetup(
	lifecycle *stubBindingLifecycle, runner *stubSetupRunner,
) *stubSetups {
	return &stubSetups{lifecycle: lifecycle, runner: runner}
}

func committedLifecycle() *stubBindingLifecycle {
	return &stubBindingLifecycle{status: botflota.BindingStatus{
		Binding: &botflota.Binding{
			Scope: "support", Bot: "demo",
			Provider: "whatsapp", Adapter: "native", Mode: "native",
			Identity:   botflota.IdentityKey{Provider: "whatsapp", Subject: "opaque-subject"},
			Generation: 1,
			CreatedAt:  testTime,
			UpdatedAt:  testTime,
		},
		Condition: botflota.ReadinessConfigured,
		Reasons:   []string{"transport_unknown"},
	}}
}

func doJSON(
	t *testing.T, handler http.Handler, method, path, token string, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeSetupResponse(t *testing.T, raw []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("decode body: %v: %s", err, raw)
	}
}

func TestBindingInspect(t *testing.T) {
	lifecycle := committedLifecycle()
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) {
			opts.Setups = testBindingSetup(lifecycle,
				&stubSetupRunner{sessions: map[string]*botflota.SetupSession{}})
		}, nil)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/binding", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{
		`"condition":"configured"`, `"provider":"whatsapp"`,
		`"subject":"opaque-subject"`, `"generation":1`,
		`"scope":"support"`, `"bot":"demo"`,
		`"created_at":"2026-09-19T12:00:00Z"`,
		`"updated_at":"2026-09-19T12:00:00Z"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body %s does not contain %s", body, want)
		}
	}
}

func TestBindingInspectUnconfigured(t *testing.T) {
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) {
			opts.Setups = testBindingSetup(&stubBindingLifecycle{status: botflota.BindingStatus{
				Condition: botflota.ReadinessUnconfigured,
			}}, &stubSetupRunner{sessions: map[string]*botflota.SetupSession{}})
		}, nil)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/binding", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"condition":"unconfigured"`) {
		t.Fatalf("body %s does not report unconfigured", body)
	}
}

func TestBindingMethods(t *testing.T) {
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) {
			opts.Setups = testBindingSetup(&stubBindingLifecycle{},
				&stubSetupRunner{
					methods:  []string{"qr", "token"},
					sessions: map[string]*botflota.SetupSession{},
				})
		}, nil)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/binding/methods", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if body := recorder.Body.String(); body != `{"methods":["qr","token"]}`+"\n" {
		t.Fatalf("body = %q, want the declared methods", body)
	}
}

// TestBindingMethodsReportRequirements: discovery names each method and the
// begin parameters it needs, so a client learns the phone requirement without
// provoking a rejected begin (ADR 0053).
func TestBindingMethodsReportRequirements(t *testing.T) {
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) {
			opts.Setups = &stubSetups{
				lifecycle: &stubBindingLifecycle{},
				runner: &discoveryRunner{
					stubSetupRunner: &stubSetupRunner{
						methods:  []string{"qr", "code"},
						sessions: map[string]*botflota.SetupSession{},
					},
					requirements: []botflota.SetupRequirement{
						{Method: "qr"},
						{Method: "code", Params: []string{"phone"}},
					},
				},
			}
		}, nil)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/binding/methods", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	want := `{"methods":["qr","code"],"requirements":{"code":["phone"]}}` + "\n"
	if body := recorder.Body.String(); body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestBindingUnsupportedSetup(t *testing.T) {
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) {
			opts.Setups = &stubSetups{err: fmt.Errorf(
				"fleet: bot %q: %w", "demo", botflota.ErrNoSetup)}
		}, nil)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/scopes/support/bots/demo/binding/methods", ""},
		{"POST", "/api/v1/scopes/support/bots/demo/binding/setups",
			`{"method":"qr","request_key":"k1"}`},
	} {
		recorder := doJSON(t, server.Handler(), tc.method, tc.path, testOwnerToken, tc.body)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s %s status = %d, want 422: %s",
				tc.method, tc.path, recorder.Code, recorder.Body.String())
		}
		if body := recorder.Body.String(); !strings.Contains(body, "unsupported_operation") {
			t.Fatalf("body %s does not report unsupported_operation", body)
		}
	}
}

// TestSetupBeginClassifiesAdapterErrors covers the error contract a real
// adapter reports through: unsupported methods and parameters answer 422
// unsupported_operation, and rejected parameter values answer 400
// invalid_request — never 500 (ADR 0053).
func TestSetupBeginClassifiesAdapterErrors(t *testing.T) {
	runner := &stubSetupRunner{sessions: map[string]*botflota.SetupSession{}}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) {
			opts.Setups = testBindingSetup(&stubBindingLifecycle{}, runner)
		}, nil)
	handler := server.Handler()
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			"unsupported method", fmt.Errorf("fake: no such method: %w",
				botflota.ErrUnsupportedKind),
			http.StatusUnprocessableEntity, "unsupported_operation",
		},
		{
			"unknown parameter", fmt.Errorf("fake: no such parameter: %w",
				botflota.ErrUnsupportedKind),
			http.StatusUnprocessableEntity, "unsupported_operation",
		},
		{
			"rejected value", fmt.Errorf("fake: bad value: %w",
				botflota.ErrInvalidParam),
			http.StatusBadRequest, "invalid_request",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner.beginErr = tc.err
			recorder := doJSON(t, handler, "POST",
				"/api/v1/scopes/support/bots/demo/binding/setups",
				testOwnerToken, `{"method":"qr","request_key":"k9"}`)
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s",
					recorder.Code, tc.status, recorder.Body.String())
			}
			if body := recorder.Body.String(); !strings.Contains(body, tc.code) {
				t.Fatalf("body %s does not report %s", body, tc.code)
			}
		})
	}
}

// TestSetupLifecycle drives begin, read, input, and cancel through the same
// routes, checking stale revisions, unknown IDs, and secret redaction.
func TestSetupLifecycle(t *testing.T) {
	runner := &stubSetupRunner{sessions: map[string]*botflota.SetupSession{}}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) {
			opts.Setups = testBindingSetup(&stubBindingLifecycle{}, runner)
		}, nil)
	handler := server.Handler()

	recorder := doJSON(t, handler, "POST",
		"/api/v1/scopes/support/bots/demo/binding/setups",
		testOwnerToken, `{"method":"token","request_key":"k1"}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("begin status = %d, want 202: %s", recorder.Code, recorder.Body.String())
	}
	if cache := recorder.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cache)
	}
	var begun struct {
		Bot struct {
			Scope string `json:"scope"`
			Bot   string `json:"bot"`
		} `json:"bot"`
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	decodeSetupResponse(t, recorder.Body.Bytes(), &begun)
	if begun.ID == "" || begun.Revision != 1 {
		t.Fatalf("begun = %+v, want an id at revision 1", begun)
	}
	if begun.Bot.Scope != "support" || begun.Bot.Bot != "demo" {
		t.Fatalf("begun bot = %+v, want the scoped bot ref", begun.Bot)
	}

	recorder = get(t, handler,
		"/api/v1/scopes/support/bots/demo/binding/setups/"+begun.ID, testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200", recorder.Code)
	}

	recorder = get(t, handler,
		"/api/v1/scopes/support/bots/demo/binding/setups/missing", testOwnerToken)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown setup status = %d, want 404", recorder.Code)
	}

	recorder = doJSON(t, handler, "POST",
		"/api/v1/scopes/support/bots/demo/binding/setups/"+begun.ID+"/inputs",
		testOwnerToken, `{"revision":9,"request_key":"i1","values":{"token":"super-secret"}}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale input status = %d, want 409", recorder.Code)
	}

	recorder = doJSON(t, handler, "POST",
		"/api/v1/scopes/support/bots/demo/binding/setups/"+begun.ID+"/inputs",
		testOwnerToken, `{"revision":1,"request_key":"i1","values":{"token":"super-secret"}}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("input status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); strings.Contains(body, "super-secret") {
		t.Fatalf("response echoes the submitted secret: %s", body)
	}

	recorder = doJSON(t, handler, "DELETE",
		"/api/v1/scopes/support/bots/demo/binding/setups/"+begun.ID,
		testOwnerToken, "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204", recorder.Code)
	}

	recorder = doJSON(t, handler, "DELETE",
		"/api/v1/scopes/support/bots/demo/binding/setups/missing",
		testOwnerToken, "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cancel unknown status = %d, want 404", recorder.Code)
	}
}

// TestSetupRedactsSubmittedSecretFromLogs proves a submitted credential is
// write-only everywhere: the response body and the server's own log never
// carry it (ADR 0053).
func TestSetupRedactsSubmittedSecretFromLogs(t *testing.T) {
	const secret = "super-secret-credential"
	var logs bytes.Buffer
	runner := &stubSetupRunner{sessions: map[string]*botflota.SetupSession{}}
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) {
			opts.Log = slog.New(slog.NewTextHandler(&logs, nil))
			opts.Setups = testBindingSetup(&stubBindingLifecycle{}, runner)
		}, nil)
	handler := server.Handler()

	recorder := doJSON(t, handler, "POST",
		"/api/v1/scopes/support/bots/demo/binding/setups",
		testOwnerToken, `{"method":"token","request_key":"redact-1"}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("begin status = %d, want 202: %s",
			recorder.Code, recorder.Body.String())
	}
	var begun struct {
		ID string `json:"id"`
	}
	decodeSetupResponse(t, recorder.Body.Bytes(), &begun)

	recorder = doJSON(t, handler, "POST",
		"/api/v1/scopes/support/bots/demo/binding/setups/"+begun.ID+"/inputs",
		testOwnerToken,
		`{"revision":1,"request_key":"redact-2","values":{"token":"`+secret+`"}}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("input status = %d, want 200: %s",
			recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); strings.Contains(body, secret) {
		t.Fatalf("response echoes the submitted secret: %s", body)
	}
	if logged := logs.String(); strings.Contains(logged, secret) {
		t.Fatalf("the server log carries the submitted secret: %s", logged)
	}
}

func TestBindingDetach(t *testing.T) {
	lifecycle := committedLifecycle()
	live := &fakeFleet{}
	server := testServerWithOptions(t, live,
		func(opts *Options) {
			opts.Setups = testBindingSetup(lifecycle,
				&stubSetupRunner{sessions: map[string]*botflota.SetupSession{}})
		}, nil)
	handler := server.Handler()

	recorder := doJSON(t, handler, "DELETE",
		"/api/v1/scopes/support/bots/demo/binding", testOwnerToken, "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("detach status = %d, want 204: %s", recorder.Code, recorder.Body.String())
	}
	if len(live.stopped) != 1 || live.stopped[0] != "support/demo" {
		t.Fatalf("stopped = %v, want the bot stopped before detach", live.stopped)
	}
	if len(lifecycle.detached) != 1 || lifecycle.detached[0] != "support/demo" {
		t.Fatalf("detached = %v, want the committed binding detached", lifecycle.detached)
	}

	recorder = doJSON(t, handler, "DELETE",
		"/api/v1/scopes/support/bots/demo/binding", testOwnerToken, "")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("second detach status = %d, want 404", recorder.Code)
	}
}

func TestBindingCrossScopeIsOpaque(t *testing.T) {
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) {
			opts.Setups = testBindingSetup(committedLifecycle(),
				&stubSetupRunner{sessions: map[string]*botflota.SetupSession{}})
		}, nil)
	handler := server.Handler()
	// testAccessToken is scoped to support; ventas stays indistinguishable
	// from unknown across every binding operation.
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/scopes/ventas/bots/other/binding", ""},
		{"GET", "/api/v1/scopes/ventas/bots/other/binding/methods", ""},
		{"POST", "/api/v1/scopes/ventas/bots/other/binding/setups",
			`{"method":"qr","request_key":"k1"}`},
	} {
		recorder := doJSON(t, handler, tc.method, tc.path, testAccessToken, tc.body)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404: %s",
				tc.method, tc.path, recorder.Code, recorder.Body.String())
		}
	}
}
