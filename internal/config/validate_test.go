package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidFixtures(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"unknown_field.yml", "field"},
		{"missing_graph.yml", "not found"},
		{"policy_without_intent.yml", "policy requires intent"},
		{"duplicate_default_edge.yml", "default edges"},
		{"unreachable_node.yml", "unreachable"},
		{"bad_bind.yml", "must be node.<var>"},
		{"variants_with_intent.yml", "variants cannot combine"},
		{"duplicate_request_name.yml", "duplicate request name"},
		{"step_two_kinds.yml", "exactly one"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join("testdata", "invalid", tc.file)
			cfg, err := testLoader().Load(path)
			if err != nil {
				if !strings.Contains(err.Error(), tc.want) && !strings.Contains(err.Error(), "field") {
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
