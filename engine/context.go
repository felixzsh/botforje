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
	ErrMissingPath = errors.New("missing path")
	ErrReadOnly    = errors.New("read-only root")
)

func writable(root botflota.Root) bool {
	return root == botflota.RootGraph || root == botflota.RootNode
}

// templateContext is not safe for concurrent use; one bot actor owns it
// (docs/architecture.md).
type templateContext struct {
	values map[botflota.Root]map[string]any
}

func newtemplateContext() *templateContext {
	c := &templateContext{values: map[botflota.Root]map[string]any{}}
	for _, root := range []botflota.Root{
		botflota.RootGraph, botflota.RootNode, botflota.RootPeer,
		botflota.RootMessage, botflota.RootBot, botflota.RootInput,
	} {
		c.values[root] = map[string]any{}
	}
	return c
}

// The values are cloned, so later writes never mutate the caller's map.
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

// Bind targets are exactly one level deep (ADR 0033).
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

// A reference to an unset value is an error, never an empty string (ADR 0018).
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

// The returned map is live, not a copy; the caller must not mutate it.
func (c *templateContext) Values(root botflota.Root) map[string]any {
	if !root.Known() {
		return nil
	}
	return c.values[root]
}

type requestResult struct {
	Status   int
	Headers  map[string]string
	Body     any
	Duration time.Duration
}

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
