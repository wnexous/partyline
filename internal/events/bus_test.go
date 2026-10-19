package events

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func msg(i int) Event { return MessageCreated{RoomID: "r1", MessageID: fmt.Sprint(i), Seq: int64(i)} }

func drain(t *testing.T, ch <-chan Event, n int) []Event {
	t.Helper()
	out := make([]Event, 0, n)
	for range n {
		select {
		case e := <-ch:
			out = append(out, e)
		case <-time.After(time.Second):
			t.Fatalf("timed out after %d of %d events", len(out), n)
		}
	}
	return out
}

func TestEverySubscriberReceivesEventsInOrder(t *testing.T) {
	bus := NewBus()
	a, cancelA := bus.Subscribe(100)
	defer cancelA()
	b, cancelB := bus.Subscribe(100)
	defer cancelB()

	want := make([]Event, 0, 50)
	for i := 1; i <= 50; i++ {
		e := msg(i)
		want = append(want, e)
		bus.Publish(e)
	}

	require.Equal(t, want, drain(t, a, 50))
	require.Equal(t, want, drain(t, b, 50))
}

func TestFullSubscriberGetsLaggedAndPublisherNeverBlocks(t *testing.T) {
	bus := NewBus()
	slow, cancelSlow := bus.Subscribe(2)
	defer cancelSlow()
	fast, cancelFast := bus.Subscribe(10)
	defer cancelFast()

	done := make(chan struct{})
	go func() {
		for i := 1; i <= 5; i++ {
			bus.Publish(msg(i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a full subscriber")
	}

	require.Equal(t, []Event{msg(1), msg(2), Lagged{}}, drain(t, slow, 3))
	select {
	case e := <-slow:
		t.Fatalf("dropped events must not be delivered, got %#v", e)
	default:
	}
	require.Equal(t, []Event{msg(1), msg(2), msg(3), msg(4), msg(5)}, drain(t, fast, 5),
		"a slow subscriber does not affect others")

	bus.Publish(msg(6))
	require.Equal(t, []Event{msg(6)}, drain(t, slow, 1), "delivery resumes once the subscriber catches up")
}

func TestCancelClosesSubscription(t *testing.T) {
	bus := NewBus()
	ch, cancel := bus.Subscribe(1)
	cancel()
	cancel() // idempotent

	_, open := <-ch
	require.False(t, open)
	bus.Publish(msg(1)) // must not panic on a cancelled subscriber
}
