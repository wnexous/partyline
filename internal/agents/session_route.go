package agents

import (
	"database/sql"
	"errors"
	"time"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/store/db"
)

// session_route.go decides which sessions of one AI a room message is pushed to and what context goes with
// it (SN-03, SN-04, SN-06, SN-07): the principal when it follows the room or the text names it, every live
// parallel session that follows the room or whose label the text addresses, and the principal again when
// the text addresses a sibling (SN-04). Every target is given the messages of the room it had not read yet
// as context, and then the message itself. Non-targets receive nothing live and only count the message as
// unread.

// pushSession is one session a room message is pushed to, with the state its catch-up batch needs (SN-03).
type pushSession struct {
	// label is the session's label ("" for the principal).
	label string
	// conn is the live connection the frames are written to.
	conn *conn
	// readSeq is where it has read up to in this room (SN-01).
	readSeq int64
	// addressed says the message's text addresses this session's label (SESS-21): it is that session's
	// message, not a copy.
	addressed bool
	// copyOfSibling says the session receives the message because it follows the room and the text
	// addresses a sibling of it (SN-04): it is a copy, and a later fallback of the same message adds
	// nothing to it.
	copyOfSibling bool
}

// pushPlan is what one non-self-authored room message is delivered as.
type pushPlan struct {
	// sessions is who receives the message, the principal first; empty when nobody is notified.
	sessions []pushSession
	// note is the label the text addresses that has no live session: the message is the principal's from
	// the start, with the note, also while the principal is offline (SESS-22, SESS-24).
	note string
}

// planPush works out the sessions a message notifies (SN-03, SN-04). target is the label the text
// addresses, or "" for the principal (SESS-21). The delivery mode and the loop guard already decided that
// the AI wants the message; this decides which of its sessions hears about it:
//   - the principal, when its setting for the room is on, or a plain message names it by its bare @handle,
//     or the message is a DM (it is addressed to the AI), and also when the text addresses a sibling of
//     it while its setting is on (SN-04);
//   - every live parallel session whose setting for the room is on, or whose label the text addresses.
//
// A message for a label with no live session is the principal's with the note (SESS-22) and notifies
// nobody else. A message the AI wrote itself never comes here: it follows SESS-38/SESS-49 instead.
func (g *Gateway) planPush(agentID, target string, m dto.AgentMessage) (pushPlan, error) {
	var plan pushPlan
	a, err := g.d.Store.R().GetAgent(g.ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return plan, nil
	}
	if err != nil {
		return plan, err
	}
	states, err := g.d.Store.R().ListSessionRoomStatesInRoom(g.ctx, db.ListSessionRoomStatesInRoomParams{
		AgentID: agentID, RoomID: m.Room.ID,
	})
	if err != nil {
		return plan, err
	}
	byKey := make(map[string]db.SessionRoomState, len(states))
	for _, st := range states {
		byKey[st.SessionKey] = st
	}
	lives := g.d.Presence.Sessions(agentID)
	live := make(map[string]bool, len(lives))
	for _, l := range lives {
		live[l.Label] = true
	}
	if target != "" && !live[target] {
		plan.note = target
	}
	type targetState struct {
		live    Live
		conn    *conn
		notify  string
		readSeq int64
	}
	items := make([]targetState, 0, len(lives))
	join := int64(-1)
	for _, l := range lives {
		key, ok := g.d.Presence.SessionKeyOf(agentID, l.Label, a.PrincipalSession)
		if !ok {
			continue
		}
		st, has := byKey[key]
		notify, readSeq := DefaultNotify(l.Label == ""), int64(0)
		if has {
			notify, readSeq = st.Notify, st.ReadSeq
		} else {
			// A session whose state row is missing joined the room after it attached (a DM created for the
			// AI, a group it was added to): it starts caught up at the room's join point, not at its history
			// (SN-01).
			if join < 0 {
				if join, err = g.d.Store.R().GetSessionJoinSeq(g.ctx, db.GetSessionJoinSeqParams{
					RoomID: m.Room.ID, AgentID: agentID,
				}); err != nil {
					return plan, err
				}
			}
			readSeq = join
		}
		items = append(items, targetState{
			live: l, conn: g.labelConn(agentID, l.Label), notify: notify, readSeq: readSeq,
		})
	}
	// The principal follows the room, unless it is set to mentions only; then a plain @handle, or a DM
	// addressed to the AI, still reaches it (SN-03).
	principalNotify, principalLive, principalGets := "", false, false
	for _, it := range items {
		if it.live.Label == "" {
			principalNotify, principalLive = it.notify, true
		}
	}
	mentions := false
	switch {
	case target != "":
	case m.Room.Kind == dto.ConversationKindDM:
		mentions = true // a DM is addressed to the AI
	case principalLive && principalNotify != NotifyOn:
		profile, err := delivery.IntelligenceOf(g.ctx, g.d.Store.R(), agentID)
		if err != nil {
			return plan, err
		}
		mentions = delivery.MentionsPrincipal(m.Message.Text, profile.Handle)
	}
	addressedLive := target != "" && live[target]
	for _, it := range items {
		if it.live.Label == "" {
			principalGets = plan.note != "" || it.notify == NotifyOn || mentions
		}
	}
	for _, it := range items {
		var wanted bool
		switch {
		case it.live.Label == "":
			wanted = principalGets
		case target != "" && target == it.live.Label:
			wanted = true
		default:
			wanted = it.notify == NotifyOn
		}
		if !wanted || it.conn == nil {
			continue
		}
		addressed := target != "" && target == it.live.Label
		plan.sessions = append(plan.sessions, pushSession{
			label: it.live.Label, conn: it.conn, readSeq: it.readSeq,
			addressed: addressed, copyOfSibling: !addressed && addressedLive,
		})
	}
	return plan, nil
}

// pushToPrincipalCopy queues a message the principal receives as a copy of another session's notification
// (SN-04): the fallback of the same message, if its addressed session never acknowledges it, adds nothing
// to a connection that already has it.
func (g *Gateway) pushToPrincipalCopy(agentID string, m dto.AgentMessage) bool {
	s, ok := g.d.Presence.Session(agentID)
	if !ok {
		return false
	}
	c, ok := s.(*conn)
	return ok && c.enqueue(outbound{typ: dto.AgentFrameTypeMessage, msg: m, copy: true})
}

// pushToSession pushes to one target the unread context of the room and then the message itself. It
// reports whether the message reached the connection live. Every pushed message gets a delivery row for
// the session, so its acknowledgement moves the session's read position (SN-03) and an edit or a delete
// follows it (SN-09).
func (g *Gateway) pushToSession(agentID string, t pushSession, m dto.AgentMessage) bool {
	if err := g.pushCatchUp(agentID, t, m); err != nil {
		g.d.Logger.Error("agent: catch-up failed", "agent_id", agentID, "room_id", m.Room.ID, "err", err)
	}
	if t.label == "" {
		// The principal has no label: its queue is its cursor, and its acknowledgement moves the read
		// position through it. The row that routes an edit back to it is its copy row (SN-09).
		if err := g.createSessionDelivery(g.ctx, m.Message.ID, agentID, "", t.conn.owner, time.Now().UnixMilli(), sql.NullInt64{}, true); err != nil {
			g.d.Logger.Error("agent: principal copy row write failed", "agent_id", agentID, "message_id", m.Message.ID, "err", err)
		}
		if t.copyOfSibling {
			return g.pushToPrincipalCopy(agentID, m)
		}
		return g.pushToPrincipal(agentID, m)
	}
	already, err := g.d.Store.R().HasSessionDelivery(g.ctx, db.HasSessionDeliveryParams{
		MessageID: m.Message.ID, AgentID: agentID, Label: t.label,
	})
	if err != nil {
		g.d.Logger.Error("agent: session delivery read failed", "agent_id", agentID, "message_id", m.Message.ID, "err", err)
		return false
	}
	if already {
		return false // it already reached that session: SN-03 pushes a message once
	}
	if err := g.createSessionDelivery(g.ctx, m.Message.ID, agentID, t.label, t.conn.owner, time.Now().UnixMilli(), sql.NullInt64{}, !t.addressed); err != nil {
		g.d.Logger.Error("agent: session delivery write failed", "agent_id", agentID, "message_id", m.Message.ID, "err", err)
		return false
	}
	if !t.addressed {
		// A copy never falls back to the principal (SN-03): the message is not that session's to own.
		return t.conn.push(m)
	}
	// The addressed session owns the message from the start; if its connection ended meanwhile, the row
	// falls back to the principal with the note (SESS-24).
	return g.deliverOwned(agentID, t.label, m)
}

// pushCatchUp writes the messages of the room the session has not read yet, oldest first and at most
// BacklogLimit of them, marked as context (SN-03). Messages the session wrote itself are not part of it
// (SN-06), and neither is one it acknowledged. When the room had more unread messages than the batch
// holds, the first frame carries how many earlier ones were left out. Each frame that reaches the queue
// gets a copy delivery row for the session, so an edit follows it (SN-09); the principal, whose rows carry
// the empty label, is no exception. A batch that was not acknowledged comes back with the next one.
func (g *Gateway) pushCatchUp(agentID string, t pushSession, m dto.AgentMessage) error {
	q := g.d.Store.R()
	params := db.CountSessionUnreadInRoomParams{
		RoomID: m.Room.ID, AfterSeq: t.readSeq, BeforeSeq: m.Message.Seq,
		AgentID: agentID, AuthorLabel: t.label, Label: t.label,
	}
	count, err := q.CountSessionUnreadInRoom(g.ctx, params)
	if err != nil || count == 0 {
		return err
	}
	views, err := q.ListSessionUnread(g.ctx, db.ListSessionUnreadParams{
		RoomID: params.RoomID, AfterSeq: params.AfterSeq, BeforeSeq: params.BeforeSeq,
		AgentID: agentID, AuthorLabel: t.label, Label: t.label, Limit: BacklogLimit,
	})
	if err != nil || len(views) == 0 {
		return err
	}
	msgs, err := g.d.Rooms.Details(g.ctx, views)
	if err != nil {
		return err
	}
	skipped := count - int64(len(views))
	trust := g.d.Trust.Open()
	// The row of a frame is written only once the frame is in the queue: a connection that dropped before
	// taking the frame leaves no record that the message was given, so the next catch-up has it again
	// (SN-03). An unacknowledged message is replayed whether or not its row exists, so the rows only route
	// an edit or a delete and keep the copy's ownership.
	rows := make([]db.CreateSessionDeliveryParams, 0, len(msgs))
	for i, msg := range msgs {
		trusted := true // the AI wrote it: its sessions trust it, as the live push says (SESS-37)
		if !selfAuthored(convert.Message(msg), agentID) {
			if trusted, err = trustedFor(g.ctx, trust, msg.MessageView, agentID); err != nil {
				return err
			}
		}
		frame := dto.AgentMessage{Room: m.Room, Trusted: trusted, Context: true, Message: convert.Message(msg)}
		if i == 0 {
			frame.Skipped = skipped
		}
		if !t.conn.push(frame) {
			break // the queue overflowed and closed the connection: it reads its unread when it returns
		}
		rows = append(rows, db.CreateSessionDeliveryParams{
			MessageID: msg.ID, AgentID: agentID, Label: t.label, OwnerLive: t.conn.owner,
			CreatedAt: time.Now().UnixMilli(), Context: 1,
		})
	}
	return g.createSessionDeliveries(rows)
}
