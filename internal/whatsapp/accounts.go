package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	walog "go.mau.fi/whatsmeow/util/log"

	"github.com/felixzsh/botflota/internal/domain"
)

// UnpairOptions configures removal of one bot's local pairing.
type UnpairOptions struct {
	Scope    string
	Bot      string
	DB       *sql.DB
	Accounts domain.AccountStore
}

// ValidateAccountOwners rejects more than one bot using the same phone number.
func ValidateAccountOwners(ctx context.Context, accounts domain.AccountStore) error {
	all, err := accounts.List(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp: list account ownership: %w", err)
	}
	owners := make(map[string]domain.Account, len(all))
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

// ValidateAccounts checks stored ownership and local whatsmeow credentials.
// Missing local credentials are recoverable and do not abort validation.
func ValidateAccounts(
	ctx context.Context, db *sql.DB, accounts domain.AccountStore,
) error {
	if db == nil {
		return errors.New("whatsapp: no database")
	}
	if err := ValidateAccountOwners(ctx, accounts); err != nil {
		return err
	}
	all, err := accounts.List(ctx)
	if err != nil {
		return fmt.Errorf("whatsapp: list paired accounts: %w", err)
	}
	container := sqlstore.NewWithDB(db, "sqlite", walog.Noop)
	if err := container.Upgrade(ctx); err != nil {
		return fmt.Errorf("whatsapp: device store: %w", err)
	}
	for i := range all {
		device, err := localDevice(ctx, container, &all[i])
		if err != nil {
			return err
		}
		if device == nil {
			if err := updatePairingStatus(ctx, accounts, all[i].Scope, all[i].Bot,
				domain.PairingUnpaired, pairingNow(nil)); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckPairing reports whether one configured bot can initialize its channel.
// Missing account records and local credentials are expected, recoverable
// states; other database and pairing errors are returned.
func CheckPairing(
	ctx context.Context,
	db *sql.DB,
	accounts domain.AccountStore,
	scope, bot string,
) (domain.PairingState, error) {
	if db == nil {
		return "", errors.New("whatsapp: no database")
	}
	account, err := accounts.Get(ctx, scope, bot)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		if err := updatePairingStatus(ctx, accounts, scope, bot,
			domain.PairingNotPaired, pairingNow(nil)); err != nil {
			return "", err
		}
		return domain.PairingNotPaired, nil
	case err != nil:
		return "", fmt.Errorf("whatsapp: get pairing for bot %q: %w", bot, err)
	}
	ready, err := HasLocalDevice(ctx, db, account)
	if err != nil {
		return "", err
	}
	if !ready {
		if err := updatePairingStatus(ctx, accounts, scope, bot,
			domain.PairingUnpaired, pairingNow(nil)); err != nil {
			return "", err
		}
		return domain.PairingUnpaired, nil
	}
	return domain.PairingPaired, nil
}

// HasLocalDevice reports whether an account has matching local credentials.
func HasLocalDevice(
	ctx context.Context, db *sql.DB, account *domain.Account,
) (bool, error) {
	if db == nil {
		return false, errors.New("whatsapp: no database")
	}
	container := sqlstore.NewWithDB(db, "sqlite", walog.Noop)
	if err := container.Upgrade(ctx); err != nil {
		return false, fmt.Errorf("whatsapp: device store: %w", err)
	}
	device, err := localDevice(ctx, container, account)
	if err != nil {
		return false, err
	}
	return device != nil, nil
}

// Unpair removes local credentials and the account record. It does not unlink
// the companion from WhatsApp; remove that old device from the phone if needed.
func Unpair(ctx context.Context, opts UnpairOptions) error {
	if opts.DB == nil {
		return errors.New("whatsapp: no database")
	}
	account, err := opts.Accounts.Get(ctx, opts.Scope, opts.Bot)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("whatsapp: bot %q is not paired", opts.Bot)
	}
	if err != nil {
		return err
	}
	container := sqlstore.NewWithDB(opts.DB, "sqlite", walog.Noop)
	if err := container.Upgrade(ctx); err != nil {
		return fmt.Errorf("whatsapp: device store: %w", err)
	}
	return removePairing(ctx, container, opts.Accounts, account, pairingNow(nil))
}

func localDevice(
	ctx context.Context, container *sqlstore.Container, account *domain.Account,
) (*store.Device, error) {
	jid, err := types.ParseJID(account.JID)
	if err != nil {
		return nil, fmt.Errorf(
			"whatsapp: bot %q (scope %q) has an invalid stored pairing",
			account.Bot, account.Scope,
		)
	}
	device, err := container.GetDevice(ctx, jid)
	if err != nil {
		return nil, fmt.Errorf(
			"whatsapp: load device for bot %q (scope %q): %w",
			account.Bot, account.Scope, err,
		)
	}
	return device, nil
}

func ensurePhoneAvailable(
	ctx context.Context, accounts domain.AccountStore, phone, scope, bot string,
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
				domain.Account{Scope: scope, Bot: bot},
			)
		}
	}
	return nil
}

func duplicateAccountError(
	phone string, first, second domain.Account,
) error {
	return fmt.Errorf(
		"whatsapp: phone number %s is assigned to bots %q (scope %q) and %q (scope %q)",
		phone, first.Bot, first.Scope, second.Bot, second.Scope,
	)
}

func updatePairingStatus(
	ctx context.Context,
	accounts domain.AccountStore,
	scope, bot string,
	state domain.PairingState,
	changedAt time.Time,
) error {
	current, err := accounts.GetPairingStatus(ctx, scope, bot)
	if err == nil && current.State == state && !current.LastChange.IsZero() {
		return nil
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("whatsapp: get pairing status: %w", err)
	}
	if changedAt.IsZero() {
		changedAt = pairingNow(nil)
	}
	if err := accounts.SetPairingStatus(ctx, &domain.PairingStatus{
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

func missingDeviceError(account *domain.Account) error {
	return fmt.Errorf(
		"whatsapp: bot %q (scope %q) has no local device credentials; "+
			"use bot pair",
		account.Bot, account.Scope,
	)
}
