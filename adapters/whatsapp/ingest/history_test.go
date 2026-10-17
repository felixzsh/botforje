package ingest

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botflota"
)

func TestHistoryImporterStoresSyncMessages(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := NewHistory("dev", "bot", db.Peers(), db.Chats(),
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
	// The group leaves no trace; the media-only conversation registers its
	// chat and keeps the entry as an anchor even without text (ADR 0035,
	// ADR 0036).
	group := historyConversation("1203630@g.us", "", "Team", historyText("G1", false, "hey", base, 1))
	media := historyConversation("5215500000002@s.whatsapp.net", "", "Cora",
		historyMedia("P1", base))
	importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{a, b, group, media},
	}})

	ana, err := db.Peers().Get(ctx, "dev", PeerKey("5215500000000"))
	if err != nil {
		t.Fatalf("sender Ana: %v", err)
	}
	if ana.DisplayName != "Ana" {
		t.Fatalf("Ana display name = %q", ana.DisplayName)
	}
	if _, err := db.Peers().Get(ctx, "dev", PeerKey("1203630")); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("group sender = %v, want not found", err)
	}
	cora, err := db.Peers().Get(ctx, "dev", PeerKey("5215500000002"))
	if err != nil {
		t.Fatalf("media-only sender: %v", err)
	}
	coraChat, err := db.Chats().ForTarget(ctx, "dev", "bot", DirectTarget(cora.Key.Subject))
	if err != nil {
		t.Fatalf("media-only chat: %v", err)
	}
	coraMessages, err := db.Messages().History(ctx, coraChat.ID, 0, 10)
	if err != nil {
		t.Fatalf("media-only history: %v", err)
	}
	if len(coraMessages) != 1 {
		t.Fatalf("media-only messages = %d, want 1 anchor", len(coraMessages))
	}
	if botflota.ContentKind(coraMessages[0].Content) != botflota.KindImage ||
		coraMessages[0].ProviderMessageID != "P1" {
		t.Fatalf("media anchor = %+v", coraMessages[0])
	}
	if !coraChat.StartedAt.Equal(time.Unix(int64(base), 0).UTC()) {
		t.Fatalf("media-only chat started = %v", coraChat.StartedAt)
	}

	chat, err := db.Chats().ForTarget(ctx, "dev", "bot", DirectTarget(ana.Key.Subject))
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
	if botflota.ContentText(messages[0].Content) != "hello" ||
		botflota.ContentText(messages[1].Content) != "reply" ||
		botflota.ContentText(messages[2].Content) != "bye" {
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

func TestHistoryImporterSkipsControlEntries(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := NewHistory("dev", "bot", db.Peers(), db.Chats(),
		db.Messages(), nil, nil)
	conversation := historyConversation("5215500000000@s.whatsapp.net", "", "Ana",
		&waHistorySync.HistorySyncMsg{
			Message: &waWeb.WebMessageInfo{
				Key: &waCommon.MessageKey{
					ID: proto.String("REACTION-1"),
				},
				Message:          &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{}},
				MessageTimestamp: proto.Uint64(uint64(t0.Unix())),
			},
		})
	stats := importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conversation},
	}})
	if stats.Imported != 0 || stats.Received != 1 || stats.Skipped != 1 {
		t.Fatalf("stats = %#v, want one hidden provider entry", stats)
	}
	// A control-only shell is not a chat: WhatsApp sends one for every number
	// the account's address book knows, and it must not become a row.
	if _, err := db.Peers().Get(ctx, "dev", PeerKey("5215500000000")); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("contact = %v, want ErrNotFound", err)
	}
}

func TestHistoryImporterDeduplicates(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := NewHistory("dev", "bot", db.Peers(), db.Chats(),
		db.Messages(), nil, nil)
	conversation := historyConversation("5215500000000@s.whatsapp.net", "", "Ana",
		historyText("M1", false, "hello", uint64(t0.Unix()), 1),
	)
	data := &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conversation},
	}
	first := importer.Handle(ctx, &events.HistorySync{Data: data})
	if first.Imported != 1 || first.Duplicate != 0 {
		t.Fatalf("first import = %#v, want 1 imported", first)
	}
	second := importer.Handle(ctx, &events.HistorySync{Data: data})
	if second.Imported != 0 || second.Duplicate != 1 {
		t.Fatalf("redelivered import = %#v, want 1 duplicate", second)
	}

	sender, err := db.Peers().Get(ctx, "dev", PeerKey("5215500000000"))
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	chat, err := db.Chats().ForTarget(ctx, "dev", "bot", DirectTarget(sender.Key.Subject))
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

// TestHistoryImporterSummarizesPerTransfer covers the log shape ADR 0038 asks
// for: one info line per finished transfer, the per-chunk detail at debug, and
// the running totals as the operation's result.
func TestHistoryImporterSummarizesPerTransfer(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	var buffer bytes.Buffer
	importer := NewHistory("dev", "bot", db.Peers(), db.Chats(),
		db.Messages(), nil, botflota.NewLogger("debug", &buffer))

	chunk := func(progress uint32, conversations ...*waHistorySync.Conversation) {
		t.Helper()
		importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
			SyncType:      waHistorySync.HistorySync_RECENT.Enum(),
			Progress:      proto.Uint32(progress),
			Conversations: conversations,
		}})
	}
	chunk(10)
	chunk(50, historyConversation("5215500000000@s.whatsapp.net", "", "Ana",
		historyText("M1", false, "hello", uint64(t0.Unix()), 1)))
	chunk(100)

	out := buffer.String()
	if got := strings.Count(out, "history sync imported"); got != 1 {
		t.Fatalf("transfer summaries = %d, want 1 in:\n%s", got, out)
	}
	if got := strings.Count(out, "history chunk imported"); got != 3 {
		t.Fatalf("chunk lines = %d, want 3 in:\n%s", got, out)
	}
	totals := importer.Totals()
	if totals.Chunks != 3 || totals.Conversations != 1 || totals.Imported != 1 {
		t.Fatalf("totals = %+v, want 3 chunks, 1 chat, 1 message", totals)
	}
	if !strings.Contains(out, "chunks=3") {
		t.Fatalf("the summary is missing the chunk count:\n%s", out)
	}
}

// TestHistoryImporterIsSilentUntilATransferEnds keeps the default level quiet:
// a sync in progress is per-chunk detail, so nothing is logged at info until
// the transfer reports progress 100.
func TestHistoryImporterIsSilentUntilATransferEnds(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	var buffer bytes.Buffer
	importer := NewHistory("dev", "bot", db.Peers(), db.Chats(),
		db.Messages(), nil, botflota.NewLogger("info", &buffer))

	data := &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_RECENT.Enum(),
		Progress: proto.Uint32(10),
	}
	importer.Handle(ctx, &events.HistorySync{Data: data})
	if buffer.Len() != 0 {
		t.Fatalf("a transfer in progress logged at info:\n%s", buffer.String())
	}
	data.Progress = proto.Uint32(100)
	importer.Handle(ctx, &events.HistorySync{Data: data})
	if got := strings.Count(buffer.String(), "history sync imported"); got != 1 {
		t.Fatalf("transfer summaries = %d, want 1 in:\n%s", got, buffer.String())
	}
}

func TestHistoryImporterAppliesPushNames(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := NewHistory("dev", "bot", db.Peers(), db.Chats(),
		db.Messages(), nil, nil)
	// A conversation creates the sender with no name of its own.
	importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{
			historyConversation("5215500000000@s.whatsapp.net", "", "",
				historyText("M1", false, "hello", uint64(t0.Unix()), 1)),
		},
	}})
	before, err := db.Peers().Get(ctx, "dev", PeerKey("5215500000000"))
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	if before.DisplayName != "" {
		t.Fatalf("display name before push names = %q, want empty", before.DisplayName)
	}

	stats := importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_PUSH_NAME.Enum(),
		Pushnames: []*waHistorySync.Pushname{{
			ID:       proto.String("5215500000000@s.whatsapp.net"),
			Pushname: proto.String("Ana"),
		}},
	}})
	if stats.PushNames != 1 {
		t.Fatalf("push names = %d, want 1", stats.PushNames)
	}
	after, err := db.Peers().Get(ctx, "dev", PeerKey("5215500000000"))
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
		out = append(out, botflota.ContentText(message.Content))
	}
	return out
}

// TestHistoryRecordsShareTheLiveNormalization: an imported row names the same
// neutral peer, conversation target, and message space a live inbound for the
// same phone would, while staying inert — sync ingest, unknown origin, and no
// first-seen claim (ADR 0026, ADR 0035, ADR 0051).
func TestHistoryRecordsShareTheLiveNormalization(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := NewHistory("dev", "bot", db.Peers(), db.Chats(),
		db.Messages(), nil, nil)
	importer.SetOwnPhone("5215500099999")
	base := uint64(t0.Unix())
	conversation := historyConversation("5215500000000@s.whatsapp.net", "", "Ana",
		historyText("M1", false, "hello", base, 1),
	)
	importer.Handle(ctx, &events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{conversation},
	}})

	peer, err := db.Peers().Get(ctx, "dev", PeerKey("5215500000000"))
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	wantTarget := DirectTarget(peer.Key.Subject)
	chat, err := db.Chats().ForTarget(ctx, "dev", "bot", wantTarget)
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
	got := messages[0]
	if got.Target != wantTarget || chat.Target != wantTarget {
		t.Fatalf("targets = %+v / %+v, want %+v", got.Target, chat.Target, wantTarget)
	}
	if got.MessageSpace != "5215500099999" {
		t.Fatalf("message space = %q, want the linked account", got.MessageSpace)
	}
	if got.Ingest != botflota.IngestSync || got.Origin != "" {
		t.Fatalf("provenance = %+v, want an inert sync row", got)
	}
	if !peer.FirstSeen.IsZero() {
		t.Fatalf("first seen = %v, want no claim from an import", peer.FirstSeen)
	}
}
