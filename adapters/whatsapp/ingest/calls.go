package ingest

import (
	"context"
	"errors"
	"time"

	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"

	"github.com/felixzsh/botflota"
)

// importCallLogs keeps call records that WhatsApp delivers outside the normal
// conversation message list, so a chat whose only trace is a call still shows
// one. The call id is not a key in the chat's message list, so it never
// anchors a history request.
func (h *HistoryImporter) importCallLogs(
	ctx context.Context, records []*waSyncAction.CallLogRecord, stats *ImportTotals,
) {
	for _, record := range records {
		phone, ok := historyCallPhone(record, h.lookup, h.ownPhone)
		if !ok || record.GetCallID() == "" {
			stats.Skipped++
			continue
		}
		at := callLogAt(record)
		if at.IsZero() {
			stats.Skipped++
			continue
		}
		name := h.pushNames[phone]
		key := PeerKey(phone)
		target := DirectTarget(phone)
		peer, err := h.peers.EnsurePeer(ctx, h.scope, key, at)
		if err != nil {
			h.log.Warn("import history call peer", "peer", phone, "error", err)
			continue
		}
		if name != "" {
			if err := h.peers.SetDisplayName(ctx, h.scope, key, name, at); err != nil {
				h.log.Warn("import history call name", "peer", phone, "error", err)
				continue
			}
		}
		chat, err := h.chats.Ensure(ctx, h.scope, h.bot, target, peer.ID, at)
		if err != nil {
			h.log.Warn("import history call chat", "peer", phone, "error", err)
			continue
		}
		if err := h.chats.NoteHistory(ctx, chat.ID, at, at); err != nil {
			h.log.Warn("import history call activity", "peer", phone, "error", err)
			continue
		}
		direction := botflota.Inbound
		origin := botflota.MessageOrigin("")
		if !record.GetIsIncoming() {
			direction = botflota.Outbound
			origin = botflota.OriginUnknown
		}
		message := botflota.Message{
			ChatID:            chat.ID,
			Target:            target,
			Direction:         direction,
			Content:           botflota.CallContent{Label: historyCallLogLabel(record)},
			At:                at,
			ProviderMessageID: record.GetCallID(),
			Ingest:            botflota.IngestSync,
			Origin:            origin,
			Routing:           botflota.RoutingUnknown,
		}
		stats.Received++
		if err := h.messages.Append(ctx, &message); err != nil {
			if errors.Is(err, botflota.ErrDuplicate) {
				stats.Duplicate++
			} else {
				h.log.Warn("import history call", "peer", phone, "error", err)
			}
			continue
		}
		stats.Imported++
	}
}

// historyCallPhone finds the non-local participant of a 1:1 call record.
func historyCallPhone(
	record *waSyncAction.CallLogRecord, lookup PNLookup, ownPhone string,
) (string, bool) {
	if record == nil {
		return "", false
	}
	if group := record.GetGroupJID(); group != "" {
		if jid, err := types.ParseJID(group); err == nil &&
			jid.Server == types.GroupServer {
			return "", false
		}
	}
	candidates := make([]string, 0, len(record.GetParticipants())+1)
	if record.GetIsIncoming() {
		candidates = append(candidates, record.GetCallCreatorJID())
	}
	for _, participant := range record.GetParticipants() {
		candidates = append(candidates, participant.GetUserJID())
	}
	if !record.GetIsIncoming() {
		candidates = append(candidates, record.GetCallCreatorJID())
	}
	for _, candidate := range candidates {
		phone, ok := historyJIDPhone(candidate, lookup)
		if ok && phone != ownPhone && phone != "0" {
			return phone, true
		}
	}
	return "", false
}

// callLogAt handles both epoch-seconds and epoch-milliseconds in sync records.
func callLogAt(record *waSyncAction.CallLogRecord) time.Time {
	value := record.GetStartTime()
	if value <= 0 {
		return time.Time{}
	}
	if value >= 1_000_000_000_000 {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}

func historyCallLogLabel(record *waSyncAction.CallLogRecord) string {
	missed := false
	switch record.GetCallResult() {
	case waSyncAction.CallLogRecord_MISSED,
		waSyncAction.CallLogRecord_REJECTED,
		waSyncAction.CallLogRecord_FAILED:
		missed = true
	}
	switch {
	case missed && record.GetIsVideo():
		return "Missed video call"
	case missed:
		return "Missed call"
	case record.GetIsVideo():
		return "Video call"
	default:
		return "Call"
	}
}
