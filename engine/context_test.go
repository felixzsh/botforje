package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

func reset(t *testing.T, ctx *templateContext, root botflota.Root, values map[string]any) {
	t.Helper()
	if err := ctx.Reset(root, values); err != nil {
		t.Fatalf("Reset(%s): %v", root, err)
	}
}

func testContext(t *testing.T) *templateContext {
	t.Helper()
	ctx := newtemplateContext()
	for _, seed := range []struct {
		root   botflota.Root
		values map[string]any
	}{
		{botflota.RootGraph, map[string]any{"customer": "Ana", "visits": 3}},
		{botflota.RootNode, map[string]any{"product": "widget"}},
		{botflota.RootPeer, map[string]any{"subject": "+52155", "name": "Ana"}},
		{botflota.RootMessage, map[string]any{"body": "hola"}},
		{botflota.RootBot, map[string]any{"id": "support-bot"}},
		{botflota.RootInput, map[string]any{"qty": 2}},
	} {
		reset(t, ctx, seed.root, seed.values)
	}
	return ctx
}

func TestGet(t *testing.T) {
	ctx := newtemplateContext()
	reset(t, ctx, botflota.RootGraph, map[string]any{"customer": "Ana", "visits": 3})
	reset(t, ctx, botflota.RootNode, map[string]any{
		"product": "widget",
		"resp":    map[string]any{"body": map[string]any{"id": "42"}},
		"items":   []any{"a", "b"},
	})
	reset(t, ctx, botflota.RootPeer, map[string]any{"subject": "+52155"})
	reset(t, ctx, botflota.RootMessage, map[string]any{"body": "hola"})
	reset(t, ctx, botflota.RootBot, map[string]any{"id": "support-bot"})
	reset(t, ctx, botflota.RootInput, map[string]any{"qty": 2})

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
		{"peer", "peer.subject", "+52155"},
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
	ctx := newtemplateContext()
	reset(t, ctx, botflota.RootGraph, map[string]any{"customer": "Ana"})
	reset(t, ctx, botflota.RootNode, map[string]any{"items": []any{"a"}})

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
	ctx := newtemplateContext()
	for _, path := range []string{"graph", "", "graph.", ".graph", "graph.a b"} {
		t.Run(path, func(t *testing.T) {
			_, err := ctx.Get(path)
			if !errors.Is(err, botflota.ErrInvalidPath) &&
				!errors.Is(err, botflota.ErrUnknownRoot) {
				t.Fatalf("Get(%q) error = %v, want a path error", path, err)
			}
		})
	}
}

func TestGetUnknownRoot(t *testing.T) {
	ctx := newtemplateContext()
	if _, err := ctx.Get("secret.key"); !errors.Is(err, botflota.ErrUnknownRoot) {
		t.Fatalf("error = %v, want ErrUnknownRoot", err)
	}
}

func TestSetWritable(t *testing.T) {
	ctx := newtemplateContext()
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
	ctx := newtemplateContext()
	for _, path := range []string{"peer.subject", "message.body", "bot.id", "input.qty"} {
		t.Run(path, func(t *testing.T) {
			if err := ctx.Set(path, "x"); !errors.Is(err, ErrReadOnly) {
				t.Fatalf("Set(%q) error = %v, want ErrReadOnly", path, err)
			}
		})
	}
}

func TestSetRejectsNestedOrUnknown(t *testing.T) {
	ctx := newtemplateContext()
	for _, path := range []string{"graph.a.b", "unknown.key", "graph", ""} {
		t.Run(path, func(t *testing.T) {
			if err := ctx.Set(path, "x"); err == nil {
				t.Fatalf("Set(%q) succeeded, want error", path)
			}
		})
	}
}

func TestResetNodeClearsPrevious(t *testing.T) {
	ctx := newtemplateContext()
	reset(t, ctx, botflota.RootNode, map[string]any{"product": "widget"})
	reset(t, ctx, botflota.RootNode, map[string]any{"quantity": 1})
	if _, err := ctx.Get("node.product"); !errors.Is(err, ErrMissingPath) {
		t.Fatalf("node.product survived the reset: %v", err)
	}
	if _, err := ctx.Get("node.quantity"); err != nil {
		t.Fatalf("node.quantity missing after reset: %v", err)
	}
}

func TestResetClonesValues(t *testing.T) {
	initial := map[string]any{"customer": "Ana"}
	ctx := newtemplateContext()
	reset(t, ctx, botflota.RootGraph, initial)
	if err := ctx.Set("graph.customer", "Bob"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if initial["customer"] != "Ana" {
		t.Fatalf("Reset aliased the caller map: %v", initial)
	}
}

func TestResetUnknownRoot(t *testing.T) {
	if err := newtemplateContext().Reset("secret", nil); !errors.Is(
		err, botflota.ErrUnknownRoot,
	) {
		t.Fatalf("error = %v, want ErrUnknownRoot", err)
	}
}

func TestSetRequest(t *testing.T) {
	ctx := newtemplateContext()
	err := ctx.SetRequest("lookup", requestResult{
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
	if err := newtemplateContext().SetRequest("bad.name", requestResult{}); !errors.Is(
		err, botflota.ErrInvalidPath,
	) {
		t.Fatalf("error = %v, want ErrInvalidPath", err)
	}
}

func TestMessageStepRenders(t *testing.T) {
	ctx := testContext(t)
	err := ctx.SetRequest("lead-capture", requestResult{
		Status:   201,
		Body:     map[string]any{"id": 7},
		Duration: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("SetRequest: %v", err)
	}
	got, err := botflota.RenderTemplate(
		"Thanks! We created lead {{node.lead-capture.resp.body.id}}.", ctx.Get)
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	want := "Thanks! We created lead 7."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
