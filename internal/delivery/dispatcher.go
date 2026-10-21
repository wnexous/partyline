package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
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
}

// MessageLoader renders a stored message as agents receive it.
type MessageLoader func(ctx context.Context, messageID string) (dto.Message, error)

// DispatcherOptions configure an AgentDispatcher; all fields but Logger are required.
type DispatcherOptions struct {
	Store  *store.Store
	Bus    *events.Bus
	Agents AgentPusher
	Load   MessageLoader
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// dispatcherBuffer is the event backlog of the dispatcher's bus subscription.
const dispatcherBuffer = 1024

// AgentDispatcher pushes every new room message to the online agents the delivery plan
// targets (DLV-02) and whose delivery mode wants it (MEM-03, MEM-04), with a trusted flag
// computed for each receiving agent (DLV-06). A project member's message goes to the
// project's active session (PROJ-07, PROJ-10). Rooms
// whose agent delivery is paused by the loop guard get no pushes (DLV-08). Offline agents
// receive the message from their backlog when they reconnect.
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
			case events.Lagged:
				d.o.Logger.Warn("agent dispatcher lagged; agents recover missed messages on reconnect")
			}
		}
	}
}

func (d *AgentDispatcher) dispatch(ctx context.Context, e events.MessageCreated) error {
	q := d.o.Store.R()
	room, err := q.GetRoom(ctx, e.RoomID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // deleted meanwhile
	}
	if err != nil {
		return err
	}
	if room.AgentsPaused {
		return nil
	}
	view, err := q.GetMessageView(ctx, e.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := q.ListRoomMembers(ctx, e.RoomID)
	if err != nil {
		return err
	}
	members := make([]Member, len(rows))
	memberRows := map[string]db.RoomMember{} // agent and project rows by kind/id
	for i, r := range rows {
		members[i] = Member{Kind: dto.MemberKind(r.RoomMember.Kind), ID: r.RoomMember.MemberID, Outbound: r.RoomMember.Outbound}
		if r.RoomMember.Kind == string(dto.MemberKindAgent) || r.RoomMember.Kind == string(dto.MemberKindProject) {
			memberRows[r.RoomMember.Kind+"/"+r.RoomMember.MemberID] = r.RoomMember
		}
	}
	var recipients []string
	_ = json.Unmarshal([]byte(view.RecipientsJson), &recipients)
	draft := Draft{AuthorKind: dto.AuthorKind(view.AuthorKind), AuthorID: view.AuthorID, Recipients: recipients}
	if draft.AuthorKind == dto.AuthorKindAgent {
		author, err := q.GetAgent(ctx, view.AuthorID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		draft.AuthorProjectID = author.ProjectID.String
	}
	targets := Plan(RoomState{BridgeChats: room.BridgeChats}, members, draft)

	var pushes []push
	direct := map[string]bool{}
	for _, id := range targets.AgentIDs {
		direct[id] = true
		if d.o.Agents.Online(id) {
			pushes = append(pushes, push{agentID: id})
		}
	}
	for _, pid := range targets.ProjectIDs {
		// PROJ-07: only the project's active session; none online → it waits (PROJ-08). An
		// active session that is also a direct member gets the message once, as a member.
		a, ok, err := ActiveSession(ctx, q, d.o.Agents.Online, pid)
		if err != nil {
			return err
		}
		if ok && !direct[a.ID] {
			pushes = append(pushes, push{agentID: a.ID, projectID: pid})
		}
	}
	if len(pushes) == 0 {
		return nil
	}
	facts, err := LoadFacts(ctx, q, view)
	if err != nil {
		return err
	}
	msg, err := d.o.Load(ctx, e.MessageID)
	if err != nil {
		return err
	}
	ref := dto.RoomRef{ID: room.ID, Name: room.Name}
	for _, t := range pushes {
		agent, err := q.GetAgent(ctx, t.agentID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		ownerID, wanted := agent.OwnerID, false
		if t.projectID == "" {
			m := memberRows[string(dto.MemberKindAgent)+"/"+t.agentID]
			wanted = facts.WantedBy(t.agentID, ownerID, AgentPrefs(agent, m.DisplayName, m.Delivery, m.KeywordsJson))
		} else {
			project, err := q.GetProject(ctx, t.projectID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			// PROJ-10: trust follows the project's owner; the project member's settings apply.
			ownerID = project.OwnerID
			m := memberRows[string(dto.MemberKindProject)+"/"+t.projectID]
			wanted = facts.WantedByProject(t.agentID, t.projectID, ownerID,
				ProjectPrefs(agent, project.Name, m.DisplayName, m.Delivery, m.KeywordsJson))
		}
		if !wanted {
			continue // MEM-03, MEM-04: stored, not pushed
		}
		d.o.Agents.Push(t.agentID, dto.AgentMessage{Room: ref, Trusted: Trusted(msg.AuthorUserID, ownerID), Message: msg})
	}
	return nil
}

// push is one live delivery: to an agent member, or to a project's active session.
type push struct {
	agentID   string
	projectID string // set for a delivery through a project member
}
