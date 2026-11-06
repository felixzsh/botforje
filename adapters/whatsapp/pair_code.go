package whatsapp

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"

	"github.com/felixzsh/botflota"
)

// pairCodeClient is the platform descriptor a code link declares. Its field is
// the companion's platform display, and the provider validates it against a
// closed set of client and platform pairs, so a code link carries this value
// instead of channel.options.device_name (ADR 0028).
const pairCodeClient = "Chrome (Linux)"

// codeLink returns the action a code setup performs once the connection is
// ready: it registers the owner's phone and hands the code to the display. A
// QR setup returns nil (ADR 0028).
func (opts PairOptions) codeLink() (func(context.Context, *whatsmeow.Client) error, error) {
	if opts.Phone == "" {
		return nil, nil
	}
	phone, err := normalizePairPhone(opts.Phone)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, client *whatsmeow.Client) error {
		code, err := client.PairPhone(ctx, phone, false,
			whatsmeow.PairClientChrome, pairCodeClient)
		if err != nil {
			return fmt.Errorf("whatsapp: pair phone: %w", err)
		}
		if opts.OnCode != nil {
			opts.OnCode(code)
		}
		return nil
	}, nil
}

// normalizePairPhone reduces the owner's number to the international digits a
// code link registers, refusing what the provider would reject anyway: too
// short, or written with a national leading zero.
func normalizePairPhone(phone string) (string, error) {
	digits := make([]byte, 0, len(phone))
	for i := 0; i < len(phone); i++ {
		if phone[i] >= '0' && phone[i] <= '9' {
			digits = append(digits, phone[i])
		}
	}
	if len(digits) <= 6 || digits[0] == '0' {
		return "", fmt.Errorf(
			"whatsapp: phone must be an international number: %w",
			botflota.ErrInvalidParam)
	}
	return string(digits), nil
}
