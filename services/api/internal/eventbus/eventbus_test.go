package eventbus

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSubscribeReceivesPublish(t *testing.T) {
	b := New()
	job := uuid.New()
	sub, cancel := b.Subscribe(job)
	defer cancel()

	go b.Publish(Event{JobID: job, Phase: "imaging", Message: "tick"})

	select {
	case ev := <-sub:
		if ev.Phase != "imaging" || ev.Message != "tick" {
			t.Fatalf("got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

func TestPublishToOtherJobIsolated(t *testing.T) {
	b := New()
	job := uuid.New()
	other := uuid.New()
	sub, cancel := b.Subscribe(job)
	defer cancel()

	b.Publish(Event{JobID: other, Phase: "imaging", Message: "no"})

	select {
	case ev := <-sub:
		t.Fatalf("unexpected event %+v from other job", ev)
	case <-time.After(50 * time.Millisecond):
		// good — sub for `job` does not receive `other`'s events
	}
}

func TestMultipleSubscribersAllReceive(t *testing.T) {
	b := New()
	job := uuid.New()
	const n = 5
	subs := make([]<-chan Event, n)
	cancels := make([]func(), n)
	for i := 0; i < n; i++ {
		subs[i], cancels[i] = b.Subscribe(job)
	}
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()

	if got := b.SubCount(job); got != n {
		t.Fatalf("SubCount = %d; want %d", got, n)
	}

	go b.Publish(Event{JobID: job, Phase: "p", Message: "m"})

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			select {
			case <-subs[i]:
			case <-time.After(time.Second):
				t.Errorf("sub %d did not receive", i)
			}
		}(i)
	}
	wg.Wait()
}

func TestCancelRemovesSubscriber(t *testing.T) {
	b := New()
	job := uuid.New()
	sub, cancel := b.Subscribe(job)
	if got := b.SubCount(job); got != 1 {
		t.Fatalf("SubCount before cancel = %d; want 1", got)
	}
	cancel()
	if got := b.SubCount(job); got != 0 {
		t.Fatalf("SubCount after cancel = %d; want 0", got)
	}
	// reading after close should be ok and yield zero value (channel closed)
	_, ok := <-sub
	if ok {
		t.Fatal("expected channel to be closed after cancel")
	}
}

func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	b := New()
	job := uuid.New()
	sub, cancel := b.Subscribe(job)
	defer cancel()
	// Don't drain `sub`. Fill its 16-slot buffer; further publishes
	// should silently drop instead of blocking.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			b.Publish(Event{JobID: job, Phase: "p"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishers blocked on slow subscriber")
	}
	// drain whatever's in the buffer; should not deadlock
	for {
		select {
		case <-sub:
		default:
			return
		}
	}
}
