package engine

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
	"time"

	"github.com/felixzsh/botflota"
)

var (
	// ErrMissingPath reports a reference whose value is not set.
	ErrMissingPath = errors.New("missing path")
	// ErrReadOnly reports a write to a read-only root.
	ErrReadOnly = errors.New("read-only root")
)

// writable reports whether the engine lets steps write the root. Graph and
// node variables are read-write (ADR 0033); the rest are fixed by the engine
// for the duration of a message.
func writable(root botflota.Root) bool {
	return root == botflota.RootGraph || root == botflota.RootNode
}

// templateContext holds the values a template reads, grouped by root. It is
// not safe for concurrent use; one bot actor owns it (docs/architecture.md).
type templateContext struct {
	values map[botflota.Root]map[string]any
}

// newtemplateContext returns an empty context with every root ready to use.
func newtemplateContext() *templateContext {
	c := &templateContext{values: map[botflota.Root]map[string]any{}}
	for _, root := range []botflota.Root{
		botflota.RootGraph, botflota.RootNode, botflota.RootSender,
		botflota.RootMessage, botflota.RootBot, botflota.RootInput,
	} {
		c.values[root] = map[string]any{}
	}
	return c
}

// Reset replaces every value of one root. The engine uses it to seed a session
// (graph), a node entry (node), a message (sender, message, bot), and a tool
// call (input). The values are cloned, so later writes never mutate the config
// that produced them.
func (c *templateContext) Reset(root botflota.Root, values map[string]any) error {
	if !root.Known() {
		return fmt.Errorf("%w: %q", botflota.ErrUnknownRoot, root)
	}
	clone := maps.Clone(values)
	if clone == nil {
		clone = map[string]any{}
	}
	c.values[root] = clone
	return nil
}

// Set writes one graph or node variable. Bind targets are exactly one level
// deep (ADR 0033); read-only roots reject the write.
func (c *templateContext) Set(path string, value any) error {
	root, segments, err := botflota.ParseTemplatePath(path)
	if err != nil {
		return err
	}
	if !writable(root) {
		return fmt.Errorf("%w: %q", ErrReadOnly, root)
	}
	if len(segments) != 1 {
		return fmt.Errorf("%w: %q", botflota.ErrInvalidPath, path)
	}
	c.values[root][segments[0]] = value
	return nil
}

// Get resolves path to its value. A step that references an unset value is an
// error, never an empty string (ADR 0018).
func (c *templateContext) Get(path string) (any, error) {
	root, segments, err := botflota.ParseTemplatePath(path)
	if err != nil {
		return nil, err
	}
	current := any(c.values[root])
	for _, segment := range segments {
		current, err = step(current, segment)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrMissingPath, path)
		}
	}
	return current, nil
}

func step(value any, segment string) (any, error) {
	switch node := value.(type) {
	case map[string]any:
		child, ok := node[segment]
		if !ok {
			return nil, ErrMissingPath
		}
		return child, nil
	case []any:
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 || index >= len(node) {
			return nil, ErrMissingPath
		}
		return node[index], nil
	default:
		return nil, ErrMissingPath
	}
}

// Values returns the live map behind a root so the engine can persist it back
// after running a step. The caller must not mutate it once the context is
// reused.
func (c *templateContext) Values(root botflota.Root) map[string]any {
	if !root.Known() {
		return nil
	}
	return c.values[root]
}

// requestResult is the captured outcome of a request step. Phase 4 fills it in
// when it runs the HTTP call.
type requestResult struct {
	Status   int
	Headers  map[string]string
	Body     any
	Duration time.Duration
}

// SetRequest stores a captured request under the node root, addressable as
// {{node.<name>.resp.body.*}}, .resp.headers.*, .status, and .duration.
func (c *templateContext) SetRequest(name string, result requestResult) error {
	if !botflota.ValidSegment(name) {
		return fmt.Errorf("%w: request name %q", botflota.ErrInvalidPath, name)
	}
	c.values[botflota.RootNode][name] = result.value()
	return nil
}

func (r requestResult) value() map[string]any {
	headers := make(map[string]any, len(r.Headers))
	for name, value := range r.Headers {
		headers[name] = value
	}
	return map[string]any{
		"status":   r.Status,
		"duration": r.Duration.Milliseconds(),
		"resp": map[string]any{
			"headers": headers,
			"body":    r.Body,
		},
	}
}
