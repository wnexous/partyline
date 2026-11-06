package agents

import (
	"testing"
	"time"
)

// TR-04: the wake-up a feed armed for a voice note that waits for its transcript ends with the feed.
func TestThePlatformFeedWakeUpStopsWithTheFeed(t *testing.T) {
	f := newPlatformFeed(nil, "agent", time.Minute, time.Now)
	f.stop() // a feed that never waited has nothing to stop

	f.wakeIn(20 * time.Millisecond)
	f.stop()
	time.Sleep(100 * time.Millisecond)
	select {
	case <-f.wake:
		t.Fatal("the feed was poked after it stopped")
	default:
	}

	f.wakeIn(time.Millisecond)
	select {
	case <-f.wake:
	case <-time.After(5 * time.Second):
		t.Fatal("an armed wake-up pokes the feed")
	}
}
