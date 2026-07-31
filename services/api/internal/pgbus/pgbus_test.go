// Integration tests against a real Postgres (DEPLOY_TEST_PG_DSN-gated,
// like the store tests). The whole point of pgbus is cross-instance
// delivery, which can't be faked in-process.

package pgbus

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/your-org/deployserver/api/internal/eventbus"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DEPLOY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("DEPLOY_TEST_PG_DSN not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// startBus runs a bus listener and blocks until LISTEN is active (a
// probe notification round-trips), so tests can't race the setup.
func startBus(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *Bus {
	t.Helper()
	b := New(pool)
	go b.Run(ctx)

	probeJob := uuid.New()
	sub, cancel := b.Subscribe(probeJob)
	defer cancel()
	deadline := time.After(10 * time.Second)
	for {
		b.Publish(eventbus.Event{JobID: probeJob, Phase: "probe"})
		select {
		case <-sub:
			return b
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("bus listener never became ready")
		}
	}
}

func TestCrossInstanceDelivery(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Two independent Bus instances — two api replicas.
	a := startBus(t, ctx, pool)
	b := startBus(t, ctx, pool)

	jobID := uuid.New()
	subB, cancelB := b.Subscribe(jobID)
	defer cancelB()

	// Publish on replica A; the subscriber on replica B must see it.
	a.Publish(eventbus.Event{JobID: jobID, Phase: "imaging", Message: "cross-replica"})
	select {
	case ev := <-subB:
		if ev.Phase != "imaging" || ev.Message != "cross-replica" {
			t.Fatalf("wrong event: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event never crossed replicas")
	}

	// The publisher's own subscribers get exactly one copy too (via its
	// listener — no local short-circuit double delivery).
	subA, cancelA := a.Subscribe(jobID)
	defer cancelA()
	a.Publish(eventbus.Event{JobID: jobID, Phase: "post_install"})
	select {
	case ev := <-subA:
		if ev.Phase != "post_install" {
			t.Fatalf("wrong event: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publisher's own subscriber never got the event")
	}
	select {
	case ev := <-subA:
		t.Fatalf("duplicate delivery: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestJobScoping(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := startBus(t, ctx, pool)

	otherJob := uuid.New()
	sub, cancelSub := b.Subscribe(otherJob)
	defer cancelSub()

	b.Publish(eventbus.Event{JobID: uuid.New(), Phase: "imaging"})
	select {
	case ev := <-sub:
		t.Fatalf("event leaked across jobs: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestOversizedMessageTruncated(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := startBus(t, ctx, pool)

	jobID := uuid.New()
	sub, cancelSub := b.Subscribe(jobID)
	defer cancelSub()

	huge := make([]byte, 20000)
	for i := range huge {
		huge[i] = 'x'
	}
	// Must not error out of pg_notify's payload cap — truncated instead.
	b.Publish(eventbus.Event{JobID: jobID, Phase: "imaging", Message: string(huge)})
	select {
	case ev := <-sub:
		if len(ev.Message) > maxMessageBytes {
			t.Fatalf("message not truncated: %d bytes", len(ev.Message))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("oversized event dropped entirely (should be truncated + delivered)")
	}
}
