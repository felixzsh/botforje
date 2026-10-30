package loadtest

// The synthetic channel measures the fleet, engine, and stores, but it says
// nothing about the persistence the WhatsApp adapter performs for every inbound
// message: its device store (Signal sessions, sender keys) is written to the
// same SQLite file (ADR 0006). This file reproduces that work with the real
// whatsmeow store, so a capacity run can include it.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/felixzsh/botflota/adapters/sqlite"
)

// deviceChurn performs the per-message device-store reads and writes whatsmeow
// does while decrypting, through the same database handle and pool the fleet
// uses. A text message resolves one Signal session and stores its updated
// record; a group message also touches a sender key.
type deviceChurn struct {
	store *sqlstore.SQLStore

	mu  sync.Mutex
	seq int
}

func newDeviceChurn(db *sqlite.DB) (*deviceChurn, error) {
	container := sqlstore.NewWithDB(db.DB, "sqlite", waLog.Noop)
	if err := container.Upgrade(context.Background()); err != nil {
		return nil, fmt.Errorf("loadtest: whatsmeow store upgrade: %w", err)
	}
	// Sessions reference the device row by foreign key, so the row must exist.
	// Its credentials are empty: the churn only needs the same tables and
	// indexes the adapter writes while decrypting.
	device := container.NewDevice()
	jid := types.NewJID("100000000000000", types.DefaultUserServer)
	device.ID = &jid
	// The schema fixes the signature lengths; the churn never verifies them.
	device.Account = &waAdv.ADVSignedDeviceIdentity{
		Details:             []byte{},
		AccountSignature:    make([]byte, 64),
		AccountSignatureKey: make([]byte, 32),
		DeviceSignature:     make([]byte, 64),
	}
	if err := container.PutDevice(context.Background(), device); err != nil {
		return nil, fmt.Errorf("loadtest: put whatsmeow device: %w", err)
	}
	return &deviceChurn{store: sqlstore.NewSQLStore(container, jid)}, nil
}

// turn is one inbound message's device-store work: read the Signal session,
// write its updated record, and every tenth message also read and write a group
// sender key. Payload sizes follow a serialized session record.
func (c *deviceChurn) turn(ctx context.Context, bot, chat string) error {
	address := fmt.Sprintf("%s.0:%s@s.whatsapp.net", bot, chat)
	if _, err := c.store.GetSession(ctx, address); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("loadtest: read session: %w", err)
	}
	c.mu.Lock()
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	session := make([]byte, 256)
	session[0] = byte(seq)
	if err := c.store.PutSession(ctx, address, session); err != nil {
		return fmt.Errorf("loadtest: write session: %w", err)
	}
	if seq%10 != 0 {
		return nil
	}
	const group = "120363000000000000@g.us"
	if _, err := c.store.GetSenderKey(ctx, group, bot); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("loadtest: read sender key: %w", err)
	}
	if err := c.store.PutSenderKey(ctx, group, bot, session); err != nil {
		return fmt.Errorf("loadtest: write sender key: %w", err)
	}
	return nil
}

// TestDeviceChurnWritesToTheSameDatabase proves the simulation exercises the
// real whatsmeow tables in the fleet's database, not an in-memory fake.
func TestDeviceChurnWritesToTheSameDatabase(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "load.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	churn, err := newDeviceChurn(db)
	if err != nil {
		t.Fatalf("new device churn: %v", err)
	}
	ctx := context.Background()
	if err := churn.turn(ctx, "b0", "peer-0"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	var sessions int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM whatsmeow_sessions").Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 1 {
		t.Fatalf("sessions = %d, want the written record", sessions)
	}
}
