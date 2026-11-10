package agents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// fakeBeats is a HeartbeatSource whose acknowledgements fail as many times as the test says.
type fakeBeats struct {
	fail    int
	applied bool
	acks    int
}

func (f *fakeBeats) Claim(context.Context, string, string, bool, func(string) bool) ([]dto.AgentHeartbeatFrame, error) {
	return nil, nil
}

func (f *fakeBeats) Ack(context.Context, string, string) (bool, error) {
	f.acks++
	if f.fail > 0 {
		f.fail--
		return false, errors.New("database is locked")
	}
	return f.applied, nil
}

func feedWith(src HeartbeatSource) *heartbeatFeed {
	f := newHeartbeatFeed(src, "ai", "principal", "dev")
	f.wait = func(context.Context, time.Duration) error { return nil }
	f.inflight["run"] = struct{}{}
	return f
}

// HB-05: an acknowledgement that cannot be recorded is retried; one that keeps failing leaves the run in flight,
// so the routine's next run is not written over it; a run that is no longer in flight is released.
func TestHeartbeatAckRetriesAndKeepsAFailedRunInFlight(t *testing.T) {
	transient := &fakeBeats{fail: heartbeatAckAttempts - 1, applied: true}
	f := feedWith(transient)
	require.NoError(t, f.ack(context.Background(), "run"))
	require.Equal(t, heartbeatAckAttempts, transient.acks)
	require.False(t, f.held("run"))
	select {
	case <-f.wake:
	default:
		require.Fail(t, "a released run wakes the feed for the next one")
	}

	down := &fakeBeats{fail: 100}
	f = feedWith(down)
	require.Error(t, f.ack(context.Background(), "run"))
	require.Equal(t, heartbeatAckAttempts, down.acks)
	require.True(t, f.held("run"), "still in flight: the next run of its routine waits")

	gone := &fakeBeats{applied: false}
	f = feedWith(gone)
	require.NoError(t, f.ack(context.Background(), "run"))
	require.False(t, f.held("run"), "a run cancelled or superseded meanwhile is done")
}

// HB-07: the version gate fails closed: only a release at or after MinHeartbeatClientVersion, or a "dev" build,
// is sent heartbeats.
func TestHeartbeatVersionGateFailsClosed(t *testing.T) {
	for v, want := range map[string]bool{
		"dev": true, "v0.23.0": true, "0.23.0": true, "v0.23.1-rc1": true, "v1.0.0": true, "v0.23.0-4-gabc1234": true,
		"": false, "v0.22.9": false, "v0.22.0-12-gabc1234": false, "abc1234": false, "test": false, "v1.x": false,
		"future-scheme": false,
	} {
		require.Equal(t, want, heartbeatCurrent(v), v)
	}
}
