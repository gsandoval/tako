package local

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// SetRate paces dispatch: 5 messages at 4/s must take at least ~1s to
// deliver (first fires immediately, then 4 slots of 250ms).
func TestSetRatePacesDelivery(t *testing.T) {
	d := NewQueueDispatcher(t.TempDir(), QueueOptions{})
	var mu sync.Mutex
	var stamps []time.Time
	d.Consume("paced", func(ctx context.Context, payload []byte) error {
		mu.Lock()
		stamps = append(stamps, time.Now())
		mu.Unlock()
		return nil
	})
	d.SetRate("paced", 4)
	start := time.Now()
	for range 5 {
		if err := d.Publish(context.Background(), "paced", []byte("x"), backends.PublishOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.WaitIdle(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 900*time.Millisecond {
		t.Fatalf("5 messages at 4/s delivered in %s — pacing is not happening", took)
	}
	if len(stamps) != 5 {
		t.Fatalf("delivered %d, want 5", len(stamps))
	}
}
