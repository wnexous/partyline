package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// mentionsOnly puts the agent member of the room in mentions mode with a keyword.
func mentionsOnly(t *testing.T, f *agentRoom) {
	t.Helper()
	setMember(t, f.alice, f.room.ID, f.agentID, dto.UpdateMemberRequest{
		Delivery: ptr(dto.DeliveryModeMentions), Keywords: ptr([]string{"deploy"}),
	})
}

// AID-06, GRP-04: after a rename a mentions-mode AI is reached by its new @handle and its new
// display name and no longer by the old handle; the room's member name and the keywords keep
// working.
func TestMentionsFollowTheProfile(t *testing.T) {
	f := newAgentRoom(t) // the agent "builder": handle builder, display name builder
	conn := f.dial(t)
	conn.Welcome(wsWait)
	mentionsOnly(t, f)

	first := f.post(f.alice, "@builder, are you there?")
	require.Equal(t, first.ID, conn.Message(wsWait).Message.ID, "the default handle reaches it")

	patchProfile(f.alice, f.agentID, dto.UpdateProfileRequest{DisplayName: ptr("Orion Bot"), Handle: ptr("nova")}).RequireStatus(http.StatusOK)
	f.post(f.alice, "ping @builder again")                // the old handle
	f.post(f.alice, "ping @nova-2 and @novae and @nova2") // handles that only start like it
	f.post(f.alice, "write to bob@nova.example")          // an address, not a mention
	f.post(f.alice, "nothing for anyone")                 // plain text
	byHandle := f.post(f.alice, "ping @NOVA, status?")    // the new handle, any case
	byName := f.post(f.alice, "hey @Orion Bot, status?")  // the new display name
	byKeyword := f.post(f.alice, "please deploy it")      // a keyword
	setMember(t, f.alice, f.room.ID, f.agentID, dto.UpdateMemberRequest{DisplayName: ptr("Lolo")})
	byRoomName := f.post(f.alice, "and @lolo too") // the name set for the AI in this room (GRP-04)

	for _, want := range []string{byHandle.ID, byName.ID, byKeyword.ID, byRoomName.ID} {
		require.Equal(t, want, conn.Message(wsWait).Message.ID)
	}
	conn.ExpectNone(quiet)
}

// AID-06: "@atlas" reaches the AI with handle atlas and not the one with handle atlas-2, and
// the other way round; each of them is reached by its own handle only.
func TestMentionOfAHandleDoesNotReachALongerHandle(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	room := createRoom(alice, "Ops")

	var conns [2]*testkit.AgentConn
	for i, name := range []string{"atlas", "atlas-2"} {
		id := registerAgent(t, env, alice, name)
		alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: id}).RequireStatus(http.StatusCreated)
		setMember(t, alice, room.ID, id, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeMentions)})
		conn, resp := tok.DialAgent(name)
		require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
		conn.Welcome(wsWait)
		conns[i] = conn
	}
	post := func(text string) dto.Message {
		return testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
	}

	short := post("@atlas, a question")
	long := post("@atlas-2, another question")
	require.Equal(t, short.ID, conns[0].Message(wsWait).Message.ID)
	require.Equal(t, long.ID, conns[1].Message(wsWait).Message.ID)
	conns[0].ExpectNone(quiet)
	conns[1].ExpectNone(quiet)
}

// The backlog an AI receives when it connects and the inbox of the turn-end hook filter by the
// profile too: only what mentions the new handle comes, in order.
func TestBacklogAndInboxFollowTheProfile(t *testing.T) {
	f := newAgentRoom(t)
	mentionsOnly(t, f)
	patchProfile(f.alice, f.agentID, dto.UpdateProfileRequest{DisplayName: ptr("Orion Bot"), Handle: ptr("nova")}).RequireStatus(http.StatusOK)
	f.post(f.alice, "@builder, the old handle")
	wanted := f.post(f.alice, "@nova, the new handle")

	inbox := testkit.Decode[dto.List[dto.AgentMessage]](f.agent.AsAgent("builder").
		Get("/api/v1/agent/pending").RequireStatus(http.StatusOK))
	require.Len(t, inbox.Items, 1)
	require.Equal(t, wanted.ID, inbox.Items[0].Message.ID)

	again := f.post(f.alice, "@nova once more")
	conn := f.dial(t)
	conn.Welcome(wsWait)
	require.Equal(t, again.ID, conn.Message(wsWait).Message.ID)
	conn.ExpectNone(quiet)
}
