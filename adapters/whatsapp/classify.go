package whatsapp

import (
	"go.mau.fi/whatsmeow/proto/waE2E"

	"github.com/felixzsh/botflota"
)

// contentType classifies an unwrapped WhatsApp message into a Botflota kind
// and its visible text (ADR 0036). ok is false for a control or internal
// message that carries no content, which the store never keeps (ADR 0035).
// Anything recognized as content but unknown falls back to KindUnknown, so the
// chat still has an anchor for `chat sync`.
func contentType(m *waE2E.Message) (botflota.MessageKind, string, bool) {
	if m == nil || controlMessage(m) {
		return "", "", false
	}
	switch {
	case m.GetConversation() != "":
		return botflota.KindText, m.GetConversation(), true
	case m.GetExtendedTextMessage() != nil:
		return botflota.KindText, m.GetExtendedTextMessage().GetText(), true
	case m.GetImageMessage() != nil:
		return botflota.KindImage, m.GetImageMessage().GetCaption(), true
	case m.GetVideoMessage() != nil:
		return botflota.KindVideo, m.GetVideoMessage().GetCaption(), true
	case m.GetPtvMessage() != nil:
		return botflota.KindVideo, m.GetPtvMessage().GetCaption(), true
	case m.GetAudioMessage() != nil:
		return botflota.KindAudio, "", true
	case m.GetDocumentMessage() != nil:
		return botflota.KindDocument, documentText(m.GetDocumentMessage()), true
	case m.GetStickerMessage() != nil:
		return botflota.KindSticker, "", true
	case m.GetLocationMessage() != nil:
		return botflota.KindLocation, m.GetLocationMessage().GetName(), true
	case m.GetLiveLocationMessage() != nil:
		return botflota.KindLocation, m.GetLiveLocationMessage().GetCaption(), true
	case m.GetContactMessage() != nil:
		return botflota.KindContact, m.GetContactMessage().GetDisplayName(), true
	case m.GetContactsArrayMessage() != nil:
		return botflota.KindContact, m.GetContactsArrayMessage().GetDisplayName(), true
	case m.GetPollCreationMessage() != nil:
		return botflota.KindPoll, m.GetPollCreationMessage().GetName(), true
	case m.GetPollCreationMessageV2() != nil:
		return botflota.KindPoll, m.GetPollCreationMessageV2().GetName(), true
	case m.GetPollCreationMessageV3() != nil:
		return botflota.KindPoll, m.GetPollCreationMessageV3().GetName(), true
	case m.GetPollCreationMessageV5() != nil:
		return botflota.KindPoll, m.GetPollCreationMessageV5().GetName(), true
	case m.GetPollCreationMessageV6() != nil:
		return botflota.KindPoll, m.GetPollCreationMessageV6().GetName(), true
	case m.GetEventMessage() != nil:
		return botflota.KindEvent, m.GetEventMessage().GetName(), true
	case m.GetButtonsResponseMessage() != nil:
		return botflota.KindInteractive, m.GetButtonsResponseMessage().GetSelectedDisplayText(), true
	case m.GetListResponseMessage() != nil:
		return botflota.KindInteractive, m.GetListResponseMessage().GetTitle(), true
	case m.GetTemplateButtonReplyMessage() != nil:
		return botflota.KindInteractive,
			m.GetTemplateButtonReplyMessage().GetSelectedDisplayText(), true
	case m.GetInteractiveResponseMessage() != nil:
		return botflota.KindInteractive,
			m.GetInteractiveResponseMessage().GetBody().GetText(), true
	case m.GetProductMessage() != nil:
		return botflota.KindCommerce, m.GetProductMessage().GetProduct().GetTitle(), true
	case m.GetOrderMessage() != nil:
		return botflota.KindCommerce, m.GetOrderMessage().GetOrderTitle(), true
	case m.GetInvoiceMessage() != nil:
		return botflota.KindCommerce, m.GetInvoiceMessage().GetNote(), true
	case isCallMessage(m):
		return botflota.KindCall, callText(m), true
	default:
		return botflota.KindUnknown, "", true
	}
}

// controlMessage reports a message that changes another message or steers the
// protocol instead of carrying content: edits, reactions, deletions, poll
// votes, pins, keeps, and key-distribution traffic. They are control-only and
// never stored (ADR 0035).
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

// unwrapMessage peels the wrappers WhatsApp puts around a real payload
// (ephemeral, view once, document with caption, device-sent, bot invoke) so the
// history import can classify what is inside. It returns nil for a control
// message (ADR 0035).
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

// documentText is a document's visible text: its caption, its title, or its
// file name (ADR 0036).
func documentText(doc *waE2E.DocumentMessage) string {
	if caption := doc.GetCaption(); caption != "" {
		return caption
	}
	if title := doc.GetTitle(); title != "" {
		return title
	}
	return doc.GetFileName()
}

// isCallMessage reports a call attempt or call log entry, which the import
// keeps under KindCall.
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
// readable row (ADR 0035). A scheduled call uses its title.
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

// callLogLabel describes an ended call attempt.
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
