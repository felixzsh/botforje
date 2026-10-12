package main

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// TestListBotsReadsTheAPI covers `bot list` as an instance query: it never
// opens the database, it maps what the API answers (ADR 0039).
func TestListBotsReadsTheAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/bots" {
				t.Errorf("path = %q, want /api/v1/bots", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{"bots":[
				{"scope":"support","bot":"demo","phone":"5215500000000",
				 "pairing":"paired","last_change":"2026-09-19T12:00:00Z"},
				{"scope":"ventas","bot":"other","pairing":"not paired"}
			]}`))
		}))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}

	entries, err := listBots(context.Background(), client)
	if err != nil {
		t.Fatalf("listBots: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want two", entries)
	}
	first := entries[0]
	if first.Scope != "support" || first.Bot != "demo" ||
		first.Phone != "5215500000000" || first.Status != "paired" {
		t.Fatalf("first = %#v", first)
	}
	if first.LastChange == nil || !first.LastChange.Equal(testBotListTime) {
		t.Fatalf("last change = %v, want %v", first.LastChange, testBotListTime)
	}
	second := entries[1]
	if second.Status != string(botflota.PairingNotPaired) ||
		second.Phone != "" || second.LastChange != nil {
		t.Fatalf("second = %#v", second)
	}
}

var testBotListTime = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

const (
	bindUnconfigured = `{"binding":null,"condition":"unconfigured"}`
	bindBound        = `{"binding":{"provider":"whatsapp","mode":"native",` +
		`"generation":1,"identity":{"provider":"whatsapp","subject":"opaque-1"}},` +
		`"condition":"configured"}`
	bindSucceededSession = `{"id":"s1","method":"qr","state":"succeeded","revision":2}`
)

func bindTestClient(server *httptest.Server) *apiClient {
	return &apiClient{base: server.URL, token: "tok", http: server.Client()}
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// TestBindThroughAPI covers `bot bind` under a live instance: it drives the
// neutral setup session over HTTP and never opens the database (ADR 0053).
func TestBindThroughAPI(t *testing.T) {
	inspects := 0
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				inspects++
				if inspects == 1 {
					writeJSON(w, http.StatusOK, bindUnconfigured)
				} else {
					writeJSON(w, http.StatusOK, bindBound)
				}
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/methods":
				writeJSON(w, http.StatusOK, `{"methods":["qr"]}`)
			case r.Method == http.MethodPost &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/setups":
				writeJSON(w, http.StatusAccepted, bindSucceededSession)
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
	defer server.Close()

	var out bytes.Buffer
	code := driveBind(context.Background(),
		remoteBind{client: bindTestClient(server)},
		"dev", "alpha", bindOptions{}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	want := `bot "alpha" (scope "dev") is bound as whatsapp/opaque-1`
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output %q is missing %q", out.String(), want)
	}
}

// TestBindThroughAPIInputStep drives an input step: the typed value travels
// toward the instance and never comes back printed.
func TestBindThroughAPIInputStep(t *testing.T) {
	var gotInputs string
	inspects := 0
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				inspects++
				if inspects == 1 {
					writeJSON(w, http.StatusOK, bindUnconfigured)
				} else {
					writeJSON(w, http.StatusOK, bindBound)
				}
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/methods":
				writeJSON(w, http.StatusOK, `{"methods":["token"]}`)
			case r.Method == http.MethodPost &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/setups":
				writeJSON(w, http.StatusAccepted, `{"id":"s1","method":"token",`+
					`"state":"awaiting_input","revision":1,`+
					`"step":{"kind":"input","fields":[`+
					`{"name":"token","kind":"credential","label":"Token"}]}}`)
			case r.Method == http.MethodPost &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/setups/s1/inputs":
				raw := make([]byte, r.ContentLength)
				_, _ = r.Body.Read(raw)
				gotInputs = string(raw)
				writeJSON(w, http.StatusOK, bindSucceededSession)
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
	defer server.Close()

	var out bytes.Buffer
	code := driveBind(context.Background(),
		remoteBind{client: bindTestClient(server)},
		"dev", "alpha",
		bindOptions{requestKey: "k1"},
		strings.NewReader("super-secret\n"), &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(gotInputs, `"token":"super-secret"`) {
		t.Fatalf("inputs request %q did not carry the typed value", gotInputs)
	}
	if strings.Contains(out.String(), "super-secret") {
		t.Fatalf("output %q echoes the submitted secret", out.String())
	}
	if !strings.Contains(out.String(), "is bound as whatsapp/opaque-1") {
		t.Fatalf("output %q is missing the bound report", out.String())
	}
}

// TestBindThroughAPIReportsABoundBot covers the idempotent answer: a bot the
// instance already knows is reported, not bound again.
func TestBindThroughAPIReportsABoundBot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/scopes/dev/bots/alpha/binding" {
				t.Errorf("path = %s, want only the binding inspect", r.URL.Path)
			}
			writeJSON(w, http.StatusOK, bindBound)
		}))
	defer server.Close()

	var out bytes.Buffer
	code := driveBind(context.Background(),
		remoteBind{client: bindTestClient(server)},
		"dev", "alpha", bindOptions{}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "is already bound as whatsapp/opaque-1") {
		t.Fatalf("output = %q", out.String())
	}
}

// TestBindThroughAPINeedsAMethod: several advertised methods with no
// --method is an error, never a silent pick.
func TestBindThroughAPINeedsAMethod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				writeJSON(w, http.StatusOK, bindUnconfigured)
			case r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/methods":
				writeJSON(w, http.StatusOK, `{"methods":["qr","token"]}`)
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
	defer server.Close()

	var out bytes.Buffer
	code := driveBind(context.Background(),
		remoteBind{client: bindTestClient(server)},
		"dev", "alpha", bindOptions{}, strings.NewReader(""), &out)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

// TestBindThroughAPIForceDetachesFirst: replacing a binding under a live
// instance is a detach followed by a new session — no state needs a stopped
// fleet (ADR 0053).
func TestBindThroughAPIForceDetachesFirst(t *testing.T) {
	deletes := 0
	inspects := 0
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				inspects++
				writeJSON(w, http.StatusOK, bindBound)
			case r.Method == http.MethodDelete &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				deletes++
				w.WriteHeader(http.StatusNoContent)
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/methods":
				writeJSON(w, http.StatusOK, `{"methods":["qr"]}`)
			case r.Method == http.MethodPost &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/setups":
				writeJSON(w, http.StatusAccepted, bindSucceededSession)
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
	defer server.Close()

	var out bytes.Buffer
	code := driveBind(context.Background(),
		remoteBind{client: bindTestClient(server)},
		"dev", "alpha", bindOptions{force: true}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if deletes != 1 || inspects != 2 {
		t.Fatalf("deletes = %d inspects = %d, want the detach-then-bind", deletes, inspects)
	}
	if !strings.Contains(out.String(), "is bound as whatsapp/opaque-1") {
		t.Fatalf("output = %q", out.String())
	}
}

// TestUnbindThroughAPI drives the running instance: the delete hits the
// binding route and its answer becomes the command's result (ADR 0053).
func TestUnbindThroughAPI(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
	defer server.Close()
	clearAPIEnv(t)
	t.Setenv("BOTFLOTA_API_URL", server.URL)
	t.Setenv("BOTFLOTA_TOKEN", "tok")

	var out bytes.Buffer
	code := unbindThroughAPI(&out, &config.Config{DataDir: t.TempDir()},
		"support", "demo")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if gotMethod != http.MethodDelete ||
		gotPath != "/api/v1/scopes/support/bots/demo/binding" {
		t.Fatalf("request = %s %s, want the binding route", gotMethod, gotPath)
	}
	want := `bot "demo" (scope "support") unbound`
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output %q is missing %q", out.String(), want)
	}
}

// TestUnbindThroughAPIReportsTheFailure: a refused delete is an error, never
// a success line on stdout.
func TestUnbindThroughAPIReportsTheFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(
				`{"error":{"code":"not_found","message":"no committed binding"}}`))
		}))
	defer server.Close()
	clearAPIEnv(t)
	t.Setenv("BOTFLOTA_API_URL", server.URL)
	t.Setenv("BOTFLOTA_TOKEN", "tok")

	var out bytes.Buffer
	code := unbindThroughAPI(&out, &config.Config{DataDir: t.TempDir()},
		"support", "demo")
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q, want nothing on stdout", out.String())
	}
}

// TestBindReauthorizesWithoutDetaching covers the readiness path: a binding
// that needs its owner enters a new setup session while the committed
// association stays put — no detach, no replacement (ADR 0053).
func TestBindReauthorizesWithoutDetaching(t *testing.T) {
	deletes := 0
	inspects := 0
	reauth := `{"binding":{"provider":"whatsapp","mode":"native",` +
		`"generation":1,"identity":{"provider":"whatsapp","subject":"opaque-1"}},` +
		`"condition":"action_required","method":"qr"}`
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				inspects++
				if inspects == 1 {
					writeJSON(w, http.StatusOK, reauth)
				} else {
					writeJSON(w, http.StatusOK, bindBound)
				}
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/methods":
				writeJSON(w, http.StatusOK, `{"methods":["qr"]}`)
			case r.Method == http.MethodPost &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/setups":
				writeJSON(w, http.StatusAccepted, bindSucceededSession)
			case r.Method == http.MethodDelete &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				deletes++
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
	defer server.Close()

	var out bytes.Buffer
	code := driveBind(context.Background(),
		remoteBind{client: bindTestClient(server)},
		"dev", "alpha", bindOptions{}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if deletes != 0 {
		t.Fatalf("deletes = %d, want reauthorization without detach", deletes)
	}
	for _, want := range []string{
		"reauthorizing without detaching",
		`is bound as whatsapp/opaque-1`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output %q is missing %q", out.String(), want)
		}
	}
}

// TestBindForceAmbiguousMethodKeepsBinding covers the ordering rule: the
// method resolves before any detach, so an argument error never alters
// durable state (ADR 0053).
func TestBindForceAmbiguousMethodKeepsBinding(t *testing.T) {
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				writeJSON(w, http.StatusOK, bindBound)
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/methods":
				writeJSON(w, http.StatusOK, `{"methods":["qr","token"]}`)
			case r.Method == http.MethodDelete &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				deletes++
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
	defer server.Close()

	var out bytes.Buffer
	code := driveBind(context.Background(),
		remoteBind{client: bindTestClient(server)},
		"dev", "alpha", bindOptions{force: true}, strings.NewReader(""), &out)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if deletes != 0 {
		t.Fatalf("deletes = %d, want the binding kept on argument error", deletes)
	}
}

type stubOfflineLifecycle struct {
	status   botflota.BindingStatus
	detached int
}

func (s *stubOfflineLifecycle) InspectBinding(
	_ context.Context, _, _ string,
) (botflota.BindingStatus, error) {
	return s.status, nil
}

func (s *stubOfflineLifecycle) DetachBinding(
	_ context.Context, _, _ string,
) error {
	s.detached++
	s.status.Binding = nil
	return nil
}

type stubOfflineRunner struct {
	session *botflota.SetupSession
	began   int
}

func (s *stubOfflineRunner) SetupMethods(
	_ context.Context, _, _ string,
) ([]string, error) {
	return []string{"token"}, nil
}

func (s *stubOfflineRunner) BeginSetup(
	_ context.Context, _, _ string, req botflota.SetupBegin,
) (*botflota.SetupSession, error) {
	s.began++
	s.session.Method = req.Method
	return s.session, nil
}

func (s *stubOfflineRunner) ReadSetup(
	_ context.Context, _, _, _ string,
) (*botflota.SetupSession, error) {
	return s.session, nil
}

func (s *stubOfflineRunner) InputSetup(
	_ context.Context, _, _, _ string, _ botflota.SetupInput,
) (*botflota.SetupSession, error) {
	return s.session, nil
}

func (s *stubOfflineRunner) CancelSetup(
	_ context.Context, _, _, _ string,
) error {
	return nil
}

// TestResolveOfflineSetupUsesTheInstalledDefinition covers the offline path
// without any device store: a non-WhatsApp definition resolves through the
// catalog and drives the same session contract (ADR 0052, ADR 0053).
func TestResolveOfflineSetupUsesTheInstalledDefinition(t *testing.T) {
	lifecycle := &stubOfflineLifecycle{}
	runner := &stubOfflineRunner{session: &botflota.SetupSession{
		Bot:      botflota.BotRef{Scope: "dev", Bot: "alpha"},
		ID:       "s1",
		State:    botflota.SetupSucceeded,
		Revision: 1,
	}}
	catalog, err := channels.New(channels.Definition{
		Provider: "fake",
		Kind:     "fake",
		Modes:    []string{"test"},
		Decode: func(spec.Bot) (any, []string) {
			return nil, nil
		},
		Setup: func(channels.Bind) (botflota.AccountLifecycle, botflota.SetupRunner, error) {
			return lifecycle, runner, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	botSpec := spec.Bot{Channel: spec.Channel{Provider: "fake", Mode: "test"}}
	gotLifecycle, gotRunner, err := resolveOfflineSetup(
		catalog, "dev", "alpha", botSpec, botflota.DiscardLogger())
	if err != nil {
		t.Fatalf("resolveOfflineSetup: %v", err)
	}
	var out bytes.Buffer
	code := driveBind(context.Background(),
		directBind{lifecycle: gotLifecycle, runner: gotRunner},
		"dev", "alpha", bindOptions{}, strings.NewReader(""), &out)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if runner.began != 1 {
		t.Fatalf("began = %d, want the fake runner's session", runner.began)
	}
	if !strings.Contains(out.String(), "setup succeeded") {
		t.Fatalf("output %q is missing the success report", out.String())
	}
}

// TestSecretFieldKind pins which input kinds read without terminal echo:
// write-only values stay off the screen, identifiers stay visible.
func TestSecretFieldKind(t *testing.T) {
	for _, kind := range []string{
		botflota.SetupFieldCredential, botflota.SetupFieldPassword, botflota.SetupFieldCode,
	} {
		if !secretFieldKind(kind) {
			t.Fatalf("kind %q reads with echo, want no-echo", kind)
		}
	}
	for _, kind := range []string{botflota.SetupFieldIdentifier, "", "other"} {
		if secretFieldKind(kind) {
			t.Fatalf("kind %q reads without echo, want visible input", kind)
		}
	}
}

// TestPromptValueFallsBackOffTerminal covers the pipe path: without a
// terminal on stdin the value still reads from the plain reader, secrets
// included, so automation and tests keep working.
func TestPromptValueFallsBackOffTerminal(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer reader.Close()
	defer writer.Close()
	go func() {
		_, _ = writer.WriteString("piped-secret\n")
	}()
	var out bytes.Buffer
	value, ok := promptValue("Token", botflota.SetupFieldCredential,
		bufio.NewReader(reader), reader, &out)
	if !ok {
		t.Fatal("promptValue failed on a pipe")
	}
	if value != "piped-secret" {
		t.Fatalf("value = %q, want the piped input", value)
	}
}

// TestBindForceUnsupportedMethodKeepsBinding covers the validation order:
// an explicit method the channel does not offer fails before any detach,
// so --force can never drop a binding a rejected begin would not replace
// (ADR 0053).
func TestBindForceUnsupportedMethodKeepsBinding(t *testing.T) {
	deletes, begins := 0, 0
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				writeJSON(w, http.StatusOK, bindBound)
			case r.Method == http.MethodGet &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/methods":
				writeJSON(w, http.StatusOK, `{"methods":["qr"]}`)
			case r.Method == http.MethodDelete &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding":
				deletes++
				w.WriteHeader(http.StatusNoContent)
			case r.Method == http.MethodPost &&
				r.URL.Path == "/api/v1/scopes/dev/bots/alpha/binding/setups":
				begins++
				writeJSON(w, http.StatusAccepted, bindSucceededSession)
			default:
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
	defer server.Close()

	var out bytes.Buffer
	code := driveBind(context.Background(),
		remoteBind{client: bindTestClient(server)},
		"dev", "alpha",
		bindOptions{force: true, method: "carrier-pigeon"},
		strings.NewReader(""), &out)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if deletes != 0 {
		t.Fatalf("deletes = %d, want no detach on argument error", deletes)
	}
	if begins != 0 {
		t.Fatalf("begins = %d, want no begin on argument error", begins)
	}
}

func fakeOfflineAssemblies(
	lifecycle *stubOfflineLifecycle, runner *stubOfflineRunner, opened *bool,
) []offlineAssembly {
	return []offlineAssembly{{
		provider: "fake", mode: "test",
		open: func(_ *config.Config, _ *sqlite.DB, _ spec.Bot,
			_ *slog.Logger,
		) (*channels.Catalog, func(), error) {
			*opened = true
			catalog, err := channels.New(channels.Definition{
				Provider: "fake",
				Kind:     "fake",
				Modes:    []string{"test"},
				Decode: func(spec.Bot) (any, []string) {
					return nil, nil
				},
				Setup: func(channels.Bind) (
					botflota.AccountLifecycle, botflota.SetupRunner, error,
				) {
					return lifecycle, runner, nil
				},
			})
			return catalog, func() {}, err
		},
	}}
}

// TestBindOfflineUsesTheRegisteredAssembly drives the real offline bind
// through the assembly registry: a non-WhatsApp channel assembles and
// binds with no device store anywhere in the path (ADR 0052, ADR 0053).
func TestBindOfflineUsesTheRegisteredAssembly(t *testing.T) {
	lifecycle := &stubOfflineLifecycle{}
	runner := &stubOfflineRunner{session: &botflota.SetupSession{
		Bot:      botflota.BotRef{Scope: "dev", Bot: "alpha"},
		ID:       "s1",
		State:    botflota.SetupSucceeded,
		Revision: 1,
	}}
	var opened bool
	cfg := &config.Config{DataDir: t.TempDir()}
	botSpec := spec.Bot{Channel: spec.Channel{Provider: "fake", Mode: "test"}}
	var out bytes.Buffer
	code := bindOffline(&out, strings.NewReader(""), cfg,
		"dev", "alpha", botSpec, bindOptions{}, false,
		fakeOfflineAssemblies(lifecycle, runner, &opened))
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !opened {
		t.Fatal("the registered fake assembly was not selected")
	}
	if runner.began != 1 {
		t.Fatalf("began = %d, want the fake runner's session", runner.began)
	}
	if lifecycle.detached != 0 {
		t.Fatalf("detached = %d, want a fresh bind without detach", lifecycle.detached)
	}
	if !strings.Contains(out.String(), "setup succeeded") {
		t.Fatalf("output %q is missing the success report", out.String())
	}
}

// TestUnbindOfflineUsesTheRegisteredAssembly drives the real offline
// unbind through the assembly registry: detachment settles through the
// installed lifecycle, never a hardcoded adapter (ADR 0053).
func TestUnbindOfflineUsesTheRegisteredAssembly(t *testing.T) {
	lifecycle := &stubOfflineLifecycle{status: botflota.BindingStatus{
		Binding: &botflota.Binding{
			Scope: "dev", Bot: "alpha",
			Provider: "fake", Adapter: "fake", Mode: "test",
			Identity: botflota.IdentityKey{Provider: "fake", Subject: "s"},
		},
		Condition: botflota.ReadinessConfigured,
	}}
	runner := &stubOfflineRunner{}
	var opened bool
	cfg := &config.Config{DataDir: t.TempDir()}
	botSpec := spec.Bot{Channel: spec.Channel{Provider: "fake", Mode: "test"}}
	var out bytes.Buffer
	code := unbindOffline(&out, cfg, "dev", "alpha", botSpec,
		fakeOfflineAssemblies(lifecycle, runner, &opened))
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !opened {
		t.Fatal("the registered fake assembly was not selected")
	}
	if lifecycle.detached != 1 {
		t.Fatalf("detached = %d, want one detach", lifecycle.detached)
	}
	if !strings.Contains(out.String(), `unbound`) {
		t.Fatalf("output %q is missing the unbound report", out.String())
	}
}

// TestBindOfflineRejectsAnUninstalledChannel covers the fallback: without a
// registered assembly nothing opens, and resolution names the missing
// installation instead of inventing state (ADR 0052).
func TestBindOfflineRejectsAnUninstalledChannel(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	botSpec := spec.Bot{Channel: spec.Channel{Provider: "ghost", Mode: "test"}}
	var out bytes.Buffer
	code := bindOffline(&out, strings.NewReader(""), cfg,
		"dev", "alpha", botSpec, bindOptions{}, false, nil)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}
