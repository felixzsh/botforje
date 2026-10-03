package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	walog "go.mau.fi/whatsmeow/util/log"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

// defaultDeviceName is the companion name WhatsApp shows in Linked devices
// when the config does not set device_name.
const defaultDeviceName = "Botflota"

// applyDeviceName sets the name of the next registered companion: WhatsApp
// only reads it while registering, so an existing link keeps its old name.
func applyDeviceName(name string) {
	if name == "" {
		name = defaultDeviceName
	}
	store.SetOSInfo(name, [3]uint32{0, 1, 0})
}

type PairOptions struct {
	Scope    string
	Bot      string
	Devices  *Devices
	Accounts botflota.LinkedAccountStore
	Pairings botflota.PairingStore
	// Contacts, Chats, and Messages are all required to import the history
	// sync; the fleet connection imports what this session misses (ADR 0035).
	Contacts botflota.ContactStore
	Chats    botflota.ChatStore
	Messages botflota.MessageStore
	// History stores whatever WhatsApp delivers while the pairing connection
	// is alive (the bot's history_sync setting).
	History bool
	// FullHistory asks for the full history window at registration and waits
	// for it; without it only the default recent window arrives and `chat
	// sync` pages older messages per chat (ADR 0035).
	FullHistory bool
	// AddressBook receives the imported book when ImportContacts is on.
	AddressBook botflota.AddressBookStore
	// ImportContacts is off by default: the book may hold people this account
	// never wrote to.
	ImportContacts bool
	Logger         walog.Logger
	// Log receives the history-sync progress; nil discards it.
	Log *slog.Logger
	// DepsLogLevel is the minimum level for the protocol log forwarded into
	// Log when Logger is nil; a nil Log discards both.
	DepsLogLevel slog.Level
	Force        bool // discard the existing local pairing before starting QR login
	// DeviceName is the companion name in the phone's Linked devices; empty
	// uses the default.
	DeviceName string
	// RenderQR defaults to clearing the terminal and writing a compact QR on
	// stdout.
	RenderQR func(code string)
	// OnSaved, when set, runs once the account is stored, before the history
	// wait: a live pairing session uses it to stop counting its expiry.
	OnSaved func()
	// Outbox fails the queued replies of the link Force replaces, so none
	// of them leaves from the account that comes after (ADR 0040).
	Outbox  botflota.OutboxStore
	Timeout time.Duration // maximum wait for one QR session
	Now     func() time.Time
}

// PairResult is what a successful pairing produced; the command prints it as
// the result instead of the log stream (ADR 0038).
type PairResult struct {
	JID         string
	History     ingest.ImportTotals
	AddressBook int
	Elapsed     time.Duration
}

// Pair runs the QR login and records the account. An already-paired bot
// returns its stored JID unless Force asks for a new pairing, and a QR session
// WhatsApp resets is retried with a new code until the context is canceled or
// a non-retryable error occurs.
func Pair(ctx context.Context, opts PairOptions) (PairResult, error) {
	if opts.Devices == nil {
		return PairResult{}, errors.New("whatsapp: no device store")
	}
	if err := ValidateAccountOwners(ctx, opts.Accounts); err != nil {
		return PairResult{}, err
	}
	account, err := opts.Accounts.Get(ctx, opts.Scope, opts.Bot)
	if err == nil && !opts.Force {
		return PairResult{JID: account.JID}, nil
	} else if err != nil && !errors.Is(err, botflota.ErrNotFound) {
		return PairResult{}, err
	}
	logger := opts.Logger
	if logger == nil {
		// Forward the protocol log into the same structured logger on stderr,
		// never whatsmeow's stdout helper: stdout stays the command's result
		// (ADR 0038). A nil Log keeps it quiet.
		base := opts.Log
		if base == nil {
			base = botflota.DiscardLogger()
		}
		logger = NewWaLog(base, opts.DepsLogLevel)
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
	configureHistorySync(opts.FullHistory)
	if account != nil {
		if err := removePairing(ctx, opts.Devices, opts.Accounts, opts.Pairings,
			opts.Outbox, account, PairingNow(opts.Now)); err != nil {
			return PairResult{}, err
		}
	}
	started := time.Now()
	for {
		result, retry, err := pairOnce(ctx, opts, logger, render, timeout)
		if err != nil {
			return PairResult{}, err
		}
		if !retry {
			result.Elapsed = time.Since(started)
			return result, nil
		}
		if err := waitForPairingRetry(ctx); err != nil {
			return PairResult{}, err
		}
	}
}

func pairOnce(
	ctx context.Context,
	opts PairOptions,
	logger walog.Logger,
	render func(string),
	timeout time.Duration,
) (PairResult, bool, error) {
	client := whatsmeow.NewClient(opts.Devices.newDevice(), logger)
	defer client.Disconnect()
	historyWaiter, historyImporter := attachHistory(ctx, client, opts)
	contactImport := attachContacts(ctx, client, opts)

	qrChannel, err := client.GetQRChannel(ctx)
	if err != nil {
		return PairResult{}, false, fmt.Errorf("whatsapp: qr channel: %w", err)
	}
	if err := client.Connect(); err != nil {
		return PairResult{}, false, fmt.Errorf("whatsapp: connect: %w", err)
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return PairResult{}, false, ctx.Err()
		case <-deadline.C:
			return PairResult{}, true, nil
		case item, ok := <-qrChannel:
			if !ok {
				return PairResult{}, true, nil
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				render(item.Code)
			case whatsmeow.QRChannelSuccess.Event:
				jid, err := saveAccount(ctx, opts, client)
				if err != nil {
					return PairResult{}, false, err
				}
				if opts.OnSaved != nil {
					opts.OnSaved()
				}
				confirmLink(client, logger)
				if historyWaiter != nil {
					if err := historyWaiter.wait(ctx,
						historyWaitBudget(opts.FullHistory)); err != nil {
						return PairResult{}, false, err
					}
					if opts.Log != nil {
						opts.Log.Info("history sync wait finished",
							"requested_full", opts.FullHistory,
							"complete", historyWaiter.finished())
					}
				}
				result := PairResult{JID: jid}
				if historyImporter != nil {
					result.History = historyImporter.Totals()
				}
				if contactImport != nil && client.Store != nil {
					result.AddressBook = contactImport.ImportCached(ctx, client.Store.Contacts)
				}
				return result, false, nil
			case whatsmeow.QRChannelTimeout.Event:
				return PairResult{}, true, nil
			case whatsmeow.QRChannelScannedWithoutMultidevice.Event:
				return PairResult{}, false, errors.New(
					"whatsapp: QR scanned, but multi-device is not enabled",
				)
			case whatsmeow.QRChannelClientOutdated.Event:
				return PairResult{}, false, errors.New("whatsapp: this client version is out of date")
			case whatsmeow.QRChannelErrUnexpectedEvent.Event:
				return PairResult{}, true, nil
			case whatsmeow.QRChannelEventError:
				return PairResult{}, false, fmt.Errorf("whatsapp: pairing: %w", item.Error)
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

// removePairing drops the queued replies the old link still owes, then the
// device, the account record, and the pairing status: the replies end
// failed before the link goes, so none of them can leave from the account
// that replaces it (ADR 0040).
func removePairing(
	ctx context.Context,
	devices *Devices,
	accounts botflota.LinkedAccountStore,
	pairings botflota.PairingStore,
	outbox botflota.OutboxStore,
	account *botflota.LinkedAccount,
	changedAt time.Time,
) error {
	if outbox == nil {
		return errors.New("whatsapp: no outbox store")
	}
	if _, err := outbox.FailPending(ctx, account.Scope, account.Bot,
		unlinkedReason, changedAt); err != nil {
		return fmt.Errorf("whatsapp: fail queued replies: %w", err)
	}
	if err := devices.delete(ctx, account); err != nil {
		return err
	}
	if err := accounts.Delete(ctx, account.Scope, account.Bot); err != nil {
		return fmt.Errorf("whatsapp: delete existing account: %w", err)
	}
	return UpdatePairingStatus(ctx, pairings, account.Scope, account.Bot,
		botflota.PairingNotPaired, changedAt)
}

// unlinkedReason is what a reply queued for a removed link records instead
// of ever being sent (ADR 0040).
const unlinkedReason = "account unlinked"

// renderQR writes a compact half-block QR: full blocks are roughly twice as
// wide and often do not fit a terminal, making the code impossible to scan.
func renderQR(code string, w io.Writer) {
	qrterminal.GenerateHalfBlock(code, qrterminal.L, w)
}

// renderPairingQR clears the previous code so only the current one is
// visible: stale codes expire and make the phone report a failed link.
func renderPairingQR(code string, w io.Writer) {
	fmt.Fprint(w, "\033[2J\033[H")
	fmt.Fprintln(w, "Scan with WhatsApp > Linked devices. The code refreshes on its own.")
	renderQR(code, w)
}

// RenderQR clears the terminal and writes the current pairing code for a
// client that shows the QR of a session it does not run itself (ADR 0025).
func RenderQR(code string) { renderPairingQR(code, os.Stdout) }

// confirmLink waits for the server to drop the pairing socket, then brings
// the companion online: the phone reports a failed link if it never comes
// online, and exiting right after pairing caused exactly that.
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
	if err := UpdatePairingStatus(ctx, opts.Pairings, opts.Scope, opts.Bot,
		botflota.PairingPaired, PairingNow(opts.Now)); err != nil {
		return "", err
	}
	return jid, nil
}
