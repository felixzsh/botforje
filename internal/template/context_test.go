package template

import (
	"errors"
	"testing"
	"time"
)

func reset(t *testing.T, ctx *Context, root Root, values map[string]any) {
	t.Helper()
	if err := ctx.Reset(root, values); err != nil {
		t.Fatalf("Reset(%s): %v", root, err)
	}
}

func TestGet(t *testing.T) {
	ctx := New()
	reset(t, ctx, RootGraph, map[string]any{"customer": "Ana", "visits": 3})
	reset(t, ctx, RootNode, map[string]any{
		"product": "widget",
		"resp":    map[string]any{"body": map[string]any{"id": "42"}},
		"items":   []any{"a", "b"},
	})
	reset(t, ctx, RootSender, map[string]any{"phone": "+52155"})
	reset(t, ctx, RootMessage, map[string]any{"body": "hola"})
	reset(t, ctx, RootBot, map[string]any{"id": "support-bot"})
	reset(t, ctx, RootInput, map[string]any{"qty": 2})

	cases := []struct {
		name string
		path string
		want any
	}{
		{"graph string", "graph.customer", "Ana"},
		{"graph integer", "graph.visits", 3},
		{"node scalar", "node.product", "widget"},
		{"nested map", "node.resp.body.id", "42"},
		{"slice index", "node.items.1", "b"},
		{"sender", "sender.phone", "+52155"},
		{"message", "message.body", "hola"},
		{"bot", "bot.id", "support-bot"},
		{"input", "input.qty", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ctx.Get(tc.path)
			if err != nil {
				t.Fatalf("Get(%q): %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("Get(%q) = %#v, want %#v", tc.path, got, tc.want)
			}
		})
	}
}

func TestGetMissing(t *testing.T) {
	ctx := New()
	reset(t, ctx, RootGraph, map[string]any{"customer": "Ana"})
	reset(t, ctx, RootNode, map[string]any{"items": []any{"a"}})

	for _, path := range []string{
		"graph.nope",
		"graph.customer.name",
		"node.resp.body.id",
		"node.items.5",
		"node.items.two",
		"input.anything",
	} {
		t.Run(path, func(t *testing.T) {
			if _, err := ctx.Get(path); !errors.Is(err, ErrMissingPath) {
				t.Fatalf("Get(%q) error = %v, want ErrMissingPath", path, err)
			}
		})
	}
}

func TestGetInvalidPath(t *testing.T) {
	ctx := New()
	for _, path := range []string{"graph", "", "graph.", ".graph", "graph.a b"} {
		t.Run(path, func(t *testing.T) {
			_, err := ctx.Get(path)
			if !errors.Is(err, ErrInvalidPath) && !errors.Is(err, ErrUnknownRoot) {
				t.Fatalf("Get(%q) error = %v, want a path error", path, err)
			}
		})
	}
}

func TestGetUnknownRoot(t *testing.T) {
	ctx := New()
	if _, err := ctx.Get("secret.key"); !errors.Is(err, ErrUnknownRoot) {
		t.Fatalf("error = %v, want ErrUnknownRoot", err)
	}
}

func TestSetWritable(t *testing.T) {
	ctx := New()
	if err := ctx.Set("graph.customer", "Ana"); err != nil {
		t.Fatalf("Set graph: %v", err)
	}
	if err := ctx.Set("node.product", "widget"); err != nil {
		t.Fatalf("Set node: %v", err)
	}
	for path, want := range map[string]any{
		"graph.customer": "Ana",
		"node.product":   "widget",
	} {
		got, err := ctx.Get(path)
		if err != nil {
			t.Fatalf("Get(%q): %v", path, err)
		}
		if got != want {
			t.Fatalf("Get(%q) = %#v, want %#v", path, got, want)
		}
	}
}

func TestSetRejectsReadOnly(t *testing.T) {
	ctx := New()
	for _, path := range []string{"sender.phone", "message.body", "bot.id", "input.qty"} {
		t.Run(path, func(t *testing.T) {
			if err := ctx.Set(path, "x"); !errors.Is(err, ErrReadOnly) {
				t.Fatalf("Set(%q) error = %v, want ErrReadOnly", path, err)
			}
		})
	}
}

func TestSetRejectsNestedOrUnknown(t *testing.T) {
	ctx := New()
	for _, path := range []string{"graph.a.b", "unknown.key", "graph", ""} {
		t.Run(path, func(t *testing.T) {
			if err := ctx.Set(path, "x"); err == nil {
				t.Fatalf("Set(%q) succeeded, want error", path)
			}
		})
	}
}

func TestResetNodeClearsPrevious(t *testing.T) {
	ctx := New()
	reset(t, ctx, RootNode, map[string]any{"product": "widget"})
	reset(t, ctx, RootNode, map[string]any{"quantity": 1})
	if _, err := ctx.Get("node.product"); !errors.Is(err, ErrMissingPath) {
		t.Fatalf("node.product survived the reset: %v", err)
	}
	if _, err := ctx.Get("node.quantity"); err != nil {
		t.Fatalf("node.quantity missing after reset: %v", err)
	}
}

func TestResetClonesValues(t *testing.T) {
	initial := map[string]any{"customer": "Ana"}
	ctx := New()
	reset(t, ctx, RootGraph, initial)
	if err := ctx.Set("graph.customer", "Bob"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if initial["customer"] != "Ana" {
		t.Fatalf("Reset aliased the caller map: %v", initial)
	}
}

func TestResetUnknownRoot(t *testing.T) {
	if err := New().Reset("secret", nil); !errors.Is(err, ErrUnknownRoot) {
		t.Fatalf("error = %v, want ErrUnknownRoot", err)
	}
}

func TestSetRequest(t *testing.T) {
	ctx := New()
	err := ctx.SetRequest("lookup", RequestResult{
		Status:   200,
		Headers:  map[string]string{"Authorization": "Bearer x"},
		Body:     map[string]any{"id": "42", "tags": []any{"a", "b"}},
		Duration: 1500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("SetRequest: %v", err)
	}

	cases := []struct {
		path string
		want any
	}{
		{"node.lookup.status", 200},
		{"node.lookup.duration", int64(1500)},
		{"node.lookup.resp.headers.Authorization", "Bearer x"},
		{"node.lookup.resp.body.id", "42"},
		{"node.lookup.resp.body.tags.1", "b"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got, err := ctx.Get(tc.path)
			if err != nil {
				t.Fatalf("Get(%q): %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("Get(%q) = %#v, want %#v", tc.path, got, tc.want)
			}
		})
	}
}

func TestSetRequestRejectsBadName(t *testing.T) {
	if err := New().SetRequest("bad.name", RequestResult{}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("error = %v, want ErrInvalidPath", err)
	}
}
