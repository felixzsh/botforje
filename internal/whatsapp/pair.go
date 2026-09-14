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
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
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
	Force    bool // discard the existing local pairing before starting QR login
	// RenderQR draws one pairing code; the default clears the terminal and
	// writes a compact QR on stdout.
	RenderQR func(code string)
	Timeout  time.Duration // maximum wait for one QR session
}

// Pair runs the QR login and records the account. If the bot is already
// paired it returns the stored JID unless Force asks for a new pairing. QR
// sessions that WhatsApp resets are retried with a new code until the context
// is canceled or a non-retryable error occurs.
func Pair(ctx context.Context, opts PairOptions) (string, error) {
	account, err := opts.Accounts.Get(ctx, opts.Scope, opts.Bot)
	if err == nil && !opts.Force {
		return account.JID, nil
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
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

	// WhatsApp shows this value as the linked-device name. It is sent only
	// while registering a new device; existing links keep their old name.
	store.SetOSInfo("BotForje", [3]uint32{0, 1, 0})
	container := sqlstore.NewWithDB(opts.DB, "sqlite", logger)
	if err := container.Upgrade(ctx); err != nil {
		return "", fmt.Errorf("whatsapp: device store: %w", err)
	}
	if account != nil {
		if err := clearPairing(ctx, container, opts.Accounts, account); err != nil {
			return "", err
		}
	}
	for {
		jid, retry, err := pairOnce(ctx, opts, container, logger, render, timeout)
		if err != nil {
			return "", err
		}
		if !retry {
			return jid, nil
		}
		if err := waitForPairingRetry(ctx); err != nil {
			return "", err
		}
	}
}

func pairOnce(
	ctx context.Context,
	opts PairOptions,
	container *sqlstore.Container,
	logger walog.Logger,
	render func(string),
	timeout time.Duration,
) (string, bool, error) {
	client := whatsmeow.NewClient(container.NewDevice(), logger)
	defer client.Disconnect()

	qrChannel, err := client.GetQRChannel(ctx)
	if err != nil {
		return "", false, fmt.Errorf("whatsapp: qr channel: %w", err)
	}
	if err := client.Connect(); err != nil {
		return "", false, fmt.Errorf("whatsapp: connect: %w", err)
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-deadline.C:
			return "", true, nil
		case item, ok := <-qrChannel:
			if !ok {
				return "", true, nil
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				render(item.Code)
			case whatsmeow.QRChannelSuccess.Event:
				jid, err := saveAccount(ctx, opts, client)
				if err != nil {
					return "", false, err
				}
				confirmLink(client, logger)
				return jid, false, nil
			case whatsmeow.QRChannelTimeout.Event:
				return "", true, nil
			case whatsmeow.QRChannelScannedWithoutMultidevice.Event:
				return "", false, errors.New(
					"whatsapp: QR scanned, but multi-device is not enabled",
				)
			case whatsmeow.QRChannelClientOutdated.Event:
				return "", false, errors.New("whatsapp: this client version is out of date")
			case whatsmeow.QRChannelErrUnexpectedEvent.Event:
				return "", true, nil
			case whatsmeow.QRChannelEventError:
				return "", false, fmt.Errorf("whatsapp: pairing: %w", item.Error)
			}
		}
	}
}

func waitForPairingRetry(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func clearPairing(
	ctx context.Context,
	container *sqlstore.Container,
	accounts domain.AccountStore,
	account *domain.Account,
) error {
	jid, err := types.ParseJID(account.JID)
	if err != nil {
		return fmt.Errorf("whatsapp: existing pairing has an invalid device address: %w", err)
	}
	device, err := container.GetDevice(ctx, jid)
	if err != nil {
		return fmt.Errorf("whatsapp: load existing device for re-pairing: %w", err)
	}
	if device != nil {
		if err := device.Delete(ctx); err != nil {
			return fmt.Errorf("whatsapp: delete existing device for re-pairing: %w", err)
		}
	}
	if err := accounts.Delete(ctx, account.Scope, account.Bot); err != nil {
		return fmt.Errorf("whatsapp: delete existing account for re-pairing: %w", err)
	}
	return nil
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
