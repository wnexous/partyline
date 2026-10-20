package agents

import (
	"context"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/store/db"
)

// PendingLimit is the most messages one Pending call returns (TURN-01, TURN-02).
const PendingLimit = 20

// Pending returns the oldest messages after the agent's cursors that its delivery modes want,
// room by room (paused rooms excepted), at most limit, and moves the cursors past everything
// it looked at: the returned messages count as delivered. It serves harnesses that cannot hold
// the WebSocket (wait_for_messages, turn-end hooks).
func (g *Gateway) Pending(ctx context.Context, a db.Agent, limit int) ([]dto.AgentMessage, error) {
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()
	out := []dto.AgentMessage{}
	states, err := g.d.Registry.Rooms(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	for _, st := range states {
		if len(out) >= limit {
			break
		}
		if st.AgentsPaused {
			continue // DLV-08
		}
		views, err := g.d.Store.R().ListAgentPending(ctx, db.ListAgentPendingParams{
			RoomID: st.ID, AfterSeq: st.Cursor, AgentID: a.ID, Limit: int64(limit - len(out)),
		})
		if err != nil {
			return nil, err
		}
		if len(views) == 0 {
			continue
		}
		msgs, err := g.d.Rooms.Details(ctx, views)
		if err != nil {
			return nil, err
		}
		ref := dto.RoomRef{ID: st.ID, Name: st.Name}
		prefs := delivery.Prefs(a.Name, st.DisplayName, st.Delivery, st.KeywordsJson)
		for _, m := range msgs {
			facts, err := delivery.LoadFacts(ctx, g.d.Store.R(), m.MessageView)
			if err != nil {
				return nil, err
			}
			if !facts.WantedBy(a.ID, a.OwnerID, prefs) {
				continue // MEM-03, MEM-04
			}
			out = append(out, dto.AgentMessage{Room: ref, Trusted: delivery.Trusted(m.AuthorUserID, a.OwnerID), Message: convert.Message(m)})
		}
		if err := g.advance(ctx, a.ID, st.ID, views[len(views)-1].Seq); err != nil {
			return nil, err
		}
	}
	return out, nil
}
