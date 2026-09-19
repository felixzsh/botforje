package engine

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"
)

// Root is the first segment of a {{path}} reference: graph, node, sender,
// message, bot, or input.
type Root string

const (
	RootGraph   Root = "graph"
	RootNode    Root = "node"
	RootSender  Root = "sender"
	RootMessage Root = "message"
	RootBot     Root = "bot"
	RootInput   Root = "input"
)

var (
	// ErrInvalidPath reports a reference that is not root.name[.name...].
	ErrInvalidPath = errors.New("invalid path")
	// ErrMissingPath reports a reference whose value is not set.
	ErrMissingPath = errors.New("missing path")
	// ErrUnknownRoot reports a reference whose root is not defined.
	ErrUnknownRoot = errors.New("unknown root")
	// ErrReadOnly reports a write to a read-only root.
	ErrReadOnly = errors.New("read-only root")
)

// Known reports whether r is a defined root.
func (r Root) Known() bool {
	switch r {
	case RootGraph, RootNode, RootSender, RootMessage, RootBot, RootInput:
		return true
	default:
		return false
	}
}

// Writable reports whether steps may write the root. Graph and node variables
// are read-write (ADR 0008); the rest are fixed by the engine for the
// duration of a message.
func (r Root) Writable() bool { return r == RootGraph || r == RootNode }

// ParseTemplatePath splits a reference into its root and segments. The root
// must be known; every segment is a non-empty name of letters, digits, `_`,
// or `-`. Numeric segments index slices when the path is resolved.
func ParseTemplatePath(path string) (Root, []string, error) {
	segments := strings.Split(path, ".")
	if len(segments) < 2 {
		return "", nil, fmt.Errorf("%w: %q", ErrInvalidPath, path)
	}
	for _, segment := range segments {
		if !validSegment(segment) {
			return "", nil, fmt.Errorf("%w: %q", ErrInvalidPath, path)
		}
	}
	root := Root(segments[0])
	if !root.Known() {
		return "", nil, fmt.Errorf("%w: %q", ErrUnknownRoot, root)
	}
	return root, segments[1:], nil
}

func validSegment(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// TemplateContext holds the values a template reads, grouped by root. It is
// not safe for concurrent use; one bot actor owns it (docs/architecture.md).
type TemplateContext struct {
	values map[Root]map[string]any
}

// NewTemplateContext returns an empty context with every root ready to use.
func NewTemplateContext() *TemplateContext {
	c := &TemplateContext{values: map[Root]map[string]any{}}
	for _, root := range []Root{
		RootGraph, RootNode, RootSender, RootMessage, RootBot, RootInput,
	} {
		c.values[root] = map[string]any{}
	}
	return c
}

// Reset replaces every value of one root. The engine uses it to seed a session
// (graph), a node entry (node), a message (sender, message, bot), and a tool
// call (input). The values are cloned, so later writes never mutate the config
// that produced them.
func (c *TemplateContext) Reset(root Root, values map[string]any) error {
	if !root.Known() {
		return fmt.Errorf("%w: %q", ErrUnknownRoot, root)
	}
	clone := maps.Clone(values)
	if clone == nil {
		clone = map[string]any{}
	}
	c.values[root] = clone
	return nil
}

// Set writes one graph or node variable. Bind targets are exactly one level
// deep (ADR 0008); read-only roots reject the write.
func (c *TemplateContext) Set(path string, value any) error {
	root, segments, err := ParseTemplatePath(path)
	if err != nil {
		return err
	}
	if !root.Writable() {
		return fmt.Errorf("%w: %q", ErrReadOnly, root)
	}
	if len(segments) != 1 {
		return fmt.Errorf("%w: %q", ErrInvalidPath, path)
	}
	c.values[root][segments[0]] = value
	return nil
}

// Get resolves path to its value. A step that references an unset value is an
// error, never an empty string (ADR 0018).
func (c *TemplateContext) Get(path string) (any, error) {
	root, segments, err := ParseTemplatePath(path)
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
func (c *TemplateContext) Values(root Root) map[string]any {
	if !root.Known() {
		return nil
	}
	return c.values[root]
}

// RequestResult is the captured outcome of a request step. Phase 4 fills it in
// when it runs the HTTP call.
type RequestResult struct {
	Status   int
	Headers  map[string]string
	Body     any
	Duration time.Duration
}

// SetRequest stores a captured request under the node root, addressable as
// {{node.<name>.resp.body.*}}, .resp.headers.*, .status, and .duration.
func (c *TemplateContext) SetRequest(name string, result RequestResult) error {
	if !validSegment(name) {
		return fmt.Errorf("%w: request name %q", ErrInvalidPath, name)
	}
	c.values[RootNode][name] = result.value()
	return nil
}

func (r RequestResult) value() map[string]any {
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
