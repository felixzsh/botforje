package botflota

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Root string

const (
	RootGraph   Root = "graph"
	RootNode    Root = "node"
	RootPeer    Root = "peer"
	RootMessage Root = "message"
	RootBot     Root = "bot"
	RootInput   Root = "input"
)

var (
	// ErrInvalidPath reports a reference that is not root.name[.name...].
	ErrInvalidPath = errors.New("invalid path")
	ErrUnknownRoot = errors.New("unknown root")
)

func (r Root) Known() bool {
	switch r {
	case RootGraph, RootNode, RootPeer, RootMessage, RootBot, RootInput:
		return true
	default:
		return false
	}
}

// ParseTemplatePath splits a reference into its root and segments of letters,
// digits, `_`, or `-`; a numeric segment indexes slices at resolution. The root
// must be one the runtime knows (ADR 0018).
func ParseTemplatePath(path string) (Root, []string, error) {
	if err := validatePathSyntax(path); err != nil {
		return "", nil, err
	}
	segments := strings.Split(path, ".")
	root := Root(segments[0])
	if !root.Known() {
		return "", nil, fmt.Errorf("%w: %q", ErrUnknownRoot, root)
	}
	return root, segments[1:], nil
}

// validatePathSyntax checks root.name[.name...] without deciding whether the
// root is known, so an adapter-owned legacy alias can reach its own check.
func validatePathSyntax(path string) error {
	segments := strings.Split(path, ".")
	if len(segments) < 2 {
		return fmt.Errorf("%w: %q", ErrInvalidPath, path)
	}
	for _, segment := range segments {
		if !ValidSegment(segment) {
			return fmt.Errorf("%w: %q", ErrInvalidPath, path)
		}
	}
	return nil
}

func ValidSegment(segment string) bool {
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

// Template is a parsed string of literal text and runtime-context references
// (ADR 0018); it performs no I/O.
type Template struct {
	parts []part
}

// part is either literal text or a reference; exactly one field is set.
type part struct {
	text string
	ref  string
}

// ParseTemplate compiles s. A reference is written {{graph.customer_name}};
// spaces just inside the braces are ignored, a literal {{ is written \{{, and
// \\ is one backslash. The root must be one the runtime knows.
func ParseTemplate(s string) (Template, error) { return parseTemplate(s, true) }

// ParseTemplateAny compiles s with the same rules but without requiring a known
// root, so a caller that owns an adapter's legacy alias table can decide which
// roots it accepts (ADR 0018/0020).
func ParseTemplateAny(s string) (Template, error) { return parseTemplate(s, false) }

func parseTemplate(s string, knownRoot bool) (Template, error) {
	var parts []part
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			parts = append(parts, part{text: literal.String()})
			literal.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], `\\`):
			literal.WriteByte('\\')
			i += 2
		case strings.HasPrefix(s[i:], `\{{`):
			literal.WriteString("{{")
			i += 3
		case strings.HasPrefix(s[i:], "{{"):
			flush()
			end := strings.Index(s[i+2:], "}}")
			if end < 0 {
				return Template{}, fmt.Errorf("unterminated reference at %q", s[i:])
			}
			ref := strings.TrimSpace(s[i+2 : i+2+end])
			var err error
			if knownRoot {
				_, _, err = ParseTemplatePath(ref)
			} else {
				err = validatePathSyntax(ref)
			}
			if err != nil {
				return Template{}, fmt.Errorf("invalid reference %q: %w", ref, err)
			}
			parts = append(parts, part{ref: ref})
			i += 2 + end + 2
		default:
			literal.WriteByte(s[i])
			i++
		}
	}
	flush()
	return Template{parts: parts}, nil
}

// TranslateTemplate rewrites the references of s whose whole path is one of the
// aliases, leaving every other reference and every escape exactly as authored.
// It is the adapter-owned compatibility step: the table belongs to the
// consuming bot's definition, and an alias no definition declares still fails
// validation instead of rendering empty (ADR 0018/0020).
func TranslateTemplate(s string, aliases map[string]string) (string, error) {
	if len(aliases) == 0 || !strings.Contains(s, "{{") {
		return s, nil
	}
	var out strings.Builder
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], `\\`):
			out.WriteString(`\\`)
			i += 2
		case strings.HasPrefix(s[i:], `\{{`):
			out.WriteString(`\{{`)
			i += 3
		case strings.HasPrefix(s[i:], "{{"):
			end := strings.Index(s[i+2:], "}}")
			if end < 0 {
				return "", fmt.Errorf("unterminated reference at %q", s[i:])
			}
			ref := strings.TrimSpace(s[i+2 : i+2+end])
			if replacement, ok := aliases[ref]; ok {
				ref = replacement
			}
			out.WriteString("{{")
			out.WriteString(ref)
			out.WriteString("}}")
			i += 2 + end + 2
		default:
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String(), nil
}

// Render resolves every reference through resolve; a missing path is an
// error, never silently dropped text.
func (t Template) Render(resolve func(string) (any, error)) (string, error) {
	var out strings.Builder
	for _, p := range t.parts {
		if p.ref == "" {
			out.WriteString(p.text)
			continue
		}
		value, err := resolve(p.ref)
		if err != nil {
			return "", err
		}
		text, err := format(value)
		if err != nil {
			return "", fmt.Errorf("reference %q: %w", p.ref, err)
		}
		out.WriteString(text)
	}
	return out.String(), nil
}

func (t Template) References() []string {
	refs := make([]string, 0, len(t.parts))
	for _, p := range t.parts {
		if p.ref != "" {
			refs = append(refs, p.ref)
		}
	}
	return refs
}

func RenderTemplate(s string, resolve func(string) (any, error)) (string, error) {
	t, err := ParseTemplate(s)
	if err != nil {
		return "", err
	}
	return t.Render(resolve)
}

// format renders a scalar as text; a whole map or slice has no text form and
// is an error.
func format(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v), nil
	case nil:
		return "", fmt.Errorf("value is empty")
	default:
		return "", fmt.Errorf("cannot render %T as text", value)
	}
}
