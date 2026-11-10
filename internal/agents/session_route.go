package agents

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/store/db"
)

// session_route.go decides which sessions of one AI a room message is pushed to and what context goes with
// it (SN-03, SN-04, SN-06, SN-07): the principal when it follows the room or the text names it, every live
// parallel session that follows the room or whose label the text addresses, and the principal again when
// the text addresses a sibling (SN-04). With no live principal, what would notify it (a DM, a bare @handle)
// notifies every live parallel session instead, and the first acknowledgement consumes it for the
// principal (SN-10). Every target is given the messages of the room it had not read yet
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
	// addressed says the message's text addresses this session's label (SESS-21, MA-02): it is that
	// session's message, not a copy.
	addressed bool
	// copyOfSibling says the session receives the message because it follows the room and the text
	// addresses a sibling of it (SN-04): it is a copy, and a later fallback of the same message adds
	// nothing to it.
	copyOfSibling bool
	// trustedOnly is set when the AI's delivery mode in the room is trusted (MEM-04): the catch-up gives
	// the session no untrusted message as context either, because untrusted text is not a boundary the
	// model may follow (SN-03, POS-10).
	trustedOnly bool
	// standIn says the session receives the message in place of the principal, which is not live (SN-10):
	// a DM or a bare @handle that would notify it. The session's acknowledgement consumes the message for
	// the principal, whose queue then leaves it out.
	standIn bool
	// writeRow says the principal's delivery row is needed (SN-03, SN-04, SN-09): there is a live parallel
	// session to dedupe against, the message also addresses a sibling (so the principal's ack must consume
	// it), or the principal reads the room on mentions only (its rows are how it follows what it got). A
	// plain push to the only session of the AI needs no row: its cursor already covers the message.
	writeRow bool
}

// pushPlan is what one non-self-authored room message is delivered as.
type pushPlan struct {
	// sessions is who receives the message, the principal first; empty when nobody is notified.
	sessions []pushSession
	// notes are the labels the text addresses that have no live session, in the text's order: the message is
	// the principal's from the start, with the note of the first one, also while the principal is offline
	// (SESS-22, SESS-24, MA-03). Every one of them gets its own fallback row.
	notes []string
}

// planPush works out the sessions a message notifies (SN-03, SN-04). targets are the labels the text
// addresses, in order and each once, and none for the principal (SESS-21, MA-01). The delivery mode and the loop guard already decided that
// the AI wants the message; this decides which of its sessions hears about it:
//   - the principal, when its setting for the room is on, or a plain message names it by its bare @handle,
//     or the message is a DM (it is addressed to the AI), and also when the text addresses a sibling of
//     it while its setting is on (SN-04);
//   - every live parallel session whose setting for the room is on, or whose label the text addresses (each
//     addressed one as its own message, MA-02);
//   - with no live principal, every live parallel session for a message that would notify the principal
//     (a DM or a bare @handle, and no label addressed): they stand in for it (SN-10).
//
// A message for labels with no live session is the principal's with the note (SESS-22, MA-03): once, however
// many of its labels are missing, and in place of any copy the principal would get (MA-04). A message the AI
// wrote itself never comes here: it follows SESS-38/SESS-49 instead.
func (g *Gateway) planPush(agentID string, targets []string, m dto.AgentMessage) (pushPlan, error) {
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
	member, err := g.d.Store.R().GetRoomMemberByMember(g.ctx, db.GetRoomMemberByMemberParams{
		RoomID: m.Room.ID, Kind: string(dto.MemberKindAgent), MemberID: agentID,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return plan, err
	}
	trustedOnly := dto.DeliveryMode(member.Delivery) == dto.DeliveryModeTrusted
	byKey := make(map[string]db.SessionRoomState, len(states))
	for _, st := range states {
		byKey[st.SessionKey] = st
	}
	lives := g.d.Presence.Sessions(agentID)
	live := make(map[string]bool, len(lives))
	for _, l := range lives {
		live[l.Label] = true
	}
	addressed := make(map[string]bool, len(targets))
	addressedLive := false
	for _, target := range targets {
		if target == "" || addressed[target] {
			continue
		}
		addressed[target] = true
		if live[target] {
			addressedLive = true
		} else {
			plan.notes = append(plan.notes, target)
		}
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
		c := g.labelConn(agentID, l.Label)
		switch {
		case has:
			notify, readSeq = st.Notify, st.ReadSeq
		case c != nil && !c.rooms.Load():
			// The session is still attaching and its state rows are not written yet: what came before this
			// message is its backlog's, not a catch-up from the room's join point (SN-01, SN-03).
			readSeq = m.Message.Seq
		default:
			// The rows may have been written after the list above was read, while the session attached:
			// read its row again before falling back to the join point.
			if again, err := g.d.Store.R().GetSessionRoomState(g.ctx, db.GetSessionRoomStateParams{
				AgentID: agentID, SessionKey: key, RoomID: m.Room.ID,
			}); err == nil {
				notify, readSeq = again.Notify, again.ReadSeq
				break
			} else if !errors.Is(err, sql.ErrNoRows) {
				return plan, err
			}
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
			live: l, conn: c, notify: notify, readSeq: readSeq,
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
	case m.Room.Kind == dto.ConversationKindDM:
		mentions = true // a DM is addressed to the AI
	case principalLive && principalNotify != NotifyOn, !live[""] && len(addressed) == 0:
		// A bare @handle names the principal even when the same text also addresses a sibling (SN-04).
		profile, err := delivery.IntelligenceOf(g.ctx, g.d.Store.R(), agentID)
		if err != nil {
			return plan, err
		}
		mentions = delivery.MentionsPrincipal(m.Message.Text, profile.Handle)
	}
	// With no live principal, the message that would notify it reaches its live parallel sessions instead
	// of waiting for a principal that may never connect (SN-10). A message for a label keeps its own route
	// (SESS-22, SESS-23).
	standIn := !live[""] && len(addressed) == 0 && mentions
	parallelLive := false
	for _, l := range lives {
		if l.Label != "" {
			parallelLive = true
		}
	}
	for _, it := range items {
		if it.live.Label == "" {
			principalGets = len(plan.notes) > 0 || it.notify == NotifyOn || mentions
		}
	}
	for _, it := range items {
		var wanted bool
		switch {
		case it.live.Label == "":
			wanted = principalGets
		case addressed[it.live.Label]:
			wanted = true
		default:
			wanted = it.notify == NotifyOn || standIn
		}
		if !wanted || it.conn == nil {
			continue
		}
		isAddressed := it.live.Label != "" && addressed[it.live.Label]
		copyOfSibling := it.live.Label == "" && addressedLive
		plan.sessions = append(plan.sessions, pushSession{
			label: it.live.Label, conn: it.conn, readSeq: it.readSeq,
			addressed: isAddressed, copyOfSibling: copyOfSibling, trustedOnly: trustedOnly,
			standIn:  standIn && it.live.Label != "",
			writeRow: parallelLive || copyOfSibling || (it.live.Label == "" && it.notify != NotifyOn),
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
		// position through it. Its copy row is written only when it routes something: an edit back to it,
		// the dedupe of a message another session also receives, or the ack of a sibling's message (SN-03,
		// SN-04, SN-09). A plain push to the only session of the AI needs none.
		if t.writeRow {
			if err := g.createSessionDelivery(g.ctx, m.Message.ID, agentID, "", t.conn.sessionKey, t.conn.owner, time.Now().UnixMilli(), sql.NullInt64{}, true); err != nil {
				g.d.Logger.Error("agent: principal copy row write failed", "agent_id", agentID, "message_id", m.Message.ID, "err", err)
			}
		}
		if t.copyOfSibling {
			return g.pushToPrincipalCopy(agentID, m)
		}
		return g.pushToPrincipal(agentID, m)
	}
	row, err := g.d.Store.R().GetSessionDelivery(g.ctx, db.GetSessionDeliveryParams{
		MessageID: m.Message.ID, AgentID: agentID, Label: t.label,
	})
	switch {
	case err == nil && t.addressed && row.Context != 0 && !row.AckedAt.Valid:
		// An addressed push out of order: an earlier catch-up batch already gave the session the message as
		// context. It is the session's own from now on, and the frame goes without the context flag (SN-03).
		if err := g.upgradeSessionDelivery(agentID, t.label, t.conn.sessionKey, t.conn.owner, m.Message.ID); err != nil {
			g.d.Logger.Error("agent: session delivery upgrade failed", "agent_id", agentID, "message_id", m.Message.ID, "err", err)
			return false
		}
		return g.deliverOwned(agentID, t.label, m)
	case err == nil:
		return false // it already reached that session: SN-03 pushes a message once
	case !errors.Is(err, sql.ErrNoRows):
		g.d.Logger.Error("agent: session delivery read failed", "agent_id", agentID, "message_id", m.Message.ID, "err", err)
		return false
	}
	if err := g.createSessionDelivery(g.ctx, m.Message.ID, agentID, t.label, t.conn.sessionKey, t.conn.owner, time.Now().UnixMilli(), sql.NullInt64{}, !t.addressed); err != nil {
		g.d.Logger.Error("agent: session delivery write failed", "agent_id", agentID, "message_id", m.Message.ID, "err", err)
		return false
	}
	if !t.addressed {
		// A copy never falls back to the principal (SN-03): the message is not that session's to own. A
		// stand-in is marked before the frame is queued, so its acknowledgement cannot arrive first (SN-10).
		if t.standIn {
			t.conn.markStandIn(m.Message.ID)
		}
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
	// The batch leaves a slot for the message that comes after it. When the connection's queue cannot hold
	// all of a batch, the newest frames that fit are sent and the rest is counted as skipped: the session
	// acknowledges the message that notified it, which moves its read position past all of them anyway.
	if free := cap(t.conn.queue) - len(t.conn.queue) - 1; len(msgs) > free {
		if free > 0 {
			msgs = msgs[len(msgs)-free:]
		} else {
			msgs = nil
		}
	}
	skipped := count - int64(len(msgs))
	trust := g.d.Trust.Open()
	// The row of a frame is written only once the frame is in the queue: a connection that dropped before
	// taking the frame leaves no record that the message was given, so the next catch-up has it again
	// (SN-03). An unacknowledged message is replayed whether or not its row exists, so the rows only route
	// an edit or a delete and keep the copy's ownership.
	rows := make([]db.CreateSessionDeliveryParams, 0, len(msgs))
	sent := 0
	for _, msg := range msgs {
		trusted := true // the AI wrote it: its sessions trust it, as the live push says (SESS-37)
		if !selfAuthored(convert.Message(msg), agentID) {
			if trusted, err = trustedFor(g.ctx, trust, msg.MessageView, agentID); err != nil {
				return err
			}
		}
		if t.trustedOnly && !trusted {
			continue // SN-03, POS-10: in trusted mode an untrusted message is not context either
		}
		frame := dto.AgentMessage{Room: m.Room, Trusted: trusted, Context: true, Message: convert.Message(msg)}
		if sent == 0 {
			frame.Skipped = skipped
		}
		if !t.conn.push(frame) {
			break // the queue overflowed and closed the connection: it reads its unread when it returns
		}
		sent++
		rows = append(rows, db.CreateSessionDeliveryParams{
			MessageID: msg.ID, AgentID: agentID, Label: t.label, SessionKey: t.conn.sessionKey,
			OwnerLive: t.conn.owner, CreatedAt: time.Now().UnixMilli(), Context: 1,
		})
	}
	return g.createSessionDeliveries(rows)
}

// standInBacklog writes, to a parallel session that attaches while the AI has no live principal, the
// messages of the principal's queue that would notify the principal (SN-10): per room, the DMs, or the
// messages that name the AI by its bare @handle, that address no label and that the delivery mode wants,
// oldest first and at most BacklogLimit of them (counted among those messages, not among the whole queue);
// when more of them wait, a notice after them says how many, and those stay in the principal's queue. They
// come with the room's messages the session had not read before them as context (SN-03), as a live push
// would bring them. Each gets a copy row for the session and is marked as its stand-in, so its
// acknowledgement consumes it for the principal. What was posted after the welcome is the live push's
// (bounds), and a message the session already acknowledged is not given again, nor one this connection's
// backlog already wrote. It runs after the backlog and before the live pushes are written.
func (g *Gateway) standInBacklog(ctx context.Context, c *conn, a db.Agent, bounds map[string]int64) error {
	if c.labelOf() == "" {
		return nil
	}
	if _, ok := g.d.Presence.Session(a.ID); ok {
		return nil // a live principal reads its own queue
	}
	states, err := g.d.Registry.SessionRooms(ctx, a.ID)
	if err != nil {
		return err
	}
	profile, err := delivery.IntelligenceOf(ctx, g.d.Store.R(), a.ID)
	if err != nil {
		return err
	}
	trust := g.d.Trust.Open()
	principalKey := SessionKey("", "", a.PrincipalSession)
	for _, st := range states {
		bound, ok := bounds[st.ID]
		if !ok || st.AgentsPaused {
			continue // DLV-08: no agent delivery while the loop guard holds the room
		}
		if err := g.standInRoom(ctx, c, a, st, bound, profile, trust, principalKey); err != nil {
			return err
		}
	}
	return nil
}

// standInPage is the page size of the principal's queue that standInRoom reads at a time.
const standInPage = 200

// StandInMoreNotice is the notice after the stand-ins of a room when more messages for the principal wait
// there than one backlog gives (SN-10): they stay in the principal's queue.
func StandInMoreNotice(n int64) string {
	return fmt.Sprintf("%d more messages for this AI wait in this conversation; use the history tool", n)
}

// standInRoom is standInBacklog for one room.
func (g *Gateway) standInRoom(ctx context.Context, c *conn, a db.Agent, st RoomState, bound int64, profile db.Intelligence, trust delivery.Trust, principalKey string) error {
	q := g.d.Store.R()
	label := c.labelOf()
	includeAddressed, err := principalIncludesAddressed(ctx, q, a.ID, principalKey, st.ID)
	if err != nil {
		return err
	}
	prefs := delivery.Prefs(profile, st.DisplayName, st.Delivery, st.KeywordsJson)
	isDM := st.Kind == string(dto.ConversationKindDM)
	// qualifies reports whether a message of the principal's queue is one the session stands in for, and
	// its trust for the AI.
	qualifies := func(v db.MessageView) (ok, trusted bool, err error) {
		if delivery.SessionLabel(v.Text, profile.Handle) != "" {
			return false, false, nil // a message for a label keeps its own route (SESS-22)
		}
		if !isDM && !delivery.MentionsPrincipal(v.Text, profile.Handle) {
			return false, false, nil // a plain group message waits for the principal (SESS-23)
		}
		row, err := q.GetSessionDelivery(ctx, db.GetSessionDeliveryParams{MessageID: v.ID, AgentID: a.ID, Label: label})
		switch {
		case err == nil && row.AckedAt.Valid:
			return false, false, nil // the session read it already
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return false, false, err
		}
		facts, err := delivery.LoadFacts(ctx, q, v, st.Kind)
		if err != nil {
			return false, false, err
		}
		return facts.WantedBy(ctx, trust, a.ID, prefs) // MEM-03, MEM-04
	}
	// The cap applies to the messages that qualify, not to the queue: a mention followed by many plain
	// messages is still found. The oldest BacklogLimit are given; the ones after them are only counted.
	var views []db.MessageView
	trustedOf := map[string]bool{} // the stand-ins, with their trust for the AI
	last, more := int64(0), int64(0)
	// What the session read before this connection is not given again, though the principal's queue still
	// holds it: a reconnect (a server restart) must not bring old DMs back as new ones. A read position
	// created by this attach is not a reading (SN-01 joins a session caught up), so it does not count.
	start := st.Cursor
	if prior, ok := c.priorRead[st.ID]; ok && prior > start {
		start = prior
	}
	for after := start; ; {
		page, err := q.ListStandInCandidates(ctx, db.ListStandInCandidatesParams{
			AgentID: a.ID, RoomID: st.ID, AfterSeq: after, BoundSeq: bound,
			AnyText: isDM, Pattern: "%@" + profile.Handle + "%", IncludeAddressed: includeAddressed,
			Limit: standInPage,
		})
		if err != nil {
			return err
		}
		for _, v := range page {
			after = v.Seq
			ok, trusted, err := qualifies(v)
			if err != nil {
				return err
			}
			switch {
			case !ok:
			case len(trustedOf) < BacklogLimit:
				views = append(views, v)
				trustedOf[v.ID] = trusted
				last = v.Seq
			default:
				more++
			}
		}
		if len(page) < standInPage {
			break
		}
	}
	if len(trustedOf) == 0 {
		return nil
	}
	// The context is what the session had not read before the last stand-in, as for a live push (SN-03).
	readSeq := bound
	if state, err := q.GetSessionRoomState(ctx, db.GetSessionRoomStateParams{
		AgentID: a.ID, SessionKey: c.sessionKey, RoomID: st.ID,
	}); err == nil {
		readSeq = state.ReadSeq
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var unread []db.MessageView
	var count int64
	if readSeq < last {
		params := db.CountSessionUnreadInRoomParams{
			RoomID: st.ID, AfterSeq: readSeq, BeforeSeq: last + 1, AgentID: a.ID, AuthorLabel: label, Label: label,
		}
		if count, err = q.CountSessionUnreadInRoom(ctx, params); err != nil {
			return err
		}
		if unread, err = q.ListSessionUnread(ctx, db.ListSessionUnreadParams{
			RoomID: st.ID, AfterSeq: readSeq, BeforeSeq: last + 1, AgentID: a.ID, AuthorLabel: label, Label: label,
			Limit: BacklogLimit,
		}); err != nil {
			return err
		}
	}
	// The frames: the stand-ins, and the unread messages around them as context, in sequence order.
	byID := make(map[string]db.MessageView, len(views)+len(unread))
	for _, v := range views {
		if _, ok := trustedOf[v.ID]; ok {
			byID[v.ID] = v
		}
	}
	skipped := count
	for _, v := range unread {
		if _, ok := trustedOf[v.ID]; ok {
			skipped-- // a stand-in is not context, but it is one of the unread messages listed
			continue
		}
		byID[v.ID] = v
		skipped--
	}
	all := make([]db.MessageView, 0, len(byID))
	for _, v := range byID {
		all = append(all, v)
	}
	slices.SortFunc(all, func(x, y db.MessageView) int { return cmp.Compare(x.Seq, y.Seq) })
	msgs, err := g.d.Rooms.Details(ctx, all)
	if err != nil {
		return err
	}
	ref := delivery.RoomRef(st.ID, st.Name, st.Kind)
	trustedOnly := dto.DeliveryMode(st.Delivery) == dto.DeliveryModeTrusted
	type item struct {
		frame dto.AgentMessage
		row   db.CreateSessionDeliveryParams
	}
	items := make([]item, 0, len(msgs))
	for _, msg := range msgs {
		// The backlog of this connection may have written the message already (its own unacknowledged row,
		// SESS-14): it is not written again, neither as context nor as a stand-in, as the queue would drop it
		// (SN-03). The skipped count goes on the first frame that is written.
		if _, dup := c.delivered[msg.ID]; dup {
			continue
		}
		if _, dup := c.pushed[msg.ID]; dup {
			if _, standIn := trustedOf[msg.ID]; !standIn {
				continue
			}
		}
		frame := dto.AgentMessage{Room: ref, Message: convert.Message(msg)}
		if trusted, ok := trustedOf[msg.ID]; ok {
			frame.Trusted = trusted
		} else {
			frame.Context = true
			trusted := true // the AI wrote it: its sessions trust it (SESS-37)
			if !selfAuthored(frame.Message, a.ID) {
				if trusted, err = trustedFor(ctx, trust, msg.MessageView, a.ID); err != nil {
					return err
				}
			}
			if trustedOnly && !trusted {
				continue // SN-03, POS-10: in trusted mode an untrusted message is not context either
			}
			frame.Trusted = trusted
		}
		items = append(items, item{frame: frame, row: db.CreateSessionDeliveryParams{
			MessageID: msg.ID, AgentID: a.ID, Label: label, SessionKey: c.sessionKey,
			OwnerLive: c.owner, CreatedAt: time.Now().UnixMilli(), Context: 1,
		}})
	}
	// The rows exist before the frames are written: the acknowledgement of a stand-in finds its row (a copy
	// row hides nothing from a later catch-up while it is not acknowledged, SN-03).
	rows := make([]db.CreateSessionDeliveryParams, len(items))
	for i, it := range items {
		rows[i] = it.row
	}
	if err := g.createSessionDeliveries(rows); err != nil {
		return err
	}
	for i, it := range items {
		if i == 0 && skipped > 0 {
			it.frame.Skipped = skipped
		}
		id := it.frame.Message.ID
		if !it.frame.Context {
			c.markStandIn(id)
			c.delivered[id] = struct{}{}
		}
		if err := c.write(ctx, dto.AgentMessageFrame{Type: dto.AgentFrameTypeMessage, AgentMessage: it.frame}); err != nil {
			return err
		}
		c.pushed[id] = struct{}{}
	}
	if more > 0 {
		// The rest stay in the principal's queue: the session can read them with the history tool.
		return c.write(ctx, dto.AgentNoticeFrame{Type: dto.AgentFrameTypeNotice, Room: ref, Text: StandInMoreNotice(more)})
	}
	return nil
}
