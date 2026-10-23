package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
)

// v03Database writes the store's v0.3 fixture (schema 0014) to a new database file.
func v03Database(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v03.db")
	st, err := store.Open(path)
	require.NoError(t, err)
	require.NoError(t, st.MigrateBefore(ctx, "0015"))
	require.NoError(t, st.Close())
	fixture, err := os.ReadFile("../store/testdata/v03_fixture.sql")
	require.NoError(t, err)
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	_, err = raw.ExecContext(ctx, string(fixture))
	require.NoError(t, err)
	require.NoError(t, raw.Close())
	return path
}

// GRP-01, MIG-01, MIG-02: after the upgrade, the first session in a folder called atlas adopts
// the v0.3 agent atlas, and the agent's reply to an old message of the v0.3 inbox room is stored in
// the room, quoting it. It goes out to no chat, and the delivery the v0.3 outbox left pending is not sent
// either: a room sends nothing to a platform any more (CID-22), and the outbox went with the upgrade
// (0018). (The catch-all routing of new chats
// is retired, CID-07: what arrives on the connector is queued for its operator, which the upgrade
// end-to-end test pins, T37.)
func TestUpgradedAgentAdoptsItsFolderAndRepliesInTheRoom(t *testing.T) {
	ctx := context.Background()
	fake := connectortest.New()
	telegram := fake.Kind()
	telegram.Name = "telegram" // the fixture's connector kind, served by the fake
	// The app's clock starts an hour after the fixture's last message, so that the retention sweep at
	// start-up (30 days in the inbox room) keeps its history whatever today is.
	afterTheFixture := func() time.Time { return time.UnixMilli(1790003600000) }
	env := testkit.New(t, testkit.WithDatabase(v03Database(t)), testkit.WithConnectorKinds(telegram), testkit.WithOutbox(), testkit.WithClock(afterTheFixture))
	// v0.3 connectors were operated by their owner; a connection runs once an AI operates it
	// (CID-02), so here the adopted agent takes it over.
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetConnectorOperator(ctx, db.SetConnectorOperatorParams{OperatorIid: sql.NullString{String: "a-atlas", Valid: true}, ID: "k-tg"})
	}))
	env.App.ConnectorManager.Apply("k-tg")

	inst := fake.Instance(t, "k-tg")
	inst.WaitRun(t)
	inst.Connected()
	_, secret, err := env.App.Users.CreateToken(ctx, "u-alice", users.TokenInput{Kind: dto.TokenKindAgent, Name: "devbox"})
	require.NoError(t, err)
	alice := env.Anon().WithBearer(secret)
	agent, _ := alice.DialAgent("atlas")
	require.NotNil(t, agent)
	require.Equal(t, "a-atlas", agent.Welcome(connectortest.Timeout).Agent.ID, "the folder adopted the v0.3 agent")
	require.Equal(t, "r-ops", agent.Notice(connectortest.Timeout).Room.ID, "ops keeps its instructions")

	reply := testkit.Decode[dto.Message](alice.AsAgent("atlas").
		Post("/api/v1/agent/messages/m-in3/reply", dto.ReplyRequest{Text: "tem sim, às 10h"}).
		RequireStatus(http.StatusCreated))
	require.Equal(t, "a-atlas", reply.AuthorID)
	require.Equal(t, "r-inbox", reply.RoomID)
	require.Equal(t, "m-in3", reply.ReplyTo)
	require.Equal(t, "tem sim, às 10h", reply.Text)
	require.Empty(t, reply.Recipients)
	require.Never(t, func() bool { return len(inst.Sent()) > 0 }, 1500*time.Millisecond, 50*time.Millisecond,
		"neither the reply nor the delivery v0.3 left pending reaches a chat")
	require.Zero(t, countRows(t, env, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'outbox'"), "the pending v0.3 delivery went with its table")
	require.Zero(t, countRows(t, env, "SELECT COUNT(*) FROM platform_sends"), "nothing was queued to send")

	// History is left as it was: the answer v0.3 sent to a chat still names the chat it was for.
	history := testkit.Decode[dto.Page[dto.AgentMessage]](alice.AsAgent("atlas").Get("/api/v1/agent/rooms/r-inbox/messages").RequireStatus(http.StatusOK))
	var answered dto.Message
	for _, m := range history.Items {
		if m.Message.ID == "m-in2" {
			answered = m.Message
		}
	}
	require.Equal(t, []string{"c-tgdm"}, answered.Recipients, "a message from before the upgrade keeps the chats it named")
}

// MIG-01, GRP-01: after the upgrade (0015 and 0016 in one transaction, the legacy name columns
// gone) the timelines of the v0.3 rooms still name every author: users and AIs by their profile,
// the display name a member had in the room, what a project member called its session, a person
// by the name the platform reported, and a chat without a sender by its title.
func TestUpgradedTimelinesNameEveryAuthor(t *testing.T) {
	ctx := context.Background()
	// The app's clock stands an hour after the fixture's last message, so that the retention
	// sweep at start-up (30 days in the catch-all room) keeps its history whatever today is.
	afterTheFixture := func() time.Time { return time.UnixMilli(1790003600000) }
	env := testkit.New(t, testkit.WithDatabase(v03Database(t)), testkit.WithClock(afterTheFixture))
	_, secret, err := env.App.Users.CreateToken(ctx, "u-alice", users.TokenInput{Kind: dto.TokenKindUser, Name: "web"})
	require.NoError(t, err)
	alice := env.Anon().WithBearer(secret)

	for room, want := range map[string]map[string]string{
		"r-ops": {
			"m-ops1": "Alice A.", "m-ops2": "Atlas Ops", "m-ops3": "Bob.Smith", "m-ops4": "Zé", "m-ops5": "Team",
			"m-ops6": "Partyline", "m-ops7": "Proj Bot",
		},
		"r-inbox": {"m-in1": "Maria Silva", "m-in2": "atlas", "m-in3": "Maria Silva"},
		"r-dev":   {"m-dev1": "Alice A.", "m-dev2": "Mine"},
	} {
		page := testkit.Decode[dto.Page[dto.Message]](alice.Get("/api/v1/rooms/" + room + "/messages").RequireStatus(http.StatusOK))
		got := map[string]string{}
		for _, m := range page.Items {
			got[m.ID] = m.AuthorName
		}
		require.Equal(t, want, got, room)
	}
}

// v04Database writes what a v0.4 installation holds: the store's v0.3 fixture upgraded to schema 0016, the
// last schema of v0.4, plus a room "Team" of the owner that held her, two of her AIs and the fixture's
// WhatsApp group "Team" as members, with a message from the group and the answer of an AI. The app that
// opens it applies the connection proxy upgrade (0017 and 0018).
func v04Database(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := v03Database(t)
	st, err := store.Open(path)
	require.NoError(t, err)
	require.NoError(t, st.MigrateBefore(ctx, "0017"))
	require.NoError(t, st.Close())
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.ExecContext(ctx, `
INSERT INTO rooms (id, owner_id, name, description, retention_days, next_seq, created_at, updated_at, updated_by)
VALUES ('r-team', 'u-alice', 'Team', 'The support team', 90, 3, 1790000000100, 1790000000100, 'u-alice');
INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES
    ('rm-team-alice', 'r-team', 'user', 'u-alice', 'u-alice', 1790000000101),
    ('rm-team-atlas', 'r-team', 'agent', 'a-atlas', 'u-alice', 1790000000102),
    ('rm-team-pc2', 'r-team', 'agent', 'a-pc2', 'u-alice', 1790000000103),
    ('rm-team-wa', 'r-team', 'chat', 'c-wagrp', 'u-alice', 1790000000104);
INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_user_id, author_iid, sender_id, sender_name,
                      kind, text, reply_to, recipients_json, external_id, created_at) VALUES
    ('m-team1', 'r-team', 1, 'chat', 'c-wagrp', '', '', '100000000000002@lid', 'Zé', 'text', 'anyone there?', '', '[]', 'wa-90', 1790000000105),
    ('m-team2', 'r-team', 2, 'agent', 'a-atlas', 'u-alice', 'a-atlas', '', '', 'text', 'yes, I am', 'm-team1', '["c-wagrp"]', NULL, 1790000000106);`)
	require.NoError(t, err)
	return path
}

// teamGroup is the platform id of the fixture's WhatsApp group "Team" (chat c-wagrp of connection k-wa).
const teamGroup = "120363000000000001@g.us"

// nextPlatformEvent reads frames until a platform event arrives, skipping the room frames before it.
func nextPlatformEvent(t *testing.T, conn *testkit.AgentConn) dto.PlatformEvent {
	t.Helper()
	for {
		f := conn.Next(connectortest.Timeout)
		if f.Type != dto.AgentFrameTypePlatform {
			continue
		}
		var p dto.AgentPlatformFrame
		require.NoError(t, json.Unmarshal(f.Raw, &p))
		return p.Event
	}
}

// platformEventsOf returns the platform events among the frames.
func platformEventsOf(t *testing.T, frames []testkit.AgentFrame) []dto.PlatformEvent {
	t.Helper()
	var out []dto.PlatformEvent
	for _, f := range frames {
		if f.Type != dto.AgentFrameTypePlatform {
			continue
		}
		var p dto.AgentPlatformFrame
		require.NoError(t, json.Unmarshal(f.Raw, &p))
		out = append(out, p.Event)
	}
	return out
}

// CID-21 (independent test), CID-02, CID-06, CID-07, CID-12, CID-27: the app boots on a v0.4 database. The room
// that held a WhatsApp group chat, the owner and two AIs lists the owner and the two AIs and no chat, and keeps
// its history. The owner-operated WhatsApp connection has no operator and runs nothing until the owner chooses
// an AI; then the next message of the group that mentions the account reaches only that AI's session, and
// no room, person or conversation is made for it. A group message that does not mention the account is not
// delivered (the upgrade's defaults), a direct message is.
func TestUpgradedWhatsAppRoomLosesItsChatAndItsGroupReachesOnlyTheOperator(t *testing.T) {
	ctx := context.Background()
	fake := connectortest.New()
	whatsapp := fake.Kind()
	whatsapp.Name = "whatsapp" // the fixture's connector kind, served by the fake
	// The app's clock starts an hour after the fixture's last message, so that the retention sweep at
	// start-up keeps its history whatever today is.
	afterTheFixture := func() time.Time { return time.UnixMilli(1790003600000) }
	env := testkit.New(t, testkit.WithDatabase(v04Database(t)), testkit.WithConnectorKinds(whatsapp), testkit.WithClock(afterTheFixture))
	token := func(kind dto.TokenKind) *testkit.Client {
		_, secret, err := env.App.Users.CreateToken(ctx, "u-alice", users.TokenInput{Kind: kind, Name: "alice " + string(kind)})
		require.NoError(t, err)
		return env.Anon().WithBearer(secret)
	}
	alice, sessions := token(dto.TokenKindUser), token(dto.TokenKindAgent)

	// The room holds its owner and the two AIs and no chat (CID-21); its history is still there.
	room := testkit.Decode[dto.Room](alice.Get("/api/v1/rooms/r-team").RequireStatus(http.StatusOK))
	require.Equal(t, "u-alice", room.Owner.ID)
	require.Equal(t, 3, room.MemberCount)
	type who struct {
		Kind dto.MemberKind
		ID   string
	}
	var members []who
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](alice.Get("/api/v1/rooms/r-team/members").RequireStatus(http.StatusOK)).Items {
		members = append(members, who{m.Kind, m.MemberID})
	}
	require.Equal(t, []who{{dto.MemberKindUser, "u-alice"}, {dto.MemberKindAgent, "a-atlas"}, {dto.MemberKindAgent, "a-pc2"}}, members, "the owner and the two AIs, and no chat")
	authors := map[string]string{}
	for _, m := range testkit.Decode[dto.Page[dto.Message]](alice.Get("/api/v1/rooms/r-team/messages").RequireStatus(http.StatusOK)).Items {
		authors[m.ID] = m.AuthorName
	}
	require.Equal(t, map[string]string{"m-team1": "Zé", "m-team2": "atlas"}, authors, "the history, the group's message included, is kept")
	chats := testkit.Decode[dto.List[dto.Chat]](alice.Get("/api/v1/connectors/k-wa/chats").RequireStatus(http.StatusOK)).Items
	require.Len(t, chats, 1, "the connection still knows its group")
	require.Equal(t, "Team", chats[0].Title)

	// The connection has no operator: it receives nothing and says what to do (CID-02), with the
	// delivery defaults of the upgrade (CID-27).
	connection := func() dto.Connector {
		return testkit.Decode[dto.Connector](alice.Get("/api/v1/connectors/k-wa").RequireStatus(http.StatusOK))
	}
	idle := connection()
	require.Nil(t, idle.Operator)
	require.Equal(t, []any{dto.ConnectorStatusError, dto.ConnectorNoOperator}, []any{idle.Status, idle.LastError})
	require.Equal(t, []any{dto.ConnectorDeliverAll, true, int64(0)}, []any{idle.Deliver, idle.GroupsMentionsOnly, idle.Queued})
	require.Nil(t, fake.Lookup("k-wa"), "nothing runs until an AI operates the connection")

	// The owner chooses Atlas. The connection starts, and both AIs of the room connect.
	chosen := "a-atlas"
	operated := testkit.Decode[dto.Connector](alice.Patch("/api/v1/connectors/k-wa", dto.UpdateConnectorRequest{OperatorID: &chosen}).RequireStatus(http.StatusOK))
	require.NotNil(t, operated.Operator)
	require.Equal(t, "a-atlas", operated.Operator.ID)
	inst := fake.Instance(t, "k-wa")
	inst.WaitRun(t)
	inst.Connected()
	dial := func(name, id string) *testkit.AgentConn {
		t.Helper()
		conn, resp := sessions.DialPlatform(name)
		require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
		w := conn.Welcome(connectortest.Timeout)
		require.Equal(t, id, w.Agent.ID, "the folder adopted the v0.3 agent")
		return conn
	}
	atlas, other := dial("atlas", "a-atlas"), dial("proj-claude-2", "a-pc2")
	for name, conn := range map[string]*testkit.AgentConn{"atlas": atlas, "proj-claude-2": other} {
		require.Empty(t, platformEventsOf(t, conn.Drain(300*time.Millisecond)), "%s: the queue starts empty (the room backlog is not a platform event)", name)
	}
	messages := func() int { return countRows(t, env, "SELECT COUNT(*) FROM messages") }
	rooms, people, before := countRows(t, env, "SELECT COUNT(*) FROM rooms"), countRows(t, env, "SELECT COUNT(*) FROM people"), messages()

	// The next message of the group that mentions the account reaches Atlas alone (CID-06, CID-12).
	inst.Emit(ctx, connectors.Inbound{
		ChatID: teamGroup, ChatTitle: "Team", ChatKind: "group", MessageID: "wamid.up1",
		SenderID: "5511999990077@s.whatsapp.net", SenderName: "Rita", Text: "can you check the invoice?", MentionsAccount: true, At: afterTheFixture(),
	})
	event := nextPlatformEvent(t, atlas)
	require.NotEmpty(t, event.ID)
	require.Equal(t, dto.PlatformEvent{
		ID: event.ID, Platform: "whatsapp", ConnectionID: "k-wa", ChatID: "c-wagrp", ChatTitle: "Team", ChatKind: dto.ChatKindGroup,
		SenderName: "Rita", SenderID: "5511999990077@s.whatsapp.net", Trusted: false, FromSelf: false, Kind: dto.MessageKindText,
		Text: "can you check the invoice?", Attachments: []dto.Attachment{}, ReceivedAt: event.ReceivedAt,
	}, event)
	require.Empty(t, platformEventsOf(t, other.Drain(300*time.Millisecond)), "the other AI of the room hears nothing")
	require.Equal(t, int64(1), connection().Queued, "it waits for Atlas to acknowledge it, and the page shows a count, not the text")
	require.NotContains(t, string(alice.Get("/api/v1/connectors/k-wa").Body), "invoice")

	// It is stored in no room, and no person or conversation is made for it (CID-07).
	require.Equal(t, before, messages(), "no message of any room")
	require.Equal(t, rooms, countRows(t, env, "SELECT COUNT(*) FROM rooms"))
	require.Equal(t, people, countRows(t, env, "SELECT COUNT(*) FROM people"))
	team := testkit.Decode[dto.Page[dto.Message]](alice.Get("/api/v1/rooms/r-team/messages").RequireStatus(http.StatusOK))
	require.Len(t, team.Items, 2, "the room is as the upgrade left it")

	atlas.AckPlatform(event.ID)
	require.Eventually(t, func() bool { return connection().Queued == 0 }, connectortest.Timeout, 10*time.Millisecond, "the acknowledgement empties the queue")

	// The upgrade's defaults: a group message that does not mention the account stops here, a direct one does not.
	inst.Emit(ctx, connectors.Inbound{
		ChatID: teamGroup, ChatTitle: "Team", ChatKind: "group", MessageID: "wamid.up2",
		SenderID: "5511999990077@s.whatsapp.net", SenderName: "Rita", Text: "lunch anyone?", At: afterTheFixture(),
	})
	inst.Emit(ctx, connectors.Inbound{
		ChatID: "5511999990077@s.whatsapp.net", ChatTitle: "Rita", ChatKind: "dm", MessageID: "wamid.up3",
		SenderID: "5511999990077@s.whatsapp.net", SenderName: "Rita", Text: "are you there?", At: afterTheFixture(),
	})
	direct := nextPlatformEvent(t, atlas)
	require.Equal(t, []any{"are you there?", dto.ChatKindDM}, []any{direct.Text, direct.ChatKind}, "the unmentioned group message was not queued before it")
	require.Empty(t, platformEventsOf(t, atlas.Drain(300*time.Millisecond)), "and was not queued after it either")
	require.Empty(t, platformEventsOf(t, other.Drain(100*time.Millisecond)))
	require.Equal(t, before, messages())
}
