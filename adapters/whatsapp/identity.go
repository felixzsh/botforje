package whatsapp

import (
	"strings"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

// Number strips the device suffix and the server from a stored account JID
// for display: the JID itself stays internal (ADR 0020).
func Number(jid string) string {
	number := jid
	if at := strings.IndexByte(number, '@'); at >= 0 {
		number = number[:at]
	}
	if colon := strings.IndexByte(number, ':'); colon >= 0 {
		number = number[:colon]
	}
	return number
}

// DevicePhone is the paired account's own number, which call-log routing uses
// to tell the local side from the peer.
func DevicePhone(device *store.Device) string {
	if device == nil || device.GetJID().Server != types.DefaultUserServer {
		return ""
	}
	return device.GetJID().User
}
