package whatsapp

import (
	"context"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botflota"
)

func TestHistoryImporterStoresCallAttempts(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := newHistoryImporter("dev", "bot", db.Senders(), db.Chats(),
		db.Messages(), nil, nil)

	base := uint64(t0.Unix())
	// A chat whose only history is a missed call is still a chat.
	conversation := historyConversation("5215500000000@s.whatsapp.net", "", "Ana",
		historyCall("C1", false, base+30, 1),
	)
	stats := importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conversation},
	}})
	if stats.Imported != 1 || stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want 1 imported, 0 skipped", stats)
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
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if botflota.ContentKind(messages[0].Content) != botflota.KindCall ||
		botflota.ContentText(messages[0].Content) != "Missed call" {
		t.Fatalf("call row = %+v, want a missed call", messages[0])
	}
	if messages[0].ProviderMessageID != "C1" ||
		messages[0].Ingest != botflota.IngestSync ||
		messages[0].Direction != botflota.Inbound {
		t.Fatalf("call provenance = %+v", messages[0])
	}
}

func TestHistoryImporterStoresAccountCallLogs(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := newHistoryImporter("dev", "bot", db.Senders(), db.Chats(),
		db.Messages(), nil, nil)
	importer.ownPhone = "5215500000000"
	call := &waSyncAction.CallLogRecord{
		CallID:         proto.String("CALL-1"),
		CallCreatorJID: proto.String("5215500000001@s.whatsapp.net"),
		IsIncoming:     proto.Bool(true),
		StartTime:      proto.Int64(t0.Unix()),
		CallResult:     waSyncAction.CallLogRecord_MISSED.Enum(),
	}
	stats := importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
		CallLogRecords: []*waSyncAction.CallLogRecord{call},
	}})
	if stats.Imported != 1 || stats.Received != 1 || stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want one imported call", stats)
	}
	sender, err := db.Senders().Get(ctx, "dev", "5215500000001")
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
	if len(messages) != 1 || messages[0].ProviderMessageID != "CALL-1" {
		t.Fatalf("call log messages = %+v", messages)
	}
}

func historyCall(
	id string, fromMe bool, timestamp uint64, order uint64,
) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{
		MsgOrderID: proto.Uint64(order),
		Message: &waWeb.WebMessageInfo{
			Key: &waCommon.MessageKey{
				FromMe: proto.Bool(fromMe),
				ID:     proto.String(id),
			},
			Message: &waE2E.Message{CallLogMesssage: &waE2E.CallLogMessage{
				CallOutcome: waE2E.CallLogMessage_MISSED.Enum(),
			}},
			MessageTimestamp: proto.Uint64(timestamp),
		},
	}
}
