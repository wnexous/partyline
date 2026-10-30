package delivery

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// AgentPusher hands messages to connected agents (implemented by agents.Gateway).
type AgentPusher interface {
	// Online reports whether the agent has a live connection.
	Online(agentID string) bool
	// Push queues a message for the agent without blocking. target is the session label the text
	// addresses (SESS-21), or "" for the principal session.
	Push(agentID, target string, m dto.AgentMessage) bool
	// PushUpdate queues, without blocking, the frame that tells the agent that a message was edited
	// (dto.AgentFrameTypeMessageEdited) or deleted (dto.AgentFrameTypeMessageDeleted). targets are the
	// session labels the text addressed before and after the change, "" for the principal: the gateway also
	// follows every delivery row the message has (SN-09).
	PushUpdate(agentID string, targets []string, t dto.AgentFrameType, m dto.AgentMessage) bool
}

// MessageLoader renders a stored message as agents receive it.
type MessageLoader func(ctx context.Context, messageID string) (dto.Message, error)

// DispatcherOptions configure an AgentDispatcher; all fields but Trust and Logger are required.
type DispatcherOptions struct {
	Store  *store.Store
	Bus    *events.Bus
	Agents AgentPusher
	Load   MessageLoader
	// Trust opens the trust of each dispatch (POS-10); without it no message is trusted.
	Trust TrustSource
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// dispatcherBuffer is the event backlog of the dispatcher's bus subscription.
const dispatcherBuffer = 1024

// AgentDispatcher pushes every new room message to the online agents the delivery plan
// targets (DLV-02) and whose delivery mode wants it (MEM-03, MEM-04), with a trusted flag
// computed for each receiving agent: true when the author manages it in its home organization
// (POS-10), decided when the message is dispatched, with one read of each organization's chart for
// the whole dispatch. Rooms
// whose agent delivery is paused by the loop guard get no pushes (DLV-08). Offline agents
// receive the message from their backlog when they reconnect.
//
// It also tells the same agents that a message was edited or deleted (EDT-07, DLT-07): the agents whose
// delivery mode wants the message as it was before the change or, for an edit, as it is now.
type AgentDispatcher struct {
	o      DispatcherOptions
	events <-chan events.Event
	cancel func()
}

// NewAgentDispatcher subscribes to the bus right away, so no message stored after it returns
// is missed; call Run to process the events.
func NewAgentDispatcher(o DispatcherOptions) *AgentDispatcher {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	ch, cancel := o.Bus.Subscribe(dispatcherBuffer)
	return &AgentDispatcher{o: o, events: ch, cancel: cancel}
}

// Run dispatches MessageCreated events until ctx ends.
func (d *AgentDispatcher) Run(ctx context.Context) error {
	defer d.cancel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-d.events:
			switch e := e.(type) {
			case events.MessageCreated:
				if err := d.dispatch(ctx, e); err != nil && ctx.Err() == nil {
					d.o.Logger.Error("agent dispatch failed", "room_id", e.RoomID, "message_id", e.MessageID, "err", err)
				}
			case events.MessageUpdated:
				if err := d.dispatchUpdate(ctx, e); err != nil && ctx.Err() == nil {
					d.o.Logger.Error("agent update dispatch failed", "room_id", e.RoomID, "message_id", e.MessageID, "err", err)
				}
			case events.Lagged:
				d.o.Logger.Warn("agent dispatcher lagged; agents recover missed messages on reconnect")
			}
		}
	}
}

// audience is who a dispatch about one message reaches: the room, the message as stored, the online AI members
// other than its author, the rows of those members, and the author AI's own sessions the text addresses (D11).
type audience struct {
	room    db.Room
	view    db.MessageView
	pushes  []string
	self    []selfTarget
	members map[string]db.RoomMember // the AI members' rows by id
}

// selfTarget is the one recipient the delivery plan leaves out: the AI that wrote a message, when the
// message addresses one of its other sessions with @handle:label (SESS-37). The label may have no live
// session; the gateway then delivers nothing (SESS-38).
type selfTarget struct {
	agentID string
	label   string
}

// audienceOf finds the sessions a message of the room is dispatched to. prevText is the text of an
// edit or a delete before the change, which may address a sibling the message no longer names
// (SESS-39), and is empty for a new message. ok is false when there is nobody to tell: the room or the
// message is gone, the loop guard paused the room (DLV-08), or neither another AI member nor a session
// of the author AI is a target.
func (d *AgentDispatcher) audienceOf(ctx context.Context, roomID, messageID, prevText string) (a audience, ok bool, err error) {
	q := d.o.Store.R()
	a.room, err = q.GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil // deleted meanwhile
	}
	if err != nil || a.room.AgentsPaused {
		return a, false, err
	}
	a.view, err = q.GetMessageView(ctx, messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, err
	}
	rows, err := q.ListRoomMembers(ctx, roomID)
	if err != nil {
		return a, false, err
	}
	members := make([]Member, len(rows))
	a.members = map[string]db.RoomMember{}
	for i, r := range rows {
		members[i] = Member{Kind: dto.MemberKind(r.RoomMember.Kind), ID: r.RoomMember.MemberID}
		if r.RoomMember.Kind == string(dto.MemberKindAgent) {
			a.members[r.RoomMember.MemberID] = r.RoomMember
		}
	}
	for _, id := range Plan(members, Draft{AuthorKind: dto.AuthorKind(a.view.AuthorKind), AuthorID: a.view.AuthorID}) {
		if d.o.Agents.Online(id) {
			a.pushes = append(a.pushes, id)
		}
	}
	if a.self, err = d.selfTargets(ctx, q, a.view, prevText); err != nil {
		return a, false, err
	}
	return a, len(a.pushes) > 0 || len(a.self) > 0, nil
}

// selfTargets returns the sessions of the AI that wrote a message that its text addresses
// (SESS-37). The stored author_label names the writing session and is never parsed from the text
// (SESS-29): the message's own text before an edit or a delete and as it is now are both read, and
// the label of the writing session itself is never a target (SESS-38, SESS-39).
func (d *AgentDispatcher) selfTargets(ctx context.Context, q *db.Queries, view db.MessageView, prevText string) ([]selfTarget, error) {
	if view.AuthorKind != string(dto.AuthorKindAgent) || view.AuthorID == "" {
		return nil, nil
	}
	profile, err := IntelligenceOf(ctx, q, view.AuthorID)
	if err != nil {
		return nil, err
	}
	var out []selfTarget
	seen := map[string]bool{}
	for _, text := range []string{prevText, view.Text} {
		// A labelled session that mentions its AI by the bare @handle reaches the principal (SESS-49); the
		// principal itself never receives its own message.
		if view.AuthorLabel != "" && !seen[PrincipalTarget] && MentionsPrincipal(text, profile.Handle) {
			seen[PrincipalTarget] = true
			out = append(out, selfTarget{agentID: view.AuthorID, label: PrincipalTarget})
		}
		label := SessionLabel(text, profile.Handle)
		if label == "" || label == view.AuthorLabel || seen[label] {
			continue
		}
		seen[label] = true
		out = append(out, selfTarget{agentID: view.AuthorID, label: label})
	}
	return out, nil
}

func (d *AgentDispatcher) dispatch(ctx context.Context, e events.MessageCreated) error {
	q := d.o.Store.R()
	a, ok, err := d.audienceOf(ctx, e.RoomID, e.MessageID, "")
	if err != nil || !ok {
		return err
	}
	if a.view.DeletedAt.Valid {
		return nil // deleted before it was dispatched: there is nothing to tell (DLT-05)
	}
	facts, err := LoadFacts(ctx, q, a.view, a.room.Kind)
	if err != nil {
		return err
	}
	msg, err := d.o.Load(ctx, e.MessageID)
	if err != nil {
		return err
	}
	refs, err := roomRefs(ctx, q, a.room)
	if err != nil {
		return err
	}
	trust := d.o.Trust.Open() // one for the dispatch: each home organization's chart is read once
	for _, agentID := range a.pushes {
		if _, err := q.GetAgent(ctx, agentID); errors.Is(err, sql.ErrNoRows) {
			continue // deleted meanwhile
		} else if err != nil {
			return err
		}
		profile, err := IntelligenceOf(ctx, q, agentID)
		if err != nil {
			return err
		}
		m := a.members[agentID]
		wanted, trusted, err := facts.WantedBy(ctx, trust, agentID, Prefs(profile, m.DisplayName, m.Delivery, m.KeywordsJson))
		if err != nil {
			return err
		}
		if !wanted {
			continue // MEM-03, MEM-04: stored, not pushed
		}
		target := SessionLabel(a.view.Text, profile.Handle)
		d.o.Agents.Push(agentID, target, dto.AgentMessage{Room: refs.of(agentID), Trusted: trusted, Message: msg})
	}
	// D11: the author AI's own sessions are outside the plan's author exclusion and outside the
	// membership's delivery mode, because the author is the AI itself (SESS-37).
	for _, t := range a.self {
		d.o.Agents.Push(t.agentID, t.label, dto.AgentMessage{Room: refs.of(t.agentID), Trusted: true, Message: msg})
	}
	return nil
}

// dispatchUpdate tells the AIs of the room that a message was edited or deleted (EDT-07, DLT-07). An AI hears it
// when its delivery mode wanted the message as it was - it may have been given it - or, for an edit, wants it
// as it is now: an edit that adds a mention reaches the AI mentioned, and one that takes it away reaches the AI
// that was told of it. The frame carries the message as it is now, with the trust of its author for that AI.
func (d *AgentDispatcher) dispatchUpdate(ctx context.Context, e events.MessageUpdated) error {
	q := d.o.Store.R()
	a, ok, err := d.audienceOf(ctx, e.RoomID, e.MessageID, e.PrevText)
	if err != nil || !ok {
		return err
	}
	if a.view.DeletedAt.Valid && !e.Deleted {
		return nil // deleted since the edit: the delete is told next
	}
	now, err := LoadFacts(ctx, q, a.view, a.room.Kind)
	if err != nil {
		return err
	}
	before := now
	before.Text = e.PrevText
	msg, err := d.o.Load(ctx, e.MessageID)
	if err != nil {
		return err
	}
	refs, err := roomRefs(ctx, q, a.room)
	if err != nil {
		return err
	}
	frame := dto.AgentFrameTypeMessageEdited
	if e.Deleted {
		frame = dto.AgentFrameTypeMessageDeleted
	}
	trust := d.o.Trust.Open()
	for _, agentID := range a.pushes {
		if _, err := q.GetAgent(ctx, agentID); errors.Is(err, sql.ErrNoRows) {
			continue // deleted meanwhile
		} else if err != nil {
			return err
		}
		profile, err := IntelligenceOf(ctx, q, agentID)
		if err != nil {
			return err
		}
		m := a.members[agentID]
		prefs := Prefs(profile, m.DisplayName, m.Delivery, m.KeywordsJson)
		wanted, trusted, err := before.WantedBy(ctx, trust, agentID, prefs)
		if err != nil {
			return err
		}
		if !wanted && !e.Deleted {
			if wanted, trusted, err = now.WantedBy(ctx, trust, agentID, prefs); err != nil {
				return err
			}
		}
		if !wanted {
			continue // it never concerned this AI
		}
		targets := []string{SessionLabel(e.PrevText, profile.Handle)}
		if !e.Deleted {
			targets = append(targets, SessionLabel(a.view.Text, profile.Handle))
		}
		// One call: the gateway follows the addressed sessions and every delivery row of the message, and
		// writes each of them once (SN-09).
		d.o.Agents.PushUpdate(agentID, dedupeTargets(targets), frame, dto.AgentMessage{Room: refs.of(agentID), Trusted: trusted, Message: msg})
	}
	// SESS-39: the update reaches the sibling the message addressed before or after the change, and
	// never another session of the author AI.
	for _, t := range a.self {
		d.o.Agents.PushUpdate(t.agentID, []string{t.label}, frame, dto.AgentMessage{Room: refs.of(t.agentID), Trusted: true, Message: msg})
	}
	return nil
}

// dedupeTargets keeps the first occurrence of each label, so an edit whose old and new text address
// the same session pushes it one frame (SESS-27).
func dedupeTargets(targets []string) []string {
	out := make([]string, 0, len(targets))
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}

// roomNames names a room for each agent that receives from it: a group has one name; a DM is
// named after the participant that is not the agent.
type roomNames struct {
	group dto.RoomRef
	dm    map[string]dto.RoomRef // by participant: the ref its counterpart reads
}

func (n roomNames) of(agentID string) dto.RoomRef {
	if ref, ok := n.dm[agentID]; ok {
		return ref
	}
	return n.group
}

func roomRefs(ctx context.Context, q *db.Queries, room db.Room) (roomNames, error) {
	names := roomNames{group: RoomRef(room.ID, room.Name, room.Kind)}
	if room.Kind != string(dto.ConversationKindDM) {
		return names, nil
	}
	parts, err := q.ListRoomParticipants(ctx, room.ID)
	if err != nil {
		return roomNames{}, err
	}
	names.dm = make(map[string]dto.RoomRef, len(parts))
	for _, p := range parts {
		for _, other := range parts {
			if other.Intelligence.ID != p.Intelligence.ID {
				names.dm[p.Intelligence.ID] = RoomRef(room.ID, other.Intelligence.DisplayName, room.Kind)
			}
		}
	}
	return names, nil
}
