package whatsapp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

var (
	t0 = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	t1 = time.Date(2026, 9, 13, 10, 5, 0, 0, time.UTC)
)

// TestApplyDeviceName covers the white-label name: the configured one wins and
// an empty value falls back to the default.
func TestApplyDeviceName(t *testing.T) {
	oldName := store.DeviceProps.GetOs()
	oldVersion := [3]uint32{
		store.DeviceProps.Version.GetPrimary(),
		store.DeviceProps.Version.GetSecondary(),
		store.DeviceProps.Version.GetTertiary(),
	}
	t.Cleanup(func() { store.SetOSInfo(oldName, oldVersion) })

	applyDeviceName("Acme Bots")
	if got := store.DeviceProps.GetOs(); got != "Acme Bots" {
		t.Fatalf("device name = %q, want the configured one", got)
	}
	applyDeviceName("")
	if got := store.DeviceProps.GetOs(); got != defaultDeviceName {
		t.Fatalf("device name = %q, want %q", got, defaultDeviceName)
	}
}

func TestBackoffBounds(t *testing.T) {
	b := NewBackoff(100*time.Millisecond, time.Second, rand.New(rand.NewSource(1)))
	for attempt := 0; attempt < 8; attempt++ {
		delay := b.Next()
		if delay < 50*time.Millisecond || delay > time.Second {
			t.Fatalf("attempt %d delay %s is out of bounds", attempt, delay)
		}
	}
	b.Reset()
	if delay := b.Next(); delay > 100*time.Millisecond {
		t.Fatalf("after reset delay %s exceeds the first interval", delay)
	}
}

func TestJitteredBounds(t *testing.T) {
	a := &Adapter{
		settings: Settings{Jitter: 100 * time.Millisecond},
		rnd:      rand.New(rand.NewSource(1)),
	}
	for i := 0; i < 50; i++ {
		got := a.jittered(50 * time.Millisecond)
		if got < 50*time.Millisecond || got > 150*time.Millisecond {
			t.Fatalf("jittered = %s, want within [50ms, 150ms]", got)
		}
	}
	a.settings.Jitter = 0
	if got := a.jittered(50 * time.Millisecond); got != 50*time.Millisecond {
		t.Fatalf("jitter 0 = %s, want the base 50ms", got)
	}
}

func TestDedup(t *testing.T) {
	d := newDedup(2)
	if !d.fresh("a") || !d.fresh("b") {
		t.Fatal("new ids must be fresh")
	}
	if d.fresh("a") {
		t.Fatal("a duplicate must be rejected")
	}
	if !d.fresh("c") {
		t.Fatal("c must be fresh")
	}
	if !d.fresh("a") {
		t.Fatal("an evicted id must be fresh again")
	}
	if !d.fresh("") || !d.fresh("") {
		t.Fatal("an empty id is always fresh")
	}
}

func TestResolveSender(t *testing.T) {
	pn := types.NewJID("5215500000000", types.DefaultUserServer)
	lid := types.NewJID("212150010273830", types.HiddenUserServer)
	found := func(types.JID) (types.JID, bool) { return pn, true }
	missing := func(types.JID) (types.JID, bool) { return types.JID{}, false }

	cases := []struct {
		name   string
		info   types.MessageInfo
		lookup pnLookup
		want   types.JID
		ok     bool
	}{
		{"phone number jid",
			types.MessageInfo{MessageSource: types.MessageSource{Sender: pn}},
			nil, pn, true},
		{"phone number via the alternative address",
			types.MessageInfo{MessageSource: types.MessageSource{Sender: lid, SenderAlt: pn}},
			nil, pn, true},
		{"phone number via the lid store",
			types.MessageInfo{MessageSource: types.MessageSource{Sender: lid}},
			found, pn, true},
		{"unresolved lid",
			types.MessageInfo{MessageSource: types.MessageSource{Sender: lid}},
			nil, types.JID{}, false},
		{"store without a match",
			types.MessageInfo{MessageSource: types.MessageSource{Sender: lid}},
			missing, types.JID{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveSender(tc.info, tc.lookup)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("resolveSender = (%s, %v), want (%s, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestRenderQRFitsATerminal(t *testing.T) {
	// A real linking code is long; the compact rendering must stay narrow
	// enough that a normal terminal can show it whole (ADR: pairing UX).
	code := strings.Repeat("Ab1", 64)
	var buffer bytes.Buffer
	renderQR(code, &buffer)
	widest := 0
	for _, line := range strings.Split(buffer.String(), "\n") {
		if width := utf8.RuneCountInString(line); width > widest {
			widest = width
		}
	}
	if widest == 0 {
		t.Fatal("the QR rendered nothing")
	}
	if widest > 100 {
		t.Fatalf("the QR is %d columns wide; too wide to scan on a terminal", widest)
	}
}

func TestNumber(t *testing.T) {
	cases := map[string]string{
		"5215500000000:65@s.whatsapp.net": "5215500000000",
		"5215500000000@s.whatsapp.net":    "5215500000000",
		"5215500000000:65":                "5215500000000",
		"5215500000000":                   "5215500000000",
	}
	for in, want := range cases {
		if got := Number(in); got != want {
			t.Fatalf("Number(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClearPairing(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	jid := types.NewADJID("5215500000000", 0, 1)
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
	account := &botflota.LinkedAccount{Scope: "dev", Bot: "bot", JID: jid.String()}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatal(err)
	}

	if err := removePairing(ctx, devices, db.LinkedAccounts(), db.Pairings(),
		account, t0); err != nil {
		t.Fatal(err)
	}
	got, err := devices.device(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("device still exists after clearing pairing")
	}
	_, err = db.LinkedAccounts().Get(ctx, account.Scope, account.Bot)
	if !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("account error = %v, want not found", err)
	}
	status, err := db.Pairings().Get(ctx, account.Scope, account.Bot)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if status.State != botflota.PairingNotPaired || !status.LastChange.Equal(t0) {
		t.Fatalf("status = %#v, want not paired at %s", status, t0)
	}
}

func TestValidateAccountOwners(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	accounts := db.LinkedAccounts()
	for _, account := range []botflota.LinkedAccount{
		{Scope: "dev", Bot: "first", JID: "5215500000000:1@s.whatsapp.net"},
		{Scope: "dev", Bot: "second", JID: "5215500000000:2@s.whatsapp.net"},
	} {
		if err := accounts.Put(ctx, &account); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateAccountOwners(ctx, accounts); err == nil {
		t.Fatal("duplicate phone number was accepted")
	}
}

func TestConnectReportsMissingDevice(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.LinkedAccount{
		Scope: "dev", Bot: "botsito", JID: "5215500000000:1@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	a := &Adapter{
		scope: "dev", bot: "botsito", accounts: db.LinkedAccounts(),
		pairings: db.Pairings(), devices: devices,
		log: botflota.DiscardLogger(),
	}
	err = a.Connect(ctx)
	if err == nil || !strings.Contains(err.Error(), "no local device credentials") {
		t.Fatalf("Connect error = %v, want missing-device error", err)
	}
}

func TestValidateAccountsAllowsMissingDevice(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.LinkedAccount{
		Scope: "dev", Bot: "botsito", JID: "5215500000000:1@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAccounts(ctx, devices, db.LinkedAccounts(), db.Pairings()); err != nil {
		t.Fatalf("ValidateAccounts: %v", err)
	}
	status, err := db.Pairings().Get(ctx, account.Scope, account.Bot)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != botflota.PairingUnpaired {
		t.Fatalf("state = %q, want unpaired", status.State)
	}
}

func TestCheckPairingAllowsRecoverableStates(t *testing.T) {
	cases := []struct {
		name    string
		account *botflota.LinkedAccount
		want    botflota.PairingState
	}{
		{name: "not paired", want: botflota.PairingNotPaired},
		{
			name: "unpaired",
			account: &botflota.LinkedAccount{
				Scope: "dev", Bot: "botsito", JID: "5215500000000:1@s.whatsapp.net",
			},
			want: botflota.PairingUnpaired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openWhatsAppTestDB(t)
			if tc.account != nil {
				if err := db.LinkedAccounts().Put(ctx, tc.account); err != nil {
					t.Fatal(err)
				}
			}
			devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
			if err != nil {
				t.Fatal(err)
			}
			state, err := CheckPairing(ctx, devices, db.LinkedAccounts(),
				db.Pairings(), "dev", "botsito")
			if err != nil {
				t.Fatalf("CheckPairing: %v", err)
			}
			if state != tc.want {
				t.Fatalf("state = %q, want %q", state, tc.want)
			}
		})
	}
}

func TestUnpairRemovesOrphanedAccount(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.LinkedAccount{
		Scope: "dev", Bot: "botsito", JID: "5215500000000:1@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	if err := Unpair(ctx, UnpairOptions{
		Scope: account.Scope, Bot: account.Bot, Devices: devices,
		Accounts: db.LinkedAccounts(), Pairings: db.Pairings(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LinkedAccounts().Get(ctx, account.Scope, account.Bot); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("account after unpair = %v, want not found", err)
	}
}

func TestPairingStatusChangesOnlyOnStateChange(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	pairings := db.Pairings()
	if err := updatePairingStatus(ctx, pairings, "dev", "bot",
		botflota.PairingUnpaired, t0); err != nil {
		t.Fatal(err)
	}
	if err := updatePairingStatus(ctx, pairings, "dev", "bot",
		botflota.PairingUnpaired, t1); err != nil {
		t.Fatal(err)
	}
	status, err := pairings.Get(ctx, "dev", "bot")
	if err != nil {
		t.Fatal(err)
	}
	if !status.LastChange.Equal(t0) {
		t.Fatalf("same state changed timestamp to %s, want %s", status.LastChange, t0)
	}
	if err := updatePairingStatus(ctx, pairings, "dev", "bot",
		botflota.PairingPaired, t1); err != nil {
		t.Fatal(err)
	}
	status, err = pairings.Get(ctx, "dev", "bot")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != botflota.PairingPaired || !status.LastChange.Equal(t1) {
		t.Fatalf("status = %#v, want paired at %s", status, t1)
	}
}

func TestLoggedOutPersistsUnpairedStatus(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.LinkedAccount{
		Scope: "dev", Bot: "bot", JID: "5215500000000:1@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{
		scope: "dev", bot: "bot", accounts: db.LinkedAccounts(),
		pairings: db.Pairings(), state: StateConnected,
		stop: make(chan struct{}), log: botflota.DiscardLogger(), now: func() time.Time { return t1 },
	}
	a.handleEvent(&events.LoggedOut{})
	status, err := db.Pairings().Get(ctx, "dev", "bot")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != botflota.PairingUnpaired || !status.LastChange.Equal(t1) {
		t.Fatalf("status = %#v, want unpaired at %s", status, t1)
	}
}

func openWhatsAppTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestWaLoGFiltersBelowMinimum(t *testing.T) {
	var buffer bytes.Buffer
	wa := newWaLog(botflota.NewLogger("debug", &buffer), slog.LevelWarn)
	wa.Debugf("dropped-debug")
	wa.Infof("dropped-info")
	wa.Warnf("kept-warn")
	wa.Errorf("kept-error")

	out := buffer.String()
	if strings.Contains(out, "dropped") {
		t.Fatalf("below minimum leaked: %q", out)
	}
	if !strings.Contains(out, "kept-warn") || !strings.Contains(out, "kept-error") {
		t.Fatalf("at or above minimum dropped: %q", out)
	}
}

func TestBuildContent(t *testing.T) {
	text, err := buildContent(botflota.OutgoingMessage{Kind: botflota.KindText, Body: "hi"})
	if err != nil {
		t.Fatalf("text: %v", err)
	}
	if text.GetConversation() != "hi" {
		t.Fatalf("text = %q", text.GetConversation())
	}

	location, err := buildContent(botflota.OutgoingMessage{
		Kind:     botflota.KindLocation,
		Location: &botflota.Location{Latitude: 1.5, Longitude: -2.5, Name: "Office"},
	})
	if err != nil {
		t.Fatalf("location: %v", err)
	}
	got := location.GetLocationMessage()
	if got.GetDegreesLatitude() != 1.5 || got.GetDegreesLongitude() != -2.5 {
		t.Fatalf("coordinates = %v, %v", got.GetDegreesLatitude(), got.GetDegreesLongitude())
	}
	if got.GetName() != "Office" {
		t.Fatalf("name = %q", got.GetName())
	}

	if _, err := buildContent(botflota.OutgoingMessage{Kind: botflota.KindLocation}); err == nil {
		t.Fatal("a location message without a location must fail")
	}
}

func newTestAdapter() *Adapter {
	return &Adapter{
		bot:      "support-bot",
		incoming: make(chan botflota.IncomingMessage, 4),
		seen:     newDedup(8),
		last:     map[string]lastInbound{},
		log:      botflota.DiscardLogger(),
	}
}

func textEvent(sender, id, body string) *events.Message {
	jid := types.NewJID(sender, types.DefaultUserServer)
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Sender: jid, Chat: jid},
			ID:            types.MessageID(id),
			Timestamp:     t0,
		},
		Message: &waE2E.Message{Conversation: proto.String(body)},
	}
}

func TestOnMessageMapsAndDedups(t *testing.T) {
	a := newTestAdapter()
	event := textEvent("5215500000000", "ABC", "hello")
	event.Info.PushName = "Ana"

	a.onMessage(event)
	a.onMessage(event) // redelivery

	select {
	case message := <-a.incoming:
		if message.Bot != "support-bot" || message.Body != "hello" {
			t.Fatalf("message = %#v", message)
		}
		if message.Sender != "5215500000000" {
			t.Fatalf("sender = %q, want the plain phone number", message.Sender)
		}
		if message.SenderName != "Ana" {
			t.Fatalf("sender name = %q, want the push name", message.SenderName)
		}
	default:
		t.Fatal("no message was delivered")
	}
	select {
	case message := <-a.incoming:
		t.Fatalf("duplicate was delivered: %#v", message)
	default:
	}
	if _, ok := a.last["5215500000000"]; !ok {
		t.Fatal("the last inbound message was not tracked")
	}
}

func TestOnMessageDropsUnresolvedLID(t *testing.T) {
	a := newTestAdapter()
	lid := types.NewJID("212150010273830", types.HiddenUserServer)
	event := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Sender: lid, Chat: lid},
			ID:            types.MessageID("XYZ"),
			Timestamp:     t0,
		},
		Message: &waE2E.Message{Conversation: proto.String("hi")},
	}
	a.onMessage(event)
	select {
	case message := <-a.incoming:
		t.Fatalf("an unresolved message was delivered: %#v", message)
	default:
	}
}

func TestOnMessageIgnoresOwnAndNonText(t *testing.T) {
	a := newTestAdapter()
	own := textEvent("5215500000000", "1", "mine")
	own.Info.IsFromMe = true
	a.onMessage(own)

	empty := textEvent("5215500000000", "2", "")
	a.onMessage(empty)

	select {
	case message := <-a.incoming:
		t.Fatalf("unexpected message: %#v", message)
	default:
	}
}

func TestHistoryChatPhone(t *testing.T) {
	pn := types.NewJID("5215500000000", types.DefaultUserServer)
	found := func(types.JID) (types.JID, bool) { return pn, true }
	missing := func(types.JID) (types.JID, bool) { return types.JID{}, false }

	cases := []struct {
		name         string
		conversation *waHistorySync.Conversation
		lookup       pnLookup
		want         string
		ok           bool
	}{
		{"phone jid in pn",
			historyConversation("212150010273830@lid", "5215500000000@s.whatsapp.net", "", nil),
			nil, "5215500000000", true},
		{"phone number chat",
			historyConversation("5215500000001@s.whatsapp.net", "", "", nil),
			nil, "5215500000001", true},
		{"hidden user resolved through the lid store",
			historyConversation("212150010273830@lid", "", "", nil),
			found, "5215500000000", true},
		{"hidden user without a mapping",
			historyConversation("212150010273830@lid", "", "", nil),
			missing, "", false},
		{"group", historyConversation("1203630@g.us", "", "", nil), nil, "", false},
		{"status broadcast", historyConversation("status@broadcast", "", "", nil), nil, "", false},
		{"empty id", historyConversation("", "", "", nil), nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := historyChatPhone(tc.conversation, tc.lookup)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("historyChatPhone = (%q, %v), want (%q, %v)",
					got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestHistoryImporterStoresSyncMessages(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := newHistoryImporter("dev", "bot", db.Senders(), db.Chats(),
		db.Messages(), nil, nil)

	base := uint64(t0.Unix())
	a := historyConversation("5215500000000@s.whatsapp.net", "", "Ana",
		historyText("M2", true, "reply", base+60, 2),
		historyText("M3", false, "bye", base+120, 3),
		historyText("M1", false, "hello", base, 1),
	)
	b := historyConversation("5215500000001@s.whatsapp.net", "", "Beto",
		historyText("N1", false, "hi bot", base+30, 1),
	)
	// A group and a chat with only a media message must leave no trace.
	group := historyConversation("1203630@g.us", "", "Team", historyText("G1", false, "hey", base, 1))
	media := historyConversation("5215500000002@s.whatsapp.net", "", "Cora",
		historyMedia("P1", base))
	importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{a, b, group, media},
	}})

	ana, err := db.Senders().Get(ctx, "dev", "5215500000000")
	if err != nil {
		t.Fatalf("sender Ana: %v", err)
	}
	if ana.DisplayName != "Ana" {
		t.Fatalf("Ana display name = %q", ana.DisplayName)
	}
	if _, err := db.Senders().Get(ctx, "dev", "5215500000002"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("media-only sender = %v, want not found", err)
	}

	chat, err := db.Chats().ForSender(ctx, "dev", "bot", ana.ID)
	if err != nil {
		t.Fatalf("chat Ana: %v", err)
	}
	if chat.MessageCount != 0 {
		t.Fatalf("message_count = %d, want 0: an import is not live traffic",
			chat.MessageCount)
	}
	// The chat's activity covers the imported range, not just the anchor.
	wantStart := time.Unix(int64(base), 0).UTC()
	wantEnd := time.Unix(int64(base+120), 0).UTC()
	if !chat.StartedAt.Equal(wantStart) || !chat.UpdatedAt.Equal(wantEnd) {
		t.Fatalf("chat activity = %v..%v, want %v..%v",
			chat.StartedAt, chat.UpdatedAt, wantStart, wantEnd)
	}
	messages, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("history Ana: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	if messages[0].Body != "hello" || messages[1].Body != "reply" ||
		messages[2].Body != "bye" {
		t.Fatalf("messages out of order: %#v", bodies(messages))
	}
	if messages[0].Direction != botflota.Inbound || messages[0].Origin != "" ||
		messages[0].Ingest != botflota.IngestSync ||
		messages[0].Routing != botflota.RoutingUnknown || messages[0].SessionID != 0 {
		t.Fatalf("inbound provenance = %+v", messages[0])
	}
	if messages[1].Direction != botflota.Outbound ||
		messages[1].Origin != botflota.OriginUnknown {
		t.Fatalf("outbound provenance = %+v", messages[1])
	}
	if messages[0].ProviderMessageID != "M1" {
		t.Fatalf("provider id = %q", messages[0].ProviderMessageID)
	}
}

func TestHistoryImporterDeduplicates(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := newHistoryImporter("dev", "bot", db.Senders(), db.Chats(),
		db.Messages(), nil, nil)
	conversation := historyConversation("5215500000000@s.whatsapp.net", "", "Ana",
		historyText("M1", false, "hello", uint64(t0.Unix()), 1),
	)
	data := &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conversation},
	}
	first := importer.Handle(ctx, &events.HistorySync{Data: data})
	if first.imported != 1 || first.duplicate != 0 {
		t.Fatalf("first import = %#v, want 1 imported", first)
	}
	second := importer.Handle(ctx, &events.HistorySync{Data: data})
	if second.imported != 0 || second.duplicate != 1 {
		t.Fatalf("redelivered import = %#v, want 1 duplicate", second)
	}

	sender, err := db.Senders().Get(ctx, "dev", "5215500000000")
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	chat, err := db.Chats().ForSender(ctx, "dev", "bot", sender.ID)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	messages, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1 after a redelivered chunk", len(messages))
	}
}

func TestHistoryImporterAppliesPushNames(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := newHistoryImporter("dev", "bot", db.Senders(), db.Chats(),
		db.Messages(), nil, nil)
	// A conversation creates the sender with no name of its own.
	importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{
			historyConversation("5215500000000@s.whatsapp.net", "", "",
				historyText("M1", false, "hello", uint64(t0.Unix()), 1)),
		},
	}})
	before, err := db.Senders().Get(ctx, "dev", "5215500000000")
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	if before.DisplayName != "" {
		t.Fatalf("display name before push names = %q, want empty", before.DisplayName)
	}

	// The account-wide PUSH_NAME chunk names it.
	stats := importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_PUSH_NAME.Enum(),
		Pushnames: []*waHistorySync.Pushname{{
			ID:       proto.String("5215500000000@s.whatsapp.net"),
			Pushname: proto.String("Ana"),
		}},
	}})
	if stats.pushNames != 1 {
		t.Fatalf("push names = %d, want 1", stats.pushNames)
	}
	after, err := db.Senders().Get(ctx, "dev", "5215500000000")
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	if after.DisplayName != "Ana" {
		t.Fatalf("display name = %q, want Ana", after.DisplayName)
	}
	if !after.LastSeen.Equal(before.LastSeen) || !after.FirstSeen.Equal(before.FirstSeen) {
		t.Fatalf("contact times moved: %v..%v, want %v..%v",
			after.FirstSeen, after.LastSeen, before.FirstSeen, before.LastSeen)
	}
}

func TestOnHistoryWithoutImporterIsInert(t *testing.T) {
	a := newTestAdapter()
	a.onHistory(&events.HistorySync{Data: &waHistorySync.HistorySync{}})
}

func historyConversation(
	id, pnJID, name string, messages ...*waHistorySync.HistorySyncMsg,
) *waHistorySync.Conversation {
	conversation := &waHistorySync.Conversation{
		ID:       proto.String(id),
		Messages: messages,
	}
	if pnJID != "" {
		conversation.PnJID = proto.String(pnJID)
	}
	if name != "" {
		conversation.Name = proto.String(name)
	}
	return conversation
}

func historyText(
	id string, fromMe bool, body string, timestamp uint64, order uint64,
) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{
		MsgOrderID: proto.Uint64(order),
		Message: &waWeb.WebMessageInfo{
			Key: &waCommon.MessageKey{
				FromMe: proto.Bool(fromMe),
				ID:     proto.String(id),
			},
			Message:          &waE2E.Message{Conversation: proto.String(body)},
			MessageTimestamp: proto.Uint64(timestamp),
		},
	}
}

func historyMedia(id string, timestamp uint64) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{
		MsgOrderID: proto.Uint64(1),
		Message: &waWeb.WebMessageInfo{
			Key:              &waCommon.MessageKey{ID: proto.String(id)},
			Message:          &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}},
			MessageTimestamp: proto.Uint64(timestamp),
		},
	}
}

func bodies(messages []botflota.Message) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.Body)
	}
	return out
}
