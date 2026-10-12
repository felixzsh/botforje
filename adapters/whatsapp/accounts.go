// Package whatsapp links a WhatsApp companion to a Botflota bot and keeps the
// link: the whatsmeow device store, linked-account ownership, pairing, and the
// helpers the channel and the history import share (ADR 0006, ADR 0011).
package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

// The neutral definition metadata WhatsApp commits with every binding
// (ADR 0053): the catalog provider, the adapter kind, the account mode this
// installation serves, and the setup method its sessions speak. WhatsApp
// subjects are provider-wide phone-number addresses, so the namespace stays
// empty; the digits are the adapter's opaque subject, never telephone
// metadata the common contracts parse (ADR 0051).
const (
	ProviderName  = ingest.Provider
	AdapterKind   = "whatsmeow"
	AccountMode   = "native"
	SetupMethodQR = "qr"
)

// Devices is the whatsmeow device store on Botflota's shared SQLite connection
// (ADR 0006). Open it once per process: the container is built and upgraded
// once.
type Devices struct {
	container *sqlstore.Container
}

// OpenDevices opens and upgrades the device store. A nil logger keeps its
// output quiet; depsLevel is the minimum level the store forwards.
func OpenDevices(
	ctx context.Context, db *sql.DB, log *slog.Logger, depsLevel slog.Level,
) (*Devices, error) {
	if db == nil {
		return nil, errors.New("whatsapp: no database")
	}
	if log == nil {
		log = botflota.DiscardLogger()
	}
	devices := &Devices{
		container: sqlstore.NewWithDB(db, "sqlite", NewWaLog(log, depsLevel)),
	}
	if err := devices.container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("whatsapp: device store: %w", err)
	}
	return devices, nil
}

// device reports a missing device as a nil result, not an error. The device
// locator stays in the SDK store: it is recovered by matching the normalized
// own subject, never stored in a shared record (ADR 0051).
func (d *Devices) Load(
	ctx context.Context, account *botflota.Binding,
) (*store.Device, error) {
	subject, err := ownSubject(account)
	if err != nil {
		return nil, err
	}
	devices, err := d.container.GetAllDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"whatsapp: load device for bot %q (scope %q): %w",
			account.Bot, account.Scope, err,
		)
	}
	for _, device := range devices {
		if device.ID == nil || device.ID.Server != types.DefaultUserServer {
			continue
		}
		if device.ID.User == subject {
			return device, nil
		}
	}
	return nil, nil
}

// ownSubject reads the normalized own identity of a WhatsApp binding.
func ownSubject(account *botflota.Binding) (string, error) {
	if account == nil || account.Identity.Provider != ingest.Provider ||
		account.Identity.Subject == "" {
		return "", fmt.Errorf(
			"whatsapp: bot %q (scope %q) has no whatsapp identity",
			account.Bot, account.Scope,
		)
	}
	return account.Identity.Subject, nil
}

// delete removes the account's local credentials; a missing device is fine.
func (d *Devices) delete(ctx context.Context, account *botflota.Binding) error {
	device, err := d.Load(ctx, account)
	if err != nil {
		return err
	}
	if device == nil {
		return nil
	}
	if err := device.Delete(ctx); err != nil {
		return fmt.Errorf("whatsapp: delete device: %w", err)
	}
	return nil
}

func (d *Devices) newDevice() *store.Device { return d.container.NewDevice() }

type UnpairOptions struct {
	Scope    string
	Bot      string
	Devices  *Devices
	Bindings botflota.BindingStore
	Pairings botflota.PairingStore
	// Outbox fails the queued replies the link still owes; unlinking
	// requires it, so a later pairing never sends the old replies (ADR
	// 0040).
	Outbox botflota.OutboxStore
}

// ValidateAccountOwners rejects more than one bot using the same identity.
func ValidateAccountOwners(ctx context.Context, accounts botflota.BindingStore) error {
	all, err := accounts.List(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp: list account ownership: %w", err)
	}
	owners := make(map[botflota.IdentityKey]botflota.Binding, len(all))
	for _, account := range all {
		if account.Identity.Subject == "" {
			continue
		}
		if previous, ok := owners[account.Identity]; ok {
			return duplicateAccountError(account.Identity, previous, account)
		}
		owners[account.Identity] = account
	}
	return nil
}

// ValidateAccounts reconciles the committed bindings this installation
// serves with their local credentials. Bindings of another provider are
// never this adapter's business: each bot is inspected through its own
// definition, never by scanning every bot through one device store
// (ADR 0053). A binding without local credentials is recoverable and marks
// the bot unpaired without aborting validation.
func ValidateAccounts(
	ctx context.Context, devices *Devices,
	accounts botflota.BindingStore, pairings botflota.PairingStore,
) error {
	if devices == nil {
		return errors.New("whatsapp: no device store")
	}
	if err := ValidateAccountOwners(ctx, accounts); err != nil {
		return err
	}
	all, err := accounts.List(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp: list linked accounts: %w", err)
	}
	for i := range all {
		if all[i].Identity.Provider != ingest.Provider {
			continue
		}
		device, err := devices.Load(ctx, &all[i])
		if err != nil {
			return err
		}
		if device == nil {
			if err := UpdatePairingStatus(ctx, pairings, all[i].Scope, all[i].Bot,
				botflota.PairingUnpaired, PairingNow(nil)); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckPairing treats a missing account or missing local credentials as an
// expected state, not an error; other database and pairing errors are returned.
func CheckPairing(
	ctx context.Context,
	devices *Devices,
	accounts botflota.BindingStore,
	pairings botflota.PairingStore,
	scope, bot string,
) (botflota.PairingState, error) {
	if devices == nil {
		return "", errors.New("whatsapp: no device store")
	}
	account, err := accounts.Get(ctx, scope, bot)
	switch {
	case errors.Is(err, botflota.ErrNotFound):
		if err := UpdatePairingStatus(ctx, pairings, scope, bot,
			botflota.PairingNotPaired, PairingNow(nil)); err != nil {
			return "", err
		}
		return botflota.PairingNotPaired, nil
	case err != nil:
		return "", fmt.Errorf("whatsapp: get pairing for bot %q: %w", bot, err)
	}
	ready, err := HasLocalDevice(ctx, devices, account)
	if err != nil {
		return "", err
	}
	if !ready {
		if err := UpdatePairingStatus(ctx, pairings, scope, bot,
			botflota.PairingUnpaired, PairingNow(nil)); err != nil {
			return "", err
		}
		return botflota.PairingUnpaired, nil
	}
	return botflota.PairingPaired, nil
}

func HasLocalDevice(
	ctx context.Context, devices *Devices, account *botflota.Binding,
) (bool, error) {
	if devices == nil {
		return false, errors.New("whatsapp: no device store")
	}
	device, err := devices.Load(ctx, account)
	if err != nil {
		return false, err
	}
	return device != nil, nil
}

// Unpair does not unlink the companion from WhatsApp; remove that old device
// from the phone if needed.
func Unpair(ctx context.Context, opts UnpairOptions) error {
	if opts.Devices == nil {
		return errors.New("whatsapp: no device store")
	}
	account, err := opts.Bindings.Get(ctx, opts.Scope, opts.Bot)
	if errors.Is(err, botflota.ErrNotFound) {
		return fmt.Errorf("whatsapp: bot %q is not paired", opts.Bot)
	}
	if err != nil {
		return err
	}
	return removePairing(ctx, opts.Devices, opts.Bindings, opts.Pairings,
		opts.Outbox, account, PairingNow(nil))
}

// ensureIdentityAvailable rejects an identity already linked to another
// bot of the instance (ADR 0021). The conflict detail is for the instance
// log; the caller reads only the sentinel.
func ensureIdentityAvailable(
	ctx context.Context, accounts botflota.BindingStore, key botflota.IdentityKey,
	scope, bot string,
) error {
	all, err := accounts.List(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp: list account ownership: %w", err)
	}
	for _, account := range all {
		if account.Scope == scope && account.Bot == bot {
			continue
		}
		if account.Identity == key {
			return duplicateAccountError(
				key,
				account,
				botflota.Binding{Scope: scope, Bot: bot},
			)
		}
	}
	return nil
}

func duplicateAccountError(
	key botflota.IdentityKey, first, second botflota.Binding,
) error {
	// The detail is for the instance log; a caller reads only the sentinel
	// text, because the other owner may sit in a scope it cannot see.
	return fmt.Errorf(
		"whatsapp: identity %s/%s is assigned to bots %q (scope %q) and %q (scope %q): %w",
		key.Provider, key.Subject, first.Bot, first.Scope, second.Bot, second.Scope,
		botflota.ErrIdentityInUse,
	)
}

func UpdatePairingStatus(
	ctx context.Context,
	pairings botflota.PairingStore,
	scope, bot string,
	state botflota.PairingState,
	changedAt time.Time,
) error {
	current, err := pairings.Get(ctx, scope, bot)
	if err == nil && current.State == state && !current.LastChange.IsZero() {
		return nil
	}
	if err != nil && !errors.Is(err, botflota.ErrNotFound) {
		return fmt.Errorf("whatsapp: get pairing status: %w", err)
	}
	if changedAt.IsZero() {
		changedAt = PairingNow(nil)
	}
	if err := pairings.Set(ctx, &botflota.PairingStatus{
		Scope: scope, Bot: bot, State: state, LastChange: changedAt,
	}); err != nil {
		return fmt.Errorf("whatsapp: set pairing status: %w", err)
	}
	return nil
}

func PairingNow(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}

// Number strips the device suffix and the server from a device locator; the
// locator itself stays internal to the adapter (ADR 0020).
func Number(jid string) string {
	number := jid
	if at := strings.IndexByte(number, '@'); at >= 0 {
		number = number[:at]
	}
	if colon := strings.IndexByte(number, ':'); colon >= 0 {
		number = number[:colon]
	}
	return number
}

// HasCredentials implements botflota.DevicePresence for the live device
// store, so a consumer asks for presence without receiving the store.
func (d *Devices) HasCredentials(
	ctx context.Context, account *botflota.Binding,
) (bool, error) {
	return HasLocalDevice(ctx, d, account)
}

// AccountOwner is the account lifecycle the control plane asks for: presence
// through its Devices, and dropping the whole link (ADR 0039). fleet run
// composes it once from the stores it already holds.
type AccountOwner struct {
	*Devices
	Bindings botflota.BindingStore
	Pairings botflota.PairingStore
	Outbox   botflota.OutboxStore
	// Pairs is the live setup runner, when the instance runs one: detach
	// waits for the bot's in-flight setup before it forgets the binding, so
	// no stale worker activates later (ADR 0053). Nil means no setup runs
	// here and detach skips the wait.
	Pairs *PairManager
}

// InspectBinding answers one bot's committed binding with its actionable
// readiness condition (ADR 0053). Credential presence, provider
// authorization, and transport health stay separate facts: a stored device
// proves neither authorization nor a live connection, and the reasons say
// so. A bot without a binding stays inspectable.
func (o AccountOwner) InspectBinding(
	ctx context.Context, scope, bot string,
) (botflota.BindingStatus, error) {
	if o.Devices == nil {
		return botflota.BindingStatus{}, errors.New("whatsapp: no device store")
	}
	account, err := o.Bindings.Get(ctx, scope, bot)
	if errors.Is(err, botflota.ErrNotFound) {
		return botflota.BindingStatus{
			Condition: botflota.ReadinessUnconfigured,
			Reasons:   []string{"no account linked"},
			Method:    SetupMethodQR,
		}, nil
	}
	if err != nil {
		return botflota.BindingStatus{}, fmt.Errorf("whatsapp: read binding: %w", err)
	}
	if account.Identity.Provider != ingest.Provider {
		return botflota.BindingStatus{}, fmt.Errorf(
			"whatsapp: bot %q (scope %q) names provider %q",
			bot, scope, account.Identity.Provider)
	}
	present, err := HasLocalDevice(ctx, o.Devices, account)
	if err != nil {
		return botflota.BindingStatus{}, err
	}
	if !present {
		status := botflota.BindingStatus{
			Binding:   account,
			Condition: botflota.ReadinessActionRequired,
			Reasons:   []string{"local credentials are missing; complete setup again"},
			Method:    SetupMethodQR,
		}
		return status, nil
	}
	return botflota.BindingStatus{
		Binding:   account,
		Condition: botflota.ReadinessConfigured,
		Reasons: []string{
			"local credentials are stored",
			"provider authorization and transport health are unknown until connect",
		},
	}, nil
}

// DetachBinding forgets one bot's local binding through the same serialized
// path as an unpair: it waits for the in-flight setup when one runs,
// settles the queued replies, and leaves chats, directory, and history
// alone. It never destroys the provider account (ADR 0053).
func (o AccountOwner) DetachBinding(ctx context.Context, scope, bot string) error {
	if o.Pairs != nil {
		wait, cancel := context.WithTimeout(ctx, pairActivationBudget)
		defer cancel()
		if err := o.Pairs.EndPair(wait, scope, bot); err != nil {
			return fmt.Errorf("whatsapp: end setup before detach: %w", err)
		}
	}
	return o.UnpairAccount(ctx, scope, bot)
}

// UnpairAccount removes the local credentials, the account record, and the
// stored pairing state of one bot, and fails the replies its link still
// owes (ADR 0021, ADR 0040). The caller stops the running bot first, so
// nothing holds the connection while the credentials go.
func (o AccountOwner) UnpairAccount(
	ctx context.Context, scope, bot string,
) error {
	return Unpair(ctx, UnpairOptions{
		Scope: scope, Bot: bot, Devices: o.Devices,
		Bindings: o.Bindings, Pairings: o.Pairings, Outbox: o.Outbox,
	})
}

// DevicePhone is the paired account's own number, which call-log routing uses
// to tell the local side from the peer.
func DevicePhone(device *store.Device) string {
	if device == nil || device.GetJID().Server != types.DefaultUserServer {
		return ""
	}
	return device.GetJID().User
}
