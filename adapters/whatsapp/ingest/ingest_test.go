package ingest

import (
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botflota/adapters/sqlite"
)

var (
	t0 = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	t1 = time.Date(2026, 9, 13, 10, 5, 0, 0, time.UTC)
)

func openWhatsAppTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
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
