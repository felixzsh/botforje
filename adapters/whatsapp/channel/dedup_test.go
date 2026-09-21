package channel

import (
	"testing"
)

func TestDedup(t *testing.T) {
	d := newDedup(2)
	if !d.fresh("a") || !d.fresh("b") {
		t.Fatal("new ids must be fresh")
	}
	if d.fresh("a") {
		t.Fatal("a duplicate must be rejected")
	}
	if !d.fresh("c") {
		t.Fatal("c must be fresh")
	}
	if !d.fresh("a") {
		t.Fatal("an evicted id must be fresh again")
	}
	if !d.fresh("") || !d.fresh("") {
		t.Fatal("an empty id is always fresh")
	}
}
