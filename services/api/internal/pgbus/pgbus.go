// Postgres LISTEN/NOTIFY-backed event bus. Closes the HA gap where a
// UI attached to api replica A never saw phone-homes that landed on
// replica B: every publish goes through pg_notify on one channel, and
// every replica runs a listener that fans received events out to its
// local in-process eventbus.
//
// Delivery model (identical guarantees to the in-process bus):
//   - Publish → pg_notify(channel, json). The publishing replica does
//     NOT short-circuit to its local bus: its own listener delivers the
//     event like everyone else's, so all replicas observe one ordering
//     and nothing is delivered twice.
//   - When the notify fails (Postgres hiccup) we fall back to local
//     delivery so a single-replica deployment degrades gracefully
//     instead of going silent.
//   - LISTEN runs on a dedicated pooled connection; on error the
//     listener reconnects with capped backoff. Notifications published
//     while disconnected are lost — same at-most-once, drop-on-overrun
//     semantics the in-process bus always had. The SSE handler's
//     backfill + the UI's polling remain the recovery path.
//
// pg_notify payloads are capped (~8000 bytes) by Postgres; events are a
// few hundred bytes. Oversized messages are truncated at the Message
// field before sending.

package pgbus

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/your-org/deployserver/api/internal/eventbus"
)

const channel = "deploy_events"
const maxMessageBytes = 4000

type Bus struct {
	local *eventbus.Bus
	pool  *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Bus {
	return &Bus{local: eventbus.New(), pool: pool}
}

// Subscribe delegates to the local bus; cross-replica events arrive via
// the Run listener.
func (b *Bus) Subscribe(jobID uuid.UUID) (<-chan eventbus.Event, func()) {
	return b.local.Subscribe(jobID)
}

// Publish sends the event through Postgres so every replica (including
// this one) delivers it to its local subscribers.
func (b *Bus) Publish(ev eventbus.Event) {
	if len(ev.Message) > maxMessageBytes {
		ev.Message = ev.Message[:maxMessageBytes]
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := b.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, string(payload)); err != nil {
		slog.Warn("pgbus notify failed; delivering locally only", "err", err)
		b.local.Publish(ev)
	}
}

// Run listens for notifications until ctx is done, fanning them out to
// the local bus. Reconnects with capped backoff on connection loss.
// Call in a goroutine at startup.
func (b *Bus) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := b.listenOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("pgbus listener disconnected; reconnecting", "err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

func (b *Bus) listenOnce(ctx context.Context) error {
	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return err
	}
	slog.Info("pgbus listening", "channel", channel)
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var ev eventbus.Event
		if err := json.Unmarshal([]byte(n.Payload), &ev); err != nil {
			slog.Warn("pgbus bad payload", "err", err)
			continue
		}
		b.local.Publish(ev)
	}
}

// SubCount mirrors eventbus.Bus for metrics/tests.
func (b *Bus) SubCount(jobID uuid.UUID) int { return b.local.SubCount(jobID) }
