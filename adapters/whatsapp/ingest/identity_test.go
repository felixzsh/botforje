package ingest

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
)

func TestHistoryChatPhone(t *testing.T) {
	pn := types.NewJID("5215500000000", types.DefaultUserServer)
	found := func(types.JID) (types.JID, bool) { return pn, true }
	missing := func(types.JID) (types.JID, bool) { return types.JID{}, false }

	cases := []struct {
		name         string
		conversation *waHistorySync.Conversation
		lookup       PNLookup
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
			got, ok := HistoryChatPhone(tc.conversation, tc.lookup)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("HistoryChatPhone = (%q, %v), want (%q, %v)",
					got, ok, tc.want, tc.ok)
			}
		})
	}
}
