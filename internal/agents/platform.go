package agents

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store/db"
)

// PlatformWindow is the most platform frames in flight on one connection: sent and not yet
// acknowledged. Each acknowledgement releases the next one (CID-08), so a queue of thousands
// never overflows the channel's frame queue.
const PlatformWindow = 50

// DroppedNotice is the text of the notice that tells an AI how many of its platform messages the
// server dropped, for being older than 7 days or over the bound of 5,000 (CID-10).
func DroppedNotice(n int64) string {
	return fmt.Sprintf("%d platform messages were dropped (older than 7 days or over 5,000 queued)", n)
}

// hasFeature reports whether the client named the feature in the comma separated list of
// dto.HeaderFeatures, spaces around the items aside.
func hasFeature(h http.Header, feature string) bool {
	for _, v := range h.Values(dto.HeaderFeatures) {
		for _, f := range strings.Split(v, ",") {
			if strings.TrimSpace(f) == feature {
				return true
			}
		}
	}
	return false
}

// platformFeed sends the platform items of an AI to one connection (CID-06): oldest first, at most
// PlatformWindow in flight. The queue in the database is the truth, and only the ids in flight are
// kept here. Every wake-up (an item queued, a lagged bus, an acknowledgement) reads the queue
// again, so the items an operator change moved to the AI, which are older than what the session
// holds, are found like new ones (CID-05), and a session that took the AI over finds every
// unacknowledged item (CID-25).
type platformFeed struct {
	queue   *platform.Queue
	agentID string

	mu       sync.Mutex
	inflight map[string]struct{}
	wake     chan struct{}
}

func newPlatformFeed(q *platform.Queue, agentID string) *platformFeed {
	return &platformFeed{queue: q, agentID: agentID, inflight: map[string]struct{}{}, wake: make(chan struct{}, 1)}
}

// poke asks the connection's loop to read the queue; pokes coalesce.
func (f *platformFeed) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// release frees the slot of an item the session acknowledged, whatever became of the item
// meanwhile (the server may have expired, dropped or moved it), and asks for the next one.
func (f *platformFeed) release(itemID string) {
	f.mu.Lock()
	_, held := f.inflight[itemID]
	delete(f.inflight, itemID)
	f.mu.Unlock()
	if held {
		f.poke()
	}
}

// fill sends the AI's unacknowledged items that are not in flight, oldest first, while the window
// has room. It runs on the connection's loop only.
func (f *platformFeed) fill(ctx context.Context, c *conn) error {
	f.mu.Lock()
	room := PlatformWindow - len(f.inflight)
	held := make(map[string]struct{}, len(f.inflight))
	for id := range f.inflight {
		held[id] = struct{}{}
	}
	f.mu.Unlock()
	if room <= 0 {
		return nil
	}
	// Whatever is in flight is among the oldest unacknowledged items or gone from the queue, so
	// this many rows hold at least `room` that are not.
	items, err := f.queue.Unacked(ctx, f.agentID, 0, room+len(held))
	if err != nil {
		return err
	}
	for _, e := range items {
		if _, ok := held[e.ID]; ok {
			continue
		}
		if room == 0 {
			break
		}
		// In flight before it is written: its acknowledgement can arrive as soon as it is.
		f.mu.Lock()
		f.inflight[e.ID] = struct{}{}
		f.mu.Unlock()
		if err := c.write(ctx, dto.AgentPlatformFrame{Type: dto.AgentFrameTypePlatform, Event: convert.PlatformEvent(e)}); err != nil {
			return err
		}
		room--
	}
	return nil
}

// platformStart is what a connection that opted in to platform events gets after the room
// backlog: the notice of what was dropped while no session could be told (CID-10), then the AI's
// queued items.
func (g *Gateway) platformStart(ctx context.Context, c *conn, agentID string) error {
	n, err := g.d.Platform.TakeDropped(ctx, agentID)
	if err != nil {
		return err
	}
	if n > 0 {
		if err := c.write(ctx, dto.AgentNoticeFrame{Type: dto.AgentFrameTypeNotice, Text: DroppedNotice(n)}); err != nil {
			return err
		}
	}
	return c.feed.fill(ctx, c)
}

// connections lists the connections the AI operates for the welcome frame (CID-01).
func (g *Gateway) connections(ctx context.Context, agentID string) ([]dto.AgentConnection, error) {
	rows, err := g.d.Store.R().ListConnectorsOperatedBy(ctx, sql.NullString{String: agentID, Valid: true})
	if err != nil {
		return nil, err
	}
	return connectionRefs(rows), nil
}

func connectionRefs(rows []db.Connector) []dto.AgentConnection {
	out := make([]dto.AgentConnection, len(rows))
	for i, r := range rows {
		out[i] = dto.AgentConnection{ID: r.ID, Platform: r.Kind, AccountLabel: r.AccountLabel}
	}
	return out
}
