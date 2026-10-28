package agents

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"hash/fnv"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/store/db"
)

// sessionSweepTimeout bounds the end-of-connection sweep, which runs after the socket is gone and must
// not depend on the request context that closed it.
const sessionSweepTimeout = 10 * time.Second

// lifeStripes is the number of locks that serialize, per AI, a connection attaching against another
// one ending (see Gateway.lifeLock).
const lifeStripes = 16

// ownerKey is the owner a connection's delivery rows carry: the live id of its process, so a process
// reconnecting owns what its old socket was given (SESS-14), or a key of its own for a client that sent
// none, which no other connection can hold. It is never empty: the empty owner is the one of a row born
// fallen back.
func ownerKey(liveID string) string {
	if liveID != "" {
		return liveID
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "anon:" + hex.EncodeToString(b[:])
}

// lifeLock returns the lock that orders, for one AI, the attach of a connection against the end of
// another: the sweep of a connection that ended and the connection that takes its place never
// interleave, so an old sweep cannot take a row a newer connection was just given.
func (g *Gateway) lifeLock(agentID string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(agentID))
	return &g.life[h.Sum32()%lifeStripes]
}

// sessionFallback is one message of the principal's fallback queue: the stored message and the label
// whose session ended (or never started) before acknowledging it.
type sessionFallback struct {
	view  db.MessageView
	label string
}

// roomDelivery is one item of a merged per-room delivery list: a message read from the principal's
// cursor, or a fallen-back message read from its session delivery row.
type roomDelivery struct {
	view     db.MessageView
	fallback bool
	label    string
}

// mergeRoomDeliveries puts the cursor messages and the fallback rows of one room in sequence order.
func mergeRoomDeliveries(views []db.MessageView, fallbacks []sessionFallback) []roomDelivery {
	out := make([]roomDelivery, 0, len(views)+len(fallbacks))
	for _, v := range views {
		out = append(out, roomDelivery{view: v})
	}
	for _, f := range fallbacks {
		out = append(out, roomDelivery{view: f.view, fallback: true, label: f.label})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].view.Seq < out[j].view.Seq })
	return out
}

// loadSessionFallbacks reads the fallback rows of one AI in one room, with the label each one was
// addressed to.
func loadSessionFallbacks(ctx context.Context, q *db.Queries, agentID, roomID string) ([]sessionFallback, error) {
	views, err := q.ListSessionFallbacks(ctx, db.ListSessionFallbacksParams{AgentID: agentID, RoomID: roomID})
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return nil, nil
	}
	labels, err := q.ListSessionFallbackLabels(ctx, db.ListSessionFallbackLabelsParams{AgentID: agentID, RoomID: roomID})
	if err != nil {
		return nil, err
	}
	byMessage := make(map[string]string, len(labels))
	for _, row := range labels {
		byMessage[row.MessageID] = row.Label
	}
	out := make([]sessionFallback, 0, len(views))
	for _, v := range views {
		out = append(out, sessionFallback{view: v, label: byMessage[v.ID]})
	}
	return out, nil
}

// trustedFor recomputes a fallback row's trust for the principal: a fallback is not filtered by the
// delivery mode again, but its trusted flag is still the author's for this AI (POS-10).
func trustedFor(ctx context.Context, trust delivery.Trust, view db.MessageView, agentID string) (bool, error) {
	author := delivery.AuthorOf(view)
	if author == "" {
		return false, nil
	}
	return trust.Trusts(ctx, author, agentID)
}

// createSessionDelivery records the ownership of a message pushed towards a label. owner is the owner
// key of the connection it was pushed to; for a row born fallen back it is "" and fellBackAt is the
// dispatch time. A retried push keeps the first row (ON CONFLICT DO NOTHING).
func (g *Gateway) createSessionDelivery(ctx context.Context, messageID, agentID, label, owner string, createdAt int64, fellBackAt sql.NullInt64) error {
	return g.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateSessionDelivery(ctx, db.CreateSessionDeliveryParams{
			MessageID: messageID, AgentID: agentID, Label: label, OwnerLive: owner, CreatedAt: createdAt, FellBackAt: fellBackAt,
		})
	})
}

// ackSessionDelivery is the acknowledgement of the connection that owns a pending row: the row stays as
// a tombstone. A row owned by another connection, or one that already fell back, is left alone, so a late
// acknowledgement cannot consume it twice (SESS-24).
func (g *Gateway) ackSessionDelivery(ctx context.Context, messageID, agentID, owner string) error {
	return g.d.Store.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.AckSessionDelivery(ctx, db.AckSessionDeliveryParams{
			AckedAt: nullNow(), MessageID: messageID, AgentID: agentID, OwnerLive: owner,
		})
		return err
	})
}

// ackFallbackDelivery is the principal's consumption of a fallen-back row, which stays as a tombstone.
// It reports whether a row was consumed: the message was a fallback.
func ackFallbackDelivery(ctx context.Context, q *db.Queries, messageID, agentID string) (bool, error) {
	n, err := q.AckFallbackDelivery(ctx, db.AckFallbackDeliveryParams{AckedAt: nullNow(), MessageID: messageID, AgentID: agentID})
	return n > 0, err
}

func nullNow() sql.NullInt64 { return sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true} }

// fallBackOwned ends the ownership of one connection: its pending rows become the principal's and their
// message ids come back. The rows of messages the AI wrote itself are acknowledged instead and are not
// returned (SESS-38). It is idempotent: a second call finds nothing.
func (g *Gateway) fallBackOwned(ctx context.Context, agentID, owner string) ([]string, error) {
	if owner == "" {
		return nil, nil // the owner of a row born fallen back is nobody's
	}
	var ids []string
	err := g.d.Store.WithTx(ctx, func(q *db.Queries) error {
		rows, err := q.FallBackOwnedDeliveries(ctx, db.FallBackOwnedDeliveriesParams{
			AgentID: agentID, OwnerLive: owner, Now: nullNow(),
		})
		for _, r := range rows {
			if r.FellBackAt.Valid {
				ids = append(ids, r.MessageID)
			}
		}
		return err
	})
	return ids, err
}

// pushToPrincipal queues a message for the AI's principal session without blocking.
func (g *Gateway) pushToPrincipal(agentID string, m dto.AgentMessage) bool {
	s, ok := g.d.Presence.Session(agentID)
	if !ok {
		return false
	}
	c, ok := s.(*conn)
	return ok && c.push(m)
}

// ownerConn returns the live connection that owns the row of owner addressed to label, if any: the
// connection must hold that label now. A process that was labelled and reconnected as the principal has
// the same live id, but it is not the session the row was pushed to, and it never acknowledges the row.
func (g *Gateway) ownerConn(agentID, owner, label string) *conn {
	if owner == "" || label == "" {
		return nil
	}
	s, ok := g.d.Presence.Find(agentID, func(s Session) bool {
		c, ok := s.(*conn)
		return ok && c.owner == owner && c.labelOf() == label
	})
	if !ok {
		return nil
	}
	return s.(*conn)
}

// labelConn returns the connection that holds a label now, if it is one the gateway serves.
func (g *Gateway) labelConn(agentID, label string) *conn {
	s, ok := g.d.Presence.SessionByLabel(agentID, label)
	if !ok {
		return nil
	}
	c, _ := s.(*conn)
	return c
}

// deliverOwned routes a message that has a delivery row by what the row says now, which is the first
// row written for it (a retried push does not change it): an acknowledged message is never delivered
// again, a fallen-back one goes to the principal with the note, and a pending one goes to the connection
// that owns it. When that connection ended meanwhile, with its sweep before the row existed, the sweep
// runs now and the principal receives the row (idempotent: an earlier sweep leaves nothing to take). It
// reports whether a connection received the message live.
func (g *Gateway) deliverOwned(agentID string, m dto.AgentMessage) bool {
	row, err := g.d.Store.R().GetSessionDelivery(g.ctx, db.GetSessionDeliveryParams{MessageID: m.Message.ID, AgentID: agentID})
	if err != nil {
		g.d.Logger.Error("agent: session delivery read failed", "agent_id", agentID, "message_id", m.Message.ID, "err", err)
		return false
	}
	switch {
	case row.AckedAt.Valid:
		return false
	case row.FellBackAt.Valid:
		m.ForLabel = row.Label
		return g.pushToPrincipal(agentID, m)
	}
	if c := g.ownerConn(agentID, row.OwnerLive, row.Label); c != nil {
		return c.push(m)
	}
	if g.afterOwnerMiss != nil {
		g.afterOwnerMiss()
	}
	return slices.Contains(g.releaseOwner(g.ctx, agentID, row.OwnerLive, row.Label), m.Message.ID)
}

// releaseOwner sweeps the rows of a connection that is no longer attached and pushes them to the
// principal with the note. It returns the messages the principal received live. The caller found no owner
// connection without the life lock, so a connection of the same process may have attached since: the
// check runs again under the lock, and the sweep only when the owner is still absent.
func (g *Gateway) releaseOwner(ctx context.Context, agentID, owner, label string) []string {
	mu := g.lifeLock(agentID)
	mu.Lock()
	if g.ownerConn(agentID, owner, label) != nil {
		mu.Unlock()
		return nil // it attached meanwhile and receives the row itself
	}
	ids, err := g.fallBackOwned(ctx, agentID, owner)
	mu.Unlock()
	if err != nil {
		g.d.Logger.Error("agent: session fallback failed", "agent_id", agentID, "err", err)
		return nil
	}
	return g.pushFallbacks(ctx, agentID, ids)
}

// connectionEnded is the end of one connection, whatever ended it: it leaves the presence (a no-op when a
// takeover, a reconnect or Kick already removed it) and the rows it owned fall back to the principal with
// the note (SESS-24). A connection replaced by the same process (same live id) owns nothing to give back:
// its socket was replaced, not its session, and the new one receives the rows again from its backlog
// (SESS-14). The whole step runs under the AI's life lock, so a connection that takes the label next
// cannot be given a row that this sweep then takes.
func (g *Gateway) connectionEnded(ctx context.Context, agentID string, c *conn) {
	mu := g.lifeLock(agentID)
	mu.Lock()
	g.d.Presence.Detach(ctx, agentID, c)
	ids, err := g.sweepEnded(ctx, agentID, c)
	mu.Unlock()
	g.finishSweep(ctx, agentID, ids, err)
}

// connectionKicked is Presence.OnEnded: Kick removed the connection from the presence before closing it.
func (g *Gateway) connectionKicked(ctx context.Context, agentID string, s Session) {
	c, ok := s.(*conn)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionSweepTimeout)
	defer cancel()
	mu := g.lifeLock(agentID)
	mu.Lock()
	ids, err := g.sweepEnded(ctx, agentID, c)
	mu.Unlock()
	g.finishSweep(ctx, agentID, ids, err)
}

// sweepEnded falls back what the ended connection c owned, unless its process holds another connection
// (a reconnect). The caller holds the AI's life lock. The principal owns no rows.
func (g *Gateway) sweepEnded(ctx context.Context, agentID string, c *conn) ([]string, error) {
	if c.labelOf() == "" || g.d.Presence.HoldsLive(agentID, c.liveID) {
		return nil, nil
	}
	return g.fallBackOwned(ctx, agentID, c.owner)
}

func (g *Gateway) finishSweep(ctx context.Context, agentID string, ids []string, err error) {
	if err != nil {
		g.d.Logger.Error("agent: session fallback failed", "agent_id", agentID, "err", err)
		return
	}
	g.pushFallbacks(ctx, agentID, ids)
}

// pushFallbacks pushes each fallen-back message to a live principal with the note. When the principal is
// offline the rows stay in its fallback list. It returns the messages the principal received live.
func (g *Gateway) pushFallbacks(ctx context.Context, agentID string, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	var pushed []string
	trust := g.d.Trust.Open()
	rooms := map[string]dto.RoomRef{}
	for _, id := range ids {
		view, err := g.d.Store.R().GetMessageView(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue // deleted meanwhile
		}
		if err != nil {
			g.d.Logger.Error("agent: fallback message read failed", "agent_id", agentID, "message_id", id, "err", err)
			continue
		}
		row, err := g.d.Store.R().GetSessionDelivery(ctx, db.GetSessionDeliveryParams{MessageID: id, AgentID: agentID})
		if err != nil {
			g.d.Logger.Error("agent: fallback row read failed", "agent_id", agentID, "message_id", id, "err", err)
			continue
		}
		ref, ok := rooms[view.RoomID]
		if !ok {
			room, err := g.d.Store.R().GetRoom(ctx, view.RoomID)
			if err != nil {
				g.d.Logger.Error("agent: fallback room read failed", "agent_id", agentID, "room_id", view.RoomID, "err", err)
				continue
			}
			ref = delivery.RoomRef(room.ID, room.Name, room.Kind)
			rooms[view.RoomID] = ref
		}
		trusted, err := trustedFor(ctx, trust, view, agentID)
		if err != nil {
			g.d.Logger.Error("agent: fallback trust read failed", "agent_id", agentID, "message_id", id, "err", err)
			continue
		}
		msg, err := g.Message(ctx, id)
		if err != nil {
			g.d.Logger.Error("agent: fallback message load failed", "agent_id", agentID, "message_id", id, "err", err)
			continue
		}
		if g.pushToPrincipal(agentID, dto.AgentMessage{Room: ref, Trusted: trusted, ForLabel: row.Label, Message: msg}) {
			pushed = append(pushed, id)
		}
	}
	return pushed
}
