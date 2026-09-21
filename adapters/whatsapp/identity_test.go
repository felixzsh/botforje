package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
)

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
