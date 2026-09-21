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
		container: sqlstore.NewWithDB(db, "sqlite", newWaLog(log, depsLevel)),
	}
	if err := devices.container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("whatsapp: device store: %w", err)
	}
	return devices, nil
}

// device reports a missing device as a nil result, not an error.
func (d *Devices) device(
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
	device, err := d.device(ctx, account)
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
		device, err := devices.device(ctx, &all[i])
		if err != nil {
			return err
		}
		if device == nil {
			if err := updatePairingStatus(ctx, pairings, all[i].Scope, all[i].Bot,
				botflota.PairingUnpaired, pairingNow(nil)); err != nil {
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
		if err := updatePairingStatus(ctx, pairings, scope, bot,
			botflota.PairingNotPaired, pairingNow(nil)); err != nil {
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
		if err := updatePairingStatus(ctx, pairings, scope, bot,
			botflota.PairingUnpaired, pairingNow(nil)); err != nil {
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
	device, err := devices.device(ctx, account)
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
	return removePairing(ctx, opts.Devices, opts.Accounts, opts.Pairings, account,
		pairingNow(nil))
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

func updatePairingStatus(
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
		changedAt = pairingNow(nil)
	}
	if err := pairings.Set(ctx, &botflota.PairingStatus{
		Scope: scope, Bot: bot, State: state, LastChange: changedAt,
	}); err != nil {
		return fmt.Errorf("whatsapp: set pairing status: %w", err)
	}
	return nil
}

func pairingNow(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}

func missingDeviceError(account *botflota.LinkedAccount) error {
	return fmt.Errorf(
		"whatsapp: bot %q (scope %q) has no local device credentials; "+
			"use bot pair",
		account.Bot, account.Scope,
	)
}
