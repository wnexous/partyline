package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

func setInstructions(c *testkit.Client, roomID, text string) {
	c.T.Helper()
	c.Patch("/api/v1/rooms/"+roomID, dto.UpdateRoomRequest{Instructions: &text}).RequireStatus(http.StatusOK)
}

// INS-02: room_info answers the room, its instructions, its members (chats with platform and
// kind) and the calling agent's own membership settings, by room id or name.
func TestAgentRoomInfo(t *testing.T) {
	f := newAgentRoom(t)
	chat := f.chatMember(t, "Family")
	setInstructions(f.alice, f.room.ID, "Answer in Portuguese. Client: Lollerie.")
	keywords := []string{"deploy", "build"}
	name, sign, mode := "Bot", false, dto.DeliveryModeMentions
	setMember(t, f.alice, f.room.ID, f.agentID, dto.UpdateMemberRequest{DisplayName: &name, SignOutbound: &sign, Delivery: &mode, Keywords: &keywords})

	for _, ref := range []string{f.room.ID, "support"} {
		info := testkit.Decode[dto.RoomInfo](f.rest().Get(agentRoomPath(ref, "/info")).RequireStatus(http.StatusOK))
		require.Equal(t, dto.RoomRef{ID: f.room.ID, Name: "Support"}, info.Room)
		require.Equal(t, "Answer in Portuguese. Client: Lollerie.", info.Instructions)
		require.Equal(t, dto.MemberKindAgent, info.Me.Kind)
		require.Equal(t, f.agentID, info.Me.MemberID)
		require.Equal(t, "Bot", info.Me.DisplayName)
		require.False(t, info.Me.SignOutbound)
		require.Equal(t, dto.DeliveryModeMentions, info.Me.Delivery)
		require.Equal(t, []string{"deploy", "build"}, info.Me.Keywords)
		require.Len(t, info.Members, 2)
		byKind := map[dto.MemberKind]dto.RoomMember{}
		for _, m := range info.Members {
			byKind[m.Kind] = m
		}
		require.Equal(t, chat, byKind[dto.MemberKindChat].MemberID)
		require.Equal(t, "Family", byKind[dto.MemberKindChat].Name)
		require.Equal(t, "telegram", byKind[dto.MemberKindChat].ConnectorKind)
		require.Equal(t, dto.ChatKindGroup, byKind[dto.MemberKindChat].ChatKind)
		require.Equal(t, f.agentID, byKind[dto.MemberKindAgent].MemberID)
	}
}

// INS-02: a room the agent is not a member of answers 403 "not a member of room <room>".
func TestAgentRoomInfoNotAMember(t *testing.T) {
	f := newAgentRoom(t)
	other := createRoom(f.alice, "Private")
	setInstructions(f.alice, other.ID, "secret context")
	for _, ref := range []string{other.ID, "Private", "nope"} {
		e := f.rest().Get(agentRoomPath(ref, "/info")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		require.Equal(t, "not a member of room "+ref, e.Message)
	}
}

// INS-03: on connect, each room with instructions gets exactly one notice (before its
// messages); rooms without instructions get none. A new connection repeats it.
func TestAgentWSInstructionsNotice(t *testing.T) {
	f := newAgentRoom(t)
	ops := createRoom(f.alice, "Ops")
	f.alice.Post("/api/v1/rooms/"+ops.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.agentID}).RequireStatus(http.StatusCreated)
	setInstructions(f.alice, ops.ID, strings.Repeat("x", 10))
	inOps := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+ops.ID+"/messages", dto.PostMessageRequest{Text: "hello ops"}).RequireStatus(http.StatusCreated))
	inSupport := f.post(f.alice, "hello support")

	for range 2 {
		conn := f.dial(t)
		conn.Welcome(wsWait)
		// Rooms come in name order: Ops (notice, then its message), then Support.
		n := conn.Notice(wsWait)
		require.Equal(t, dto.RoomRef{ID: ops.ID, Name: "Ops"}, n.Room)
		require.Equal(t, agents.InstructionsNotice, n.Text)
		require.Equal(t, inOps.ID, conn.Message(wsWait).Message.ID)
		require.Equal(t, inSupport.ID, conn.Message(wsWait).Message.ID)
		conn.ExpectNone(300 * time.Millisecond)
		conn.Close()
		require.Eventually(t, func() bool { return !f.env.App.Gateway.Online(f.agentID) }, wsWait, 10*time.Millisecond)
	}
}
