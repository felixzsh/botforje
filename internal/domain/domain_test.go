package domain

import "testing"

func TestPhoneAndDisplay(t *testing.T) {
	cases := []struct {
		name        string
		msg         IncomingMessage
		wantPhone   string
		wantDisplay string
	}{
		{"resolved", IncomingMessage{Sender: "521@s.whatsapp.net", SenderLID: "212@lid"},
			"521@s.whatsapp.net", "521@s.whatsapp.net"},
		{"phone without a known lid", IncomingMessage{Sender: "521@s.whatsapp.net"},
			"521@s.whatsapp.net", "521@s.whatsapp.net"},
		{"unresolved lid", IncomingMessage{Sender: "212@lid", SenderLID: "212@lid"},
			"", "unresolved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.msg.Phone(); got != tc.wantPhone {
				t.Fatalf("Phone = %q, want %q", got, tc.wantPhone)
			}
			if got := tc.msg.Display(); got != tc.wantDisplay {
				t.Fatalf("Display = %q, want %q", got, tc.wantDisplay)
			}
		})
	}
}
