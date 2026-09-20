package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	walog "go.mau.fi/whatsmeow/util/log"

	"github.com/felixzsh/botflota"
)

// defaultDeviceName is the companion name WhatsApp shows in Linked devices
// when the config does not set device_name.
const defaultDeviceName = "Botflota"

// applyDeviceName sets the name of the next registered companion. WhatsApp
// only reads it while registering a new device, so an existing link keeps the
// name it was registered with.
func applyDeviceName(name string) {
	if name == "" {
		name = defaultDeviceName
	}
	store.SetOSInfo(name, [3]uint32{0, 1, 0})
}

// PairOptions configures QR pairing for a bot.
type PairOptions struct {
	Scope    string
	Bot      string
	Devices  *Devices
	Accounts botflota.LinkedAccountStore
	Pairings botflota.PairingStore
	// Senders, Chats, and Messages receive the account-wide history sync that
	// WhatsApp delivers right after linking; all three are required to import
	// it. The fleet connection imports the rest (ADR 0035).
	Senders  botflota.SenderStore
	Chats    botflota.ChatStore
	Messages botflota.MessageStore
	History  bool
	Logger   walog.Logger
	Force    bool // discard the existing local pairing before starting QR login
	// DeviceName is the companion name shown in the phone's Linked devices;
	// empty uses the default.
	DeviceName string
	// RenderQR draws one pairing code; the default clears the terminal and
	// writes a compact QR on stdout.
	RenderQR func(code string)
	Timeout  time.Duration // maximum wait for one QR session
	Now      func() time.Time
}

// Pair runs the QR login and records the account. If the bot is already
// paired it returns the stored JID unless Force asks for a new pairing. QR
// sessions that WhatsApp resets are retried with a new code until the context
// is canceled or a non-retryable error occurs.
func Pair(ctx context.Context, opts PairOptions) (string, error) {
	if opts.Devices == nil {
		return "", errors.New("whatsapp: no device store")
	}
	if err := ValidateAccountOwners(ctx, opts.Accounts); err != nil {
		return "", err
	}
	account, err := opts.Accounts.Get(ctx, opts.Scope, opts.Bot)
	if err == nil && !opts.Force {
		return account.JID, nil
	} else if err != nil && !errors.Is(err, botflota.ErrNotFound) {
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

	applyDeviceName(opts.DeviceName)
	if account != nil {
		if err := removePairing(ctx, opts.Devices, opts.Accounts, opts.Pairings, account,
			pairingNow(opts.Now)); err != nil {
			return "", err
		}
	}
	for {
		jid, retry, err := pairOnce(ctx, opts, logger, render, timeout)
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
	logger walog.Logger,
	render func(string),
	timeout time.Duration,
) (string, bool, error) {
	client := whatsmeow.NewClient(opts.Devices.newDevice(), logger)
	defer client.Disconnect()
	attachHistory(ctx, client, opts)

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

// attachHistory imports the account-wide history sync that WhatsApp delivers
// right after linking. Whichever connection is alive captures it: this pairing
// session imports the chunks it sees before it exits, and the fleet connection
// imports the rest with the same importer (ADR 0035).
func attachHistory(ctx context.Context, client *whatsmeow.Client, opts PairOptions) {
	if !opts.History {
		return
	}
	lookup := func(lid types.JID) (types.JID, bool) {
		if client.Store == nil || client.Store.LIDs == nil {
			return types.JID{}, false
		}
		pn, err := client.Store.LIDs.GetPNForLID(ctx, lid)
		if err != nil || pn.IsEmpty() {
			return types.JID{}, false
		}
		return pn, true
	}
	importer := newHistoryImporter(opts.Scope, opts.Bot, opts.Senders, opts.Chats,
		opts.Messages, lookup, nil)
	if importer == nil {
		return
	}
	client.AddEventHandler(func(raw any) {
		if event, ok := raw.(*events.HistorySync); ok {
			importer.Handle(ctx, event)
		}
	})
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

func removePairing(
	ctx context.Context,
	devices *Devices,
	accounts botflota.LinkedAccountStore,
	pairings botflota.PairingStore,
	account *botflota.LinkedAccount,
	changedAt time.Time,
) error {
	if err := devices.delete(ctx, account); err != nil {
		return err
	}
	if err := accounts.Delete(ctx, account.Scope, account.Bot); err != nil {
		return fmt.Errorf("whatsapp: delete existing account: %w", err)
	}
	return updatePairingStatus(ctx, pairings, account.Scope, account.Bot,
		botflota.PairingNotPaired, changedAt)
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
	phone := Number(jid)
	if err := ensurePhoneAvailable(ctx, opts.Accounts, phone, opts.Scope, opts.Bot); err != nil {
		if cleanupErr := client.Store.Delete(ctx); cleanupErr != nil {
			return "", fmt.Errorf("%w; local cleanup failed: %v", err, cleanupErr)
		}
		return "", err
	}
	account := &botflota.LinkedAccount{Scope: opts.Scope, Bot: opts.Bot, JID: jid}
	if err := opts.Accounts.Put(ctx, account); err != nil {
		if cleanupErr := client.Store.Delete(ctx); cleanupErr != nil {
			return "", fmt.Errorf("%w; local cleanup failed: %v", err, cleanupErr)
		}
		return "", err
	}
	if err := updatePairingStatus(ctx, opts.Pairings, opts.Scope, opts.Bot,
		botflota.PairingPaired, pairingNow(opts.Now)); err != nil {
		return "", err
	}
	return jid, nil
}
