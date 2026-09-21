package channel

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
	"google.golang.org/protobuf/proto"
)

func TestBackfillChatSplitsTheRequestedTotalIntoBatches(t *testing.T) {
	var counts []int
	request := func(
		_ context.Context, from *historyCursor, count int, _ time.Duration,
	) (historyPage, error) {
		counts = append(counts, count)
		return historyPage{
			Received: count,
			Imported: count,
			Next:     &historyCursor{ID: from.ID + "-older", At: from.At.Add(-time.Hour)},
		}, nil
	}
	result, err := backfillChat(
		context.Background(), 75, time.Second,
		func() (*historyCursor, error) {
			return &historyCursor{ID: "start", At: t0}, nil
		}, request,
	)
	if err != nil {
		t.Fatalf("backfillChat: %v", err)
	}
	if result.Rounds != 2 || result.Received != 75 || result.Imported != 75 {
		t.Fatalf("result = %+v, want two batches totaling 75", result)
	}
	if len(counts) != 2 || counts[0] != 50 || counts[1] != 25 {
		t.Fatalf("batch sizes = %v, want [50 25]", counts)
	}
}

func TestBackfillChatStopsWhenAPageHasNothingOlder(t *testing.T) {
	requests := 0
	request := func(
		_ context.Context, from *historyCursor, _ int, _ time.Duration,
	) (historyPage, error) {
		requests++
		if requests == 1 {
			return historyPage{
				Received: 50,
				Next: &historyCursor{
					ID: from.ID + "-older", At: from.At.Add(-time.Hour),
				},
			}, nil
		}
		return historyPage{Received: 0}, nil
	}
	result, err := backfillChat(
		context.Background(), 0, time.Second,
		func() (*historyCursor, error) {
			return &historyCursor{ID: "start", At: t0}, nil
		}, request,
	)
	if err != nil {
		t.Fatalf("backfillChat: %v", err)
	}
	if requests != 2 || result.Rounds != 2 || result.Received != 50 ||
		!result.Stable || result.Exhausted {
		t.Fatalf("result = %+v, want two rounds ending stable", result)
	}
}

func TestBackfillChatStopsWhenThePhoneReportsNoAccess(t *testing.T) {
	request := func(
		_ context.Context, _ *historyCursor, _ int, _ time.Duration,
	) (historyPage, error) {
		return historyPage{Received: 50, Exhausted: true}, nil
	}
	result, err := backfillChat(
		context.Background(), 0, time.Second,
		func() (*historyCursor, error) {
			return &historyCursor{ID: "start", At: t0}, nil
		}, request,
	)
	if err != nil {
		t.Fatalf("backfillChat: %v", err)
	}
	if result.Rounds != 1 || !result.Exhausted || result.Stable {
		t.Fatalf("result = %+v, want one exhausted round", result)
	}
}

func TestSyncRequestIgnoresChunksForOtherRequests(t *testing.T) {
	request := newSyncRequest("REQ", "5215500000000", nil)
	other := &events.HistorySync{
		Data: &waHistorySync.HistorySync{Progress: proto.Uint32(100)},
		Notification: &waE2E.HistorySyncNotification{
			PeerDataRequestSessionID: proto.String("OTHER"),
		},
	}
	request.observe(other, ingest.ImportTotals{Imported: 3})
	select {
	case <-request.done:
		t.Fatal("a chunk for another request must not complete this one")
	default:
	}
	if page := request.snapshot(); page.Imported != 0 {
		t.Fatalf("page = %+v, want no accumulation", page)
	}
}

func TestSyncRequestCompletesWithMatchingChunk(t *testing.T) {
	request := newSyncRequest("REQ", "5215500000000", nil)
	conversation := historyConversation("5215500000000@s.whatsapp.net", "", "Ana",
		historyText("M1", false, "hello", uint64(t0.Unix()), 1))
	event := &events.HistorySync{
		Data: &waHistorySync.HistorySync{
			Progress:      proto.Uint32(100),
			Conversations: []*waHistorySync.Conversation{conversation},
		},
		Notification: &waE2E.HistorySyncNotification{
			PeerDataRequestSessionID: proto.String("REQ"),
		},
	}
	request.observe(event, ingest.ImportTotals{Imported: 1})
	select {
	case <-request.done:
	case <-time.After(time.Second):
		t.Fatal("a matching complete chunk must finish the request")
	}
	page := request.snapshot()
	if page.Received != 1 || page.Imported != 1 ||
		page.Next == nil || page.Next.ID != "M1" {
		t.Fatalf("page = %+v, want one received message and its cursor", page)
	}
}

func TestHistorySyncCompleteForUsesProgressOrEndMarker(t *testing.T) {
	if historySyncCompleteFor(nil, "5215500000000", nil) {
		t.Fatal("nil event must not be complete")
	}
	complete := &events.HistorySync{Data: &waHistorySync.HistorySync{
		Progress: proto.Uint32(100),
	}}
	if !historySyncCompleteFor(complete, "5215500000000", nil) {
		t.Fatal("progress 100 must complete a response")
	}
	running := &events.HistorySync{Data: &waHistorySync.HistorySync{
		Progress: proto.Uint32(50),
	}}
	if historySyncCompleteFor(running, "5215500000000", nil) {
		t.Fatal("progress 50 must not complete a response")
	}
}

func TestHistoryEventPageReadsOnlyTheRequestedChat(t *testing.T) {
	mine := historyConversation("5215500000000@s.whatsapp.net", "", "Ana",
		historyText("M2", false, "newer", uint64(t1.Unix()), 2),
		historyText("M1", false, "older", uint64(t0.Unix()), 1))
	other := historyConversation("5215599999999@s.whatsapp.net", "", "Beto",
		historyText("X1", false, "hi", uint64(t0.Unix()), 1))
	event := &events.HistorySync{Data: &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{other, mine},
	}}
	received, next := historyEventPage(event, "5215500000000", nil)
	if received != 2 {
		t.Fatalf("received = %d, want 2", received)
	}
	if next == nil || next.ID != "M1" {
		t.Fatalf("next cursor = %+v, want the oldest message M1", next)
	}
}

func TestHistoryAnchorInfoAddressesTheLID(t *testing.T) {
	lids := fakeLIDStore{mapping: map[string]types.JID{
		"5215500000000": types.NewJID("162405085098836", types.HiddenUserServer),
	}}
	info := historyAnchorInfo(context.Background(), "5215500000000",
		&historyCursor{ID: "M1", FromMe: true, At: t0}, lids)
	if info.Chat.Server != types.HiddenUserServer {
		t.Fatalf("chat = %s, want the LID", info.Chat)
	}
	if info.ID != "M1" || !info.IsFromMe || !info.Timestamp.Equal(t0) {
		t.Fatalf("anchor = %+v", info)
	}
}

type fakeLIDStore struct {
	mapping map[string]types.JID
}

func (f fakeLIDStore) PutManyLIDMappings(context.Context, []store.LIDMapping) error {
	return nil
}

func (f fakeLIDStore) PutLIDMapping(context.Context, types.JID, types.JID) error {
	return nil
}

func (f fakeLIDStore) GetPNForLID(_ context.Context, lid types.JID) (types.JID, error) {
	return lid, nil
}

func (f fakeLIDStore) GetLIDForPN(_ context.Context, pn types.JID) (types.JID, error) {
	return f.mapping[pn.User], nil
}

func (f fakeLIDStore) GetManyLIDsForPNs(
	context.Context, []types.JID,
) (map[types.JID]types.JID, error) {
	return nil, nil
}
