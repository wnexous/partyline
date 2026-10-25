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
	// Push queues a message for the agent without blocking.
	Push(agentID string, m dto.AgentMessage) bool
	// PushUpdate queues, without blocking, the frame that tells the agent that a message was edited
	// (dto.AgentFrameTypeMessageEdited) or deleted (dto.AgentFrameTypeMessageDeleted).
	PushUpdate(agentID string, t dto.AgentFrameType, m dto.AgentMessage) bool
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
// other than its author and the rows of those members.
type audience struct {
	room    db.Room
	view    db.MessageView
	pushes  []string
	members map[string]db.RoomMember // the AI members' rows by id
}

// audienceOf finds the online AI members a message of the room is dispatched to. ok is false when there is
// nobody to tell: the room or the message is gone, the loop guard paused the room (DLV-08), or no AI member other
// than the author is online.
func (d *AgentDispatcher) audienceOf(ctx context.Context, roomID, messageID string) (a audience, ok bool, err error) {
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
	return a, len(a.pushes) > 0, nil
}

func (d *AgentDispatcher) dispatch(ctx context.Context, e events.MessageCreated) error {
	q := d.o.Store.R()
	a, ok, err := d.audienceOf(ctx, e.RoomID, e.MessageID)
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
		d.o.Agents.Push(agentID, dto.AgentMessage{Room: refs.of(agentID), Trusted: trusted, Message: msg})
	}
	return nil
}

// dispatchUpdate tells the AIs of the room that a message was edited or deleted (EDT-07, DLT-07). An AI hears it
// when its delivery mode wanted the message as it was - it may have been given it - or, for an edit, wants it
// as it is now: an edit that adds a mention reaches the AI mentioned, and one that takes it away reaches the AI
// that was told of it. The frame carries the message as it is now, with the trust of its author for that AI.
func (d *AgentDispatcher) dispatchUpdate(ctx context.Context, e events.MessageUpdated) error {
	q := d.o.Store.R()
	a, ok, err := d.audienceOf(ctx, e.RoomID, e.MessageID)
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
		d.o.Agents.PushUpdate(agentID, frame, dto.AgentMessage{Room: refs.of(agentID), Trusted: trusted, Message: msg})
	}
	return nil
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
