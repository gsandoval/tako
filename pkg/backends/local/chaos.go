package local

import (
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// chaosState injects at-least-once realities into the dispatcher:
// duplicate delivery, redelivery after ack, and reordering delays.
//
// Decisions are a pure function of (seed, destination, delivery ordinal)
// — never of goroutine timing — so a seed replays the same injection
// pattern wherever publish order is stable, which sequential test
// bodies guarantee.
type chaosState struct {
	mu       sync.Mutex
	seed     int64
	ordinals map[string]uint64 // destination → deliveries dispatched so far
}

// chaosPlan is the injection decision for one dispatched delivery.
type chaosPlan struct {
	duplicate bool          // deliver a second copy
	redeliver bool          // deliver again after a successful ack
	delay     time.Duration // pre-delivery delay (reordering)
}

// EnableChaos turns chaos on with the given seed. Callers print the
// seed themselves (test log, dev banner) — reproducibility is the point.
func (d *QueueDispatcher) EnableChaos(seed int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.chaos = &chaosState{seed: seed, ordinals: map[string]uint64{}}
}

// OnChaos subscribes to chaos injections (the dev console's ☢ lines).
// action is "duplicate" or "redeliver".
func (d *QueueDispatcher) OnChaos(fn func(source, consumer, action string)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onChaos = fn
}

// chaosPlanFor draws the plan for the next delivery to one destination.
func (d *QueueDispatcher) chaosPlanFor(to delivery) chaosPlan {
	d.mu.Lock()
	chaos := d.chaos
	d.mu.Unlock()
	if chaos == nil {
		return chaosPlan{}
	}
	chaos.mu.Lock()
	key := to.source + "/" + to.consumer
	n := chaos.ordinals[key]
	chaos.ordinals[key] = n + 1
	seed := chaos.seed
	chaos.mu.Unlock()

	return chaosPlan{
		duplicate: chance(seed, key, n, "duplicate", 25),
		redeliver: chance(seed, key, n, "redeliver", 15),
		delay:     time.Duration(draw(seed, key, n, "delay")%20) * time.Millisecond,
	}
}

// chance reports a percent-probability event drawn deterministically.
func chance(seed int64, key string, n uint64, salt string, percent uint64) bool {
	return draw(seed, key, n, salt)%100 < percent
}

// draw hashes one decision's identity to a uniform value.
func draw(seed int64, key string, n uint64, salt string) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%s|%d|%s", seed, key, n, salt)
	return h.Sum64()
}

func (d *QueueDispatcher) notifyChaos(to delivery, action string) {
	d.mu.Lock()
	fn := d.onChaos
	d.mu.Unlock()
	if fn != nil {
		fn(to.source, to.consumer, action)
	}
}
