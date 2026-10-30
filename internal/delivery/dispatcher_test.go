package delivery_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// pushCall is one push the recording pusher received.
type pushCall struct {
	agentID string
	target  string
	message dto.AgentMessage
}

// updateCall is one update the recording pusher received.
type updateCall struct {
	agentID string
	target  string
	typ     dto.AgentFrameType
	message dto.AgentMessage
}

// recordingPusher stands in for the real gateway, so the test asserts the dispatcher's target
// without a socket. It keeps the gateway's rule that a message the AI itself wrote only reaches a
// live session of it: everything else is refused and never reported as delivered (SESS-37, SESS-38).
type recordingPusher struct {
	mu      sync.Mutex
	online  map[string]bool
	live    func(agentID, label string) bool
	pushes  []pushCall
	updates []updateCall
}

func newRecordingPusher(online ...string) *recordingPusher {
	r := &recordingPusher{online: map[string]bool{}}
	for _, id := range online {
		r.online[id] = true
	}
	return r
}

func (r *recordingPusher) Online(agentID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.online[agentID]
}

func (r *recordingPusher) Push(agentID, target string, m dto.AgentMessage) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m.Message.AuthorKind == dto.AuthorKindAgent && m.Message.AuthorID == agentID &&
		(target == "" || r.live == nil || !r.live(agentID, target)) {
		return false // the AI's own message only reaches a live session of it (SESS-38)
	}
	r.pushes = append(r.pushes, pushCall{agentID: agentID, target: target, message: m})
	return true
}

func (r *recordingPusher) PushUpdate(agentID string, targets []string, t dto.AgentFrameType, m dto.AgentMessage) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, target := range targets {
		r.updates = append(r.updates, updateCall{agentID: agentID, target: target, typ: t, message: m})
	}
	return true
}

func (r *recordingPusher) calls() []pushCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pushCall(nil), r.pushes...)
}

// expect waits for the dispatcher to make exactly want pushes and returns them.
func (r *recordingPusher) expect(t *testing.T, want []pushCall) []pushCall {
	t.Helper()
	require.Eventually(t, func() bool { return len(r.calls()) == len(want) }, 5*time.Second, 5*time.Millisecond)
	got := r.calls()
	require.Len(t, got, len(want))
	for i := range want {
		require.Equal(t, want[i].agentID, got[i].agentID)
		require.Equal(t, want[i].target, got[i].target)
	}
	return got
}

// expectNone checks that the dispatcher has stopped after a short settle time.
func (r *recordingPusher) expectNone(t *testing.T) {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	require.Empty(t, r.calls())
}

// expectNoMore checks that the dispatcher made no further push after the first n.
func (r *recordingPusher) expectNoMore(t *testing.T, n int) {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	require.Len(t, r.calls(), n)
}

// dispatcherFixture is a group with the given AIs, plus a recording dispatcher on the app's bus.
type dispatcherFixture struct {
	env   *testkit.Env
	alice *testkit.Client
	room  dto.Room
	ids   map[string]string
	rec   *recordingPusher
}

func newDispatcherFixture(t *testing.T, names ...string) *dispatcherFixture {
	t.Helper()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(201))
	ids := map[string]string{}
	var online []string
	for _, name := range names {
		id := env.NewAI(alice, name)
		ids[name] = id
		online = append(online, id)
		alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: id}).RequireStatus(201)
	}
	rec := newRecordingPusher(online...)
	rec.live = func(agentID, label string) bool {
		_, ok := env.App.Presence.SessionByLabel(agentID, label)
		return ok
	}
	d := delivery.NewAgentDispatcher(delivery.DispatcherOptions{
		Store: env.App.Store, Bus: env.App.Bus, Agents: rec, Load: env.App.Gateway.Message,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = d.Run(ctx) }()
	return &dispatcherFixture{env: env, alice: alice, room: room, ids: ids, rec: rec}
}

func (f *dispatcherFixture) post(t *testing.T, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(201))
}

func (f *dispatcherFixture) postAs(t *testing.T, name, text string) dto.Message {
	t.Helper()
	client := f.alice.Token(dto.TokenKindAgent, "session").AsAgent(name)
	return testkit.Decode[dto.Message](client.Post("/api/v1/agent/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(201))
}

// postFrom posts in the room as the session of folder, whose live id the client sends too, so the
// stored author_label is the one the server resolved (SESS-29).
func (f *dispatcherFixture) postFrom(t *testing.T, folder testkit.Folder, text string) dto.Message {
	t.Helper()
	client := f.alice.Token(dto.TokenKindAgent, "session").AsFolder(folder)
	return testkit.Decode[dto.Message](client.Post("/api/v1/agent/rooms/"+f.room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(201))
}

// dialWorktree opens the AI's :wt session in a linked worktree of its own folder and returns the
// identity it dialed with, so posts from it carry its live id.
func (f *dispatcherFixture) dialWorktree(t *testing.T) testkit.Folder {
	t.Helper()
	main := testkit.FolderOf("atlas")
	folder := testkit.Folder{Machine: main.Machine, Path: "/work/wt", MainFolder: main.Path, Harness: main.Harness, Live: testkit.NewLiveID()}
	conn, resp := f.alice.Token(dto.TokenKindAgent, "session").DialFolder(folder)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(time.Minute)
	t.Cleanup(conn.Close)
	return folder
}

// setDelivery sets an agent member's delivery mode (MEM-03).
func (f *dispatcherFixture) setDelivery(t *testing.T, agentID string, mode dto.DeliveryMode) {
	t.Helper()
	members := testkit.Decode[dto.List[dto.RoomMember]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/members").RequireStatus(200)).Items
	for _, m := range members {
		if m.Kind == dto.MemberKindAgent && m.MemberID == agentID {
			f.alice.Patch("/api/v1/rooms/"+f.room.ID+"/members/"+m.ID, dto.UpdateMemberRequest{Delivery: &mode}).RequireStatus(200)
			return
		}
	}
	t.Fatalf("no agent member with id %s", agentID)
}

// SESS-20, SESS-21: a plain message reaches the principal only; @handle:label reaches that session
// only; the first address wins.
func TestDispatchRoutesToTheAddressedSession(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		target string
	}{
		{"plain", "hello", ""},
		{"labelled", "@atlas:claude-2 hi", "claude-2"},
		{"first address wins", "@atlas:claude-2 and @atlas:wt", "claude-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDispatcherFixture(t, "atlas")
			f.post(t, tc.text)
			f.rec.expect(t, []pushCall{{agentID: f.ids["atlas"], target: tc.target}})
		})
	}
}

// SESS-21: a DM follows the same address rule.
func TestDispatchRoutesADirectMessageToTheAddressedSession(t *testing.T) {
	f := newDispatcherFixture(t, "atlas")
	dm := testkit.Decode[dto.Room](f.alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: f.ids["atlas"]}).RequireStatus(201))
	msg := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+dm.ID+"/messages", dto.PostMessageRequest{Text: "@atlas:claude-2 hi"}).RequireStatus(201))
	require.Equal(t, dm.ID, msg.RoomID)
	f.rec.expect(t, []pushCall{{agentID: f.ids["atlas"], target: "claude-2"}})
}

// SESS-26: the loop guard suppresses every session.
func TestDispatchSkipsAPausedRoomForEveryTarget(t *testing.T) {
	f := newDispatcherFixture(t, "atlas")
	require.NoError(t, f.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SetRoomLoopState(context.Background(), db.SetRoomLoopStateParams{AgentStreak: 25, AgentsPaused: true, ID: f.room.ID})
	}))
	f.post(t, "@atlas:claude-2 hi")
	f.post(t, "hello")
	f.rec.expectNone(t)
}

// SESS-25: a plain message the AI wrote is not delivered to its own sessions; the room's other AI
// receives it once. D11's labelled sibling exception is T15.
func TestDispatchDoesNotEchoAnAIMessageToItsOwnSessions(t *testing.T) {
	f := newDispatcherFixture(t, "atlas", "builder")
	f.postAs(t, "atlas", "hello")
	f.rec.expect(t, []pushCall{{agentID: f.ids["builder"], target: ""}})
}

// SESS-37: the AI's principal writes to a live sibling of its own AI. That session receives the
// message once, trusted because the AI wrote it, and the principal does not receive it back.
func TestDispatchRoutesAPrincipalsMessageToItsLiveSibling(t *testing.T) {
	f := newDispatcherFixture(t, "atlas")
	f.dialWorktree(t)
	m := f.postAs(t, "atlas", "@atlas:wt do X")
	require.Empty(t, m.AuthorLabel, "the principal writes without a label")

	calls := f.rec.expect(t, []pushCall{{agentID: f.ids["atlas"], target: "wt"}})
	require.True(t, calls[0].message.Trusted, "the AI wrote it: the sibling trusts it")
	require.Empty(t, calls[0].message.ForLabel, "a message the AI wrote never carries the note")
}

// SESS-38: a message the AI's session writes reaches no session of that AI when it names no label,
// the writing session's own label, or a label with no live session.
func TestDispatchDoesNotRouteASessionsMessageToItsOwnAI(t *testing.T) {
	cases := []struct{ name, text string }{
		{"no label", "@atlas do Y"},
		{"its own label", "@atlas:wt do Z"},
		{"a label with no live session", "@atlas:gone do W"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDispatcherFixture(t, "atlas")
			folder := f.dialWorktree(t)
			m := f.postFrom(t, folder, tc.text)
			require.Equal(t, "wt", m.AuthorLabel, "the stored label names the writing session")
			f.rec.expectNone(t)
		})
	}
}

// SESS-26, SESS-37: a person's labelled message still takes D3's normal path - the plan targets
// the AI and the delivery mode decides whether it wants the message at all - and the addressed
// session receives it when it does.
func TestDispatchStillAppliesD3ToAPersonsLabelledMessage(t *testing.T) {
	cases := []struct{ name, text, target string }{
		{"no label", "@atlas do Y", ""},
		{"a live label", "@atlas:wt do Z", "wt"},
		{"a label with no live session", "@atlas:gone do W", "gone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDispatcherFixture(t, "atlas")
			f.setDelivery(t, f.ids["atlas"], dto.DeliveryModeMentions)
			f.dialWorktree(t)

			f.post(t, tc.text)
			calls := f.rec.expect(t, []pushCall{{agentID: f.ids["atlas"], target: tc.target}})
			require.False(t, calls[0].message.Trusted, "a person wrote it: the trust rule of AD-018 decides")

			f.post(t, "hi all") // no mention: the mentions mode drops it
			f.rec.expectNoMore(t, 1)
		})
	}
}

// SESS-37: the loop guard suppresses the sibling target of a message the AI wrote, as it does every
// session of the room (SESS-26).
func TestDispatchSkipsASelfTargetInAPausedRoom(t *testing.T) {
	f := newDispatcherFixture(t, "atlas")
	f.dialWorktree(t)
	require.NoError(t, f.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SetRoomLoopState(context.Background(), db.SetRoomLoopStateParams{AgentStreak: 25, AgentsPaused: true, ID: f.room.ID})
	}))
	f.postAs(t, "atlas", "@atlas:wt do X")
	f.rec.expectNone(t)
}
