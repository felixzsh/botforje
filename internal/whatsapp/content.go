package whatsapp

import (
	"errors"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botforje/internal/domain"
)

// buildContent maps an outbound message to the WhatsApp wire message.
func buildContent(msg domain.OutgoingMessage) (*waE2E.Message, error) {
	switch msg.Kind {
	case domain.KindLocation:
		if msg.Location == nil {
			return nil, errors.New("whatsapp: location message without a location")
		}
		return &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude:  proto.Float64(msg.Location.Latitude),
			DegreesLongitude: proto.Float64(msg.Location.Longitude),
			Name:             optional(msg.Location.Name),
			Address:          optional(msg.Location.Address),
			URL:              optional(msg.Location.URL),
			Comment:          optional(msg.Location.Description),
		}}, nil
	default:
		return &waE2E.Message{Conversation: proto.String(msg.Body)}, nil
	}
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return proto.String(value)
}
