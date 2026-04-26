// In-process pub/sub for deployment events. Single-api-process design;
// if we ever scale api to >1 replica we'll back this with Postgres
// LISTEN/NOTIFY (the schema/wire format here is forward-compatible).
//
// Usage:
//   bus := eventbus.New()
//   sub, unsub := bus.Subscribe(jobID)
//   defer unsub()
//   for ev := range sub { ... }   // blocks until publish or unsub
//
//   bus.Publish(jobID, ev)         // fanout to all subscribers of jobID

package eventbus

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type Event struct {
	ID      int64     `json:"id"`
	JobID   uuid.UUID `json:"job_id"`
	Phase   string    `json:"phase"`
	Message string    `json:"message"`
	State   string    `json:"state,omitempty"` // post-transition state, if applicable
	At      time.Time `json:"at"`
}

type Bus struct {
	mu   sync.RWMutex
	subs map[uuid.UUID]map[chan Event]struct{}
}

func New() *Bus {
	return &Bus{subs: make(map[uuid.UUID]map[chan Event]struct{})}
}

// Subscribe returns a channel of events for the given job, plus a
// cancel function the caller MUST invoke when done (typically via
// defer) to avoid leaking the channel.
//
// Buffer size 16 is enough for normal deployment cadence (events
// arrive seconds apart). If a slow consumer overruns, we drop on
// publish — operators who want guaranteed delivery should poll the
// /api/v1/jobs/{id} endpoint instead.
func (b *Bus) Subscribe(jobID uuid.UUID) (<-chan Event, func()) {
	ch := make(chan Event, 16)
	b.mu.Lock()
	if b.subs[jobID] == nil {
		b.subs[jobID] = make(map[chan Event]struct{})
	}
	b.subs[jobID][ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if set, ok := b.subs[jobID]; ok {
			delete(set, ch)
			if len(set) == 0 {
				delete(b.subs, jobID)
			}
		}
		close(ch)
	}
	return ch, cancel
}

// Publish fans out to every subscriber of the event's JobID. Drops on
// full buffer rather than blocking the publisher — see Subscribe doc.
func (b *Bus) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[ev.JobID] {
		select {
		case ch <- ev:
		default:
			// subscriber too slow; drop
		}
	}
}

// SubCount returns how many subscribers a job has. For metrics/tests.
func (b *Bus) SubCount(jobID uuid.UUID) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[jobID])
}
