package connectors

import (
	"context"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store/db"
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

// PlatformLabel is the display name of a connector kind (WhatsApp, Telegram, Discord,
// Webhook); other kinds get their name capitalized.
func PlatformLabel(kind string) string {
	switch kind {
	case "whatsapp":
		return "WhatsApp"
	case "telegram":
		return "Telegram"
	case "discord":
		return "Discord"
	case "webhook":
		return "Webhook"
	}
	if kind == "" {
		return "Chat"
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}
