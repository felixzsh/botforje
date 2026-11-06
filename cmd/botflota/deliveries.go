package main

import (
	"log/slog"
	"net/http"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/webhook"
)

// deliveryWorker is the instance's durable webhook queue. Its client carries
// the egress policy, so an owner-set target obeys it like every other outbound
// fetch (ADR 0045, ADR 0048).
func deliveryWorker(
	cfg *config.Config, store botflota.WebhookStore, log *slog.Logger,
	client *http.Client,
) *webhook.Worker {
	return webhook.NewWorker(store, webhook.Targets(cfg.Webhooks),
		webhook.WorkerOptions{Client: client, Log: log})
}
