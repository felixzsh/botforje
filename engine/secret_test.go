package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

// fakeLLM returns fixed text from Realize, so a test controls what a model
// would generate for a message. A non-nil err makes realization fail.
type fakeLLM struct {
	body string
	err  error
}

func (f fakeLLM) Classify(
	context.Context, botflota.ClassifyRequest,
) (botflota.ClassifyResult, error) {
	return botflota.ClassifyResult{}, nil
}

func (f fakeLLM) Realize(context.Context, botflota.RealizeRequest) (string, error) {
	return f.body, f.err
}

func TestCheckNoSecret(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"plain", "hello", false},
		{"literal dollar", "cost: $5", false},
		{"secret", "token ${API_TOKEN}", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkNoSecret(tc.text)
			if (err != nil) != tc.want {
				t.Fatalf("checkNoSecret(%q) = %v, want error=%v", tc.text, err, tc.want)
			}
		})
	}
}

// testRun builds the minimal state a message or location step needs.
func testRun() *run {
	return &run{
		ctx:    context.Background(),
		engine: New(Options{}),
		tctx:   newtemplateContext(),
		msg: botflota.IncomingMessage{
			SenderPhone: "5215500000000", Content: botflota.TextContent{Body: "hi"},
		},
	}
}

func TestMessageRejectsSecret(t *testing.T) {
	r := testRun()
	err := r.message(spec.Message{Body: spec.Text{"token ${API_TOKEN}"}})
	if !errors.Is(err, errSecretInMessage) {
		t.Fatalf("message error = %v, want errSecretInMessage", err)
	}
	if len(r.out) != 0 {
		t.Fatalf("queued %d messages, want none", len(r.out))
	}
}

func TestMessageQueuesPlainBody(t *testing.T) {
	r := testRun()
	if err := r.message(spec.Message{Body: spec.Text{"hi there"}}); err != nil {
		t.Fatalf("message: %v", err)
	}
	if len(r.out) != 1 {
		t.Fatalf("queued %d messages, want one", len(r.out))
	}
}

func TestLocationRejectsSecret(t *testing.T) {
	r := testRun()
	err := r.location(spec.Location{Latitude: 1, Longitude: 2, Name: "${API_TOKEN}"})
	if !errors.Is(err, errSecretInMessage) {
		t.Fatalf("location error = %v, want errSecretInMessage", err)
	}
	if len(r.out) != 0 {
		t.Fatalf("queued %d messages, want none", len(r.out))
	}
}

// TestMediaStepRejectsBadSource covers the engine guard behind config
// validation: a media step needs exactly one source and a known kind.
func TestMediaStepRejectsBadSource(t *testing.T) {
	r := testRun()
	if err := r.media(spec.Media{Kind: "image"}); err == nil {
		t.Fatal("a media step without a source must fail")
	}
	if err := r.media(spec.Media{
		Kind: "image", URL: "https://x", Path: "/tmp/x",
	}); err == nil {
		t.Fatal("a media step with two sources must fail")
	}
	if err := r.media(spec.Media{Kind: "video_note", URL: "https://x"}); err == nil {
		t.Fatal("an unknown media kind must fail")
	}
}

func TestMediaRejectsSecret(t *testing.T) {
	r := testRun()
	err := r.media(spec.Media{Kind: "image", URL: "https://x/${API_TOKEN}"})
	if !errors.Is(err, errSecretInMessage) {
		t.Fatalf("media error = %v, want errSecretInMessage", err)
	}
	if len(r.out) != 0 {
		t.Fatalf("queued %d messages, want none", len(r.out))
	}
}

func TestContactRejectsSecret(t *testing.T) {
	r := testRun()
	err := r.contact(spec.Contact{Name: "Ana", Phone: "${API_TOKEN}"})
	if !errors.Is(err, errSecretInMessage) {
		t.Fatalf("contact error = %v, want errSecretInMessage", err)
	}
	if len(r.out) != 0 {
		t.Fatalf("queued %d messages, want none", len(r.out))
	}
}

func TestPollRejectsSecret(t *testing.T) {
	r := testRun()
	err := r.poll(spec.Poll{Question: "${API_TOKEN}", Options: []string{"a", "b"}})
	if !errors.Is(err, errSecretInMessage) {
		t.Fatalf("poll error = %v, want errSecretInMessage", err)
	}
	if len(r.out) != 0 {
		t.Fatalf("queued %d messages, want none", len(r.out))
	}
}

// TestGeneratedBodyRejectsSecret covers a model tricked into writing a secret
// reference: the guard runs after realization and before the message is
// queued, so nothing leaves (ADR 0014).
func TestGeneratedBodyRejectsSecret(t *testing.T) {
	r := testRun()
	r.engine = New(Options{LLM: fakeLLM{body: "the key is ${API_TOKEN}"}})
	err := r.message(spec.Message{Intent: "greet"})
	if !errors.Is(err, errSecretInMessage) {
		t.Fatalf("message error = %v, want errSecretInMessage", err)
	}
	if len(r.out) != 0 {
		t.Fatalf("queued %d messages, want none", len(r.out))
	}
}

func TestGeneratedBodyQueuesPlainText(t *testing.T) {
	r := testRun()
	r.engine = New(Options{LLM: fakeLLM{body: "hello there"}})
	if err := r.message(spec.Message{Intent: "greet"}); err != nil {
		t.Fatalf("message: %v", err)
	}
	if len(r.out) != 1 || botflota.ContentText(r.out[0].Content) != "hello there" {
		t.Fatalf("out = %#v, want the generated body", r.out)
	}
}

// headerTransport records the headers of one request.
type headerTransport struct{ header http.Header }

func (h *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	h.header = req.Header.Clone()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(strings.NewReader("ok")),
	}, nil
}

// TestRequestHeaderRevealsSecret checks that a secret header reaches the call
// resolved, while an ordinary header stays a template (ADR 0014, ADR 0018).
func TestRequestHeaderRevealsSecret(t *testing.T) {
	transport := &headerTransport{}
	r := testRun()
	if err := r.tctx.Reset(botflota.RootSender, map[string]any{"phone": "52155"}); err != nil {
		t.Fatal(err)
	}
	r.engine = New(Options{HTTPClient: &http.Client{Transport: transport}})
	err := r.request(spec.Request{
		Name: "ping", URL: "http://example.test",
		Headers: map[string]spec.Secret{
			"Authorization": spec.NewSecretRef("Bearer ${API_TOKEN}", "Bearer sk-live"),
			"X-Phone":       spec.NewSecret("{{sender.phone}}"),
		},
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if got := transport.header.Get("Authorization"); got != "Bearer sk-live" {
		t.Fatalf("Authorization = %q, want the revealed value", got)
	}
	if got := transport.header.Get("X-Phone"); got != "52155" {
		t.Fatalf("X-Phone = %q, want the rendered template", got)
	}
}
