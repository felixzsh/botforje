package spec

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDisplayNamesRoundTripWithoutChangingKeys(t *testing.T) {
	var scope Scope
	data := []byte(`
display_name: Sales
llms:
  primary:
    display_name: Main model
    provider: openai
    model: example
actions:
  greet:
    display_name: Greeting
    steps: []
graphs:
  welcome:
    display_name: Welcome flow
    root: hello
    nodes:
      hello:
        display_name: Hello
bots:
  assistant:
    display_name: Sales assistant
    graph: welcome
`)
	if err := yaml.Unmarshal(data, &scope); err != nil {
		t.Fatal(err)
	}
	if scope.DisplayName != "Sales" || scope.Bots["assistant"].DisplayName != "Sales assistant" {
		t.Fatalf("scope or bot display name not decoded: %#v", scope)
	}
	if scope.Graphs["welcome"].Nodes["hello"].DisplayName != "Hello" {
		t.Fatalf("node display name not decoded: %#v", scope.Graphs["welcome"].Nodes)
	}

	encoded, err := yaml.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"greet:", "welcome:", "hello:", "assistant:"} {
		if !strings.Contains(string(encoded), key) {
			t.Errorf("YAML lost stable key %q: %s", key, encoded)
		}
	}

	encodedJSON, err := json.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedJSON), `"display_name":"Sales"`) {
		t.Errorf("JSON does not expose display_name: %s", encodedJSON)
	}
}

func TestDisplayNameIsOmittedWhenUnset(t *testing.T) {
	encoded, err := yaml.Marshal(Scope{Bots: map[string]Bot{"assistant": {Graph: "welcome"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "display_name") {
		t.Fatalf("empty display name was serialized: %s", encoded)
	}

	encodedJSON, err := json.Marshal(Bot{Graph: "welcome"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedJSON), "display_name") {
		t.Fatalf("empty display name was serialized: %s", encodedJSON)
	}
}
