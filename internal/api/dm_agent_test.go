package api_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// dmEnv is alice with her AI atlas and bob, a member of her organization, with atlas connected.
type dmEnv struct {
	env        *testkit.Env
	alice, bob *testkit.Client
	atlas      string
	agent      *testkit.Client // an agent token of alice, acting as atlas
	conn       *testkit.AgentConn
}

func newDMEnv(t *testing.T) *dmEnv {
	t.Helper()
	env := testkit.New(t)
	f := &dmEnv{env: env, alice: env.NewUser("alice"), bob: env.NewUser("bob")}
	f.atlas = env.NewAI(f.alice, "atlas")
	shareAI(f.alice, f.atlas, f.bob)
	f.agent = f.alice.Token(dto.TokenKindAgent, "laptop")
	conn, resp := f.agent.DialAgent("atlas")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(wsWait)
	f.conn = conn
	return f
}

// asAtlas returns an agent client acting as atlas.
func (f *dmEnv) asAtlas() *testkit.Client { return f.agent.AsAgent("atlas") }

// DM-03: a user's DM to a live AI arrives as a DM, named by the sender, with the sender's name and
// handle in the channel notification; the AI's reply appears in that DM, and in no other.
func TestUserDMReachesALiveAIAsADM(t *testing.T) {
	f := newDMEnv(t)
	patchProfile(f.alice, f.alice.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Alice A."), Handle: ptr("alice-a")}).RequireStatus(http.StatusOK)
	hers := dmWith(f.alice, f.atlas, http.StatusCreated)
	his := dmWith(f.bob, f.atlas, http.StatusCreated)
	aliceLive := dialLive(t, f.alice)

	sent := say(f.alice, hers.ID, "oi atlas")
	got := f.conn.Message(wsWait)
	require.Equal(t, dto.RoomRef{ID: hers.ID, Name: "Alice A.", Kind: dto.ConversationKindDM}, got.Room, "a DM is named by the other participant")
	require.Equal(t, sent.ID, got.Message.ID)
	require.True(t, got.Trusted, "alice owns atlas")

	n := channel.Format(got.AgentMessage)
	require.Equal(t, "oi atlas", n.Content)
	require.Equal(t, map[string]string{
		"conversation": "dm", "room": hers.ID, "room_name": "Alice A.", "message_id": sent.ID,
		"author": "Alice A.", "author_kind": "user", "author_id": f.alice.User.ID, "author_handle": "alice-a",
		"sender_name": "", "trusted": "true", "kind": "text",
	}, n.Meta)

	// The reply lands in the DM the message came from.
	reply := testkit.Decode[dto.Message](f.asAtlas().Post("/api/v1/agent/messages/"+sent.ID+"/reply", dto.ReplyRequest{Text: "hello alice"}).RequireStatus(http.StatusCreated))
	require.Equal(t, hers.ID, reply.RoomID)
	require.Equal(t, sent.ID, reply.ReplyTo)
	timeline := timelineOf(t, f.alice, hers.ID)
	require.Len(t, timeline, 2)
	require.Equal(t, []any{f.atlas, "atlas", dto.IntelligenceRoleAI}, []any{timeline[0].Author.ID, timeline[0].Author.DisplayName, timeline[0].Author.Role}, "newest first: atlas answered")
	var live dto.Message
	aliceLive.Expect(dto.LiveEventTypeRoomMessage, &live, liveWait)
	for live.ID != reply.ID {
		aliceLive.Expect(dto.LiveEventTypeRoomMessage, &live, liveWait)
	}
	require.Empty(t, timelineOf(t, f.bob, his.ID), "bob's DM with atlas is another conversation")

	// Bob belongs to atlas's organization: his DM arrives in his own DM, untrusted, and the reply goes there.
	asked := say(f.bob, his.ID, "hi atlas, it is bob")
	got = f.conn.Message(wsWait)
	require.Equal(t, dto.RoomRef{ID: his.ID, Name: "bob", Kind: dto.ConversationKindDM}, got.Room)
	require.False(t, got.Trusted, "bob is not atlas's owner")
	bn := channel.Format(got.AgentMessage)
	require.Equal(t, []string{"dm", "bob", f.bob.User.ID, "bob", "false"},
		[]string{bn.Meta["conversation"], bn.Meta["author_handle"], bn.Meta["author_id"], bn.Meta["room_name"], bn.Meta["trusted"]})
	answer := testkit.Decode[dto.Message](f.asAtlas().Post("/api/v1/agent/messages/"+asked.ID+"/reply", dto.ReplyRequest{Text: "hello bob"}).RequireStatus(http.StatusCreated))
	require.Equal(t, his.ID, answer.RoomID)
	require.Len(t, timelineOf(t, f.alice, hers.ID), 2, "nothing of bob's DM reached alice's")
}

// GRP-04: a group delivery is marked conversation="group", names its room as before and keeps the
// author fields; a message of another AI carries that AI's id and handle.
func TestGroupDeliveryIsMarkedAsGroup(t *testing.T) {
	f := newDMEnv(t)
	orion := f.env.NewAI(f.alice, "orion")
	group := createRoom(f.alice, "Ops")
	for _, id := range []string{f.atlas, orion} {
		f.alice.Post("/api/v1/rooms/"+group.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: id}).RequireStatus(http.StatusCreated)
	}
	sent := say(f.alice, group.ID, "team, status?")
	got := f.conn.Message(wsWait)
	require.Equal(t, dto.RoomRef{ID: group.ID, Name: "Ops"}, got.Room, "a group carries no kind")
	n := channel.Format(got.AgentMessage)
	require.Equal(t, map[string]string{
		"conversation": "group", "room": group.ID, "room_name": "Ops", "message_id": sent.ID,
		"author": "alice", "author_kind": "user", "author_id": f.alice.User.ID, "author_handle": "alice",
		"sender_name": "", "trusted": "true", "kind": "text",
	}, n.Meta)

	// Another AI speaking in the group: its intelligence is the author.
	orionConn, resp := f.agent.DialAgent("orion")
	require.NotNil(t, orionConn, "upgrade refused: %d", resp.Status)
	orionConn.Welcome(wsWait)
	f.conn.ExpectNone(300 * time.Millisecond)
	reply := testkit.Decode[dto.Message](f.agent.AsAgent("orion").Post("/api/v1/agent/rooms/"+group.ID+"/messages", dto.PostMessageRequest{Text: "all green"}).RequireStatus(http.StatusCreated))
	got = f.conn.Message(wsWait)
	require.Equal(t, reply.ID, got.Message.ID)
	meta := channel.Format(got.AgentMessage).Meta
	require.Equal(t, []string{"group", "orion", orion, "orion", "agent"}, []string{meta["conversation"], meta["author"], meta["author_id"], meta["author_handle"], meta["author_kind"]})
}

// setDelivery stores the delivery mode of an AI's membership in a room, as a member update would
// (the API refuses member updates in a DM).
func setDelivery(t *testing.T, env *testkit.Env, roomID, agentID string, mode dto.DeliveryMode) {
	t.Helper()
	ctx := context.Background()
	m, err := env.App.Store.R().GetRoomMemberByMember(ctx, db.GetRoomMemberByMemberParams{RoomID: roomID, Kind: string(dto.MemberKindAgent), MemberID: agentID})
	require.NoError(t, err)
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.UpdateRoomMemberSettings(ctx, db.UpdateRoomMemberSettingsParams{
			DisplayName: m.DisplayName, Delivery: string(mode), KeywordsJson: m.KeywordsJson, ID: m.ID,
		})
	}))
}

// DM-03, GRP-04: a DM is addressed to the AI, so it passes the all and mentions delivery modes,
// which still filter a group; the trusted mode still holds back what its owner did not write.
func TestDMPassesAllAndMentionsDelivery(t *testing.T) {
	f := newDMEnv(t)
	hers := dmWith(f.alice, f.atlas, http.StatusCreated)
	his := dmWith(f.bob, f.atlas, http.StatusCreated)
	group := createRoom(f.alice, "Ops")
	f.alice.Post("/api/v1/rooms/"+group.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.atlas}).RequireStatus(http.StatusCreated)
	addToRoom(t, f.env, f.alice, group.ID, f.bob)
	members := membersOf(t, f.alice, group.ID)
	f.alice.Patch("/api/v1/rooms/"+group.ID+"/members/"+members[f.atlas].ID, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeMentions)}).RequireStatus(http.StatusOK)
	setDelivery(t, f.env, hers.ID, f.atlas, dto.DeliveryModeMentions)
	setDelivery(t, f.env, his.ID, f.atlas, dto.DeliveryModeMentions)

	// mentions: an unmentioned message reaches the AI in a DM and not in the group.
	say(f.bob, group.ID, "nobody is mentioned here")
	f.conn.ExpectNone(400 * time.Millisecond)
	plain := say(f.bob, his.ID, "no mention needed")
	require.Equal(t, plain.ID, f.conn.Message(wsWait).Message.ID)
	mentioned := say(f.bob, group.ID, "@atlas please look")
	// SN-03: the unmentioned message of the group comes as context of the mention.
	require.NotEqual(t, mentioned.ID, f.conn.Message(wsWait).Message.ID)
	require.Equal(t, mentioned.ID, f.conn.Message(wsWait).Message.ID)

	// all: everything arrives.
	setDelivery(t, f.env, his.ID, f.atlas, dto.DeliveryModeAll)
	other := say(f.bob, his.ID, "anything")
	require.Equal(t, other.ID, f.conn.Message(wsWait).Message.ID)

	// trusted: only the owner's messages arrive, in a DM as anywhere.
	setDelivery(t, f.env, hers.ID, f.atlas, dto.DeliveryModeTrusted)
	setDelivery(t, f.env, his.ID, f.atlas, dto.DeliveryModeTrusted)
	say(f.bob, his.ID, "bob is not the owner")
	f.conn.ExpectNone(400 * time.Millisecond)
	owner := say(f.alice, hers.ID, "alice is the owner")
	got := f.conn.Message(wsWait)
	require.Equal(t, owner.ID, got.Message.ID)
	require.True(t, got.Trusted)
}

// SES-08 with DMs: DMs written while the AI is offline arrive as its backlog, as DMs.
func TestDMBacklogArrivesAsDMs(t *testing.T) {
	f := newDMEnv(t)
	hers := dmWith(f.alice, f.atlas, http.StatusCreated)
	setDelivery(t, f.env, hers.ID, f.atlas, dto.DeliveryModeMentions) // a DM needs no mention, also in the backlog
	f.conn.Close()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(f.atlas) }, wsWait, 20*time.Millisecond)
	first, second := say(f.alice, hers.ID, "while you were away"), say(f.alice, hers.ID, "me again")

	conn, resp := f.agent.DialAgent("atlas")
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	conn.Welcome(wsWait)
	for _, want := range []dto.Message{first, second} {
		got := conn.Message(wsWait)
		require.Equal(t, want.ID, got.Message.ID)
		require.Equal(t, dto.RoomRef{ID: hers.ID, Name: "alice", Kind: dto.ConversationKindDM}, got.Room)
		require.Equal(t, "dm", channel.Format(got.AgentMessage).Meta["conversation"])
	}
}

// TOOL-09 with DMs: list_rooms (GET /agent/rooms) includes the AI's DMs with their kind and the
// other participant, and its groups with theirs.
func TestAgentRoomsListsDMs(t *testing.T) {
	f := newDMEnv(t)
	hers := dmWith(f.alice, f.atlas, http.StatusCreated)
	group := createRoom(f.alice, "Ops")
	f.alice.Post("/api/v1/rooms/"+group.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.atlas}).RequireStatus(http.StatusCreated)

	list := testkit.Decode[dto.List[dto.AgentRoom]](f.asAtlas().Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK))
	byID := map[string]dto.AgentRoom{}
	for _, r := range list.Items {
		byID[r.ID] = r
	}
	require.Len(t, byID, 2)

	dm := byID[hers.ID]
	require.Equal(t, dto.ConversationKindDM, dm.Kind)
	require.Equal(t, "alice", dm.Name, "a DM is named by the other participant")
	require.NotNil(t, dm.With)
	require.Equal(t, []any{f.alice.User.ID, "alice", dto.IntelligenceRoleUser}, []any{dm.With.ID, dm.With.Handle, dm.With.Role})
	require.Len(t, dm.Members, 2)

	g := byID[group.ID]
	require.Equal(t, dto.ConversationKindGroup, g.Kind)
	require.Equal(t, "Ops", g.Name)
	require.Nil(t, g.With)
}

// TOOL-04 with DMs: a room is found by id, by name, or for a DM by the @handle of the other
// participant; a name never finds a DM, and the other AIs' DMs are not the AI's.
func TestAgentResolvesRoomsByHandle(t *testing.T) {
	f := newDMEnv(t)
	patchProfile(f.alice, f.alice.User.ID, dto.UpdateProfileRequest{Handle: ptr("alice-a")}).RequireStatus(http.StatusOK)
	hers := dmWith(f.alice, f.atlas, http.StatusCreated)
	say(f.alice, hers.ID, "first")
	// A group whose name equals the display name of the DM's counterpart, and one named "agent"
	// (what an empty name slugs to): neither may be confused with the DM.
	same := createRoom(f.alice, "alice")
	agentNamed := createRoom(f.alice, "agent")
	for _, id := range []string{same.ID, agentNamed.ID} {
		f.alice.Post("/api/v1/rooms/"+id+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.atlas}).RequireStatus(http.StatusCreated)
	}
	atlas := f.asAtlas()

	info := func(ref string) *testkit.Response {
		return atlas.Get("/api/v1/agent/rooms/" + url.PathEscape(ref) + "/info")
	}
	for _, ref := range []string{"@alice-a", "@Alice-A", hers.ID} {
		got := testkit.Decode[dto.RoomInfo](info(ref).RequireStatus(http.StatusOK))
		require.Equal(t, dto.RoomRef{ID: hers.ID, Name: "alice", Kind: dto.ConversationKindDM}, got.Room, ref)
	}
	history := testkit.Decode[dto.Page[dto.AgentMessage]](atlas.Get("/api/v1/agent/rooms/@alice-a/messages").RequireStatus(http.StatusOK))
	require.Len(t, history.Items, 1)
	require.Equal(t, dto.RoomRef{ID: hers.ID, Name: "alice", Kind: dto.ConversationKindDM}, history.Items[0].Room)
	require.Equal(t, "dm", channel.Format(history.Items[0]).Meta["conversation"])

	posted := testkit.Decode[dto.Message](atlas.Post("/api/v1/agent/rooms/@alice-a/messages", dto.PostMessageRequest{Text: "hello by handle"}).RequireStatus(http.StatusCreated))
	require.Equal(t, hers.ID, posted.RoomID)

	// Names find groups only: "alice" is the group, "agent" the group, no 409 from the empty name.
	require.Equal(t, same.ID, testkit.Decode[dto.RoomInfo](info("alice").RequireStatus(http.StatusOK)).Room.ID)
	require.Equal(t, agentNamed.ID, testkit.Decode[dto.RoomInfo](info("agent").RequireStatus(http.StatusOK)).Room.ID)

	// Not a handle of a DM atlas has, a handle of a user it has no DM with, a plain handle.
	for _, ref := range []string{"@nobody", "@bob", "alice-a", "@"} {
		info(ref).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	}
	// Bob's DM with atlas is bob's and atlas's: atlas finds it by @bob once it exists.
	his := dmWith(f.bob, f.atlas, http.StatusCreated)
	require.Equal(t, his.ID, testkit.Decode[dto.RoomInfo](info("@bob").RequireStatus(http.StatusOK)).Room.ID)
	// Another AI (orion) is not a member of either DM.
	f.env.NewAI(f.alice, "orion")
	f.agent.AsAgent("orion").Get("/api/v1/agent/rooms/@alice-a/info").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.agent.AsAgent("orion").Get("/api/v1/agent/rooms/"+hers.ID+"/info").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}
