// Package conformance is the reusable conformance suite for channel adapters.
//
// An adapter's own tests prove its contracts by calling Run with a Subject:
// its installed definition, the authored options, the binding and live
// channel handles its tests already hold, and the content kinds it declares.
// The suite drives the real config, fleet, engine, SQLite stores, and API, so
// an adapter that passes has shown that its declared capabilities, strict
// options, discovery, conversation reads, filtering, holds, durable sends,
// restart, and detachment work through the shared runtime — not through a
// bespoke test pipeline (ADR 0052, ADR 0054, docs/channels/phases.md P6).
//
// A subject supplies only what the suite cannot know: how to build its
// definition and how to observe the channel that definition built. Everything
// else is the adapter's real code plus the shared production paths.
//
// Typed usage:
//
//	backend := testchannel.NewBackend()
//	def := testchannel.Definition()
//	def.Build = testchannel.Builder(testchannel.Deps{Backend: backend, Bindings: ...})
//	...
//	conformance.Run(t, conformance.Subject{
//	    Name:       "testchannel",
//	    Definition: def,
//	    Options:    conformance.Options(t, "options:\n  namespace: conf\n"),
//	    Binding:    func(scope, bot string) botflota.Binding { ... },
//	    Channel:    func(scope, bot string) conformance.Channel { ... },
//	})
//
// The suite requires text support: it is the baseline content every installed
// channel must send. A subject whose definition declares no sendable kind, or
// only kinds it never constructs, fails its first check.
package conformance

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// Channel is the test-side view of one live channel a definition built: how
// the suite injects inbound messages and reads what was delivered.
type Channel interface {
	// Inject delivers one inbound message to the running bot.
	Inject(msg botflota.IncomingMessage)
	// Sent returns the deliveries the channel accepted, oldest first.
	Sent() []botflota.OutgoingMessage
}

// Subject is one adapter under the conformance suite. Definition, Binding, and
// Channel are required; Options is the authored channel.options one bot
// selects.
type Subject struct {
	// Name labels the subject in sub-test output.
	Name string
	// Install builds the subject's installed definition over one fixture's
	// binding store, the way the composition root binds an adapter's
	// dependencies. The suite calls it with a real store before the fleet
	// starts; the pure checks call it with nil.
	Install func(bindings botflota.BindingStore) channels.Definition
	// Options is the authored channel.options the suite's bot selects.
	Options spec.ChannelOptions
	// Binding is the committed association the suite installs for one bot
	// before the fleet starts.
	Binding func(scope, bot string) botflota.Binding
	// Channel returns the live channel the definition built for one bot.
	Channel func(scope, bot string) Channel
	// SupportedTarget and UnsupportedTarget are well-formed conversations the
	// definition addresses and refuses. Zero values use a direct target on the
	// definition's provider and a group target on it.
	SupportedTarget   botflota.ChatTarget
	UnsupportedTarget botflota.ChatTarget
}

// declaration is the definition's pure half: no store is injected, so the
// store-free checks run without one.
func (s Subject) declaration() channels.Definition { return s.Install(nil) }

// Options parses an authored `options:` block the way a config file does, so a
// subject declares its options without touching spec.ChannelOptions' node.
func Options(t *testing.T, document string) spec.ChannelOptions {
	t.Helper()
	var channel spec.Channel
	if err := yaml.Unmarshal([]byte(document), &channel); err != nil {
		t.Fatalf("conformance: parse options: %v", err)
	}
	return channel.Options
}

// Run executes the suite through t.Run. Every subject builds a fresh instance
// per sub-test, so the checks are independent.
func Run(t *testing.T, s Subject) {
	t.Helper()
	requireSubject(t, s)
	t.Run("definition", func(t *testing.T) { runDefinition(t, s) })
	t.Run("capability-consistency", func(t *testing.T) { runCapabilities(t, s) })
	t.Run("runtime", func(t *testing.T) { runRuntime(t, s) })
}

// Definition runs only the store-free declaration checks, for an installed
// definition that cannot run the runtime suite here — a real provider without
// credentials, or a definition the composition root injects without builders.
func Definition(t *testing.T, name string, def channels.Definition) {
	t.Helper()
	if name == "" || def.Provider == "" || len(def.Modes) == 0 {
		t.Fatal("conformance: Definition needs a name, a provider, and a mode")
	}
	t.Run("definition", func(t *testing.T) {
		runDefinition(t, Subject{
			Name:    name,
			Install: func(botflota.BindingStore) channels.Definition { return def },
		})
	})
}

func requireSubject(t *testing.T, s Subject) {
	t.Helper()
	if s.Name == "" {
		t.Fatal("conformance: Subject.Name is required")
	}
	if s.Install == nil {
		t.Fatal("conformance: Subject.Install is required")
	}
	def := s.declaration()
	if def.Provider == "" || len(def.Modes) == 0 {
		t.Fatal("conformance: the definition needs a provider and a mode")
	}
	if s.Binding == nil || s.Channel == nil {
		t.Fatal("conformance: Subject.Binding and Subject.Channel are required")
	}
}

func supportedTarget(s Subject) botflota.ChatTarget {
	if s.SupportedTarget.Provider != "" {
		return s.SupportedTarget
	}
	return botflota.ChatTarget{
		Provider: s.declaration().Provider, Kind: botflota.ChatDirect,
		Thread: "conf-thread",
	}
}

func unsupportedTarget(s Subject) botflota.ChatTarget {
	if s.UnsupportedTarget.Provider != "" {
		return s.UnsupportedTarget
	}
	return botflota.ChatTarget{
		Provider: s.declaration().Provider, Kind: botflota.ChatGroup,
		Thread: "conf-group",
	}
}

// runDefinition checks the pure declaration: identity, strict options that
// agree with the generated schema, and capabilities that match the content the
// suite can construct.
func runDefinition(t *testing.T, s Subject) {
	t.Helper()
	def := s.declaration()
	if def.Kind == "" {
		t.Fatal("the definition declares no adapter kind")
	}
	t.Run("options", func(t *testing.T) { checkOptions(t, def) })
	t.Run("capabilities", func(t *testing.T) {
		supported := supportedKind(t, def)
		if !def.Capabilities.SupportsSend(supported) {
			t.Fatalf("capabilities do not declare %s", supported)
		}
		unsupported := unsupportedKind(def)
		if unsupported == "" {
			t.Skip("the definition declares every constructible kind")
		}
		if def.Capabilities.SupportsSend(unsupported) {
			t.Fatalf("capabilities claim %s though it is the refused case",
				unsupported)
		}
	})
}

// checkOptions proves the strict decoder and the generated schema tell the same
// story: a valid block decodes, an unknown key is refused, and a missing
// required key is refused when the schema declares one.
func checkOptions(t *testing.T, def channels.Definition) {
	t.Helper()
	if def.Decode == nil || def.OptionsSchema == nil {
		t.Skip("the definition declares no options")
	}
	schema := def.OptionsSchema()
	if schema == nil {
		t.Fatal("the definition returns no options schema")
	}
	if !allowsNoAdditionalProperties(t, schema) {
		t.Fatal("the schema must not allow additional properties")
	}
	empty := botWithOptions(t, "")
	_, emptyReasons := def.Decode(empty)
	if len(schema.Required) > 0 && len(emptyReasons) == 0 {
		t.Fatal("an empty options block must be refused when keys are required")
	}
	if len(schema.Required) == 0 && len(emptyReasons) > 0 {
		t.Fatalf("an empty options block must be accepted: %v", emptyReasons)
	}
	valid := botWithOptions(t, validOptionsYAML(def))
	if _, reasons := def.Decode(valid); len(reasons) != 0 {
		t.Fatalf("the valid block was refused: %v", reasons)
	}
	unknown := validOptionsYAML(def) + "  unknown_key: true\n"
	_, reasons := def.Decode(botWithOptions(t, unknown))
	if len(reasons) == 0 ||
		!strings.Contains(strings.Join(reasons, "; "), "unknown_key") {
		t.Fatalf("an unknown key must be reported: %v", reasons)
	}
}

// allowsNoAdditionalProperties reports whether the schema's JSON form closes
// the object, which is what the strict decoder promises.
func allowsNoAdditionalProperties(t *testing.T, schema any) bool {
	t.Helper()
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal options schema: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode options schema: %v", err)
	}
	closed, ok := raw["additionalProperties"].(bool)
	return ok && !closed
}

// validOptionsYAML fills every required key of the options schema with a
// plausible value, so a subject needs no separate valid-options fixture.
func validOptionsYAML(def channels.Definition) string {
	schema := def.OptionsSchema()
	var builder strings.Builder
	if schema == nil {
		return ""
	}
	for _, key := range schema.Required {
		builder.WriteString("  " + key + ": conformance\n")
	}
	return builder.String()
}

func botWithOptions(t *testing.T, options string) spec.Bot {
	t.Helper()
	var channel spec.Channel
	if options != "" {
		if err := yaml.Unmarshal([]byte("options:\n"+options), &channel); err != nil {
			t.Fatalf("conformance: parse options: %v", err)
		}
	}
	return spec.Bot{Channel: spec.Channel{Options: channel.Options}}
}

// runCapabilities proves config validation, runtime/API admission, and per-bot
// discovery agree on the declared capabilities: an unsupported kind or
// operation cannot bypass any of them.
func runCapabilities(t *testing.T, s Subject) {
	t.Helper()
	def := s.declaration()
	supported := supportedContent(t, def)
	unsupported := unsupportedContent(t, def)
	if unsupported == nil {
		t.Skip("the definition declares every constructible kind")
	}
	target := supportedTarget(s)

	if reasons := channels.AdmitSend(def, target, supported); len(reasons) > 0 {
		t.Fatalf("admission refused the declared content: %v", reasons)
	}
	if reasons := channels.AdmitSend(def, target, unsupported); len(reasons) == 0 {
		t.Fatal("admission accepted content the definition cannot send")
	}
	if def.ValidateTarget != nil {
		if reasons := channels.AdmitSend(
			def, unsupportedTarget(s), supported,
		); len(reasons) == 0 {
			t.Fatal("admission accepted a target the definition cannot address")
		}
	}

	t.Run("config-validation", func(t *testing.T) {
		catalog := mustCatalog(t, def)
		if errs := config.Validate(supportedConfig(s, supported), catalog); len(errs) > 0 {
			t.Fatalf("the supported action failed validation: %v", errs)
		}
		errs := config.Validate(unsupportedConfig(s, unsupported), catalog)
		if len(errs) == 0 {
			t.Fatal("an action sending unsupported content passed validation")
		}
	})
	t.Run("discovery-and-admission", func(t *testing.T) {
		f := newFixture(t, s, nil)
		defer f.stop()
		api := newAPIFixture(t, s, f)
		checkDiscovery(t, api, s, supported, unsupported)
		checkAPIAdmission(t, api, s, target)
	})
}

// checkDiscovery proves the API's capability view is exactly the declaration.
func checkDiscovery(
	t *testing.T, a *apiFixture, s Subject,
	supported botflota.SendableContent, unsupported botflota.SendableContent,
) {
	t.Helper()
	def := s.declaration()
	recorder := a.get(t, "/api/v1/scopes/conf/bots/bot/channel")
	if recorder.Code != 200 {
		t.Fatalf("discovery status = %d, want 200: %s",
			recorder.Code, recorder.Body.String())
	}
	var view struct {
		Provider     string   `json:"provider"`
		Adapter      string   `json:"adapter"`
		Capabilities struct { //nolint:govet // grouped for readability
			Send         []string `json:"send"`
			Features     []string `json:"features"`
			CardVariants []string `json:"card_variants"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode discovery: %v: %s", err, recorder.Body.String())
	}
	if view.Provider != def.Provider || view.Adapter != def.Kind {
		t.Fatalf("discovery = %s/%s, want %s/%s",
			view.Provider, view.Adapter, def.Provider, def.Kind)
	}
	if !containsString(view.Capabilities.Send, string(botflota.ContentKind(supported))) {
		t.Fatalf("discovery omits the sendable kind %s: %v",
			botflota.ContentKind(supported), view.Capabilities.Send)
	}
	if containsString(
		view.Capabilities.Send, string(botflota.ContentKind(unsupported)),
	) {
		t.Fatalf("discovery reports unsendable %s: %v",
			botflota.ContentKind(unsupported), view.Capabilities.Send)
	}
	if !sameStrings(view.Capabilities.Features, def.Capabilities.Features) {
		t.Fatalf("discovery features = %v, want %v",
			view.Capabilities.Features, def.Capabilities.Features)
	}
}

// checkAPIAdmission proves the API admits the declared target and refuses the
// one the definition cannot address, through the real handler.
func checkAPIAdmission(
	t *testing.T, a *apiFixture, s Subject, target botflota.ChatTarget,
) {
	t.Helper()
	body := `{"bot":"bot","target":` + targetJSON(target) + `,"body":"hello"}`
	recorder := a.post(t, "/api/v1/scopes/conf/messages", body)
	if recorder.Code != 202 {
		t.Fatalf("queueing a supported send = %d, want 202: %s",
			recorder.Code, recorder.Body.String())
	}
	unsupported := `{"bot":"bot","target":` + targetJSON(unsupportedTarget(s)) +
		`,"body":"hello"}`
	recorder = a.post(t, "/api/v1/scopes/conf/messages", unsupported)
	if s.declaration().ValidateTarget == nil {
		return
	}
	if recorder.Code != 422 {
		t.Fatalf("admission of an unsupported target = %d, want 422: %s",
			recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "unsupported_operation") {
		t.Fatalf("refusal body = %s, want unsupported_operation", recorder.Body.String())
	}
}

// supportedConfig and unsupportedConfig build a one-bot document whose root
// action sends the given kind, so config validation can be exercised per
// consuming bot (ADR 0052).
func supportedConfig(s Subject, content botflota.SendableContent) *config.Config {
	return contentConfig(s, "supported", content)
}

func unsupportedConfig(s Subject, content botflota.SendableContent) *config.Config {
	return contentConfig(s, "unsupported", content)
}

func contentConfig(
	s Subject, actionName string, content botflota.SendableContent,
) *config.Config {
	def := s.declaration()
	step := stepFor(botflota.ContentKind(content))
	return &config.Config{Scopes: map[string]spec.Scope{
		"conf": {
			Actions: map[string]spec.Action{actionName: {Steps: []spec.Step{step}}},
			Graphs: map[string]spec.Graph{"main": {
				Root:  "root",
				Nodes: map[string]spec.Node{"root": {OnEnter: []string{actionName}}},
			}},
			Bots: map[string]spec.Bot{"bot": {
				Graph: "main",
				Channel: spec.Channel{
					Provider: def.Provider,
					Mode:     def.Modes[0],
					Options:  s.Options,
				},
			}},
		},
	}}
}

func mustCatalog(t *testing.T, def channels.Definition) *channels.Catalog {
	t.Helper()
	catalog, err := channels.New(def)
	if err != nil {
		t.Fatalf("conformance: catalog: %v", err)
	}
	return catalog
}

func containsString(list []string, want string) bool {
	for _, have := range list {
		if have == want {
			return true
		}
	}
	return false
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
