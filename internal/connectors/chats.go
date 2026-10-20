package connectors

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// ChatView is a chat with its connector.
type ChatView struct {
	Chat      db.Chat
	Connector db.Connector
}

// Chats lists a connector's chats; requires use.
func (s *Service) Chats(ctx context.Context, p authn.Principal, connectorID string) ([]ChatView, error) {
	if _, err := s.authorize(ctx, p, connectorID, dto.LevelUse); err != nil {
		return nil, err
	}
	c, err := s.d.Store.R().GetConnector(ctx, connectorID)
	if err != nil {
		return nil, err
	}
	rows, err := s.d.Store.R().ListConnectorChats(ctx, connectorID)
	if err != nil {
		return nil, err
	}
	out := make([]ChatView, len(rows))
	for i, chat := range rows {
		out[i] = ChatView{Chat: chat, Connector: c}
	}
	return out, nil
}

// RefreshChats asks the running connector for its chats and records them (CON-09);
// requires ownership (manage). Kinds that cannot list chats get ErrCannotListChats, stopped
// connectors ErrNotRunning.
func (s *Service) RefreshChats(ctx context.Context, p authn.Principal, connectorID string) error {
	if _, err := s.authorize(ctx, p, connectorID, dto.LevelManage); err != nil {
		return err
	}
	c, err := s.d.Store.R().GetConnector(ctx, connectorID)
	if err != nil {
		return err
	}
	if !s.Kind(c.Kind).CanListChats {
		return ErrCannotListChats
	}
	if s.d.Runner == nil {
		return ErrNotRunning
	}
	chats, err := s.d.Runner.ListChats(ctx, connectorID)
	if err != nil {
		return err
	}
	for _, info := range chats {
		if info.ID == "" {
			continue
		}
		if _, err := UpsertChat(ctx, s.d.Store, s.d.Bus, connectorID, info, time.Time{}, s.d.Now()); err != nil {
			return err
		}
	}
	return nil
}

// UsableChats lists the chats of every connector the user owns or may use.
func (s *Service) UsableChats(ctx context.Context, p authn.Principal) ([]ChatView, error) {
	if p.Kind != authn.KindUser {
		return nil, access.ErrForbidden
	}
	rows, err := s.d.Store.R().ListUsableChats(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]ChatView, len(rows))
	for i, r := range rows {
		out[i] = ChatView{Chat: r.Chat, Connector: r.Connector}
	}
	return out, nil
}

// RouteChat creates a private room of the caller named after the chat, with the chat and
// the agent as members (UI-13). The caller must use the chat's connector (else not found)
// and the agent (else forbidden).
func (s *Service) RouteChat(ctx context.Context, p authn.Principal, chatID, agentID string) (rooms.Room, error) {
	if p.Kind != authn.KindUser {
		return rooms.Room{}, access.ErrForbidden
	}
	chat, err := s.d.Store.R().GetChat(ctx, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return rooms.Room{}, access.ErrNotFound
	}
	if err != nil {
		return rooms.Room{}, err
	}
	if ok, err := s.d.Access.CanUseConnector(ctx, p.UserID, chat.ConnectorID); err != nil {
		return rooms.Room{}, err
	} else if !ok {
		return rooms.Room{}, access.ErrNotFound
	}
	if agentID == "" {
		return rooms.Room{}, &validation.Error{Fields: map[string]string{"agent_id": "required"}}
	}
	if ok, err := s.d.Access.CanUseAgent(ctx, p.UserID, agentID); err != nil {
		return rooms.Room{}, err
	} else if !ok {
		return rooms.Room{}, access.ErrForbidden
	}
	room, err := s.d.Rooms.Create(ctx, p, rooms.CreateInput{Name: roomName(chat)})
	if err != nil {
		return rooms.Room{}, err
	}
	for _, m := range []rooms.MemberInput{
		{Kind: dto.MemberKindChat, MemberID: chat.ID},
		{Kind: dto.MemberKindAgent, MemberID: agentID},
	} {
		if _, err := s.d.Rooms.AddMember(ctx, p, room.ID, m); err != nil {
			return rooms.Room{}, errors.Join(err, s.d.Rooms.Delete(ctx, p, room.ID))
		}
	}
	return s.d.Rooms.Get(ctx, p, room.ID)
}

// roomName is the chat title (or its id) cut to the room name limit.
func roomName(c db.Chat) string {
	name := strings.TrimSpace(c.Title)
	if name == "" {
		name = c.ExternalID
	}
	if r := []rune(name); len(r) > rooms.MaxNameLength {
		name = string(r[:rooms.MaxNameLength])
	}
	return name
}
