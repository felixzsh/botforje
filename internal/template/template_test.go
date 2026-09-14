package template

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testContext(t *testing.T) *Context {
	t.Helper()
	ctx := New()
	for _, seed := range []struct {
		root   Root
		values map[string]any
	}{
		{RootGraph, map[string]any{"customer": "Ana", "visits": 3}},
		{RootNode, map[string]any{"product": "widget"}},
		{RootSender, map[string]any{"phone": "+52155", "name": "Ana"}},
		{RootMessage, map[string]any{"body": "hola"}},
		{RootBot, map[string]any{"id": "support-bot"}},
		{RootInput, map[string]any{"qty": 2}},
	} {
		if err := ctx.Reset(seed.root, seed.values); err != nil {
			t.Fatalf("Reset(%s): %v", seed.root, err)
		}
	}
	return ctx
}

func TestRender(t *testing.T) {
	ctx := testContext(t)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{"single", "Hi {{graph.customer}}", "Hi Ana"},
		{"multiple", "{{graph.customer}} ordered {{input.qty}}", "Ana ordered 2"},
		{"adjacent", "{{graph.customer}}{{message.body}}", "Anahola"},
		{"spaces inside braces", "{{ graph.customer }}", "Ana"},
		{"integer", "{{graph.visits}} visits", "3 visits"},
		{"all roots",
			"{{bot.id}}/{{sender.phone}}/{{node.product}}", "support-bot/+52155/widget"},
		{"escaped braces", `\{{graph.customer}}`, "{{graph.customer}}"},
		{"escaped backslash", `\\{{graph.customer}}`, `\Ana`},
		{"literal close braces", "a}}b", "a}}b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(tc.in, ctx)
			if err != nil {
				t.Fatalf("Render(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("Render(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderMissingPath(t *testing.T) {
	ctx := testContext(t)
	_, err := Render("Hi {{graph.nope}}", ctx)
	if err == nil {
		t.Fatal("want an error for the missing path")
	}
	if !errors.Is(err, ErrMissingPath) {
		t.Fatalf("error = %v, want ErrMissingPath", err)
	}
	if !strings.Contains(err.Error(), "graph.nope") {
		t.Fatalf("error %v does not name the path", err)
	}
}

func TestRenderNonScalar(t *testing.T) {
	ctx := New()
	if err := ctx.Reset(RootGraph, map[string]any{"obj": map[string]any{"a": 1}}); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := Render("{{graph.obj}}", ctx); err == nil {
		t.Fatal("want an error rendering a map")
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{
		"{{}}",
		"{{ }}",
		"{{graph}}",
		"{{unknown.x}}",
		"{{graph.a b}}",
		"open {{graph.x",
	} {
		t.Run(in, func(t *testing.T) {
			if _, err := Parse(in); err == nil {
				t.Fatalf("Parse(%q) succeeded, want error", in)
			}
		})
	}
}

func TestTemplateReuse(t *testing.T) {
	tmpl, err := Parse("{{graph.customer}}")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := New()
	if err := ctx.Reset(RootGraph, map[string]any{"customer": "Ana"}); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	got, err := tmpl.Render(ctx)
	if err != nil || got != "Ana" {
		t.Fatalf("first render = %q, %v", got, err)
	}
	if err := ctx.Reset(RootGraph, map[string]any{"customer": "Bob"}); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	got, err = tmpl.Render(ctx)
	if err != nil || got != "Bob" {
		t.Fatalf("second render = %q, %v", got, err)
	}
}

// TestMessageStepRenders mirrors a message step whose body references a
// captured request result (Phase 2 gate).
func TestMessageStepRenders(t *testing.T) {
	ctx := testContext(t)
	err := ctx.SetRequest("lead-capture", RequestResult{
		Status:   201,
		Body:     map[string]any{"id": 7},
		Duration: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("SetRequest: %v", err)
	}
	got, err := Render("Thanks! We created lead {{node.lead-capture.resp.body.id}}.", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "Thanks! We created lead 7."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
