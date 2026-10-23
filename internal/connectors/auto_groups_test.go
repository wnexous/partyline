package connectors_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// groupPeer is a synthetic WhatsApp-style group id.
func groupPeer(n int) string { return fmt.Sprintf("120363%012d@g.us", n) }

// ACC-06 (independent test): with scope groups and no catch-all room, the first message of a new
// group chat creates a group named after the chat, owned by the connector's owner, with the
// operator AI and the chat as members; the AI receives it and its reply goes back through the
// chat; the group is made once.
func TestNewGroupChatBecomesAGroup(t *testing.T) {
	pc := newPerChat(t)
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeGroups)})

	pc.emit(text(groupID, "G1", "where is my order?"))

	got := pc.atlas.Message(connectortest.Timeout)
	require.Equal(t, "Support group", got.Room.Name, "the group is named after the chat")
	require.Empty(t, got.Room.Kind, "a group carries no kind in the frames of an AI")
	require.Equal(t, "where is my order?", got.Message.Text)
	require.False(t, got.Trusted)
	chat := pc.chatID(t, groupID)
	require.Equal(t, []string{got.Room.ID}, pc.roomsOf(t, chat))

	room := pc.room(t, pc.alice, got.Room.ID)
	require.Equal(t, []any{dto.ConversationKindGroup, "Support group", pc.alice.User.ID, dto.LevelManage, true},
		[]any{room.Kind, room.Name, room.Owner.ID, room.MyLevel, room.Broadcast}, "alice owns it and replies broadcast")
	require.False(t, room.BridgeChats)
	require.False(t, room.CatchAll)
	members := map[dto.MemberKind]dto.RoomMember{}
	for _, m := range pc.members(t, pc.alice, got.Room.ID) {
		require.NotContains(t, members, m.Kind)
		members[m.Kind] = m
	}
	require.Len(t, members, 2)
	require.Equal(t, pc.atlasID, members[dto.MemberKindAgent].MemberID, "the operator is a member")
	require.Equal(t, chat, members[dto.MemberKindChat].MemberID)
	require.True(t, members[dto.MemberKindChat].Outbound)

	// The AI's answer is queued for the chat; more messages go to the same group.
	reply := testkit.Decode[dto.Message](pc.tok.AsAgent("atlas").Post("/api/v1/agent/rooms/"+got.Room.ID+"/messages", dto.PostMessageRequest{Text: "on it"}).RequireStatus(http.StatusCreated))
	require.Len(t, reply.Outbox, 1)
	require.Equal(t, chat, reply.Outbox[0].ChatID)
	pc.emit(text(groupID, "G2", "hello?"))
	require.Equal(t, got.Room.ID, pc.atlas.Message(connectortest.Timeout).Room.ID)
	require.Len(t, pc.roomsOf(t, chat), 1)
	require.Zero(t, pc.dmRooms(t))
}

// ACC-06: a channel chat (Discord) is a group too, and a chat without a title is named after its
// platform. (The owner-operated case, where the owner became the member, is retired with the
// owner as operator, CID-01.)
func TestGroupChatsOfEveryKindAndOperator(t *testing.T) {
	pc := newPerChat(t)
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeAll)})

	pc.emit(channelText("channel-1", "H1", "in a channel"))
	room := pc.atlas.Message(connectortest.Timeout).Room
	require.Equal(t, "Support group", room.Name, "channelText reuses the support group title")

	untitled := text(groupPeer(1), "U1", "no title")
	untitled.ChatTitle = ""
	pc.emit(untitled)
	require.Equal(t, "Fake chat", pc.atlas.Message(connectortest.Timeout).Room.Name)

	long := text(groupPeer(2), "L1", "long title")
	long.ChatTitle = strings.Repeat("é", 150)
	pc.emit(long)
	require.Equal(t, strings.Repeat("é", 100), pc.atlas.Message(connectortest.Timeout).Room.Name, "cut to the room name limit")
}

// ACC-06: scopes dms and off leave group chats unrouted (and their senders unrecorded); a
// connector with a catch-all room keeps adding them to it; a chat the owner routed by hand stays
// where it is.
func TestGroupChatScopes(t *testing.T) {
	pc := newPerChat(t) // scope dms
	pc.emit(text(groupID, "G1", "while dms"))
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeOff)})
	pc.emit(text(otherID, "O1", "while off"))
	require.True(t, pc.hasChat(t, groupID))
	require.True(t, pc.hasChat(t, otherID))
	require.Empty(t, pc.roomsOf(t, pc.chatID(t, groupID)))
	require.Empty(t, pc.people(t))
	pc.atlas.ExpectNone(300 * time.Millisecond)

	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeGroups)})
	pc.routedRoom(t, "by hand", pc.chatID(t, otherID))
	pc.emit(text(otherID, "O2", "already in a room"))
	require.Len(t, pc.roomsOf(t, pc.chatID(t, otherID)), 1)
	pc.atlas.ExpectNone(300 * time.Millisecond)

	catchAll := testkit.Decode[dto.Room](pc.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "catch-all"}).RequireStatus(http.StatusCreated))
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeGroups), AutoRouteRoomID: &catchAll.ID})
	pc.emit(text(groupPeer(1), "C1", "to the catch-all room"))
	require.Equal(t, []string{catchAll.ID}, pc.roomsOf(t, pc.chatID(t, groupPeer(1))))
	pc.atlas.ExpectNone(300 * time.Millisecond)
}

// ACC-06 (concurrency): first messages of one new group chat arriving at once make one group and
// store every message.
func TestGroupChatConcurrentFirstMessagesMakeOneGroup(t *testing.T) {
	pc := newPerChat(t)
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeGroups)})
	var wg sync.WaitGroup
	for _, id := range []string{"G1", "G2", "G3", "G4"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pc.inst.Emit(context.Background(), text(groupID, id, "burst "+id))
		}()
	}
	wg.Wait()

	rooms := pc.roomsOf(t, pc.chatID(t, groupID))
	require.Len(t, rooms, 1)
	require.Equal(t, 1, pc.count(t, "SELECT COUNT(*) FROM rooms WHERE kind = 'group'"))
	require.Len(t, pc.messages(t, rooms[0]), 4)
}

// ACC-06, ROUTE-08, ROUTE-11: DMs and groups share the connector's limit of 60 new routes per
// hour; the 61st new chat is not recorded, nothing is stored and the connector says so; chats
// already routed keep flowing, and an hour later the chat is routed.
func TestGroupChatHourlyCapIsSharedWithDMs(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	pc := newPerChat(t, testkit.WithClock(clock.Now))
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeAll)})
	for i := range 30 {
		pc.emit(dmText(dmPeer(i), fmt.Sprintf("D%d", i), "hello"))
		pc.emit(text(groupPeer(i), fmt.Sprintf("G%d", i), "hello"))
	}
	require.Equal(t, 30, pc.dmRooms(t))
	require.Equal(t, 30, pc.count(t, "SELECT COUNT(*) FROM rooms WHERE kind = 'group'"))
	require.Empty(t, pc.connector(t).LastError)

	before := pc.messageCount(t)
	pc.emit(text(groupPeer(60), "G60", "one too many"))
	pc.emit(dmText(dmPeer(61), "D61", "and a DM"))
	require.Equal(t, before, pc.messageCount(t), "nothing stored")
	require.False(t, pc.hasChat(t, groupPeer(60)), "a capped chat leaves no chat record")
	require.False(t, pc.hasChat(t, dmPeer(61)))
	require.Equal(t, "auto-route limit reached", pc.connector(t).LastError)
	pc.emit(text(groupPeer(0), "G0b", "already routed"))
	require.Equal(t, before+1, pc.messageCount(t), "routed chats keep flowing")

	clock.Advance(time.Hour + time.Second)
	pc.emit(text(groupPeer(60), "G60b", "an hour later"))
	require.Equal(t, 31, pc.count(t, "SELECT COUNT(*) FROM rooms WHERE kind = 'group'"))
	require.Empty(t, pc.connector(t).LastError, "the next successful route clears the detail")
}

// ROUTE-02 edge case, for per-chat groups: a chat a manager removed from its group is not given
// a new group for 24 hours (fake clock: still out just before, routed just after).
func TestGroupChatRemovedFromItsGroupWaits24Hours(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	pc := newPerChat(t, testkit.WithClock(clock.Now))
	pc.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeGroups)})
	pc.emit(text(groupID, "G1", "first"))
	chat := pc.chatID(t, groupID)
	group := pc.roomsOf(t, chat)[0]
	var member dto.RoomMember
	for _, m := range pc.members(t, pc.alice, group) {
		if m.Kind == dto.MemberKindChat {
			member = m
		}
	}
	pc.alice.Delete("/api/v1/rooms/" + group + "/members/" + member.ID).RequireStatus(http.StatusNoContent)

	before := pc.messageCount(t)
	pc.emit(text(groupID, "G2", "right after"))
	clock.Advance(24*time.Hour - time.Minute)
	pc.emit(text(groupID, "G3", "almost a day later"))
	require.Empty(t, pc.roomsOf(t, chat))
	require.Equal(t, before, pc.messageCount(t), "nothing stored while it waits")
	require.True(t, pc.hasChat(t, groupID), "the chat stays recorded")

	clock.Advance(2 * time.Minute)
	pc.emit(text(groupID, "G4", "a day later"))
	rooms := pc.roomsOf(t, chat)
	require.Len(t, rooms, 1)
	require.NotEqual(t, group, rooms[0], "a new group")
	require.Equal(t, "a day later", pc.messages(t, rooms[0])[0].Text)

	// A manager can put it back at once.
	again := pc.members(t, pc.alice, rooms[0])
	for _, m := range again {
		if m.Kind == dto.MemberKindChat {
			pc.alice.Delete("/api/v1/rooms/" + rooms[0] + "/members/" + m.ID).RequireStatus(http.StatusNoContent)
		}
	}
	pc.alice.Post("/api/v1/rooms/"+rooms[0]+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: chat}).RequireStatus(http.StatusCreated)
	pc.emit(text(groupID, "G5", "manual"))
	require.Equal(t, "manual", pc.messages(t, rooms[0])[0].Text)
}
