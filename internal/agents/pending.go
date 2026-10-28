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

// pendingMessage is one message selected for a Pending answer, with the trust already decided for the
// receiving AI and the label of a fallback row.
type pendingMessage struct {
	view     db.MessageView
	trusted  bool
	fallback bool
	label    string
}

// Pending returns the oldest messages after the agent's cursors that its delivery modes want,
// room by room (paused rooms excepted), at most limit, and moves the cursors past everything
// it looked at: the returned messages count as delivered. It serves harnesses that cannot hold
// the WebSocket (GET /agent/pending; no supported harness uses it, AD-034). Fallen-back messages are merged into the
// same sequence order with their note, are returned without the delivery filter, and their rows
// are acknowledged by this read: they stay as tombstones (SESS-22, SESS-24). The cursor follows the
// cursor messages only: a fallback's sequence never moves it, and it never moves backwards.
//
// label is the session the request came from. A process whose folder resolves to a non-empty
// label has no live session: nothing is addressed to it and its label's messages are the
// principal's, so it reads no queue at all - no message, no cursor, no delivery row (SESS-06).
func (g *Gateway) Pending(ctx context.Context, a db.Agent, label string, limit int) ([]dto.AgentMessage, error) {
	if label != "" {
		return []dto.AgentMessage{}, nil
	}
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()
	out := []dto.AgentMessage{}
	states, err := g.d.Registry.Rooms(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	profile, err := delivery.IntelligenceOf(ctx, g.d.Store.R(), a.ID)
	if err != nil {
		return nil, err
	}
	trust := g.d.Trust.Open() // one for the call: the AI's home chart is read once (POS-10)
	for _, st := range states {
		remaining := limit - len(out)
		if remaining <= 0 {
			break
		}
		if st.AgentsPaused {
			continue // DLV-08
		}
		fallbacks, err := loadSessionFallbacks(ctx, g.d.Store.R(), a.ID, st.ID)
		if err != nil {
			return nil, err
		}
		prefs := delivery.Prefs(profile, st.DisplayName, st.Delivery, st.KeywordsJson)
		var cursor []db.MessageView
		after, exhausted := st.Cursor, false
		fetch := func(n int) error {
			if exhausted {
				return nil
			}
			batch, err := g.d.Store.R().ListAgentPending(ctx, db.ListAgentPendingParams{
				RoomID: st.ID, AfterSeq: after, AgentID: a.ID, Limit: int64(n),
			})
			if err != nil {
				return err
			}
			if len(batch) > 0 {
				after = batch[len(batch)-1].Seq
			}
			if len(batch) < n {
				exhausted = true
			}
			cursor = append(cursor, batch...)
			return nil
		}
		if err := fetch(remaining); err != nil {
			return nil, err
		}
		var selected []pendingMessage
		advanceTo := int64(0)
		ci, fi := 0, 0
		for len(selected) < remaining {
			// Read more cursor messages only while a fallback still needs the stream caught up to it;
			// without a fallback one batch is all this call looks at, as before.
			if ci >= len(cursor) && !exhausted && fi < len(fallbacks) {
				if err := fetch(remaining); err != nil {
					return nil, err
				}
			}
			if ci >= len(cursor) && fi >= len(fallbacks) {
				break // no message left in either stream
			}
			fallbackNext := fi < len(fallbacks) && (ci >= len(cursor) || fallbacks[fi].view.Seq < cursor[ci].Seq)
			if fallbackNext {
				// The cursor stream is caught up past this fallback, so advancing over it later
				// cannot skip an unseen cursor message.
				f := fallbacks[fi]
				fi++
				trusted, err := trustedFor(ctx, trust, f.view, a.ID)
				if err != nil {
					return nil, err
				}
				selected = append(selected, pendingMessage{view: f.view, trusted: trusted, fallback: true, label: f.label})
				continue // the cursor does not follow a fallback: its sequence is not the stream's
			}
			v := cursor[ci]
			ci++
			if v.Seq > advanceTo {
				advanceTo = v.Seq
			}
			facts, err := delivery.LoadFacts(ctx, g.d.Store.R(), v, st.Kind)
			if err != nil {
				return nil, err
			}
			wanted, trusted, err := facts.WantedBy(ctx, trust, a.ID, prefs)
			if err != nil {
				return nil, err
			}
			if !wanted {
				continue // MEM-03, MEM-04
			}
			selected = append(selected, pendingMessage{view: v, trusted: trusted})
		}
		if len(selected) == 0 {
			if advanceTo > st.Cursor {
				if err := g.advance(ctx, a.ID, st.ID, advanceTo); err != nil {
					return nil, err
				}
			}
			continue
		}
		views := make([]db.MessageView, len(selected))
		for i, s := range selected {
			views[i] = s.view
		}
		msgs, err := g.d.Rooms.Details(ctx, views)
		if err != nil {
			return nil, err
		}
		ref := delivery.RoomRef(st.ID, st.Name, st.Kind)
		for i, s := range selected {
			if s.fallback {
				if err := g.d.Store.WithTx(ctx, func(q *db.Queries) error { _, err := ackFallbackDelivery(ctx, q, s.view.ID, a.ID); return err }); err != nil {
					return nil, err
				}
			}
			out = append(out, dto.AgentMessage{Room: ref, Trusted: s.trusted, ForLabel: s.label, Message: convert.Message(msgs[i])})
		}
		if advanceTo > st.Cursor {
			if err := g.advance(ctx, a.ID, st.ID, advanceTo); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
