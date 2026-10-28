package agents

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/store/db"
)

// sessionSweepTimeout bounds the end-of-session sweep, which runs after the socket is gone and must
// not depend on the request context that closed it.
const sessionSweepTimeout = 10 * time.Second

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

// createSessionDelivery records the ownership of a message pushed towards a label. fellBackAt is
// invalid while the label's session is live, or the dispatch time when the label has no session.
func (g *Gateway) createSessionDelivery(ctx context.Context, messageID, agentID, label string, createdAt int64, fellBackAt sql.NullInt64) error {
	return g.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateSessionDelivery(ctx, db.CreateSessionDeliveryParams{
			MessageID: messageID, AgentID: agentID, Label: label, OwnerLive: "", CreatedAt: createdAt, FellBackAt: fellBackAt,
		})
	})
}

// ackSessionDelivery deletes a live label's pending row. A row that already fell back is left to the
// principal, so a late acknowledgement cannot consume it twice (SESS-24).
func (g *Gateway) ackSessionDelivery(ctx context.Context, messageID, agentID string) error {
	return g.d.Store.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.ReleaseSessionDelivery(ctx, db.ReleaseSessionDeliveryParams{MessageID: messageID, AgentID: agentID})
		return err
	})
}

// deleteSessionDelivery consumes a row that belongs to the principal or a tools-only process.
func (g *Gateway) deleteSessionDelivery(ctx context.Context, messageID, agentID string) error {
	return g.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.DeleteSessionDelivery(ctx, db.DeleteSessionDeliveryParams{MessageID: messageID, AgentID: agentID})
	})
}

// deleteSelfSessionDeliveries drops the pending rows of a label whose message the AI itself wrote:
// they are never handed to the principal (SESS-38).
func (g *Gateway) deleteSelfSessionDeliveries(ctx context.Context, agentID, label string) (int64, error) {
	var n int64
	err := g.d.Store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		n, err = q.DeleteSelfSessionDeliveries(ctx, db.DeleteSelfSessionDeliveriesParams{AgentID: agentID, Label: label})
		return err
	})
	return n, err
}

// fallBackSessionDeliveries marks the pending rows of a label as the principal's and returns their
// message ids, so the caller can push each with the note (SESS-24).
func (g *Gateway) fallBackSessionDeliveries(ctx context.Context, agentID, label string, at int64) ([]string, error) {
	var ids []string
	err := g.d.Store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		ids, err = q.FallBackSessionDeliveries(ctx, db.FallBackSessionDeliveriesParams{
			FellBackAt: sql.NullInt64{Int64: at, Valid: true}, AgentID: agentID, Label: label,
		})
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

// sessionEnded is the end-of-session sweep: when a labelled connection was the current session of
// its label, the rows it did not acknowledge become the principal's, and each is pushed to a live
// principal with the note (SESS-24). When the principal is offline the rows stay in its fallback list.
// The rows of messages the AI itself wrote are dropped instead: the AI never receives its own text
// (SESS-38).
func (g *Gateway) sessionEnded(ctx context.Context, agentID, label string) {
	if label == "" {
		return
	}
	if _, err := g.deleteSelfSessionDeliveries(ctx, agentID, label); err != nil {
		g.d.Logger.Error("agent: self session delivery sweep failed", "agent_id", agentID, "label", label, "err", err)
	}
	ids, err := g.fallBackSessionDeliveries(ctx, agentID, label, time.Now().UnixMilli())
	if err != nil {
		g.d.Logger.Error("agent: session fallback failed", "agent_id", agentID, "label", label, "err", err)
		return
	}
	if len(ids) == 0 {
		return
	}
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
		g.pushToPrincipal(agentID, dto.AgentMessage{Room: ref, Trusted: trusted, ForLabel: label, Message: msg})
	}
}
