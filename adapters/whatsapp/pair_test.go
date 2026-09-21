package whatsapp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

func TestApplyDeviceName(t *testing.T) {
	oldName := store.DeviceProps.GetOs()
	oldVersion := [3]uint32{
		store.DeviceProps.Version.GetPrimary(),
		store.DeviceProps.Version.GetSecondary(),
		store.DeviceProps.Version.GetTertiary(),
	}
	oldPlatform := store.DeviceProps.GetPlatformType()
	oldRequireFullSync := store.DeviceProps.GetRequireFullSync()
	oldHistoryConfig := store.DeviceProps.HistorySyncConfig
	t.Cleanup(func() {
		store.SetOSInfo(oldName, oldVersion)
		store.DeviceProps.PlatformType = oldPlatform.Enum()
		store.DeviceProps.RequireFullSync = proto.Bool(oldRequireFullSync)
		store.DeviceProps.HistorySyncConfig = oldHistoryConfig
	})

	applyDeviceName("Acme Bots")
	if got := store.DeviceProps.GetOs(); got != "Acme Bots" {
		t.Fatalf("device name = %q, want the configured one", got)
	}
	applyDeviceName("")
	if got := store.DeviceProps.GetOs(); got != defaultDeviceName {
		t.Fatalf("device name = %q, want %q", got, defaultDeviceName)
	}
	configureHistorySync(true)
	if got := store.DeviceProps.GetPlatformType(); got != waCompanionReg.DeviceProps_DESKTOP {
		t.Fatalf("platform type = %v, want desktop", got)
	}
	if !store.DeviceProps.GetRequireFullSync() {
		t.Fatal("full history sync was not requested")
	}
	config := store.DeviceProps.GetHistorySyncConfig()
	if config.GetFullSyncDaysLimit() != fullSyncDays ||
		config.GetFullSyncSizeMbLimit() != fullSyncSize ||
		config.GetStorageQuotaMb() != storageQuota {
		t.Fatalf("history sync config = %+v, want the full window", config)
	}
	// A whatsmeow default the import does not set must survive, so this never
	// copies the whole struct and loses a future capability flag.
	if !config.GetSupportCallLogHistory() {
		t.Fatal("an inherited history sync capability was cleared")
	}
	configureHistorySync(false)
	if store.DeviceProps.GetRequireFullSync() {
		t.Fatal("history sync disabled must not require a full sync")
	}
	configureHistorySync(true)
}

func TestRenderQRFitsATerminal(t *testing.T) {
	// A real linking code is long; the compact rendering must stay narrow
	// enough that a normal terminal can show it whole.
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
	got, err := devices.Load(ctx, account)
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
