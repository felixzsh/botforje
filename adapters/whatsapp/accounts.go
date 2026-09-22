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
	"time"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"

	"github.com/felixzsh/botflota"
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

// device reports a missing device as a nil result, not an error.
func (d *Devices) Load(
	ctx context.Context, account *botflota.LinkedAccount,
) (*store.Device, error) {
	jid, err := types.ParseJID(account.JID)
	if err != nil {
		return nil, fmt.Errorf(
			"whatsapp: bot %q (scope %q) has an invalid stored pairing",
			account.Bot, account.Scope,
		)
	}
	device, err := d.container.GetDevice(ctx, jid)
	if err != nil {
		return nil, fmt.Errorf(
			"whatsapp: load device for bot %q (scope %q): %w",
			account.Bot, account.Scope, err,
		)
	}
	return device, nil
}

// delete removes the account's local credentials; a missing device is fine.
func (d *Devices) delete(ctx context.Context, account *botflota.LinkedAccount) error {
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
	Accounts botflota.LinkedAccountStore
	Pairings botflota.PairingStore
	// Outbox fails the queued replies the link still owes; unlinking
	// requires it, so a later pairing never sends the old replies (ADR
	// 0040).
	Outbox botflota.OutboxStore
}

// ValidateAccountOwners rejects more than one bot using the same phone number.
func ValidateAccountOwners(ctx context.Context, accounts botflota.LinkedAccountStore) error {
	all, err := accounts.List(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp: list account ownership: %w", err)
	}
	owners := make(map[string]botflota.LinkedAccount, len(all))
	for _, account := range all {
		phone := Number(account.JID)
		if phone == "" {
			continue
		}
		if previous, ok := owners[phone]; ok {
			return duplicateAccountError(phone, previous, account)
		}
		owners[phone] = account
	}
	return nil
}

// Missing local credentials are recoverable and do not abort validation.
func ValidateAccounts(
	ctx context.Context, devices *Devices,
	accounts botflota.LinkedAccountStore, pairings botflota.PairingStore,
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
	accounts botflota.LinkedAccountStore,
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
	ctx context.Context, devices *Devices, account *botflota.LinkedAccount,
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
	account, err := opts.Accounts.Get(ctx, opts.Scope, opts.Bot)
	if errors.Is(err, botflota.ErrNotFound) {
		return fmt.Errorf("whatsapp: bot %q is not paired", opts.Bot)
	}
	if err != nil {
		return err
	}
	return removePairing(ctx, opts.Devices, opts.Accounts, opts.Pairings,
		opts.Outbox, account, PairingNow(nil))
}

func ensurePhoneAvailable(
	ctx context.Context, accounts botflota.LinkedAccountStore, phone, scope, bot string,
) error {
	all, err := accounts.List(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp: list account ownership: %w", err)
	}
	for _, account := range all {
		if account.Scope == scope && account.Bot == bot {
			continue
		}
		if Number(account.JID) == phone {
			return duplicateAccountError(
				phone,
				account,
				botflota.LinkedAccount{Scope: scope, Bot: bot},
			)
		}
	}
	return nil
}

func duplicateAccountError(
	phone string, first, second botflota.LinkedAccount,
) error {
	return fmt.Errorf(
		"whatsapp: phone number %s is assigned to bots %q (scope %q) and %q (scope %q)",
		phone, first.Bot, first.Scope, second.Bot, second.Scope,
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

// Number strips the device suffix and the server from a stored account JID
// for display: the JID itself stays internal (ADR 0020).
func Number(jid string) string {
	return botflota.LinkedAccount{JID: jid}.Phone()
}

// HasCredentials implements botflota.DevicePresence for the live device
// store, so a consumer asks for presence without receiving the store.
func (d *Devices) HasCredentials(
	ctx context.Context, account *botflota.LinkedAccount,
) (bool, error) {
	return HasLocalDevice(ctx, d, account)
}

// AccountOwner is the account lifecycle the control plane asks for: presence
// through its Devices, and dropping the whole link (ADR 0039). fleet run
// composes it once from the stores it already holds.
type AccountOwner struct {
	*Devices
	Accounts botflota.LinkedAccountStore
	Pairings botflota.PairingStore
	Outbox   botflota.OutboxStore
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
		Accounts: o.Accounts, Pairings: o.Pairings, Outbox: o.Outbox,
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
