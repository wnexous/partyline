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
// without a socket.
type recordingPusher struct {
	mu      sync.Mutex
	online  map[string]bool
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
	r.pushes = append(r.pushes, pushCall{agentID: agentID, target: target, message: m})
	return true
}

func (r *recordingPusher) PushUpdate(agentID, target string, t dto.AgentFrameType, m dto.AgentMessage) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, updateCall{agentID: agentID, target: target, typ: t, message: m})
	return true
}

func (r *recordingPusher) calls() []pushCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pushCall(nil), r.pushes...)
}

// expect waits for the dispatcher to make exactly want pushes.
func (r *recordingPusher) expect(t *testing.T, want []pushCall) {
	t.Helper()
	require.Eventually(t, func() bool { return len(r.calls()) == len(want) }, 5*time.Second, 5*time.Millisecond)
	got := r.calls()
	require.Len(t, got, len(want))
	for i := range want {
		require.Equal(t, want[i].agentID, got[i].agentID)
		require.Equal(t, want[i].target, got[i].target)
	}
}

// expectNone checks that the dispatcher has stopped after a short settle time.
func (r *recordingPusher) expectNone(t *testing.T) {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	require.Empty(t, r.calls())
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
