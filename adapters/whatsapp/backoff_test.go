package whatsapp

import (
	"math/rand"
	"testing"
	"time"
)

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
