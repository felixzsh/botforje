package channel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/egress"
)

// roundTripFunc lets a test answer a media fetch without a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// TestReadMediaClassifiesAPolicyRefusal proves a refused media URL fails the
// send now, while a network failure stays worth retrying (ADR 0048).
func TestReadMediaClassifiesAPolicyRefusal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failure   error
		permanent bool
	}{
		{name: "policy refusal", failure: egress.ErrBlocked, permanent: true},
		{name: "network failure", failure: io.ErrUnexpectedEOF, permanent: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &Adapter{http: &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, tc.failure
				}),
			}}
			_, err := adapter.readMedia(context.Background(), botflota.MediaContent{
				URL: "https://media.example.test/image.png",
			})
			failure, ok := botflota.AsSendFailure(err)
			if !ok {
				t.Fatalf("readMedia error = %v, want a classified send failure", err)
			}
			if failure.Permanent != tc.permanent {
				t.Fatalf("permanent = %v, want %v", failure.Permanent, tc.permanent)
			}
			if !errors.Is(err, tc.failure) {
				t.Fatalf("error = %v, want %v kept as the cause", err, tc.failure)
			}
		})
	}
}
