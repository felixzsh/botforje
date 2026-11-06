package main

import (
	"errors"
	"testing"

	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/egress"
)

func TestOutboundClientFollowsTheInstanceEgressPolicy(t *testing.T) {
	t.Run("public_only refuses a loopback destination before dialing", func(t *testing.T) {
		client := outboundClient(&config.Config{Egress: "public_only"})
		_, err := client.Get("http://127.0.0.1:1/admin/reset")
		if !errors.Is(err, egress.ErrBlocked) {
			t.Fatalf("Get error = %v, want %v", err, egress.ErrBlocked)
		}
	})
	t.Run("no egress line keeps the standard client", func(t *testing.T) {
		if client := outboundClient(&config.Config{}); client.Transport != nil {
			t.Fatalf("transport = %T, want the standard client", client.Transport)
		}
	})
}
