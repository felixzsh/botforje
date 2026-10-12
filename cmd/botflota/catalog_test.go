package main

import (
	"testing"

	"github.com/felixzsh/botflota/channels"
)

// testCatalog is the installed set the CLI tests validate against, built the
// same way every config-only command builds it (ADR 0052, ADR 0054).
func testCatalog(t *testing.T) *channels.Catalog {
	t.Helper()
	catalog, err := pureCatalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return catalog
}
