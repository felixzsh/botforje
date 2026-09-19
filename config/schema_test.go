package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchema(t *testing.T) {
	// The comments come from the Go sources, and AddGoComments only resolves
	// them when the process runs from the module root, which is how the
	// Makefile target runs. Regenerating from anywhere else silently drops
	// every description.
	t.Chdir("..")
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
	// Descriptions come from the Go doc comments through AddGoComments; assert
	// one so a silent failure to resolve the package or module path is caught.
	if !strings.Contains(text, "reusable pipeline of steps") {
		t.Error("schema has no Go doc comments; check AddGoComments wiring")
	}
}
