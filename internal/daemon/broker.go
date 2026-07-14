// broker.go: the in-process event fan-out between the worker loop (§23.6
// step 7 "emit event") and the SSE stream (§23.4 GET /v1/events/stream).
// Deliberately memory-only: the durable record of what happened is the
// pause_records/wake_jobs rows themselves; SSE is a live view for attached
// clients (the VS Code extension, `curl`), not an event store — a
// subscriber that was not connected when an event fired reads current
// state from the status/jobs endpoints instead of replaying history.
package daemon

import (
	"sync"

	protocol "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// subscriberBuffer is each subscriber's channel capacity. A subscriber
// that falls further behind than this drops events (documented SSE
// semantics above — live view, not history); blocking the worker loop on
// a slow HTTP client would be strictly worse (§23.6's loop must keep
// executing wake jobs no matter who is watching).
const subscriberBuffer = 64

// Broker is a minimal publish/subscribe fan-out for protocol v1 events.
type Broker struct {
	mu   sync.Mutex
	subs map[int]chan protocol.Event
	next int
}

// NewBroker constructs an empty Broker.
func NewBroker() *Broker {
	return &Broker{subs: map[int]chan protocol.Event{}}
}

// Subscribe registers a new subscriber and returns its receive channel and
// a cancel func. Cancel is idempotent and closes the channel, so a ranging
// receiver terminates cleanly.
func (b *Broker) Subscribe() (<-chan protocol.Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan protocol.Event, subscriberBuffer)
	b.subs[id] = ch
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if sub, ok := b.subs[id]; ok {
				delete(b.subs, id)
				close(sub)
			}
		})
	}
	return ch, cancel
}

// Publish delivers ev to every current subscriber, dropping it for any
// subscriber whose buffer is full (never blocks the publisher).
func (b *Broker) Publish(ev protocol.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default: // slow subscriber: drop, never block the worker loop
		}
	}
}
