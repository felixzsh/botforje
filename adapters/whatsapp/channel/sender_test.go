package channel

import (
	"testing"

	"go.mau.fi/whatsmeow/types"

	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

func TestResolveSender(t *testing.T) {
	pn := types.NewJID("5215500000000", types.DefaultUserServer)
	lid := types.NewJID("212150010273830", types.HiddenUserServer)
	found := func(types.JID) (types.JID, bool) { return pn, true }
	missing := func(types.JID) (types.JID, bool) { return types.JID{}, false }

	cases := []struct {
		name   string
		info   types.MessageInfo
		lookup ingest.PNLookup
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
