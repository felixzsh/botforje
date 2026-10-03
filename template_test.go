package botflota

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

var errNoValue = errors.New("no value")

func testResolver(values map[string]any) func(string) (any, error) {
	return func(path string) (any, error) {
		value, ok := values[path]
		if !ok {
			return nil, fmt.Errorf("%w: %q", errNoValue, path)
		}
		return value, nil
	}
}

func TestRenderTemplate(t *testing.T) {
	resolve := testResolver(map[string]any{
		"graph.customer": "Ana",
		"graph.visits":   3,
		"node.product":   "widget",
		"contact.phone":  "+52155",
		"message.body":   "hola",
		"bot.id":         "support-bot",
		"input.qty":      2,
	})
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
			"{{bot.id}}/{{contact.phone}}/{{node.product}}", "support-bot/+52155/widget"},
		{"escaped braces", `\{{graph.customer}}`, "{{graph.customer}}"},
		{"escaped backslash", `\\{{graph.customer}}`, `\Ana`},
		{"literal close braces", "a}}b", "a}}b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RenderTemplate(tc.in, resolve)
			if err != nil {
				t.Fatalf("RenderTemplate(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("RenderTemplate(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderPropagatesResolverError(t *testing.T) {
	_, err := RenderTemplate("Hi {{graph.nope}}", testResolver(nil))
	if !errors.Is(err, errNoValue) {
		t.Fatalf("error = %v, want errNoValue", err)
	}
}

func TestRenderNonScalar(t *testing.T) {
	resolve := testResolver(map[string]any{"graph.obj": map[string]any{"a": 1}})
	if _, err := RenderTemplate("{{graph.obj}}", resolve); err == nil {
		t.Fatal("want an error rendering a map")
	}
}

func TestParseTemplateErrors(t *testing.T) {
	for _, in := range []string{
		"{{}}",
		"{{ }}",
		"{{graph}}",
		"{{unknown.x}}",
		"{{graph.a b}}",
		"open {{graph.x",
	} {
		t.Run(in, func(t *testing.T) {
			if _, err := ParseTemplate(in); err == nil {
				t.Fatalf("ParseTemplate(%q) succeeded, want error", in)
			}
		})
	}
}

func TestTemplateReuse(t *testing.T) {
	tmpl, err := ParseTemplate("{{graph.customer}}")
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	name := "Ana"
	resolve := func(string) (any, error) { return name, nil }
	if got, err := tmpl.Render(resolve); err != nil || got != "Ana" {
		t.Fatalf("first render = %q, %v", got, err)
	}
	name = "Bob"
	if got, err := tmpl.Render(resolve); err != nil || got != "Bob" {
		t.Fatalf("second render = %q, %v", got, err)
	}
}

func TestTemplateReferences(t *testing.T) {
	tmpl, err := ParseTemplate("Hi {{graph.customer}}, {{ input.qty }} items")
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	want := []string{"graph.customer", "input.qty"}
	if got := tmpl.References(); !slices.Equal(got, want) {
		t.Fatalf("References() = %q, want %q", got, want)
	}
}

func TestParseTemplatePath(t *testing.T) {
	root, segments, err := ParseTemplatePath("node.lead-capture.resp.body")
	if err != nil {
		t.Fatalf("ParseTemplatePath: %v", err)
	}
	if root != RootNode {
		t.Fatalf("root = %q, want %q", root, RootNode)
	}
	if want := []string{"lead-capture", "resp", "body"}; !slices.Equal(segments, want) {
		t.Fatalf("segments = %q, want %q", segments, want)
	}
	for _, path := range []string{"graph", "", "graph.", ".graph", "graph.a b", "secret.a"} {
		t.Run(path, func(t *testing.T) {
			if _, _, err := ParseTemplatePath(path); err == nil {
				t.Fatalf("ParseTemplatePath(%q) succeeded, want error", path)
			}
		})
	}
}

func TestValidSegment(t *testing.T) {
	for _, segment := range []string{"a", "customer_name", "lead-capture", "qty2"} {
		if !ValidSegment(segment) {
			t.Fatalf("ValidSegment(%q) = false, want true", segment)
		}
	}
	for _, segment := range []string{"", "a.b", "a b", "a/b"} {
		if ValidSegment(segment) {
			t.Fatalf("ValidSegment(%q) = true, want false", segment)
		}
	}
}
