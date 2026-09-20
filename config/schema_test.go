package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchema(t *testing.T) {
	// Regeneration ignores the working directory: the doc comments must
	// resolve from anywhere, not only from the module root.
	t.Chdir(t.TempDir())
	data, err := JSONSchema()
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	text := string(data)
	for _, want := range []string{"graphs", "actions", "bots", "llms", "api_key"} {
		if !strings.Contains(text, `"`+want+`"`) {
			t.Errorf("schema is missing %q", want)
		}
	}
	// Descriptions come from the Go doc comments of both packages; assert one
	// of each so a silent failure to resolve them is caught.
	for _, want := range []string{
		"Config is the root document",
		"reusable pipeline of steps",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("schema has no description %q; check addGoComments wiring", want)
		}
	}
}
