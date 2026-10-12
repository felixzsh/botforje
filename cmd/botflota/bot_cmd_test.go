package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
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
