package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/testkit"
)

const (
	liveWait = 2 * time.Second
	// quiet is how long a user without access must receive nothing (T22: 500 ms).
	quiet = 500 * time.Millisecond
)

// dialLive opens the live socket and consumes the hello frame.
func dialLive(t *testing.T, c *testkit.Client) *testkit.LiveConn {
	t.Helper()
	conn, resp := c.DialLive("")
	resp.RequireStatus(http.StatusSwitchingProtocols)
	f := conn.Next(liveWait)
	require.Equal(t, dto.LiveEventTypeHello, f.Type)
	return conn
}

// LIVE-01, LIVE-02: a stored message reaches every live connection of users with view on the
// room, and nobody else.
func TestLiveRoomMessageReachesViewersOnly(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	carol := env.NewUser("carol")
	room := createRoom(alice, "ops")
	shareRoom(alice, room.ID, dto.SharingUser{UserID: bob.User.ID, Level: dto.LevelView})

	aliceLive, aliceLive2 := dialLive(t, alice), dialLive(t, alice)
	bobLive, carolLive := dialLive(t, bob), dialLive(t, carol)

	sent := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "deploy at 5"}).RequireStatus(http.StatusCreated))

	for _, c := range []*testkit.LiveConn{aliceLive, aliceLive2, bobLive} {
		var got dto.Message
		c.Expect(dto.LiveEventTypeRoomMessage, &got, liveWait)
		require.Equal(t, sent.ID, got.ID)
		require.Equal(t, room.ID, got.RoomID)
		require.Equal(t, "deploy at 5", got.Text)
		require.Equal(t, int64(1), got.Seq)
	}
	carolLive.ExpectNone(quiet)
}

// LIVE-02: losing access stops events and is announced as room.deleted; room changes reach
// only users who can view the room, rendered with their own level.
func TestLiveRoomEventsFollowAccess(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	carol := env.NewUser("carol")
	room := createRoom(alice, "ops")
	aliceLive, bobLive, carolLive := dialLive(t, alice), dialLive(t, bob), dialLive(t, carol)

	// Sharing with bob: bob learns about the room with his own level.
	shareRoom(alice, room.ID, dto.SharingUser{UserID: bob.User.ID, Level: dto.LevelPost})
	var seen dto.Room
	bobLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, room.ID, seen.ID)
	require.Equal(t, dto.LevelPost, seen.MyLevel)
	aliceLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, dto.LevelManage, seen.MyLevel)

	// Room updates and member changes reach viewers only.
	alice.Patch("/api/v1/rooms/"+room.ID, dto.UpdateRoomRequest{Name: ptr("ops-2")}).RequireStatus(http.StatusOK)
	bobLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, "ops-2", seen.Name)
	_, chat := storeChat(t, env, alice.User.ID, "group")
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: chat}).RequireStatus(http.StatusCreated)
	var ref dto.RoomIDData
	bobLive.Expect(dto.LiveEventTypeMembersChanged, &ref, liveWait)
	require.Equal(t, room.ID, ref.RoomID)

	// Revoking bob's access: he gets room.deleted, then nothing about the room.
	alice.Put("/api/v1/rooms/"+room.ID+"/sharing", dto.Sharing{Mode: dto.VisibilityPrivate}).RequireStatus(http.StatusOK)
	bobLive.Expect(dto.LiveEventTypeRoomDeleted, &ref, liveWait)
	require.Equal(t, room.ID, ref.RoomID)
	alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "secret"}).RequireStatus(http.StatusCreated)
	aliceLive.Expect(dto.LiveEventTypeRoomMessage, nil, liveWait)
	bobLive.ExpectNone(quiet)

	// Deleting the room tells its viewers only.
	alice.Delete("/api/v1/rooms/" + room.ID).RequireStatus(http.StatusNoContent)
	aliceLive.Expect(dto.LiveEventTypeRoomDeleted, &ref, liveWait)
	require.Equal(t, room.ID, ref.RoomID)
	bobLive.ExpectNone(quiet)
	carolLive.ExpectNone(quiet)
}

// LIVE-04: agent presence, connector status and outbox status reach only users with access.
func TestLiveStatusEventsReachUsersWithAccess(t *testing.T) {
	env := testkit.New(t)
	ctx := context.Background()
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	carol := env.NewUser("carol")
	agent := storeAgent(t, env, alice.User.ID, "builder")
	conn, chat := storeChat(t, env, alice.User.ID, "group")
	require.NoError(t, env.App.Access.SetVisibility(ctx, alice.User.ID, access.KindAgent, agent, alice.User.ID,
		access.Visibility{Mode: dto.VisibilityShared, Users: []access.UserLevel{{UserID: bob.User.ID}}}))
	require.NoError(t, env.App.Access.SetVisibility(ctx, alice.User.ID, access.KindConnector, conn, alice.User.ID,
		access.Visibility{Mode: dto.VisibilityShared, Users: []access.UserLevel{{UserID: bob.User.ID}}}))
	room := createRoom(alice, "ops")
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: chat}).RequireStatus(http.StatusCreated)
	msg := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "hi"}).RequireStatus(http.StatusCreated))
	require.Len(t, msg.Outbox, 1)

	aliceLive, bobLive, carolLive := dialLive(t, alice), dialLive(t, bob), dialLive(t, carol)

	env.App.Bus.Publish(events.AgentPresence{AgentID: agent, OwnerID: alice.User.ID, Online: true})
	for _, c := range []*testkit.LiveConn{aliceLive, bobLive} {
		var p dto.AgentPresence
		c.Expect(dto.LiveEventTypeAgentPresence, &p, liveWait)
		require.Equal(t, dto.AgentPresence{AgentID: agent, Online: true}, p)
	}

	env.App.Bus.Publish(events.ConnectorStatus{ConnectorID: conn, Status: string(dto.ConnectorStatusError), Detail: "bad gateway"})
	for _, c := range []*testkit.LiveConn{aliceLive, bobLive} {
		var s dto.ConnectorStatusData
		c.Expect(dto.LiveEventTypeConnectorStatus, &s, liveWait)
		require.Equal(t, dto.ConnectorStatusData{ConnectorID: conn, Status: dto.ConnectorStatusError, LastError: "bad gateway"}, s)
	}

	// The outbox entry belongs to alice's private room: bob uses the connector but cannot see the room.
	env.App.Bus.Publish(events.OutboxChanged{OutboxID: msg.Outbox[0].ID, MessageID: msg.ID, RoomID: room.ID, ChatID: chat, Status: "pending"})
	var up dto.OutboxUpdate
	aliceLive.Expect(dto.LiveEventTypeOutboxStatus, &up, liveWait)
	require.Equal(t, room.ID, up.RoomID)
	require.Equal(t, msg.ID, up.MessageID)
	require.Equal(t, msg.Outbox[0].ID, up.ID)
	require.Equal(t, chat, up.ChatID)
	require.Equal(t, dto.OutboxStatusPending, up.Status)

	bobLive.ExpectNone(quiet)
	carolLive.ExpectNone(quiet)
}

// LIVE-03: the upgrade needs the instance's own Origin and a logged-in user.
func TestLiveUpgradeChecks(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")

	_, resp := alice.DialLive("https://evil.example")
	require.Equal(t, http.StatusForbidden, resp.Status)
	_, resp = alice.DialLive("null")
	require.Equal(t, http.StatusForbidden, resp.Status)
	// Same host, other scheme is another origin.
	otherScheme := strings.Replace(env.Origin, "http://", "https://", 1)
	require.NotEqual(t, env.Origin, otherScheme)
	_, resp = alice.DialLive(otherScheme)
	require.Equal(t, http.StatusForbidden, resp.Status)

	_, resp = env.Anon().DialLive("")
	require.Equal(t, http.StatusUnauthorized, resp.Status)

	conn, resp := alice.DialLive("")
	require.Equal(t, http.StatusSwitchingProtocols, resp.Status)
	var hello dto.HelloData
	conn.Expect(dto.LiveEventTypeHello, &hello, liveWait)
	require.WithinDuration(t, time.Now(), hello.ServerTime, time.Minute)
}

// A connection that falls behind is told to refetch with a lagged frame.
func TestLiveForwardsLagged(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	live := dialLive(t, alice)
	env.App.Bus.Publish(events.Lagged{})
	live.Expect(dto.LiveEventTypeLagged, nil, liveWait)
}
