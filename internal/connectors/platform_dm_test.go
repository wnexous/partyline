package connectors_test

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// perChat is an inbound env whose connector of alice has no catch-all room and routes DMs per
// chat to its operator, alice's live AI Atlas (ACC-02).
type perChat struct {
	*inboundEnv
	atlasID string
	atlas   *testkit.AgentConn
	tok     *testkit.Client // alice's agent token
}

func newPerChat(t *testing.T, opts ...testkit.Option) *perChat {
	t.Helper()
	e := newInboundEnv(t, opts...)
	tok := e.alice.Token(dto.TokenKindAgent, "laptop")
	atlas, resp := tok.DialAgent("atlas")
	require.NotNil(t, atlas, "upgrade refused: %d", resp.Status)
	atlas.Welcome(connectortest.Timeout)
	pc := &perChat{inboundEnv: e, atlasID: e.env.NewAI(e.alice, "atlas"), atlas: atlas, tok: tok}
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeDMs), AutoRouteRoomID: strOf(""), OperatorID: &pc.atlasID})
	return pc
}

func scopeOf(s dto.AutoRouteScope) *dto.AutoRouteScope { return &s }
func strOf(s string) *string                           { return &s }

func (e *inboundEnv) patch(t *testing.T, req dto.UpdateConnectorRequest) dto.Connector {
	t.Helper()
	return testkit.Decode[dto.Connector](e.alice.Patch("/api/v1/connectors/"+e.conn.ID, req).RequireStatus(http.StatusOK))
}

// count runs a SELECT COUNT(*) on the database.
func (e *inboundEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(e.env.App.Config.DataDir, app.DatabaseFile)+"?mode=ro&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	var n int
	require.NoError(t, raw.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

func (e *inboundEnv) dmRooms(t *testing.T) int {
	return e.count(t, "SELECT COUNT(*) FROM rooms WHERE kind = 'dm'")
}

// roomsOf returns the ids of the rooms the chat is a member of.
func (e *inboundEnv) roomsOf(t *testing.T, chatID string) []string {
	t.Helper()
	ids, err := e.env.App.Store.R().ListChatRoomIDs(context.Background(), chatID)
	require.NoError(t, err)
	return ids
}

func (e *inboundEnv) room(t *testing.T, c *testkit.Client, id string) dto.Room {
	t.Helper()
	return testkit.Decode[dto.Room](c.Get("/api/v1/rooms/" + id).RequireStatus(http.StatusOK))
}

func (e *inboundEnv) members(t *testing.T, c *testkit.Client, id string) []dto.RoomMember {
	t.Helper()
	return testkit.Decode[dto.List[dto.RoomMember]](c.Get("/api/v1/rooms/" + id + "/members").RequireStatus(http.StatusOK)).Items
}

// ACC-02 (independent test): a person's platform DM to a connector operated by an AI reaches
// the AI as a DM between the person and the AI, created once; the AI sees the person's name and
// platform, the message is untrusted, and the AI's owner can read the DM but is not in it.
func TestPlatformDMReachesTheOperatorAI(t *testing.T) {
	pc := newPerChat(t)

	pc.emit(dmText(dmA, "A1", "oi, quero um orçamento"))

	got := pc.atlas.Message(connectortest.Timeout)
	require.Equal(t, dto.ConversationKindDM, got.Room.Kind)
	require.Equal(t, "Customer", got.Room.Name, "a DM is named after the other participant")
	require.Equal(t, "oi, quero um orçamento", got.Message.Text)
	require.Equal(t, dto.AuthorKindChat, got.Message.AuthorKind)
	person := got.Message.Author
	require.Equal(t, []any{dto.IntelligenceRolePerson, "Customer", connectortest.KindName}, []any{person.Role, person.DisplayName, person.Platform})
	require.False(t, got.Trusted, "a person is not the AI's owner")

	chat := pc.chatID(t, dmA)
	require.Equal(t, []string{got.Room.ID}, pc.roomsOf(t, chat), "the chat is in the DM and nowhere else")
	require.Equal(t, 1, pc.dmRooms(t))

	room := pc.room(t, pc.alice, got.Room.ID)
	require.Equal(t, dto.ConversationKindDM, room.Kind)
	require.Equal(t, dto.LevelView, room.MyLevel, "the AI's owner reads its DMs (DM-08)")
	require.Len(t, room.Participants, 2)
	ids := []string{room.Participants[0].ID, room.Participants[1].ID}
	require.ElementsMatch(t, []string{person.ID, pc.atlasID}, ids)

	members := pc.members(t, pc.alice, got.Room.ID)
	require.Len(t, members, 2)
	kinds := map[dto.MemberKind]dto.RoomMember{}
	for _, m := range members {
		kinds[m.Kind] = m
	}
	require.Equal(t, pc.atlasID, kinds[dto.MemberKindAgent].MemberID)
	require.Equal(t, chat, kinds[dto.MemberKindChat].MemberID)
	require.True(t, kinds[dto.MemberKindChat].Outbound, "replies go back through the chat")

	// More messages go to the same DM; nobody else sees it.
	pc.emit(dmText(dmA, "A2", "alguém aí?"))
	pc.emit(dmText(dmA, "A3", "ping"))
	require.Equal(t, got.Room.ID, pc.atlas.Message(connectortest.Timeout).Room.ID)
	require.Equal(t, got.Room.ID, pc.atlas.Message(connectortest.Timeout).Room.ID)
	require.Equal(t, 1, pc.dmRooms(t))
	require.Len(t, pc.messages(t, got.Room.ID), 3)
	bob := pc.env.NewUser("bob")
	bob.Get("/api/v1/rooms/"+got.Room.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// ACC-02 (concurrency, DM-01): first messages of one new DM chat arriving at once create one DM
// and store every message.
func TestPlatformDMConcurrentFirstMessagesCreateOneDM(t *testing.T) {
	pc := newPerChat(t)
	var wg sync.WaitGroup
	for _, id := range []string{"A1", "A2", "A3", "A4"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pc.inst.Emit(context.Background(), dmText(dmA, id, "burst "+id))
		}()
	}
	wg.Wait()

	require.Equal(t, 1, pc.dmRooms(t))
	rooms := pc.roomsOf(t, pc.chatID(t, dmA))
	require.Len(t, rooms, 1)
	require.Len(t, pc.messages(t, rooms[0]), 4)
}

// ACC-02: scope off routes nothing; scope groups routes no DMs; a chat is recorded either way
// and its sender is not (CON-07); and a chat the owner routed by hand is left where it is.
func TestPlatformDMScopes(t *testing.T) {
	pc := newPerChat(t)

	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeOff)})
	pc.emit(dmText(dmA, "A1", "while off"))
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeGroups)})
	pc.emit(dmText(dmB, "B1", "while groups"))
	require.Zero(t, pc.dmRooms(t))
	require.Empty(t, pc.people(t), "unrouted senders are not recorded")
	require.True(t, pc.hasChat(t, dmA))
	require.True(t, pc.hasChat(t, dmB))
	pc.atlas.ExpectNone(300 * time.Millisecond)

	// Every scope that includes DMs routes them.
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeAll)})
	pc.emit(dmText(dmA, "A2", "while all"))
	require.Equal(t, "while all", pc.atlas.Message(connectortest.Timeout).Message.Text)
	require.Equal(t, 1, pc.dmRooms(t))

	// A chat the owner put in a room herself stays there: no DM for it.
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeOff)})
	pc.emit(dmText(dmC, "C0", "first"))
	pc.routedRoom(t, "support", pc.chatID(t, dmC))
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeAll)})
	before := pc.dmRooms(t)
	pc.emit(dmText(dmC, "C1", "in the support room"))
	require.Equal(t, before, pc.dmRooms(t))
	require.Len(t, pc.roomsOf(t, pc.chatID(t, dmC)), 1)
	pc.atlas.ExpectNone(300 * time.Millisecond)
}

// ACC-02 / spec issue 2: a connector that still has a catch-all room keeps routing new chats
// into that room; no DM is made for them.
func TestPlatformDMNotUsedWhileTheConnectorHasACatchAllRoom(t *testing.T) {
	pc := newPerChat(t)
	room := testkit.Decode[dto.Room](pc.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "catch-all"}).RequireStatus(http.StatusCreated))
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeDMs), AutoRouteRoomID: &room.ID})

	pc.emit(dmText(dmA, "A1", "to the catch-all room"))

	require.Equal(t, []string{room.ID}, pc.roomsOf(t, pc.chatID(t, dmA)))
	require.Zero(t, pc.dmRooms(t))
	require.Len(t, pc.messages(t, room.ID), 1)
}

// ACC-02 (spec edge case, issue 2): the operator can change; the chat's next message goes to the
// DM between the person and the new operator, the old DM keeps its history and no longer
// receives the chat, a later switch back reuses the old DM, and a deleted operator AI means the
// owner.
func TestPlatformDMFollowsTheOperator(t *testing.T) {
	pc := newPerChat(t)
	pc.emit(dmText(dmA, "A1", "to Atlas"))
	atlasDM := pc.atlas.Message(connectortest.Timeout).Room.ID
	chat := pc.chatID(t, dmA)

	// Atlas answers; the answer is queued for the chat but not sent yet (no outbox worker here).
	reply := testkit.Decode[dto.Message](pc.tok.AsAgent("atlas").Post("/api/v1/agent/rooms/"+atlasDM+"/messages", dto.PostMessageRequest{Text: "on it"}).RequireStatus(http.StatusCreated))
	require.Len(t, reply.Outbox, 1)

	// The owner operates the account herself: the next message is a DM with her.
	pc.patch(t, dto.UpdateConnectorRequest{OperatorID: strOf("")})
	pc.emit(dmText(dmA, "A2", "to alice"))
	rooms := pc.roomsOf(t, chat)
	require.Len(t, rooms, 1)
	aliceDM := rooms[0]
	require.NotEqual(t, atlasDM, aliceDM)
	room := pc.room(t, pc.alice, aliceDM)
	require.Equal(t, dto.LevelPost, room.MyLevel, "alice is a participant of this DM")
	require.Len(t, room.Participants, 2)
	require.Equal(t, 2, pc.dmRooms(t))
	require.Len(t, pc.messages(t, atlasDM), 2, "the old DM keeps what was said in it")
	for _, m := range pc.messages(t, atlasDM) {
		require.Empty(t, m.Outbox, "what was queued for the chat in the old DM is cancelled (ROOM-01): %s", m.Text)
	}
	require.Len(t, pc.messages(t, aliceDM), 1)
	for _, m := range pc.members(t, pc.alice, atlasDM) {
		require.NotEqual(t, dto.MemberKindChat, m.Kind, "the chat left the old DM")
	}
	pc.atlas.ExpectNone(300 * time.Millisecond)

	// Back to Atlas: its DM with the person is reused.
	pc.patch(t, dto.UpdateConnectorRequest{OperatorID: &pc.atlasID})
	pc.emit(dmText(dmA, "A3", "to Atlas again"))
	require.Equal(t, atlasDM, pc.atlas.Message(connectortest.Timeout).Room.ID)
	require.Equal(t, []string{atlasDM}, pc.roomsOf(t, chat))
	require.Equal(t, 2, pc.dmRooms(t))
	require.Len(t, pc.messages(t, atlasDM), 3)

	// Atlas is deleted: its DMs go with it and the owner operates the account again.
	pc.alice.Delete("/api/v1/agents/" + pc.atlasID).RequireStatus(http.StatusNoContent)
	pc.emit(dmText(dmA, "A4", "Atlas is gone"))
	rooms = pc.roomsOf(t, chat)
	require.Equal(t, []string{aliceDM}, rooms, "the DM with the owner")
	require.Len(t, pc.messages(t, aliceDM), 2)
}

// logSink collects what the app logs.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logSink) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// ACC-02 (contract-notes T30, L-019): a DM chat message whose sender has no id has nobody to make a
// DM with, so it is not routed: the chat is recorded, no DM, person or message is made, the
// operator AI receives nothing and nothing fails (a DM with an empty participant would be refused
// by the database and logged as a failed message). A later message of the chat that names its
// sender is routed as usual.
func TestPlatformDMWithoutASenderIsNotRouted(t *testing.T) {
	logs := &logSink{}
	pc := newPerChat(t, testkit.WithLogger(slog.New(slog.NewTextHandler(logs, nil))))
	stored := pc.messageCount(t)

	anonymous := dmText(dmA, "N1", "who writes?")
	anonymous.SenderID, anonymous.SenderAltID, anonymous.SenderName = "", "", ""
	pc.emit(anonymous)

	require.True(t, pc.hasChat(t, dmA), "the chat is recorded")
	require.Empty(t, pc.roomsOf(t, pc.chatID(t, dmA)), "and sits in no room")
	require.Zero(t, pc.dmRooms(t))
	require.Empty(t, pc.people(t))
	require.Equal(t, stored, pc.messageCount(t), "the message is not stored")
	pc.atlas.ExpectNone(300 * time.Millisecond)
	require.NotContains(t, logs.String(), "inbound message failed", "leaving it unrouted is no failure")

	pc.emit(dmText(dmA, "A1", "now with a sender"))
	require.Equal(t, "now with a sender", pc.atlas.Message(connectortest.Timeout).Message.Text)
	require.Equal(t, 1, pc.dmRooms(t))
}

// ACC-02 (no self-DM): a sender who is the operator gets no DM with themself; the chat is
// recorded and its message is not stored. A sender linked to the owner is still a DM partner of
// the owner's AI, and is trusted by it.
func TestPlatformDMNeverMakesADMWithOneself(t *testing.T) {
	pc := newPerChat(t)
	pc.patch(t, dto.UpdateConnectorRequest{OperatorID: strOf("")})
	code := testkit.Decode[dto.PairingCode](pc.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: pc.conn.ID}).RequireStatus(http.StatusCreated))
	pc.emit(dmText(dmA, "L0", "!link "+code.Code))

	before := pc.messageCount(t)
	pc.emit(dmText(dmA, "A1", "alice writes to her own account"))
	require.Zero(t, pc.dmRooms(t))
	require.Equal(t, before, pc.messageCount(t))
	require.True(t, pc.hasChat(t, dmA))
	require.Empty(t, pc.people(t), "an unrouted sender is not recorded")

	pc.patch(t, dto.UpdateConnectorRequest{OperatorID: &pc.atlasID})
	pc.emit(dmText(dmA, "A2", "alice writes to Atlas"))
	got := pc.atlas.Message(connectortest.Timeout)
	require.True(t, got.Trusted, "the owner's verified account is trusted")
	require.Equal(t, pc.alice.User.ID, got.Message.Author.ID, "authored by alice's intelligence")
	require.Equal(t, "alice", got.Room.Name)
	room := pc.room(t, pc.alice, got.Room.ID)
	require.Equal(t, dto.LevelPost, room.MyLevel, "alice is a participant")
	require.Equal(t, 1, pc.dmRooms(t))
}

// ACC-06 (cap): a connector routes at most 60 new chats per hour; the 61st is not recorded as a
// chat, nothing is stored and the connector says so; an hour later it is routed.
func TestPlatformDMHourlyCap(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	pc := newPerChat(t, testkit.WithClock(clock.Now))
	for i := range 60 {
		pc.emit(dmText(dmPeer(i), "M"+string(rune('A'+i%26))+string(rune('a'+i/26)), "hello"))
	}
	require.Equal(t, 60, pc.dmRooms(t))
	require.Empty(t, pc.connector(t).LastError)

	before := pc.messageCount(t)
	pc.emit(dmText(dmPeer(60), "M60", "one too many"))
	require.Equal(t, 60, pc.dmRooms(t))
	require.False(t, pc.hasChat(t, dmPeer(60)), "a capped chat leaves no chat record")
	require.Equal(t, before, pc.messageCount(t))
	require.Equal(t, "auto-route limit reached", pc.connector(t).LastError)
	pc.emit(dmText(dmPeer(0), "M0b", "already routed"))
	require.Equal(t, before+1, pc.messageCount(t), "routed chats keep flowing")

	clock.Advance(time.Hour + time.Second)
	pc.emit(dmText(dmPeer(60), "M60b", "an hour later"))
	require.Equal(t, 61, pc.dmRooms(t))
	require.Empty(t, pc.connector(t).LastError, "the next successful route clears the detail")
}

// DM-05: only the account's operator reaches the person through its platform DM: the operator AI
// opens that DM (never creating one) and its message goes out through the chat; another AI of the
// same owner gets 422 no_route_to_person, an AI whose owner cannot see the person 404 contact_not_found,
// and a person nobody routed yet is no_route_to_person for the operator too.
func TestSendToAPersonOnlyThroughTheOperator(t *testing.T) {
	pc := newPerChat(t)
	pc.emit(dmText(dmA, "A1", "hello"))
	got := pc.atlas.Message(connectortest.Timeout)
	person := got.Message.Author
	handle := "@" + person.Handle

	// Atlas, the operator, answers through the platform DM.
	msg := testkit.Decode[dto.Message](pc.tok.AsAgent("atlas").Post("/api/v1/agent/dms", dto.AgentDMRequest{To: handle, Text: "olá, tudo bem?"}).RequireStatus(http.StatusCreated))
	require.Equal(t, got.Room.ID, msg.RoomID, "the DM the person's chat is in")
	require.Equal(t, dto.AuthorKindAgent, msg.AuthorKind)
	require.Len(t, msg.Outbox, 1, "queued for the person's chat")
	require.Equal(t, pc.chatID(t, dmA), msg.Outbox[0].ChatID)
	require.Equal(t, 1, pc.dmRooms(t))
	// by id too
	again := testkit.Decode[dto.Message](pc.tok.AsAgent("atlas").Post("/api/v1/agent/dms", dto.AgentDMRequest{To: person.ID, Text: "second"}).RequireStatus(http.StatusCreated))
	require.Equal(t, got.Room.ID, again.RoomID)

	// Another AI of the owner has no route; so has the owner herself while an AI operates it.
	sage := pc.env.NewAI(pc.alice, "sage")
	_ = sage
	pc.tok.AsAgent("sage").Post("/api/v1/agent/dms", dto.AgentDMRequest{To: handle, Text: "hi"}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson)
	pc.alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: person.ID}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson)
	require.Equal(t, 1, pc.dmRooms(t), "refusals create nothing")

	// A person nobody has routed: not even Atlas has a DM with it.
	other := plantPerson(t, pc.env, pc.conn.ID, "5511999990099@s.whatsapp.net", "Never Routed")
	pc.tok.AsAgent("atlas").Post("/api/v1/agent/dms", dto.AgentDMRequest{To: other, Text: "hi"}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson)

	// An AI whose owner cannot see the person: contact_not_found; once the owner may use the
	// connector the person is visible but still not reachable by that owner's AIs.
	bob := pc.env.NewUser("bob")
	bobTok := bob.Token(dto.TokenKindAgent, "desktop")
	pc.env.NewAI(bob, "bobby")
	bobTok.AsAgent("bobby").Post("/api/v1/agent/dms", dto.AgentDMRequest{To: handle, Text: "hi"}).RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	pc.alice.Put("/api/v1/connectors/"+pc.conn.ID+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: bob.User.ID, Level: dto.LevelUse}}}).RequireStatus(http.StatusOK)
	bobTok.AsAgent("bobby").Post("/api/v1/agent/dms", dto.AgentDMRequest{To: handle, Text: "hi"}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson)

	// Users who cannot see the person are told nothing about it.
	for _, name := range []string{"carol", "dave"} {
		pc.env.NewUser(name).Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: person.ID}).RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	}
	pc.env.NewAdmin("root").Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: person.ID}).RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)

	// The operator changes: the old operator loses the route, the new one gets it.
	pc.patch(t, dto.UpdateConnectorRequest{OperatorID: strOf("")})
	pc.tok.AsAgent("atlas").Post("/api/v1/agent/dms", dto.AgentDMRequest{To: handle, Text: "still there?"}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson)
	pc.emit(dmText(dmA, "A2", "I am talking to alice now"))
	room := testkit.Decode[dto.Room](pc.alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: person.ID}).RequireStatus(http.StatusOK))
	require.NotEqual(t, got.Room.ID, room.ID, "alice's DM with the person")
	require.Equal(t, 2, pc.dmRooms(t), "opening it created nothing")
	posted := testkit.Decode[dto.Message](pc.alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "sou a Alice"}).RequireStatus(http.StatusCreated))
	require.Len(t, posted.Outbox, 1)
	require.Equal(t, pc.chatID(t, dmA), posted.Outbox[0].ChatID)
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
