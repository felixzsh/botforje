package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
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
	// RenderQR draws one pairing code; the default clears the terminal and
	// writes a compact QR on stdout.
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
		// Pairing is interactive; keep whatsmeow's routine warnings (like the
		// websocket close after a successful link) out of the terminal, but
		// still surface real errors.
		logger = walog.Stdout("whatsmeow/"+opts.Bot, "error", false)
	}
	render := opts.RenderQR
	if render == nil {
		render = func(code string) { renderPairingQR(code, os.Stdout) }
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
				jid, err := saveAccount(ctx, opts, client)
				if err != nil {
					return "", err
				}
				confirmLink(client, logger)
				return jid, nil
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

// renderQR writes a compact half-block QR, the rendering whatsmeow's own
// example uses. Full blocks are roughly twice as wide and often do not fit a
// terminal, which makes the code impossible to scan.
func renderQR(code string, w io.Writer) {
	qrterminal.GenerateHalfBlock(code, qrterminal.L, w)
}

// renderPairingQR clears the previous code so only the current one is visible:
// stale codes expire and make the phone report a failed link, and the client
// rotates them every ~20 seconds.
func renderPairingQR(code string, w io.Writer) {
	fmt.Fprint(w, "\033[2J\033[H")
	fmt.Fprintln(w, "Scan with WhatsApp > Linked devices. The code refreshes on its own.")
	renderQR(code, w)
}

// confirmLink waits for the server to drop the pairing socket, reconnects, and
// waits until the companion is online. The server accepts the link at
// pair-success, but the phone reports failure if the companion never comes
// online; exiting right after pairing caused exactly that.
func confirmLink(client *whatsmeow.Client, logger walog.Logger) {
	if !waitUntilDisconnected(client, 5*time.Second) {
		logger.Debugf("pairing socket still open; confirming anyway")
	}
	if err := client.Connect(); err != nil && !errors.Is(err, whatsmeow.ErrAlreadyConnected) {
		logger.Errorf("reconnect after pairing failed: %v", err)
		return
	}
	if !client.WaitForConnection(20 * time.Second) {
		logger.Errorf("paired, but the confirmation connection did not come up")
	}
}

func waitUntilDisconnected(client *whatsmeow.Client, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !client.IsConnected() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
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
