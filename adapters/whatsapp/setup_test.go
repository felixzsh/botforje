package whatsapp

// The P3 bind-runtime gate for the WhatsApp translation (ADR 0053): the same
// neutral session machinery the fakes speak — inspection, bind, and unbind
// selected by the bot's definition, with staged commits, idempotent keys,
// and generations — over the real QR pairing, device store, and binding
// persistence. No test here uses a phone field or a universal QR.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"github.com/felixzsh/botflota"
)

func testSetupManager(t *testing.T, opts PairManagerOptions) (*PairManager, *int64) {
	t.Helper()
	var seq int64
	if opts.ID == nil {
		opts.ID = func() string {
			return fmt.Sprintf("session-%d", atomic.AddInt64(&seq, 1))
		}
	}
	manager := testPairManager(t, opts)
	t.Cleanup(manager.Close)
	return manager, &seq
}

func readNeutral(
	t *testing.T, manager *PairManager, scope, bot, id string,
) *botflota.SetupSession {
	t.Helper()
	session, err := manager.ReadSetup(context.Background(), scope, bot, id)
	if err != nil {
		t.Fatalf("ReadSetup: %v", err)
	}
	return session
}

func TestSetupBeginReadAndRotation(t *testing.T) {
	codes := make(chan string, 4)
	manager, _ := testSetupManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		for _, code := range []string{"code-1", "code-2"} {
			opts.RenderQR(code)
		}
		codes <- "rendered"
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
	started, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	if started.Method != SetupMethodQR || started.Mode != AccountMode {
		t.Fatalf("session = %#v, want the qr/native session", started)
	}
	if started.State != botflota.SetupStarting || started.Revision != 1 {
		t.Fatalf("session = %#v, want starting at revision 1", started)
	}
	if started.Step != nil {
		t.Fatalf("starting session carries a step: %#v", started.Step)
	}
	select {
	case <-codes:
	case <-time.After(2 * time.Second):
		t.Fatal("the pairing worker never rendered a code")
	}
	first := readNeutral(t, manager, "dev", "alpha", started.ID)
	if first.State != botflota.SetupAwaitingInput {
		t.Fatalf("state = %s, want awaiting_input", first.State)
	}
	if first.Step == nil || first.Step.Kind != botflota.SetupStepDisplay ||
		first.Step.Display == nil || first.Step.Display.Format != "qr" ||
		first.Step.Display.Payload != "code-2" {
		t.Fatalf("step = %#v, want the latest display challenge", first.Step)
	}
	if first.Revision < 3 {
		t.Fatalf("revision = %d, want one bump per rotation", first.Revision)
	}
	if _, err := manager.ReadSetup(
		context.Background(), "dev", "alpha", "stale-id",
	); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("stale id read = %v, want ErrNotFound", err)
	}
	if _, err := manager.ReadSetup(
		context.Background(), "dev", "other", started.ID,
	); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("foreign bot read = %v, want ErrNotFound", err)
	}
	if err := manager.CancelSetup(
		context.Background(), "dev", "alpha", started.ID,
	); err != nil {
		t.Fatalf("CancelSetup: %v", err)
	}
}

func TestSetupRequestKeyIdempotency(t *testing.T) {
	release := make(chan struct{})
	manager, _ := testSetupManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, _ PairOptions) (PairResult, error) {
		<-ctx.Done()
		<-release
		return PairResult{}, ctx.Err()
	}
	defer close(release)
	ctx := context.Background()
	first, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	again, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup same key: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("same key returned %s, want %s", again.ID, first.ID)
	}
	if _, err := manager.BeginSetup(ctx, "dev", "alpha", botflota.SetupBegin{
		Method: "qr", RequestKey: "key-1", Params: map[string]string{"full_history": "true"},
	}); !errors.Is(err, botflota.ErrConflict) {
		t.Fatalf("reused key with other params = %v, want ErrConflict", err)
	}
	if _, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-2"}); !errors.Is(
		err, botflota.ErrBusy,
	) {
		t.Fatalf("second live session = %v, want ErrBusy", err)
	}
	if _, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "token", RequestKey: "key-3"}); err == nil {
		t.Fatal("unknown method was accepted")
	}
}

func TestSetupInputAndCancelSemantics(t *testing.T) {
	release := make(chan struct{})
	manager, _ := testSetupManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, _ PairOptions) (PairResult, error) {
		<-ctx.Done()
		<-release
		return PairResult{}, ctx.Err()
	}
	defer close(release)
	ctx := context.Background()
	live, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	current := readNeutral(t, manager, "dev", "alpha", live.ID)
	// QR setup takes no inputs: even a well-formed one is refused, and a
	// stale revision never acts.
	if _, err := manager.InputSetup(ctx, "dev", "alpha", live.ID, botflota.SetupInput{
		Revision: current.Revision, RequestKey: "input-1",
	}); !errors.Is(err, botflota.ErrConflict) {
		t.Fatalf("qr input = %v, want ErrConflict", err)
	}
	if _, err := manager.InputSetup(ctx, "dev", "alpha", live.ID, botflota.SetupInput{
		Revision: current.Revision + 10, RequestKey: "input-2",
	}); !errors.Is(err, botflota.ErrConflict) {
		t.Fatalf("stale revision input = %v, want ErrConflict", err)
	}
	if _, err := manager.InputSetup(ctx, "dev", "alpha", "stale-id",
		botflota.SetupInput{Revision: 1}); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("stale id input = %v, want ErrNotFound", err)
	}
	// Cancellation ends the live session; the worker's late failure cannot
	// rewrite the outcome.
	if err := manager.CancelSetup(ctx, "dev", "alpha", live.ID); err != nil {
		t.Fatalf("CancelSetup: %v", err)
	}
	cancelled := readNeutral(t, manager, "dev", "alpha", live.ID)
	if cancelled.State != botflota.SetupCancelled || cancelled.ErrorCode != "cancelled" {
		t.Fatalf("session = %#v, want the cancellation to stand", cancelled)
	}
	if cancelled.Step != nil {
		t.Fatalf("cancelled session keeps a step: %#v", cancelled.Step)
	}
	if err := manager.CancelSetup(ctx, "dev", "alpha", "stale-id"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("stale id cancel = %v, want ErrNotFound", err)
	}
}

func TestSetupCommitKeepsSuccessAcrossCancel(t *testing.T) {
	manager, _ := testSetupManager(t, PairManagerOptions{})
	manager.pair = func(_ context.Context, opts PairOptions) (PairResult, error) {
		opts.RenderQR("code")
		opts.OnSaved()
		return PairResult{Identity: waKey("5215500000001")}, nil
	}
	ctx := context.Background()
	started, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		session := readNeutral(t, manager, "dev", "alpha", started.ID)
		if session.State == botflota.SetupSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session never committed: %#v", session)
		}
		time.Sleep(time.Millisecond)
	}
	// The commit is staged: cancelling now cannot claim the binding never
	// existed.
	if err := manager.CancelSetup(ctx, "dev", "alpha", started.ID); err != nil {
		t.Fatalf("CancelSetup after commit: %v", err)
	}
	kept := readNeutral(t, manager, "dev", "alpha", started.ID)
	if kept.State != botflota.SetupSucceeded {
		t.Fatalf("state = %s, want the committed success kept", kept.State)
	}
	legacy, err := manager.CurrentPair(ctx, "dev", "alpha")
	if err != nil {
		t.Fatalf("CurrentPair: %v", err)
	}
	if legacy.State != botflota.PairSucceeded ||
		legacy.Result.Identity != waKey("5215500000001") {
		t.Fatalf("legacy session = %#v, want the same committed outcome", legacy)
	}
}

func TestSetupActivationFailureIsReportedSeparately(t *testing.T) {
	manager, _ := testSetupManager(t, PairManagerOptions{
		OnPaired: func(context.Context, string, string) error {
			return errors.New("channel failed to start")
		},
	})
	manager.pair = func(_ context.Context, opts PairOptions) (PairResult, error) {
		opts.RenderQR("code")
		opts.OnSaved()
		return PairResult{Identity: waKey("5215500000001")}, nil
	}
	ctx := context.Background()
	started, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		session := readNeutral(t, manager, "dev", "alpha", started.ID)
		if session.State == botflota.SetupSucceeded {
			if session.ActivationError == "" {
				t.Fatalf("neutral session = %#v, want the activation failure", session)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session never committed: %#v", session)
		}
		time.Sleep(time.Millisecond)
	}
	legacy, err := manager.CurrentPair(ctx, "dev", "alpha")
	if err != nil {
		t.Fatalf("CurrentPair: %v", err)
	}
	if legacy.State != botflota.PairSucceeded {
		t.Fatalf("state = %s, want the committed success kept", legacy.State)
	}
	if legacy.ActivationError == "" {
		t.Fatal("the activation failure was not reported separately")
	}
}

func TestSetupExpiryEndsTheSession(t *testing.T) {
	manager, _ := testSetupManager(t, PairManagerOptions{Lifetime: 20 * time.Millisecond})
	manager.pair = func(ctx context.Context, _ PairOptions) (PairResult, error) {
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
	ctx := context.Background()
	started, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		session := readNeutral(t, manager, "dev", "alpha", started.ID)
		if session.State == botflota.SetupExpired {
			if session.ErrorCode != "expired" || session.Step != nil {
				t.Fatalf("session = %#v, want an expired close", session)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session never expired: %#v", session)
		}
		time.Sleep(time.Millisecond)
	}
}

func saveTestDevice(
	t *testing.T, devices *Devices, ctx context.Context, subject string,
) {
	t.Helper()
	jid := types.NewADJID(subject, 0, 1)
	device := devices.newDevice()
	device.ID = &jid
	device.Account = &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte{},
		AccountSignature:    make([]byte, 64),
		AccountSignatureKey: make([]byte, 32),
		DeviceSignature:     make([]byte, 64),
	}
	if err := device.Save(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestInspectBindingSeparatesReadinessFacts(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	owner := AccountOwner{
		Devices: devices, Bindings: db.Bindings(), Pairings: db.Pairings(),
		Outbox: db.Outbox(),
	}
	// No binding: the bot stays inspectable and setup-capable.
	fresh, err := owner.InspectBinding(ctx, "dev", "nobody")
	if err != nil {
		t.Fatalf("InspectBinding: %v", err)
	}
	if fresh.Condition != botflota.ReadinessUnconfigured || fresh.Binding != nil ||
		fresh.Method != SetupMethodQR {
		t.Fatalf("status = %#v, want unconfigured with a method", fresh)
	}
	// A binding without local credentials needs its owner, not a restart
	// of its peers.
	account := &botflota.Binding{
		Scope: "dev", Bot: "revoked", Provider: ProviderName,
		Adapter: AdapterKind, Mode: AccountMode, Identity: waKey("5215500000001"),
	}
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	revoked, err := owner.InspectBinding(ctx, "dev", "revoked")
	if err != nil {
		t.Fatalf("InspectBinding: %v", err)
	}
	if revoked.Condition != botflota.ReadinessActionRequired ||
		revoked.Binding == nil || revoked.Binding.Generation != 1 ||
		revoked.Method != SetupMethodQR {
		t.Fatalf("status = %#v, want action_required with the binding", revoked)
	}
	// Stored credentials prove presence only: authorization and transport
	// stay reported unknowns, never assumed permissions.
	saveTestDevice(t, devices, ctx, "5215500000002")
	linked := &botflota.Binding{
		Scope: "dev", Bot: "linked", Provider: ProviderName,
		Adapter: AdapterKind, Mode: AccountMode, Identity: waKey("5215500000002"),
	}
	if err := db.Bindings().Commit(ctx, linked); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	ready, err := owner.InspectBinding(ctx, "dev", "linked")
	if err != nil {
		t.Fatalf("InspectBinding: %v", err)
	}
	if ready.Condition != botflota.ReadinessConfigured || ready.Binding == nil {
		t.Fatalf("status = %#v, want the committed binding reported", ready)
	}
	if len(ready.Reasons) == 0 {
		t.Fatal("configured readiness reports no reasons")
	}
	// Another provider's binding is never this adapter's business.
	foreign := &botflota.Binding{
		Scope: "dev", Bot: "foreign", Provider: "telegram",
		Identity: botflota.IdentityKey{Provider: "telegram", Subject: "7"},
	}
	if err := db.Bindings().Commit(ctx, foreign); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := owner.InspectBinding(ctx, "dev", "foreign"); err == nil {
		t.Fatal("a foreign binding inspected without an error")
	}
}

func TestDetachBindingSettlesAndKeepsHistory(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	manager, _ := testSetupManager(t, PairManagerOptions{})
	owner := AccountOwner{
		Devices: devices, Bindings: db.Bindings(), Pairings: db.Pairings(),
		Outbox: db.Outbox(), Pairs: manager,
	}
	saveTestDevice(t, devices, ctx, "5215500000003")
	account := &botflota.Binding{
		Scope: "dev", Bot: "alpha", Provider: ProviderName,
		Adapter: AdapterKind, Mode: AccountMode, Identity: waKey("5215500000003"),
	}
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	target := botflota.ChatTarget{Provider: "whatsapp", Kind: botflota.ChatDirect, Thread: "9"}
	if _, err := db.Chats().Ensure(ctx, "dev", "alpha", target, 0, time.Now()); err != nil {
		t.Fatalf("Ensure chat: %v", err)
	}
	if err := owner.DetachBinding(ctx, "dev", "alpha"); err != nil {
		t.Fatalf("DetachBinding: %v", err)
	}
	if _, err := db.Bindings().Get(ctx, "dev", "alpha"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("binding after detach = %v, want it forgotten", err)
	}
	if _, err := db.Chats().ForTarget(ctx, "dev", "alpha", target); err != nil {
		t.Fatalf("chat after detach = %v, want the history kept", err)
	}
	if _, err := owner.InspectBinding(ctx, "dev", "alpha"); err != nil {
		t.Fatalf("InspectBinding after detach: %v", err)
	}
}

// TestDetachDuringImport: detaching while the import wait is still running
// ends the wait without activating the bot, and the queued replies never
// leave from the account that replaces the link.
func TestDetachDuringImport(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	activated := make(chan struct{}, 1)
	manager, _ := testSetupManager(t, PairManagerOptions{
		OnPaired: func(context.Context, string, string) error {
			activated <- struct{}{}
			return nil
		},
	})
	importing := make(chan struct{})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		opts.RenderQR("code")
		opts.OnSaved()
		close(importing)
		<-ctx.Done()
		return PairResult{Identity: waKey("5215500000004")}, ctx.Err()
	}
	owner := AccountOwner{
		Devices: devices, Bindings: db.Bindings(), Pairings: db.Pairings(),
		Outbox: db.Outbox(), Pairs: manager,
	}
	// The staged account commits before the teardown arrives; the detach
	// then forgets exactly that binding.
	staged := &botflota.Binding{
		Scope: "dev", Bot: "alpha", Provider: ProviderName,
		Adapter: AdapterKind, Mode: AccountMode, Identity: waKey("5215500000004"),
	}
	if err := db.Bindings().Commit(ctx, staged); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	started, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	select {
	case <-importing:
	case <-time.After(2 * time.Second):
		t.Fatal("the pairing worker never reached the import wait")
	}
	if err := owner.DetachBinding(ctx, "dev", "alpha"); err != nil {
		t.Fatalf("DetachBinding: %v", err)
	}
	select {
	case <-activated:
		t.Fatal("the teardown activated a bot it removes right after")
	default:
	}
	// The staged account was stored before the teardown arrived, so the
	// session still reports the success that already happened — while the
	// detach forgot the binding.
	kept := readNeutral(t, manager, "dev", "alpha", started.ID)
	if kept.State != botflota.SetupSucceeded {
		t.Fatalf("state = %s, want the stored outcome reported", kept.State)
	}
	if _, err := db.Bindings().Get(ctx, "dev", "alpha"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("binding after detach = %v, want it forgotten", err)
	}
}

// TestUsableBindingNeedsCredentials: a committed row alone never proves a
// usable account. Renewal must run the ceremony again instead of reporting
// a link that cannot send.
func TestUsableBindingNeedsCredentials(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	account := &botflota.Binding{
		Scope: "dev", Bot: "alpha", Provider: ProviderName,
		Adapter: AdapterKind, Mode: AccountMode, Identity: waKey("5215500000001"),
	}
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	opts := PairOptions{
		Scope: "dev", Bot: "alpha", Devices: devices, Bindings: db.Bindings(),
	}
	if usable, err := usableBinding(ctx, opts, account); err != nil || usable {
		t.Fatalf("usable = %v, err = %v; want false without credentials", usable, err)
	}
	saveTestDevice(t, devices, ctx, "5215500000001")
	if usable, err := usableBinding(ctx, opts, account); err != nil || !usable {
		t.Fatalf("usable = %v, err = %v; want true with credentials", usable, err)
	}
	if usable, err := usableBinding(ctx, opts, nil); err != nil || usable {
		t.Fatalf("usable = %v, err = %v; want false without a binding", usable, err)
	}
}

// TestRetireStaleLinkKeepsBindingRow: settling the old link drops its queue
// and credentials but keeps the row, so the next commit tells a renewal of
// the same identity from a replacement by another one.
func TestRetireStaleLinkKeepsBindingRow(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	account := &botflota.Binding{
		Scope: "dev", Bot: "alpha", Provider: ProviderName,
		Adapter: AdapterKind, Mode: AccountMode, Identity: waKey("5215500000001"),
	}
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := retireStaleLink(ctx, devices, db.Outbox(), account, time.Now()); err != nil {
		t.Fatalf("retireStaleLink: %v", err)
	}
	kept, err := db.Bindings().Get(ctx, "dev", "alpha")
	if err != nil {
		t.Fatalf("Get after retire: %v", err)
	}
	if kept.Generation != 1 {
		t.Fatalf("generation = %d, want the row kept at 1", kept.Generation)
	}
	renewal := &botflota.Binding{
		Scope: "dev", Bot: "alpha", Provider: ProviderName,
		Adapter: AdapterKind, Mode: AccountMode, Identity: waKey("5215500000001"),
	}
	if err := db.Bindings().Commit(ctx, renewal); err != nil {
		t.Fatalf("Commit renewal: %v", err)
	}
	if renewal.Generation != 1 {
		t.Fatalf("generation = %d, want the renewal to keep 1", renewal.Generation)
	}
	replacement := &botflota.Binding{
		Scope: "dev", Bot: "alpha", Provider: ProviderName,
		Adapter: AdapterKind, Mode: AccountMode, Identity: waKey("5215500000002"),
	}
	if err := db.Bindings().Commit(ctx, replacement); err != nil {
		t.Fatalf("Commit replacement: %v", err)
	}
	if replacement.Generation != 2 {
		t.Fatalf("generation = %d, want the replacement at 2", replacement.Generation)
	}
}

// TestSetupCommitWinsOverRacingCancel: a commit that lands after cancel
// or expiry won the race still reports the success that already happened,
// instead of hiding a durable association behind a cancelled outcome.
func TestSetupCommitWinsOverRacingCancel(t *testing.T) {
	release := make(chan struct{})
	manager, _ := testSetupManager(t, PairManagerOptions{})
	manager.pair = func(_ context.Context, opts PairOptions) (PairResult, error) {
		opts.RenderQR("code")
		<-release
		return PairResult{Identity: waKey("5215500000001")}, nil
	}
	ctx := context.Background()
	ref := botflota.BotRef{Scope: "dev", Bot: "alpha"}
	started, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	// The cancel wins the race first; the commit lands after, while the
	// worker still runs.
	if err := manager.CancelSetup(ctx, "dev", "alpha", started.ID); err != nil {
		t.Fatalf("CancelSetup: %v", err)
	}
	manager.mu.Lock()
	late, ok := manager.sessions[ref]
	manager.mu.Unlock()
	if !ok {
		t.Fatal("the cancelled session left the manager")
	}
	manager.markSaved(ref, late)
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		session := readNeutral(t, manager, "dev", "alpha", started.ID)
		if session.State == botflota.SetupSucceeded {
			if session.ErrorCode != "" {
				t.Fatalf("session = %#v, want no error on the reconciled race",
					session)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session = %#v, want the commit reported", session)
		}
		time.Sleep(time.Millisecond)
	}
}

type failingPairings struct{ botflota.PairingStore }

func (failingPairings) Get(
	_ context.Context, _, _ string,
) (*botflota.PairingStatus, error) {
	return nil, botflota.ErrNotFound
}

func (failingPairings) Set(
	_ context.Context, _ *botflota.PairingStatus,
) error {
	return errors.New("status store is down")
}

// TestSaveAccountToleratesStatusFailure: once the binding is committed, a
// later auxiliary write cannot fail the setup or hide the link.
func TestSaveAccountToleratesStatusFailure(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	jid := types.NewADJID("5215500000005", 0, 1)
	device := devices.newDevice()
	device.ID = &jid
	client := newOfflineClient(device)
	committed := make(chan struct{}, 1)
	opts := PairOptions{
		Scope: "dev", Bot: "alpha", Devices: devices,
		Bindings: db.Bindings(), Pairings: failingPairings{},
		Outbox: db.Outbox(), Log: botflota.DiscardLogger(), Now: time.Now,
		OnSaved: func() { committed <- struct{}{} },
	}
	identity, err := saveAccount(ctx, opts, client)
	if err != nil {
		t.Fatalf("saveAccount: %v", err)
	}
	if identity != waKey("5215500000005") {
		t.Fatalf("identity = %#v, want the committed one", identity)
	}
	select {
	case <-committed:
	default:
		t.Fatal("OnSaved did not run before the status write")
	}
	stored, err := db.Bindings().Get(ctx, "dev", "alpha")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Identity != identity || stored.Generation != 1 {
		t.Fatalf("binding = %#v, want the committed association", stored)
	}
}

// TestDetachExcludesNewBegin: the detach wait and the removal hold one
// per-bot exclusion, so a new session can neither slip between them nor
// lose its binding to a detach that started first.
func TestDetachExcludesNewBegin(t *testing.T) {
	manager, _ := testSetupManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, _ PairOptions) (PairResult, error) {
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
	ctx := context.Background()
	live, err := manager.BeginSetup(ctx, "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	detached := make(chan error, 1)
	go func() {
		detached <- manager.Detach(ctx, "dev", "alpha",
			func(context.Context) error {
				close(entered)
				<-release
				return nil
			})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the detach never reached the removal")
	}
	nextDone := make(chan *botflota.SetupSession, 1)
	nextErr := make(chan error, 1)
	go func() {
		next, err := manager.BeginSetup(ctx, "dev", "alpha",
			botflota.SetupBegin{Method: "qr", RequestKey: "key-2"})
		if err != nil {
			nextErr <- err
			return
		}
		nextDone <- next
	}()
	select {
	case next := <-nextDone:
		t.Fatalf("a new session %s slipped between the wait and the removal", next.ID)
	case err := <-nextErr:
		t.Fatalf("a new session failed while detached: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// While the detach still holds the exclusion, the old session stays
	// readable: the detach ended the live setup before removing the
	// binding, and the blocked begin cannot have replaced it yet.
	old, err := manager.ReadSetup(ctx, "dev", "alpha", live.ID)
	if err != nil {
		t.Fatalf("ReadSetup old: %v", err)
	}
	if old.State != botflota.SetupCancelled {
		t.Fatalf("old session = %s, want the detach to have ended it", old.State)
	}
	close(release)
	if err := <-detached; err != nil {
		t.Fatalf("Detach: %v", err)
	}
	select {
	case next := <-nextDone:
		if next.ID == live.ID {
			t.Fatal("the new session reused the detached one")
		}
		// The replacement evicts the ended slot: a stale id stays
		// inert and can never touch the new session.
		if _, err := manager.ReadSetup(ctx, "dev", "alpha", live.ID); !errors.Is(
			err, botflota.ErrNotFound,
		) {
			t.Fatalf("old id after replacement = %v, want ErrNotFound", err)
		}
	case err := <-nextErr:
		t.Fatalf("BeginSetup after detach: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("the new session never started after the detach")
	}
}

// newOfflineClient builds a WhatsApp client that never dials: the device
// carries the linked identity, so account bookkeeping runs without a
// transport.
func newOfflineClient(device *store.Device) *whatsmeow.Client {
	return whatsmeow.NewClient(device,
		NewWaLog(botflota.DiscardLogger(), slog.LevelError))
}

// TestDetachWithoutSessionRemovesDirectly: with no live setup there is no
// wait, only the removal.
func TestDetachWithoutSessionRemovesDirectly(t *testing.T) {
	manager, _ := testSetupManager(t, PairManagerOptions{})
	removed := false
	err := manager.Detach(context.Background(), "dev", "alpha",
		func(context.Context) error { removed = true; return nil })
	if err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if !removed {
		t.Fatal("the removal never ran")
	}
}
