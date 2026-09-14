package whatsapp

import (
	"bytes"
	"log/slog"
	"math/rand"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botforje/internal/domain"
	"github.com/felixzsh/botforje/internal/logging"
)

var t0 = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

func TestBackoffBounds(t *testing.T) {
	b := NewBackoff(100*time.Millisecond, time.Second, rand.New(rand.NewSource(1)))
	for attempt := 0; attempt < 8; attempt++ {
		delay := b.Next()
		if delay < 50*time.Millisecond || delay > time.Second {
			t.Fatalf("attempt %d delay %s is out of bounds", attempt, delay)
		}
	}
	b.Reset()
	if delay := b.Next(); delay > 100*time.Millisecond {
		t.Fatalf("after reset delay %s exceeds the first interval", delay)
	}
}

func TestDedup(t *testing.T) {
	d := newDedup(2)
	if !d.fresh("a") || !d.fresh("b") {
		t.Fatal("new ids must be fresh")
	}
	if d.fresh("a") {
		t.Fatal("a duplicate must be rejected")
	}
	if !d.fresh("c") {
		t.Fatal("c must be fresh")
	}
	if !d.fresh("a") {
		t.Fatal("an evicted id must be fresh again")
	}
	if !d.fresh("") || !d.fresh("") {
		t.Fatal("an empty id is always fresh")
	}
}

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

func TestRenderQRFitsATerminal(t *testing.T) {
	// A real linking code is long; the compact rendering must stay narrow
	// enough that a normal terminal can show it whole (ADR: pairing UX).
	code := strings.Repeat("Ab1", 64)
	var buffer bytes.Buffer
	renderQR(code, &buffer)
	widest := 0
	for _, line := range strings.Split(buffer.String(), "\n") {
		if width := utf8.RuneCountInString(line); width > widest {
			widest = width
		}
	}
	if widest == 0 {
		t.Fatal("the QR rendered nothing")
	}
	if widest > 100 {
		t.Fatalf("the QR is %d columns wide; too wide to scan on a terminal", widest)
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

func TestWaLoGFiltersBelowMinimum(t *testing.T) {
	var buffer bytes.Buffer
	wa := newWaLog(logging.New("debug", &buffer), slog.LevelWarn)
	wa.Debugf("dropped-debug")
	wa.Infof("dropped-info")
	wa.Warnf("kept-warn")
	wa.Errorf("kept-error")

	out := buffer.String()
	if strings.Contains(out, "dropped") {
		t.Fatalf("below minimum leaked: %q", out)
	}
	if !strings.Contains(out, "kept-warn") || !strings.Contains(out, "kept-error") {
		t.Fatalf("at or above minimum dropped: %q", out)
	}
}

func TestBuildContent(t *testing.T) {
	text, err := buildContent(domain.OutgoingMessage{Kind: domain.KindText, Body: "hi"})
	if err != nil {
		t.Fatalf("text: %v", err)
	}
	if text.GetConversation() != "hi" {
		t.Fatalf("text = %q", text.GetConversation())
	}

	location, err := buildContent(domain.OutgoingMessage{
		Kind:     domain.KindLocation,
		Location: &domain.Location{Latitude: 1.5, Longitude: -2.5, Name: "Office"},
	})
	if err != nil {
		t.Fatalf("location: %v", err)
	}
	got := location.GetLocationMessage()
	if got.GetDegreesLatitude() != 1.5 || got.GetDegreesLongitude() != -2.5 {
		t.Fatalf("coordinates = %v, %v", got.GetDegreesLatitude(), got.GetDegreesLongitude())
	}
	if got.GetName() != "Office" {
		t.Fatalf("name = %q", got.GetName())
	}

	if _, err := buildContent(domain.OutgoingMessage{Kind: domain.KindLocation}); err == nil {
		t.Fatal("a location message without a location must fail")
	}
}

func newTestAdapter() *Adapter {
	return &Adapter{
		bot:      "support-bot",
		incoming: make(chan domain.IncomingMessage, 4),
		seen:     newDedup(8),
		last:     map[string]lastInbound{},
		log:      logging.Discard(),
	}
}

func textEvent(sender, id, body string) *events.Message {
	jid := types.NewJID(sender, types.DefaultUserServer)
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Sender: jid, Chat: jid},
			ID:            types.MessageID(id),
			Timestamp:     t0,
		},
		Message: &waE2E.Message{Conversation: proto.String(body)},
	}
}

func TestOnMessageMapsAndDedups(t *testing.T) {
	a := newTestAdapter()
	event := textEvent("5215500000000", "ABC", "hello")

	a.onMessage(event)
	a.onMessage(event) // redelivery

	select {
	case message := <-a.incoming:
		if message.Bot != "support-bot" || message.Body != "hello" {
			t.Fatalf("message = %#v", message)
		}
		if message.Sender != "5215500000000" {
			t.Fatalf("sender = %q, want the plain phone number", message.Sender)
		}
	default:
		t.Fatal("no message was delivered")
	}
	select {
	case message := <-a.incoming:
		t.Fatalf("duplicate was delivered: %#v", message)
	default:
	}
	if _, ok := a.last["5215500000000"]; !ok {
		t.Fatal("the last inbound message was not tracked")
	}
}

func TestOnMessageDropsUnresolvedLID(t *testing.T) {
	a := newTestAdapter()
	lid := types.NewJID("212150010273830", types.HiddenUserServer)
	event := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Sender: lid, Chat: lid},
			ID:            types.MessageID("XYZ"),
			Timestamp:     t0,
		},
		Message: &waE2E.Message{Conversation: proto.String("hi")},
	}
	a.onMessage(event)
	select {
	case message := <-a.incoming:
		t.Fatalf("an unresolved message was delivered: %#v", message)
	default:
	}
}

func TestOnMessageIgnoresOwnAndNonText(t *testing.T) {
	a := newTestAdapter()
	own := textEvent("5215500000000", "1", "mine")
	own.Info.IsFromMe = true
	a.onMessage(own)

	empty := textEvent("5215500000000", "2", "")
	a.onMessage(empty)

	select {
	case message := <-a.incoming:
		t.Fatalf("unexpected message: %#v", message)
	default:
	}
}
