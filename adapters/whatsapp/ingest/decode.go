// Package ingest stores what WhatsApp reports about the past: history syncs,
// call logs, and the address book, decoded into Botflota records (ADR 0035,
// ADR 0036, ADR 0037).
package ingest

import (
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/felixzsh/botflota"
)

// Decode classifies an unwrapped WhatsApp message (ADR 0036). ok is
// false for a control or internal message, which the store never keeps (ADR
// 0035). Anything else falls back to UnknownContent, so the chat still has an
// anchor for `chat sync`.
func Decode(m *waE2E.Message) (botflota.MessageContent, bool) {
	if m == nil || controlMessage(m) {
		return nil, false
	}
	switch {
	case m.GetConversation() != "":
		return botflota.TextContent{Body: m.GetConversation()}, true
	case m.GetExtendedTextMessage() != nil:
		return botflota.TextContent{
			Body: m.GetExtendedTextMessage().GetText(),
		}, true
	case m.GetImageMessage() != nil:
		image := m.GetImageMessage()
		return botflota.MediaContent{
			Type: botflota.MediaImage, MIME: image.GetMimetype(),
			Caption: image.GetCaption(),
		}, true
	case m.GetVideoMessage() != nil:
		video := m.GetVideoMessage()
		return botflota.MediaContent{
			Type: botflota.MediaVideo, MIME: video.GetMimetype(),
			Caption: video.GetCaption(),
		}, true
	case m.GetPtvMessage() != nil:
		ptv := m.GetPtvMessage()
		return botflota.MediaContent{
			Type: botflota.MediaVideo, MIME: ptv.GetMimetype(),
			Caption: ptv.GetCaption(),
		}, true
	case m.GetAudioMessage() != nil:
		return botflota.MediaContent{
			Type: botflota.MediaAudio, MIME: m.GetAudioMessage().GetMimetype(),
		}, true
	case m.GetDocumentMessage() != nil:
		document := m.GetDocumentMessage()
		return botflota.MediaContent{
			Type: botflota.MediaDocument, MIME: document.GetMimetype(),
			Name: document.GetFileName(), Caption: documentText(document),
		}, true
	case m.GetStickerMessage() != nil:
		return botflota.MediaContent{
			Type: botflota.MediaSticker, MIME: m.GetStickerMessage().GetMimetype(),
		}, true
	case m.GetLocationMessage() != nil:
		location := m.GetLocationMessage()
		return botflota.LocationContent{
			Latitude:    location.GetDegreesLatitude(),
			Longitude:   location.GetDegreesLongitude(),
			Name:        location.GetName(),
			Address:     location.GetAddress(),
			URL:         location.GetURL(),
			Description: location.GetComment(),
		}, true
	case m.GetLiveLocationMessage() != nil:
		location := m.GetLiveLocationMessage()
		return botflota.LocationContent{
			Latitude:    location.GetDegreesLatitude(),
			Longitude:   location.GetDegreesLongitude(),
			Description: location.GetCaption(),
		}, true
	case m.GetContactMessage() != nil:
		card := m.GetContactMessage()
		return contactContent(card.GetDisplayName(), card.GetVcard()), true
	case m.GetContactsArrayMessage() != nil:
		array := m.GetContactsArrayMessage()
		raw := ""
		if len(array.GetContacts()) > 0 {
			raw = array.GetContacts()[0].GetVcard()
		}
		return contactContent(array.GetDisplayName(), raw), true
	case m.GetPollCreationMessage() != nil:
		return pollContent(m.GetPollCreationMessage()), true
	case m.GetPollCreationMessageV2() != nil:
		return pollContent(m.GetPollCreationMessageV2()), true
	case m.GetPollCreationMessageV3() != nil:
		return pollContent(m.GetPollCreationMessageV3()), true
	case m.GetPollCreationMessageV5() != nil:
		return pollContent(m.GetPollCreationMessageV5()), true
	case m.GetPollCreationMessageV6() != nil:
		return pollContent(m.GetPollCreationMessageV6()), true
	case m.GetEventMessage() != nil:
		return botflota.EventContent{Name: m.GetEventMessage().GetName()}, true
	case m.GetButtonsResponseMessage() != nil:
		return botflota.InteractiveContent{
			Text: m.GetButtonsResponseMessage().GetSelectedDisplayText(),
		}, true
	case m.GetListResponseMessage() != nil:
		return botflota.InteractiveContent{
			Text: m.GetListResponseMessage().GetTitle(),
		}, true
	case m.GetTemplateButtonReplyMessage() != nil:
		return botflota.InteractiveContent{
			Text: m.GetTemplateButtonReplyMessage().GetSelectedDisplayText(),
		}, true
	case m.GetInteractiveResponseMessage() != nil:
		return botflota.InteractiveContent{
			Text: m.GetInteractiveResponseMessage().GetBody().GetText(),
		}, true
	case m.GetProductMessage() != nil:
		return botflota.CommerceContent{
			Title: m.GetProductMessage().GetProduct().GetTitle(),
		}, true
	case m.GetOrderMessage() != nil:
		return botflota.CommerceContent{Title: m.GetOrderMessage().GetOrderTitle()}, true
	case m.GetInvoiceMessage() != nil:
		return botflota.CommerceContent{Title: m.GetInvoiceMessage().GetNote()}, true
	case isCallMessage(m):
		return botflota.CallContent{Label: callText(m)}, true
	default:
		return botflota.UnknownContent{}, true
	}
}

func pollContent(poll *waE2E.PollCreationMessage) botflota.PollContent {
	content := botflota.PollContent{
		Question:   poll.GetName(),
		Selectable: int(poll.GetSelectableOptionsCount()),
	}
	for _, option := range poll.GetOptions() {
		content.Options = append(content.Options, option.GetOptionName())
	}
	return content
}

// controlMessage is a message that changes another message or steers the
// protocol instead of carrying content; it is never stored (ADR 0035).
func controlMessage(m *waE2E.Message) bool {
	switch {
	case m == nil,
		m.GetProtocolMessage() != nil,
		m.GetEditedMessage() != nil,
		m.GetReactionMessage() != nil,
		m.GetEncReactionMessage() != nil,
		m.GetPollUpdateMessage() != nil,
		m.GetKeepInChatMessage() != nil,
		m.GetPinInChatMessage() != nil,
		m.GetSenderKeyDistributionMessage() != nil,
		m.GetFastRatchetKeySenderKeyDistributionMessage() != nil,
		m.GetStickerSyncRmrMessage() != nil:
		return true
	default:
		return false
	}
}

// unwrapMessage peels the wrappers WhatsApp puts around a real payload; it
// reports nil for a control message (ADR 0035).
func unwrapMessage(m *waE2E.Message) *waE2E.Message {
	if controlMessage(m) {
		return nil
	}
	if inner := m.GetDeviceSentMessage().GetMessage(); inner != nil {
		m = inner
	}
	if inner := m.GetBotInvokeMessage().GetMessage(); inner != nil {
		m = inner
	}
	if inner := m.GetEphemeralMessage().GetMessage(); inner != nil {
		m = inner
	}
	if inner := m.GetViewOnceMessage().GetMessage(); inner != nil {
		m = inner
	}
	if inner := m.GetViewOnceMessageV2().GetMessage(); inner != nil {
		m = inner
	}
	if inner := m.GetViewOnceMessageV2Extension().GetMessage(); inner != nil {
		m = inner
	}
	if inner := m.GetLottieStickerMessage().GetMessage(); inner != nil {
		m = inner
	}
	if inner := m.GetDocumentWithCaptionMessage().GetMessage(); inner != nil {
		m = inner
	}
	if inner := m.GetPollCreationMessageV4().GetMessage(); inner != nil {
		m = inner
	}
	if controlMessage(m) {
		return nil
	}
	return m
}

// documentText falls back from caption to title to file name (ADR 0036).
func documentText(doc *waE2E.DocumentMessage) string {
	if caption := doc.GetCaption(); caption != "" {
		return caption
	}
	if title := doc.GetTitle(); title != "" {
		return title
	}
	return doc.GetFileName()
}

func isCallMessage(m *waE2E.Message) bool {
	switch {
	case m.GetCall() != nil,
		m.GetCallLogMesssage() != nil,
		m.GetBcallMessage() != nil,
		m.GetScheduledCallCreationMessage() != nil,
		m.GetScheduledCallEditMessage() != nil:
		return true
	default:
		return false
	}
}

// callText labels a call entry so a chat whose only history is calls keeps a
// readable row; a scheduled call uses its title (ADR 0035).
func callText(m *waE2E.Message) string {
	if schedule := m.GetScheduledCallCreationMessage(); schedule != nil {
		if title := schedule.GetTitle(); title != "" {
			return title
		}
		return "Scheduled call"
	}
	if label, ok := callLogLabel(m); ok {
		return label
	}
	return "Call"
}

func callLogLabel(web *waE2E.Message) (string, bool) {
	call := web.GetCallLogMesssage()
	if call == nil {
		return "", false
	}
	missed := false
	switch call.GetCallOutcome() {
	case waE2E.CallLogMessage_MISSED,
		waE2E.CallLogMessage_REJECTED,
		waE2E.CallLogMessage_FAILED:
		missed = true
	}
	switch {
	case missed && call.GetIsVideo():
		return "Missed video call", true
	case missed:
		return "Missed call", true
	case call.GetIsVideo():
		return "Video call", true
	default:
		return "Call", true
	}
}

// contactContent parses a vCard into neutral entries. Only the standard
// TEL, EMAIL, ADR, URL, and NOTE properties are read; anything else stays
// out rather than leaking wire details into the domain. Values stay
// opaque: the parser unescapes the field separators but never interprets
// the content.
func contactContent(name, raw string) botflota.ContactContent {
	content := botflota.ContactContent{Name: name}
	for _, line := range vcardLines(raw) {
		prop, value := vcardProperty(line)
		switch prop {
		case "TEL":
			content.Entries = append(content.Entries,
				botflota.ContactEntry{Kind: "phone", Value: value})
		case "EMAIL":
			content.Entries = append(content.Entries,
				botflota.ContactEntry{Kind: "email", Value: value})
		case "ADR":
			content.Entries = append(content.Entries,
				botflota.ContactEntry{Kind: "address", Value: value})
		case "URL":
			content.Entries = append(content.Entries,
				botflota.ContactEntry{Kind: "url", Value: value})
		case "NOTE":
			if content.Note == "" {
				content.Note = value
			} else {
				content.Note += "\n" + value
			}
		case "FN":
			if content.Name == "" {
				content.Name = value
			}
		}
	}
	return content
}

// vcardLines splits vCard text into unfolded logical lines.
func vcardLines(raw string) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += strings.TrimPrefix(strings.TrimPrefix(line, " "), "\t")
			continue
		}
		if line = strings.TrimRight(line, "\r"); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// vcardProperty splits one line into its uppercase property name and its
// unescaped value.
func vcardProperty(line string) (string, string) {
	name := line
	if i := strings.IndexAny(line, ";:"); i >= 0 {
		name = line[:i]
	}
	value := ""
	if i := strings.Index(line, ":"); i >= 0 {
		value = vcardUnescape(line[i+1:])
	}
	return strings.ToUpper(name), value
}

// vcardUnescape reverses the vCard 3.0 field separator escapes.
func vcardUnescape(value string) string {
	return strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\;`, ";", `\,`, ",", `\\`, `\`).Replace(value)
}
