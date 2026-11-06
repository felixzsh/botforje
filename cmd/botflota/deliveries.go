package main

import (
	"log/slog"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/webhook"
)

// deliveryWorker is the instance's durable webhook queue. It builds the client
// that carries the egress policy, so an owner-set target obeys it like every
// other outbound fetch (ADR 0045, ADR 0048).
func deliveryWorker(
	cfg *config.Config, store botflota.WebhookStore, log *slog.Logger,
) (*webhook.Worker, error) {
	client, err := outboundClient(cfg)
	if err != nil {
		return nil, err
	}
	return webhook.NewWorker(store, webhook.Targets(cfg.Webhooks),
		webhook.WorkerOptions{Client: client, Log: log}), nil
}
