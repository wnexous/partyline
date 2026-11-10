package agents

import (
	"context"
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
)

// MinHeartbeatClientVersion is the oldest client version that renders a heartbeat frame to the harness
// (HB-07): v0.23.0. An older client ignores the frame type, so its principal is sent no heartbeat and the run
// is recorded client_outdated; a local "dev" build is current.
const MinHeartbeatClientVersion = "v0.23.0"

// heartbeatCurrent reports whether a client of version v renders heartbeats. It fails closed: a missing version,
// and one that is no release version (a bare git hash, another scheme), is old; only "dev", a build from this
// source, passes without a number.
func heartbeatCurrent(v string) bool {
	if v == "dev" {
		return true
	}
	if _, ok := parseVersion(v); !ok || v == "" {
		return false
	}
	return !versionOlder(v, MinHeartbeatClientVersion)
}

// HeartbeatSource is the side of the schedules the gateway reads (HB-04..HB-07): the runs to write to a
// principal session, and their acknowledgements. The app registers the schedules service.
type HeartbeatSource interface {
	// Claim returns the runs to write now to the AI's principal session; inflight names the runs written to
	// this session and not acknowledged yet. current is false for a client too old to render them.
	Claim(ctx context.Context, agentID, sessionKey string, current bool, inflight func(runID string) bool) ([]dto.AgentHeartbeatFrame, error)
	// Ack records that the session handed the run to the model. applied is false for a run that is not in flight
	// any more (cancelled, superseded, acknowledged already); err is a failure to record it, worth a retry.
	Ack(ctx context.Context, agentID, runID string) (applied bool, err error)
}

// heartbeatFeed writes the due runs of the AI's schedules to its principal session (HB-04), one in flight per
// schedule (HB-05). The runs in the database are the truth; only the ids in flight on this connection are kept
// here. Every wake-up (a run came due, a lagged bus, an acknowledgement) claims again.
type heartbeatFeed struct {
	src        HeartbeatSource
	agentID    string
	sessionKey string
	current    bool

	mu       sync.Mutex
	inflight map[string]struct{}
	wake     chan struct{}
	// wait sleeps between the attempts of an acknowledgement; tests make it instant.
	wait func(ctx context.Context, d time.Duration) error
}

// heartbeatAckAttempts is how many times an acknowledgement is recorded before it is left in flight.
const heartbeatAckAttempts = 3

// ack records the session's acknowledgement of a run. A run that is not in flight any more is released. A
// failure to record it is retried; one that keeps failing leaves the run in flight, so the routine's next run is
// not written over an unrecorded one (HB-05): the next connection claims it again, and the client, which saw it,
// acknowledges it without handing it over twice.
func (f *heartbeatFeed) ack(ctx context.Context, runID string) error {
	var err error
	for attempt := range heartbeatAckAttempts {
		if attempt > 0 {
			if werr := f.wait(ctx, time.Duration(50<<attempt)*time.Millisecond); werr != nil {
				return werr
			}
		}
		// Recorded, or not in flight any more: either way the run is done on this connection.
		if _, err = f.src.Ack(ctx, f.agentID, runID); err == nil {
			f.release(runID)
			return nil
		}
	}
	return err
}

// sleepCtx sleeps for d unless ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func newHeartbeatFeed(src HeartbeatSource, agentID, sessionKey, clientVersion string) *heartbeatFeed {
	return &heartbeatFeed{
		src: src, agentID: agentID, sessionKey: sessionKey, current: heartbeatCurrent(clientVersion), wait: sleepCtx,
		inflight: map[string]struct{}{}, wake: make(chan struct{}, 1),
	}
}

// poke asks the connection's loop to claim; pokes coalesce.
func (f *heartbeatFeed) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *heartbeatFeed) held(runID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.inflight[runID]
	return ok
}

// release frees a run the session acknowledged and asks for the next one.
func (f *heartbeatFeed) release(runID string) {
	f.mu.Lock()
	_, held := f.inflight[runID]
	delete(f.inflight, runID)
	f.mu.Unlock()
	if held {
		f.poke()
	}
}

// fill writes the runs that are due. It runs on the connection's loop only.
func (f *heartbeatFeed) fill(ctx context.Context, c *conn) error {
	frames, err := f.src.Claim(ctx, f.agentID, f.sessionKey, f.current, f.held)
	if err != nil {
		return err
	}
	for _, fr := range frames {
		// In flight before it is written: its acknowledgement can arrive as soon as it is.
		f.mu.Lock()
		f.inflight[fr.RunID] = struct{}{}
		f.mu.Unlock()
		if err := c.write(ctx, fr); err != nil {
			return err
		}
	}
	return nil
}
