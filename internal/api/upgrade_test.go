package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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

// historyEntry is one message as the old database held it and as the API lists it.
type historyEntry struct {
	ID, AuthorKind, AuthorID, Text string
	Seq                            int64
}

// roomHistory is the messages of one room before the upgrade, and who reads them afterwards: the user who owns it.
type roomHistory struct {
	room    string
	reader  *testkit.Client
	entries []historyEntry
}

// upgradedAI is an AI of the upgraded installation: its handle and the folder its session starts in.
type upgradedAI struct {
	handle string
	folder testkit.Folder
}

// upgradeWorld is a v0.5 installation that the app has opened and upgraded (OUPG-01), and what the end-to-end checks
// look at: alice administers the instance, owns the AIs atlas and other and the group team they both belong to; bob
// was shared atlas for use, and a room for posting and another for viewing; root is a second administrator of the
// instance, the heir of OUPG-02. password is everyone's.
type upgradeWorld struct {
	env                *testkit.Env
	alice, bob, root   *testkit.Client
	password           string
	aliceSlug, bobSlug string // the addresses of their personal organizations
	bobHandle          string
	atlas, other       upgradedAI
	team               string
	history            []roomHistory
	// bobReads are the rooms bob posted in (he keeps them as a room member) and bobLoses those he could only
	// view: the read-only view is not carried over (AD-016).
	bobReads, bobLoses []string
	// connection is a connection of alice's, which her organization owns after the upgrade ("" when there is none).
	connection string
}

// v05Database writes what a v0.5 installation holds: v04Database upgraded by 0017 and 0018, the schema v0.5 ends
// with, plus what organizations have to move: bob, who was shared the AI atlas for use, and root, a second
// administrator of the instance. The app that opens it applies 0019 and 0020. It also returns the messages of every
// room, read from the database before the app sees it.
func v05Database(t *testing.T) (string, map[string][]historyEntry) {
	t.Helper()
	ctx := context.Background()
	path := v04Database(t)
	st, err := store.Open(path)
	require.NoError(t, err)
	require.NoError(t, st.MigrateBefore(ctx, "0019"))
	require.NoError(t, st.Close())
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.ExecContext(ctx, `
INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u-root', 'root', 'x', 'admin', 1790000000200);
INSERT INTO intelligences (id, kind, display_name, handle, created_at, updated_at)
VALUES ('u-root', 'human', 'root', 'root', 1790000000200, 1790000000200);
INSERT INTO grants (resource_kind, resource_id, subject, level, created_by, created_at) VALUES
    ('agent', 'a-atlas', 'u-bob', 'use', 'u-alice', 1790000000201),
    ('agent', 'a-pc2', '*', 'use', 'u-alice', 1790000000202);`)
	require.NoError(t, err)

	history := map[string][]historyEntry{}
	rows, err := raw.QueryContext(ctx, `SELECT room_id, id, seq, author_kind, author_id, text FROM messages ORDER BY room_id, seq`)
	require.NoError(t, err)
	for rows.Next() {
		var room string
		var e historyEntry
		require.NoError(t, rows.Scan(&room, &e.ID, &e.Seq, &e.AuthorKind, &e.AuthorID, &e.Text))
		history[room] = append(history[room], e)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Len(t, history["r-team"], 2, "fixture sanity")
	return path, history
}

// entriesOf renders a room's timeline, oldest first, as the history before the upgrade was written.
func entriesOf(msgs []dto.Message) []historyEntry {
	out := make([]historyEntry, len(msgs))
	for i, m := range msgs {
		out[i] = historyEntry{ID: m.ID, Seq: m.Seq, AuthorKind: string(m.AuthorKind), AuthorID: m.AuthorID, Text: m.Text}
	}
	return out
}

// organizationsOf lists the organizations of the viewer by address.
func organizationsOf(t *testing.T, c *testkit.Client) map[string]dto.Organization {
	t.Helper()
	out := map[string]dto.Organization{}
	for _, o := range testkit.Decode[dto.List[dto.Organization]](c.Get("/api/v1/orgs").RequireStatus(http.StatusOK)).Items {
		out[o.Slug] = o
	}
	return out
}

// orgMembers lists the members of an organization by the handle of their intelligence.
func orgMembers(t *testing.T, c *testkit.Client, orgID string) map[string]dto.OrgMember {
	t.Helper()
	out := map[string]dto.OrgMember{}
	for _, m := range testkit.Decode[dto.List[dto.OrgMember]](c.Get("/api/v1/orgs/" + orgID + "/members").RequireStatus(http.StatusOK)).Items {
		out[m.Intelligence.Handle] = m
	}
	return out
}

// checkUpgradedInstallation is the end-to-end proof of the organizations upgrade (OUPG-01, OUPG-02, POS-10), through
// the API of the app that opened a v0.5 database. Its steps run in order: the last two delete an AI and an account.
func checkUpgradedInstallation(t *testing.T, w *upgradeWorld) {
	t.Helper()
	var aliceOrg dto.Organization
	var atlasID string

	t.Run("every user has a personal organization where they are the admin, and the AIs are members", func(t *testing.T) {
		mine := organizationsOf(t, w.alice)
		require.Len(t, mine, 1, "alice belongs to her own organization and no other")
		aliceOrg = mine[w.aliceSlug]
		require.NotEmpty(t, aliceOrg.ID)
		require.Equal(t, dto.PermissionAdmin, aliceOrg.Permission)
		require.NotEmpty(t, aliceOrg.PositionID, "alice holds the root position")
		require.Equal(t, aliceOrg.ID, testkit.Decode[dto.Me](w.alice.Get("/api/v1/me").RequireStatus(http.StatusOK)).DefaultOrgID, "it is her default")

		members := orgMembers(t, w.alice, aliceOrg.ID)
		require.Equal(t, []any{dto.PermissionAdmin, "Admin"}, []any{members["alice"].Permission, members["alice"].PositionName})
		for _, ai := range []upgradedAI{w.atlas, w.other} {
			m, ok := members[ai.handle]
			require.True(t, ok, "%s is a member of its owner's organization", ai.handle)
			require.Equal(t, dto.PermissionMember, m.Permission, "%s joined as a member", ai.handle)
			require.Empty(t, m.PositionID, "%s holds no position", ai.handle)
			require.Nil(t, m.InvitedBy)
			require.Equal(t, dto.IntelligenceKindAI, m.Intelligence.Kind)
		}
		atlasID = members[w.atlas.handle].Intelligence.ID

		theirs := organizationsOf(t, w.bob)
		require.Len(t, theirs, 2, "bob has his own organization and joined alice's, where he was granted something")
		require.Equal(t, dto.PermissionAdmin, theirs[w.bobSlug].Permission)
		require.NotEmpty(t, theirs[w.bobSlug].PositionID)
		require.Equal(t, []any{dto.PermissionMember, ""}, []any{theirs[w.aliceSlug].Permission, theirs[w.aliceSlug].PositionID}, "a grantee is a member with no position")
		require.Equal(t, aliceOrg.ID, theirs[w.aliceSlug].ID)
		require.Equal(t, dto.PermissionMember, orgMembers(t, w.alice, aliceOrg.ID)[w.bobHandle].Permission)

		heirs := organizationsOf(t, w.root)
		require.Len(t, heirs, 1, "root has an organization of its own and has not joined alice's")
		require.Equal(t, dto.PermissionAdmin, heirs["root"].Permission)
	})

	t.Run("a former use grantee sees the AI and cannot delete it", func(t *testing.T) {
		var seen *dto.Agent
		for _, a := range testkit.Decode[dto.List[dto.Agent]](w.bob.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items {
			if a.ID == atlasID {
				seen = &a
			}
		}
		require.NotNil(t, seen, "bob sees the AI he was shared, through alice's organization")
		require.Equal(t, dto.LevelUse, seen.MyLevel, "and only sees it")
		w.bob.Get("/api/v1/agents/" + atlasID).RequireStatus(http.StatusOK)
		w.bob.Delete("/api/v1/agents/"+atlasID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		w.alice.Get("/api/v1/agents/" + atlasID).RequireStatus(http.StatusOK) // it is still there
	})

	t.Run("room history is intact, and a view-only grantee loses the room", func(t *testing.T) {
		for _, h := range w.history {
			require.Equal(t, h.entries, entriesOf(timeline(t, h.reader, h.room)), "%s: every message, in order, by the same author", h.room)
		}
		for _, room := range w.bobReads {
			w.bob.Get("/api/v1/rooms/" + room).RequireStatus(http.StatusOK)
		}
		for _, room := range w.bobLoses {
			w.bob.Get("/api/v1/rooms/"+room).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		}
	})

	t.Run("the owner's AIs do not trust each other, and the owner's post is trusted", func(t *testing.T) {
		agents := w.alice.Token(dto.TokenKindAgent, "laptop")
		dial := func(ai upgradedAI) *testkit.AgentConn {
			t.Helper()
			conn, resp := agents.DialFolder(ai.folder)
			require.NotNil(t, conn, "%s: upgrade refused: %d %s", ai.handle, resp.Status, resp.Body)
			conn.Welcome(wsWait)
			conn.Drain(300 * time.Millisecond) // the backlog of what was said before
			return conn
		}
		atlas, other := dial(w.atlas), dial(w.other)
		defer atlas.Close()
		defer other.Close()

		// alice created both AIs: she manages them, so her post is trusted by each.
		owner := testkit.Decode[dto.Message](w.alice.Post("/api/v1/rooms/"+w.team+"/messages", dto.PostMessageRequest{Text: "from the owner"}).RequireStatus(http.StatusCreated))
		for name, conn := range map[string]*testkit.AgentConn{w.atlas.handle: atlas, w.other.handle: other} {
			got := conn.Message(wsWait)
			require.Equal(t, owner.ID, got.Message.ID, name)
			require.True(t, got.Trusted, "%s trusts its owner", name)
		}
		// Neither manages the other, so what one says is data to the other.
		say := func(from upgradedAI, text string) string {
			t.Helper()
			return testkit.Decode[dto.Message](agents.AsFolder(from.folder).Post(agentRoomPath(w.team, "/messages"), dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated)).ID
		}
		id := say(w.atlas, "from "+w.atlas.handle)
		got := other.Message(wsWait)
		require.Equal(t, id, got.Message.ID)
		require.False(t, got.Trusted, "%s does not trust its sibling %s", w.other.handle, w.atlas.handle)
		id = say(w.other, "from "+w.other.handle)
		got = atlas.Message(wsWait)
		require.Equal(t, id, got.Message.ID)
		require.False(t, got.Trusted, "%s does not trust its sibling %s", w.atlas.handle, w.other.handle)
	})

	t.Run("the owner, as admin, deletes the AI, and the history stays", func(t *testing.T) {
		before := len(timeline(t, w.alice, w.team))
		w.alice.Delete("/api/v1/agents/" + atlasID).RequireStatus(http.StatusNoContent)
		w.alice.Get("/api/v1/agents/"+atlasID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		w.bob.Get("/api/v1/agents/"+atlasID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		require.NotContains(t, orgMembers(t, w.alice, aliceOrg.ID), w.atlas.handle, "it left the organization")
		require.Contains(t, orgMembers(t, w.alice, aliceOrg.ID), w.other.handle, "its sibling did not")
		require.Len(t, timeline(t, w.alice, w.team), before, "what it said is still in the room")
	})

	t.Run("the last admin deleting her account makes the instance admin an admin of her organization", func(t *testing.T) {
		w.alice.Do(http.MethodDelete, "/api/v1/me", dto.DeleteAccountRequest{Password: w.password}).RequireStatus(http.StatusNoContent)
		w.env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "alice", Password: w.password}).RequireStatus(http.StatusUnauthorized)

		inherited := organizationsOf(t, w.root)[w.aliceSlug]
		require.Equal(t, []any{aliceOrg.ID, dto.PermissionAdmin}, []any{inherited.ID, inherited.Permission}, "root is an admin of the organization alice administered alone")
		members := orgMembers(t, w.root, aliceOrg.ID)
		require.Equal(t, dto.PermissionAdmin, members["root"].Permission)
		require.NotContains(t, members, "alice", "alice left with her account")
		require.Contains(t, members, w.other.handle, "the AI she hosted stays in its organization")
		require.Equal(t, dto.PermissionMember, members[w.bobHandle].Permission, "and so does everyone else")
		w.root.Get("/api/v1/rooms/" + w.team).RequireStatus(http.StatusOK) // her group stays with its organization
		if w.connection != "" {
			w.root.Get("/api/v1/connectors/" + w.connection).RequireStatus(http.StatusOK) // and so does her connection
		}
	})
}

// OUPG-01, OUPG-02, POS-10, ORG-04 (independent test of the upgrade): the app opens a v0.5 database that holds grants.
// Every user has a personal organization, and their AIs are members of it; the user who was shared an AI for use sees
// it and cannot delete it, while its owner, an admin, can; the owner's two AIs do not trust each other, and the owner's
// post is trusted; room history is intact; and the last admin of an organization who deletes their account leaves it to
// the instance admin.
func TestUpgradedV05InstallationBecomesOrganizations(t *testing.T) {
	ctx := context.Background()
	path, history := v05Database(t)
	fake := connectortest.New()
	whatsapp := fake.Kind()
	whatsapp.Name = "whatsapp" // the fixture's connector kind, served by the fake
	telegram := fake.Kind()
	telegram.Name = "telegram"
	// The app's clock starts an hour after the fixture's last message, so that the retention sweep at start-up
	// keeps its history whatever today is.
	afterTheFixture := func() time.Time { return time.UnixMilli(1790003600000) }
	env := testkit.New(t, testkit.WithDatabase(path), testkit.WithConnectorKinds(whatsapp, telegram), testkit.WithClock(afterTheFixture))
	require.Equal(t, 2, countRows(t, env, `SELECT COUNT(*) FROM schema_migrations WHERE version IN ('0019_organizations', '0020_drop_grants')`), "the app applied 0019 and 0020")

	// The fixture's passwords are placeholders: give the three accounts the one the test knows.
	for _, username := range []string{"alice", "Bob.Smith", "root"} {
		require.NoError(t, env.App.Users.ResetPassword(ctx, username, testkit.Password))
	}
	alice, bob, root := env.LoginAs("alice", testkit.Password), env.LoginAs("Bob.Smith", testkit.Password), env.LoginAs("root", testkit.Password)
	rooms := func(reader *testkit.Client, ids ...string) []roomHistory {
		var out []roomHistory
		for _, id := range ids {
			out = append(out, roomHistory{room: id, reader: reader, entries: history[id]})
		}
		return out
	}
	w := &upgradeWorld{
		env: env, alice: alice, bob: bob, root: root, password: testkit.Password,
		aliceSlug: "alice", bobSlug: "bob-smith", bobHandle: "bob-smith",
		atlas: upgradedAI{"atlas", testkit.FolderOf("atlas")}, other: upgradedAI{"proj-claude-2", testkit.FolderOf("proj-claude-2")},
		team:     "r-team",
		history:  slices.Concat(rooms(alice, "r-ops", "r-inbox", "r-dev", "r-team"), rooms(bob, "r-bob")),
		bobReads: []string{"r-ops"}, bobLoses: []string{"r-inbox"},
		connection: "k-wa",
	}
	checkUpgradedInstallation(t, w)
}
