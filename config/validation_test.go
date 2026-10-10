package config

import (
	"path/filepath"
	"strings"
	"testing"
)

type fixtureCase struct {
	file string
	want string
}

var invalidFixtures = []fixtureCase{
	{"unknown_field.yml", "field"},
	{"empty.yml", "empty document"},
	{"malformed.yml", "yaml"},
	{"include_no_match.yml", "matched no files"},
	{"bad_log_level.yml", "log_level"},
	{"bad_log_level_deps.yml", "log_level_deps"},
	{"bad_rate_limit.yml", "rate_limit"},
	{"bad_llm_routing.yml", "routing"},
	{"bad_llm_realization.yml", "realization"},
	{"bad_llm_bounds.yml", ">= 0"},
	{"llm_no_provider.yml", "provider is required"},
	{"action_no_steps.yml", "no steps"},
	{"bad_input_type.yml", "invalid type"},
	{"bad_cooldown.yml", "cooldown duration"},
	{"message_empty.yml", "body or intent"},
	{"policy_without_intent.yml", "policy requires intent"},
	{"variants_with_intent.yml", "variants cannot combine"},
	{"request_no_name.yml", "needs a name"},
	{"request_no_url.yml", "needs a url"},
	{"bad_method.yml", "invalid method"},
	{"negative_request_timeout.yml", ">= 0"},
	{"duplicate_request_name.yml", "duplicate request name"},
	{"step_two_kinds.yml", "exactly one"},
	{"media_no_source.yml", "media needs a url or a path"},
	{"media_two_sources.yml", "cannot set both url and path"},
	{"bad_media_kind.yml", "media kind"},
	{"poll_too_few_options.yml", "at least two options"},
	{"contact_no_entries.yml", "at least one entry"},
	{"contact_bad_entry_kind.yml", "must be phone, email, address, or url"},
	{"contact_entry_no_value.yml", "entry 0 needs a value"},
	{"bad_bind.yml", "must be node.<var>"},
	{"bind_node_undeclared.yml", "not declared in initial_context"},
	{"bind_graph_undeclared.yml", "not declared in initial_context"},
	{"graph_bad_routing.yml", "invalid routing"},
	{"graph_no_root.yml", "no root node"},
	{"graph_root_missing.yml", "does not exist"},
	{"graph_no_nodes.yml", "no nodes"},
	{"graph_bad_fallback.yml", "fallback"},
	{"edge_unknown_goto.yml", "edge goes to unknown node"},
	{"duplicate_default_edge.yml", "default edges"},
	{"unreachable_node.yml", "unreachable"},
	{"unknown_on_enter.yml", "on_enter"},
	{"unknown_tool.yml", "tool"},
	{"missing_graph.yml", "not found"},
	{"bot_no_graph.yml", "no graph"},
	{"bot_unknown_llm.yml", "not found"},
	{"bot_negative_settings.yml", "must be >= 0"},
	{"bad_template_root.yml", "unknown root"},
	{"undeclared_template_var.yml", "not declared in initial_context"},
	{"undeclared_template_input.yml", "input"},
	{"undeclared_to_var.yml", "not declared in initial_context"},
	{"webhook_no_url.yml", "url is required"},
	{"webhook_bad_url.yml", "absolute http or https URL"},
	{"webhook_no_secret.yml", "secret is required"},
	{"webhook_no_events.yml", "at least one event"},
	{"webhook_unknown_event.yml", "unknown event"},
	{"webhook_duplicate_url.yml", "duplicate url"},
}

func TestInvalidFixtures(t *testing.T) {
	for _, tc := range invalidFixtures {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join("testdata", "invalid", tc.file)
			cfg, err := testLoader().Load(path)
			if err != nil {
				if !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("load error %v does not mention %q", err, tc.want)
				}
				return
			}
			errs := Validate(cfg)
			for _, e := range errs {
				if strings.Contains(e.Error(), tc.want) {
					return
				}
			}
			t.Fatalf("no error mentions %q; got %v", tc.want, errs)
		})
	}
}

func TestNoOrphanInvalidFixtures(t *testing.T) {
	listed := map[string]bool{}
	for _, tc := range invalidFixtures {
		listed[tc.file] = true
	}
	files, err := filepath.Glob(filepath.Join("testdata", "invalid", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if name := filepath.Base(file); !listed[name] {
			t.Errorf("fixture %s is not referenced by any test", name)
		}
	}
}

func TestValidFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "valid", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no valid fixtures found")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			cfg, err := testLoader().Load(file)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if errs := Validate(cfg); len(errs) != 0 {
				t.Fatalf("want valid, got %v", errs)
			}
		})
	}
}
