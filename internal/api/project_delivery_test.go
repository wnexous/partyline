package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// projectRoom is alice's room "Ops" with her project "atlas" as a member. Its sessions are AIs
// atlas-1, atlas-2 … put in the project and connecting one clock second apart, so the last one
// dialed is the active session.
type projectRoom struct {
	env      *testkit.Env
	clock    *testkit.Clock
	alice    *testkit.Client
	token    *testkit.Client // alice's agent token
	room     dto.Room
	project  string // project id
	member   dto.RoomMember
	sessions int
	names    map[string]string // agent id → the name its folder is called
}

func newProjectRoom(t *testing.T) *projectRoom {
	t.Helper()
	clock := testkit.NewClock(time.Now())
	env := testkit.New(t, testkit.WithClock(clock.Now))
	alice := env.NewUser("alice")
	f := &projectRoom{env: env, clock: clock, alice: alice, token: alice.Token(dto.TokenKindAgent, "laptop"), names: map[string]string{}}
	f.room = createRoom(alice, "Ops")
	return f
}

// session connects a new session of project "atlas": a new AI atlas-N; the first one creates
// the project and puts it in the room.
func (f *projectRoom) session(t *testing.T) (*testkit.AgentConn, dto.AgentInfo) {
	t.Helper()
	f.clock.Advance(time.Second)
	f.sessions++
	name := fmt.Sprintf("atlas-%d", f.sessions)
	id := f.env.NewAI(f.alice, name)
	if f.project == "" {
		f.project = createProject(t, f.env, f.alice, "atlas", "Atlas")
		f.member = testkit.Decode[dto.RoomMember](addProject(f.alice, f.room.ID, f.project).RequireStatus(http.StatusCreated))
	}
	putInProject(t, f.env, id, f.project)
	conn, resp := f.token.DialAgent(name)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	info := conn.Welcome(wsWait).Agent
	require.Equal(t, id, info.ID)
	f.names[id] = name
	return conn, info
}

// rest is the session's REST client (the channel's tool calls).
func (f *projectRoom) rest(info dto.AgentInfo) *testkit.Client {
	return f.token.AsAgent(f.names[info.ID])
}

func (f *projectRoom) post(c *testkit.Client, text string) dto.Message {
	c.T.Helper()
	return testkit.Decode[dto.Message](c.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

func (f *projectRoom) waitOffline(t *testing.T, agentID string) {
	t.Helper()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(agentID) }, wsWait, 20*time.Millisecond)
}

func (f *projectRoom) addAgent(t *testing.T, agentID string) {
	t.Helper()
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agentID}).RequireStatus(http.StatusCreated)
}

// PROJ-07: a project member's message goes only to the project's most recently connected
// online session; when that session disconnects, the next most recent one gets new messages.
func TestProjectMessageGoesToTheActiveSession(t *testing.T) {
	f := newProjectRoom(t)
	first, _ := f.session(t)
	second, two := f.session(t)
	require.Equal(t, "atlas-2", two.DisplayName)

	one := f.post(f.alice, "first message")
	got := second.Message(wsWait)
	require.Equal(t, one.ID, got.Message.ID)
	require.Equal(t, dto.RoomRef{ID: f.room.ID, Name: "Ops"}, got.Room)
	first.ExpectNone(300 * time.Millisecond)

	second.Close()
	f.waitOffline(t, two.ID)
	next := f.post(f.alice, "after the restart")
	require.Equal(t, next.ID, first.Message(wsWait).Message.ID, "the remaining session became active")
}

// PROJ-07 edge case: the active session that is also a member of the room itself receives
// each message once.
func TestProjectActiveSessionThatIsAMemberGetsEachMessageOnce(t *testing.T) {
	f := newProjectRoom(t)
	conn, info := f.session(t)
	f.addAgent(t, info.ID)

	m := f.post(f.alice, "only once")
	require.Equal(t, m.ID, conn.Message(wsWait).Message.ID)
	conn.ExpectNone(500 * time.Millisecond)
}

// PROJ-09 (amended, AD-009): the active session's reply is authored by that agent and is not
// delivered through the project member: the active session does not get it back, and a post
// of another session of the project (here a direct member of the room) does not reach the
// active session through the project either. A session of the same project that is a direct
// member of the room still receives the active session's reply as that member.
func TestProjectRepliesDoNotEchoToTheProject(t *testing.T) {
	f := newProjectRoom(t)
	memberConn, memberInfo := f.session(t)
	f.addAgent(t, memberInfo.ID)
	active, info := f.session(t)

	question := f.post(f.alice, "status?")
	require.Equal(t, question.ID, active.Message(wsWait).Message.ID)
	require.Equal(t, question.ID, memberConn.Message(wsWait).Message.ID)

	reply := testkit.Decode[dto.Message](f.rest(info).Post("/api/v1/agent/messages/"+question.ID+"/reply",
		dto.ReplyRequest{Text: "all green"}).RequireStatus(http.StatusCreated))
	require.Equal(t, []any{dto.AuthorKindAgent, info.ID, "atlas-2", question.ID},
		[]any{reply.AuthorKind, reply.AuthorID, reply.AuthorName, reply.ReplyTo})
	require.Equal(t, reply.ID, memberConn.Message(wsWait).Message.ID, "a member session receives it as a member")

	own := testkit.Decode[dto.Message](f.rest(memberInfo).Post(agentRoomPath(f.room.ID, "/messages"),
		dto.PostMessageRequest{Text: "from the other session"}).RequireStatus(http.StatusCreated))
	require.Equal(t, memberInfo.ID, own.AuthorID)
	active.ExpectNone(500 * time.Millisecond)
	memberConn.ExpectNone(100 * time.Millisecond)
}

// PROJ-10: trust for a project delivery follows the project's owner: in alice's room, her
// project's session trusts alice and not bob; bob's project (shared with alice) trusts bob
// and not alice.
func TestProjectDeliveryTrustFollowsTheProjectOwner(t *testing.T) {
	f := newProjectRoom(t)
	alices, _ := f.session(t)
	bob := f.env.NewUser("bob")
	shareRoom(f.alice, f.room.ID, dto.SharingUser{UserID: bob.User.ID, Level: dto.LevelPost})
	bobAgent, bobProject := sessionProject(t, f.env, bob, "web")
	bobs, resp := bob.Token(dto.TokenKindAgent, "desk").DialAgent("web-claude")
	require.NotNil(t, bobs, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, bobAgent, bobs.Welcome(wsWait).Agent.ID)
	bob.Put("/api/v1/projects/"+bobProject+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: []dto.SharingUser{
		{UserID: f.alice.User.ID, Level: dto.LevelUse},
	}}).RequireStatus(http.StatusOK)
	addProject(f.alice, f.room.ID, bobProject).RequireStatus(http.StatusCreated)

	fromAlice := f.post(f.alice, "from alice")
	got := alices.Message(wsWait)
	require.Equal(t, fromAlice.ID, got.Message.ID)
	require.True(t, got.Trusted, "alice's message to alice's project")
	got = bobs.Message(wsWait)
	require.Equal(t, fromAlice.ID, got.Message.ID)
	require.False(t, got.Trusted, "alice's message to bob's project")

	fromBob := f.post(bob, "from bob")
	got = alices.Message(wsWait)
	require.Equal(t, fromBob.ID, got.Message.ID)
	require.False(t, got.Trusted, "bob's message to alice's project")
	got = bobs.Message(wsWait)
	require.Equal(t, fromBob.ID, got.Message.ID)
	require.True(t, got.Trusted, "bob's message to bob's project")
}

// PROJ-06, PROJ-07: the project member's settings apply to its active session: delivery
// mode mentions (the project's name, the member's display name and a reply to any session of
// the project count), and its display name names the session's posts in the room.
func TestProjectMemberSettingsApplyToTheActiveSession(t *testing.T) {
	f := newProjectRoom(t)
	older, _ := f.session(t)
	newer, newerInfo := f.session(t)
	f.alice.Patch("/api/v1/rooms/"+f.room.ID+"/members/"+f.member.ID, dto.UpdateMemberRequest{
		DisplayName: ptr("Atlas bot"), Delivery: ptr(dto.DeliveryModeMentions),
	}).RequireStatus(http.StatusOK)

	f.post(f.alice, "hello everyone")
	byProject := f.post(f.alice, "@atlas are you there?")
	require.Equal(t, byProject.ID, newer.Message(wsWait).Message.ID, "the plain message is not delivered")
	byDisplay := f.post(f.alice, "ping @Atlas bot")
	require.Equal(t, byDisplay.ID, newer.Message(wsWait).Message.ID)

	status := testkit.Decode[dto.Message](f.rest(newerInfo).Post(agentRoomPath(f.room.ID, "/messages"),
		dto.PostMessageRequest{Text: "status: green"}).RequireStatus(http.StatusCreated))
	require.Equal(t, "Atlas bot", status.AuthorName, "the project member's display name names the session")

	newer.Close()
	f.waitOffline(t, newerInfo.ID)
	reply := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages",
		dto.PostMessageRequest{Text: "thanks", ReplyTo: status.ID}).RequireStatus(http.StatusCreated))
	require.Equal(t, reply.ID, older.Message(wsWait).Message.ID, "a reply to another session of the project counts")
	older.ExpectNone(300 * time.Millisecond)
}

// PROJ-07 (REST tools): the active session of a member project uses the room's tools as a
// member would (list_rooms, room_info, history, post, reply, attachments); another session
// of the project gets the answers of a non-member.
func TestProjectActiveSessionUsesTheRoomTools(t *testing.T) {
	f := newProjectRoom(t)
	_, idle := f.session(t)
	_, active := f.session(t)
	question := testkit.Decode[dto.Message](postMultipart(f.alice, f.room.ID, dto.PostMessageRequest{Text: "the report"},
		upload{"report.txt", "text/plain", "all good"}).RequireStatus(http.StatusCreated))

	rest := f.rest(active)
	rooms := testkit.Decode[dto.List[dto.AgentRoom]](rest.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)).Items
	require.Len(t, rooms, 1)
	require.Equal(t, f.room.ID, rooms[0].ID)
	info := testkit.Decode[dto.RoomInfo](rest.Get("/api/v1/agent/rooms/Ops/info").RequireStatus(http.StatusOK))
	require.Equal(t, []any{dto.MemberKindProject, f.project}, []any{info.Me.Kind, info.Me.MemberID})
	history := testkit.Decode[dto.Page[dto.AgentMessage]](rest.Get(agentRoomPath(f.room.ID, "/messages")).RequireStatus(http.StatusOK))
	require.Len(t, history.Items, 1)
	require.Equal(t, question.ID, history.Items[0].Message.ID)
	require.True(t, history.Items[0].Trusted)
	require.Equal(t, "all good", string(rest.Get("/api/v1/agent/attachments/"+question.Attachments[0].ID).RequireStatus(http.StatusOK).Body))
	rest.Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "posted"}).RequireStatus(http.StatusCreated)
	rest.Post("/api/v1/agent/messages/"+question.ID+"/reply", dto.ReplyRequest{Text: "replied"}).RequireStatus(http.StatusCreated)

	other := f.rest(idle)
	require.Empty(t, testkit.Decode[dto.List[dto.AgentRoom]](other.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)).Items)
	e := other.Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "x"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Equal(t, "not a member of room "+f.room.ID, e.Message)
	other.Get(agentRoomPath(f.room.ID, "/messages")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	other.Post("/api/v1/agent/messages/"+question.ID+"/reply", dto.ReplyRequest{Text: "x"}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	other.Get("/api/v1/agent/attachments/"+question.Attachments[0].ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// PROJ-07 (member list): a project member shows its active session and whether it is online;
// a session connecting or leaving sends members.changed to the room's live viewers, and the
// list then shows the new active session.
func TestProjectMemberShowsTheActiveSession(t *testing.T) {
	f := newProjectRoom(t)
	first, one := f.session(t)
	live := dialLive(t, f.alice)

	member := func() dto.RoomMember {
		t.Helper()
		for _, m := range testkit.Decode[dto.List[dto.RoomMember]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/members").RequireStatus(http.StatusOK)).Items {
			if m.Kind == dto.MemberKindProject {
				return m
			}
		}
		require.FailNow(t, "no project member")
		return dto.RoomMember{}
	}
	m := member()
	require.Equal(t, ptr(true), m.Online)
	require.Equal(t, &dto.MemberSession{ID: one.ID, Name: one.ID, DisplayName: "atlas-1"}, m.ActiveSession, "the row of an AI of a folder is named by its id")

	second, two := f.session(t)
	var changed dto.RoomIDData
	live.Expect(dto.LiveEventTypeMembersChanged, &changed, liveWait)
	require.Equal(t, f.room.ID, changed.RoomID)
	require.Equal(t, two.ID, member().ActiveSession.ID)

	second.Close()
	f.waitOffline(t, two.ID)
	live.Expect(dto.LiveEventTypeMembersChanged, &changed, liveWait)
	require.Equal(t, one.ID, member().ActiveSession.ID, "the remaining session is active again")

	first.Close()
	f.waitOffline(t, one.ID)
	live.Expect(dto.LiveEventTypeMembersChanged, &changed, liveWait)
	m = member()
	require.Equal(t, ptr(false), m.Online)
	require.Nil(t, m.ActiveSession)
}

// PROJ-06 (live): sharing a project sends access.changed for it to the users who can use it
// (so their member picker lists it), and to nobody else.
func TestLiveProjectSharingSendsAccessChanged(t *testing.T) {
	f := newProjectRoom(t)
	f.session(t)
	bob, carol := f.env.NewUser("bob"), f.env.NewUser("carol")
	bobLive, carolLive := dialLive(t, bob), dialLive(t, carol)

	f.alice.Put("/api/v1/projects/"+f.project+"/sharing", dto.Sharing{Mode: dto.VisibilityShared, Users: []dto.SharingUser{
		{UserID: bob.User.ID, Level: dto.LevelUse},
	}}).RequireStatus(http.StatusOK)
	var got dto.AccessChangedData
	bobLive.Expect(dto.LiveEventTypeAccessChanged, &got, liveWait)
	require.Equal(t, dto.AccessChangedData{ResourceKind: "project", ResourceID: f.project}, got)
	carolLive.ExpectNone(quiet)
}
