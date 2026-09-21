package whatsapp

import (
	"math/rand"
	"time"
)

// Backoff is capped exponential backoff with full jitter (ADR 0011): each
// delay falls between half and the cap, so no fixed reconnect cadence becomes
// a fingerprint.
type Backoff struct {
	min, max time.Duration
	rand     *rand.Rand
	attempt  int
}

// NewBackoff falls back to 1s/1min on zero or negative bounds.
func NewBackoff(min, max time.Duration, rnd *rand.Rand) *Backoff {
	if min <= 0 {
		min = time.Second
	}
	if max < min {
		max = min
	}
	if rnd == nil {
		rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &Backoff{min: min, max: max, rand: rnd}
}

func (b *Backoff) Next() time.Duration {
	delay := b.min
	for i := 0; i < b.attempt && delay < b.max; i++ {
		delay *= 2
	}
	if delay > b.max {
		delay = b.max
	}
	b.attempt++
	half := delay / 2
	return half + time.Duration(b.rand.Int63n(int64(half)+1))
}

func (b *Backoff) Reset() { b.attempt = 0 }
