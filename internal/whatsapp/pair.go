package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	walog "go.mau.fi/whatsmeow/util/log"

	"github.com/felixzsh/botforje/internal/domain"
)

// PairOptions configures QR pairing for a bot.
type PairOptions struct {
	Scope    string
	Bot      string
	DB       *sql.DB
	Accounts domain.AccountStore
	Logger   walog.Logger
	// RenderQR prints one pairing code; defaults to an ASCII QR on stdout.
	RenderQR func(code string)
	Timeout  time.Duration
}

// Pair runs the QR login and records the account. If the bot is already
// paired it returns the stored JID without contacting WhatsApp.
func Pair(ctx context.Context, opts PairOptions) (string, error) {
	if account, err := opts.Accounts.Get(ctx, opts.Scope, opts.Bot); err == nil {
		return account.JID, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}
	logger := opts.Logger
	if logger == nil {
		logger = walog.Stdout("whatsmeow/"+opts.Bot, "warn", false)
	}
	render := opts.RenderQR
	if render == nil {
		render = func(code string) { qrterminal.Generate(code, qrterminal.L, os.Stdout) }
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}

	container := sqlstore.NewWithDB(opts.DB, "sqlite", logger)
	if err := container.Upgrade(ctx); err != nil {
		return "", fmt.Errorf("whatsapp: device store: %w", err)
	}
	client := whatsmeow.NewClient(container.NewDevice(), logger)
	defer client.Disconnect()

	qrChannel, err := client.GetQRChannel(ctx)
	if err != nil {
		return "", fmt.Errorf("whatsapp: qr channel: %w", err)
	}
	if err := client.Connect(); err != nil {
		return "", fmt.Errorf("whatsapp: connect: %w", err)
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("whatsapp: pairing timed out after %s", timeout)
		case item, ok := <-qrChannel:
			if !ok {
				return saveAccount(ctx, opts, client)
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				render(item.Code)
			case whatsmeow.QRChannelEventError:
				return "", fmt.Errorf("whatsapp: pairing: %w", item.Error)
			}
		}
	}
}

func saveAccount(ctx context.Context, opts PairOptions, client *whatsmeow.Client) (string, error) {
	if client.Store == nil || client.Store.ID == nil {
		return "", errors.New("whatsapp: pairing finished without an account id")
	}
	jid := client.Store.ID.String()
	account := &domain.Account{Scope: opts.Scope, Bot: opts.Bot, JID: jid}
	if err := opts.Accounts.Put(ctx, account); err != nil {
		return "", err
	}
	return jid, nil
}
