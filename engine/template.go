package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// Template is a parsed string of literal text and references to the runtime
// context (ADR 0018). It performs no I/O and holds no state beyond the parsed
// template.
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
// \\ is one backslash.
func ParseTemplate(s string) (Template, error) {
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
			if _, _, err := ParseTemplatePath(ref); err != nil {
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

// Render resolves every reference against ctx. A missing path is an error;
// text is never silently dropped.
func (t Template) Render(ctx *TemplateContext) (string, error) {
	var out strings.Builder
	for _, p := range t.parts {
		if p.ref == "" {
			out.WriteString(p.text)
			continue
		}
		value, err := ctx.Get(p.ref)
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

// References returns the reference paths this template resolves, in order.
func (t Template) References() []string {
	refs := make([]string, 0, len(t.parts))
	for _, p := range t.parts {
		if p.ref != "" {
			refs = append(refs, p.ref)
		}
	}
	return refs
}

// RenderTemplate parses and renders s in one step.
func RenderTemplate(s string, ctx *TemplateContext) (string, error) {
	t, err := ParseTemplate(s)
	if err != nil {
		return "", err
	}
	return t.Render(ctx)
}

// format renders a scalar as text. A whole map or slice has no text form and
// is an error; slice elements are addressed by numeric segment instead.
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
