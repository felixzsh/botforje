package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchema(t *testing.T) {
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
}
