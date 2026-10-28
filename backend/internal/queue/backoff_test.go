package queue

import (
	"testing"
	"time"
)

// AC4: exponential backoff with equal jitter, capped at the maximum.
func TestBackoffExponentialWithJitter(t *testing.T) {
	w := &Worker{Jitter: func() float64 { return 1 }}
	var prev time.Duration
	for n := 0; n < 5; n++ {
		d := w.backoff(2, 30, n)
		if d <= prev && d < 30*time.Second {
			t.Fatalf("attempt %d: backoff %v did not grow (prev %v)", n, d, prev)
		}
		prev = d
	}
	if d := w.backoff(2, 30, 20); d != 30*time.Second {
		t.Fatalf("cap: got %v, want 30s", d)
	}
	lo := (&Worker{Jitter: func() float64 { return 0 }}).backoff(2, 30, 3)
	hi := (&Worker{Jitter: func() float64 { return 0.999 }}).backoff(2, 30, 3)
	if lo != 8*time.Second || hi <= lo || hi > 16*time.Second {
		t.Fatalf("jitter range [%v,%v], want [8s,16s)", lo, hi)
	}
}
