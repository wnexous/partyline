package connectors_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// mergeEnv is the inbound env with alice's AI Atlas and the history the retired inbound pipeline
// left: platform DMs between a person and an AI, with the person's chat as a member.
type mergeEnv struct {
	*inboundEnv
	atlasID string
	tok     *testkit.Client // alice's agent token
	person  string          // the person behind the DM chat, planted by the first platformDM
}

func newMergeEnv(t *testing.T) *mergeEnv {
	t.Helper()
	e := newInboundEnv(t) // no outbox worker: what is queued for a chat stays pending
	return &mergeEnv{inboundEnv: e, atlasID: e.alice.NewAI("atlas"), tok: e.alice.Token(dto.TokenKindAgent, "laptop")}
}

// plantPerson stores a person of the connector that no conversation involves and returns its
// intelligence id.
func plantPerson(t *testing.T, env *testkit.Env, connectorID, externalID, name string) string {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if _, err := intelligences.Create(ctx, q, intelligences.NewParams{ID: id, Kind: handles.Person, DisplayName: name, HandleBase: name, Now: time.Now()}); err != nil {
			return err
		}
		return q.CreatePerson(ctx, db.CreatePersonParams{ID: id, ConnectorID: connectorID, ExternalID: externalID, PlatformName: name, CreatedAt: time.Now().UnixMilli()})
	}))
	return id
}

// platformDM puts the DM chat in the DM between its person and the AI, as the retired pipeline
// did when a DM arrived while the AI operated the account, and stores what the person wrote. It
// returns the chat and the room.
func (e *mergeEnv) platformDM(t *testing.T, ai, externalMessage, body string) (chat, room string) {
	t.Helper()
	ctx := context.Background()
	c, err := connectors.UpsertChat(ctx, e.env.App.Store, e.env.App.Bus, e.conn.ID, connectors.ChatInfo{ID: dmA, Title: "Customer", Kind: "dm"}, time.Now(), time.Now())
	require.NoError(t, err)
	if e.person == "" {
		e.person = plantPerson(t, e.env, e.conn.ID, dmA, "Customer")
	}
	route, err := e.env.App.Conversations.EnsurePlatformDM(ctx, conversations.PlatformDM{
		OwnerID: e.alice.User.ID, ChatID: c.ID, Author: conversations.Party{ID: e.person}, Operator: conversations.Party{ID: ai, Kind: dto.MemberKindAgent},
	})
	require.NoError(t, err)
	_, err = e.env.App.Rooms.PostFromChat(ctx, route.RoomID, rooms.ChatMessage{
		ChatID: c.ID, SenderID: dmA, SenderName: "Customer", AuthorIID: e.person, ExternalID: externalMessage, Kind: dto.MessageKindText, Text: body,
	})
	require.NoError(t, err)
	return c.ID, route.RoomID
}

func (e *mergeEnv) roomsOf(t *testing.T, chatID string) []string {
	t.Helper()
	ids, err := e.env.App.Store.R().ListChatRoomIDs(context.Background(), chatID)
	require.NoError(t, err)
	return ids
}

func (e *mergeEnv) members(t *testing.T, id string) []dto.RoomMember {
	t.Helper()
	return testkit.Decode[dto.List[dto.RoomMember]](e.alice.Get("/api/v1/rooms/" + id + "/members").RequireStatus(http.StatusOK)).Items
}

func (e *mergeEnv) dmRooms(t *testing.T) int {
	return e.count(t, "SELECT COUNT(*) FROM rooms WHERE kind = 'dm'")
}

// mergeAI merges the AI from into the AI into as alice.
func (e *mergeEnv) mergeAI(t *testing.T, from, into string) {
	t.Helper()
	e.alice.Post("/api/v1/agents/"+from+"/merge", dto.MergeAgentRequest{Into: into}).RequireStatus(http.StatusOK)
}

// say has an AI of alice post in a room, the way its session would.
func (e *mergeEnv) say(t *testing.T, ai, room, text string) rooms.Message {
	t.Helper()
	m, err := e.env.App.Rooms.Post(context.Background(), authn.Principal{Kind: authn.KindAgent, UserID: e.alice.User.ID, AgentID: ai}, room, rooms.PostInput{Text: text})
	require.NoError(t, err)
	return m
}

// MRG-01 with CID-01: the AI that operates a connector is merged into another: the connector is
// operated by the survivor, and the person's platform DM history goes on in the same room, with
// its chat, the survivor now on the other side.
func TestMergeKeepsAPlatformDMRunning(t *testing.T) {
	e := newMergeEnv(t)
	oldID := e.env.NewLegacyAI(e.alice, "old")
	e.alice.OperateConnector(e.conn.ID, oldID)
	chat, room := e.platformDM(t, oldID, "A1", "to old")
	require.Equal(t, []string{room}, e.roomsOf(t, chat))

	e.mergeAI(t, oldID, e.atlasID)

	require.Equal(t, e.atlasID, e.connector(t).Operator.ID)
	require.Equal(t, []string{room}, e.roomsOf(t, chat), "the chat stays in its DM")
	got := testkit.Decode[dto.Room](e.alice.Get("/api/v1/rooms/" + room).RequireStatus(http.StatusOK))
	var participants []string
	for _, p := range got.Participants {
		participants = append(participants, p.ID)
	}
	require.Contains(t, participants, e.atlasID)
	require.NotContains(t, participants, oldID)
	kinds := map[dto.MemberKind]dto.RoomMember{}
	for _, m := range e.members(t, room) {
		kinds[m.Kind] = m
	}
	require.Equal(t, e.atlasID, kinds[dto.MemberKindAgent].MemberID)
	require.True(t, kinds[dto.MemberKindChat].Outbound, "replies still go through the chat")
	require.Equal(t, 1, e.dmRooms(t))

	reply := testkit.Decode[dto.Message](e.tok.AsAgent("atlas").Post("/api/v1/agent/rooms/"+room+"/messages", dto.PostMessageRequest{Text: "on it"}).RequireStatus(http.StatusCreated))
	require.Len(t, reply.Outbox, 1)
	require.Equal(t, chat, reply.Outbox[0].ChatID, "and the survivor's reply is queued for the person's chat")
}

// MRG-01: both AIs had a platform DM with the person, at different times. The merged AI's history is
// appended to the survivor's DM, the person's chat joins it, and what was queued for the chat stays
// queued.
func TestMergeAppendsAPlatformDMAndMovesItsChat(t *testing.T) {
	e := newMergeEnv(t)
	oldID := e.env.NewLegacyAI(e.alice, "old")

	// The person wrote to Atlas, then the account moved to old and they wrote to old, whose reply
	// waits in the outbox.
	chat, atlasDM := e.platformDM(t, e.atlasID, "A1", "to atlas")
	_, oldDM := e.platformDM(t, oldID, "A2", "to old")
	require.NotEqual(t, atlasDM, oldDM, "two DMs with the person, one per operator")
	waiting := e.say(t, oldID, oldDM, "old's answer")
	require.Len(t, waiting.Outbox, 1)
	require.Equal(t, 2, e.dmRooms(t))

	e.mergeAI(t, oldID, e.atlasID)

	require.Equal(t, 1, e.dmRooms(t))
	e.alice.Get("/api/v1/rooms/"+oldDM).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, []string{atlasDM}, e.roomsOf(t, chat), "the chat is in the DM that is left")
	var texts []string
	var answer dto.Message
	for _, m := range e.messages(t, atlasDM) { // newest first
		texts = append([]string{m.Text}, texts...)
		if m.Text == "old's answer" {
			answer = m
		}
	}
	require.Equal(t, []string{"to atlas", "History merged from @old", "to old", "old's answer"}, texts)
	require.Equal(t, e.atlasID, answer.Author.ID)
	require.Len(t, answer.Outbox, 1, "what was queued for the chat went with its message")
	require.Equal(t, []any{chat, dto.OutboxStatusPending}, []any{answer.Outbox[0].ChatID, answer.Outbox[0].Status})

	// ... and it can still be sent: the chat is a member of the room the message is in now.
	due, err := e.env.App.Store.R().ListDueOutbox(context.Background(), db.ListDueOutboxParams{Now: time.Now().Add(time.Hour).UnixMilli(), Limit: 10})
	require.NoError(t, err)
	var dueMessages []string
	for _, d := range due {
		require.Equal(t, atlasDM, d.RoomID)
		dueMessages = append(dueMessages, d.MessageID)
	}
	require.Contains(t, dueMessages, answer.ID)
}
