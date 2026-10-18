package channels

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

// Installation is one adapter's registration with the composition root: its
// pure declaration, validatable without opening a store or starting a
// transport, plus the optional lifecycle that opens the adapter's own
// resources once a bot selects it (ADR 0052, ADR 0053, ADR 0054). The adapter
// package owns its value; the command layer iterates the registered set and
// names no adapter, so onboarding one is its own files plus one entry in
// `adapters/installed`.
type Installation struct {
	Definition Definition
	// Install opens this adapter's process-wide resources and returns the
	// declaration bound to them. The root calls it only when a bot selects
	// Definition.Provider and one of its modes; a nil Install means the pure
	// declaration is already the runtime one.
	Install func(ctx context.Context, host Host) (Installed, error)
}

// Host is the neutral process context an installation receives: the shared
// stores and the root's own services. No adapter type appears here, so the
// root composes every installed adapter without a provider switch
// (ADR 0004, ADR 0054).
type Host struct {
	// DB is the instance database, for an adapter's own durable state in
	// the same file.
	DB *sql.DB
	// The shared stores an adapter may read or write while it runs.
	Bindings  botflota.BindingStore
	Peers     botflota.PeerStore
	Chats     botflota.ChatStore
	Messages  botflota.MessageStore
	Directory botflota.DirectoryStore
	Outbox    botflota.OutboxStore
	// Log receives the adapter's own diagnostics.
	Log *slog.Logger
	// DepsLogLevel is the minimum level the adapter forwards from its
	// dependencies.
	DepsLogLevel slog.Level
	// Bot returns one configured bot from the snapshot the command holds:
	// the live config while a fleet runs, the loaded file for a one-shot
	// command.
	Bot func(scope, bot string) (spec.Bot, error)
	// StartBot starts one configured bot through the fleet the root builds
	// from this same catalog. A setup that completes calls it; it reports
	// an error while the fleet is not running (ADR 0053).
	StartBot func(ctx context.Context, scope, bot string) error
}

// Installed is one adapter's runtime result.
type Installed struct {
	// Definition pairs the pure declaration with the adapter's builder,
	// readiness, and setup bound to the resources Install opened.
	Definition Definition
	// Validate reports durable state the process must not start with, such
	// as two bots claiming one provider account. The root calls it before it
	// builds the fleet; a one-shot command that repairs that state may skip
	// it (ADR 0021, ADR 0053).
	Validate func(ctx context.Context) error
	// Close releases the adapter's own resources. The root calls it after
	// the setup workers that write them have stopped (ADR 0053).
	Close func()
}
