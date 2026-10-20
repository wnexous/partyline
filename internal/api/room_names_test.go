package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// CTX-07: a routed room is named after the chat title; a chat without a title gives
// "<Platform> chat" (never the raw platform id); long titles are cut to the room name limit.
func TestRoutedRoomIsNamedAfterTheChat(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	agent := registerAgent(t, env, alice, "helper")
	ctx := context.Background()
	chat := func(kind, title string) string {
		conn, id := store.NewID(), store.NewID()
		require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
			if err := q.CreateConnector(ctx, db.CreateConnectorParams{ID: conn, OwnerID: alice.User.ID, Kind: kind, Name: kind, ConfigJson: "{}"}); err != nil {
				return err
			}
			return q.CreateChat(ctx, db.CreateChatParams{ID: id, ConnectorID: conn, ExternalID: "acme|120363000000000001@g.us", Title: title, Kind: "group"})
		}))
		return id
	}
	route := func(chatID string) string {
		return testkit.Decode[dto.Room](alice.Post("/api/v1/chats/"+chatID+"/route", dto.RouteChatRequest{AgentID: agent}).RequireStatus(http.StatusCreated)).Name
	}

	require.Equal(t, "Lojinha Sintética", route(chat("whatsapp", "Lojinha Sintética")))
	require.Equal(t, "WhatsApp chat", route(chat("whatsapp", "")))
	require.Equal(t, "Telegram chat", route(chat("telegram", "  ")))
	require.Equal(t, "Discord chat", route(chat("discord", "")))
	require.Equal(t, "Webhook chat", route(chat("webhook", "")))
	require.Equal(t, strings.Repeat("t", 100), route(chat("telegram", strings.Repeat("t", 120))))
}
