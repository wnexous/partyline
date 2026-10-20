package channel

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// builderMember returns the member row of the agent "builder" in the room.
func (e *chanEnv) builderMember() dto.RoomMember {
	e.t.Helper()
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](e.alice.Get("/api/v1/rooms/" + e.room.ID + "/members").RequireStatus(http.StatusOK)).Items {
		if m.Kind == dto.MemberKindAgent && m.MemberID == e.agentID {
			return m
		}
	}
	e.t.Fatal("builder is not a member")
	return dto.RoomMember{}
}

func (e *chanEnv) setInstructions(text string) {
	e.t.Helper()
	e.alice.Patch("/api/v1/rooms/"+e.room.ID, dto.UpdateRoomRequest{Instructions: &text}).RequireStatus(http.StatusOK)
}

// INS-02: room_info returns the room name, instructions, members (chats with platform and
// kind) and the agent's own membership settings.
func TestToolRoomInfo(t *testing.T) {
	e := newChanEnv(t)
	group := e.chatIn("whatsapp", "Lollerie", dto.ChatKindGroup)
	e.setInstructions("Tone: friendly. Language: Portuguese.")
	name, mode, keywords := "Lolo bot", dto.DeliveryModeMentions, []string{"pedido"}
	e.alice.Patch("/api/v1/rooms/"+e.room.ID+"/members/"+e.builderMember().ID, dto.UpdateMemberRequest{
		DisplayName: &name, Delivery: &mode, Keywords: &keywords,
	}).RequireStatus(http.StatusOK)
	s := e.connected(e.options())
	require.Equal(t, insNotice, s.notification().Content)

	text, isErr := s.call("room_info", toolArgs("room", "Support"))
	require.False(t, isErr, text)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &got))
	require.Equal(t, map[string]any{"id": e.room.ID, "name": "Support"}, got["room"])
	require.Equal(t, "Tone: friendly. Language: Portuguese.", got["instructions"])
	require.Equal(t, map[string]any{
		"display_name": "Lolo bot", "sign_outbound": true, "delivery": "mentions", "keywords": []any{"pedido"},
	}, got["me"])
	members := got["members"].([]any)
	require.Len(t, members, 2)
	byKind := map[string]map[string]any{}
	for _, m := range members {
		mm := m.(map[string]any)
		byKind[mm["kind"].(string)] = mm
	}
	require.Equal(t, map[string]any{
		"kind": "chat", "member_id": group, "name": "Lollerie", "platform": "whatsapp", "chat_kind": "group",
	}, byKind["chat"])
	require.Equal(t, map[string]any{
		"kind": "agent", "member_id": e.agentID, "name": "builder", "owner": "alice", "online": true,
	}, byKind["agent"])
}

// INS-03: the instructions notice reaches the session as a system event of its room, once
// per connection; a room without instructions sends none.
func TestInstructionsNoticeOnConnect(t *testing.T) {
	e := newChanEnv(t)
	e.setInstructions("Be brief.")
	m := e.post("first")
	s := e.connected(e.options())
	n := s.notification()
	require.Equal(t, insNotice, n.Content)
	require.Equal(t, map[string]string{
		"room": e.room.ID, "room_name": "Support", "author": "Partyline", "author_kind": "system",
		"sender_name": "", "trusted": "false", "kind": "system",
	}, n.Meta)
	require.Equal(t, m.ID, s.notification().Meta["message_id"])

	other := newChanEnv(t) // no instructions
	s2 := other.connected(other.options())
	m2 := other.post("no notice before me")
	require.Equal(t, m2.ID, s2.notification().Meta["message_id"])
}

// CTX-04: send_message takes chat_ids to answer only some chats; the older recipients name
// still works and both together reach the union.
func TestToolSendMessageChatIDs(t *testing.T) {
	e := newChanEnv(t)
	family := e.chatMember("Family")
	work := e.chatMember("Work")
	e.chatMember("Friends")
	s := e.connected(e.options())

	text, isErr := s.call("send_message", toolArgs("room", "Support", "text", "only family", "chat_ids", []string{family}))
	require.False(t, isErr, text)
	stored := e.messages(e.room.ID)[0]
	require.Equal(t, "only family", stored.Text)
	require.Equal(t, []string{family}, stored.Recipients)

	text, isErr = s.call("send_message", toolArgs("room", "Support", "text", "both", "chat_ids", []string{family}, "recipients", []string{work, family}))
	require.False(t, isErr, text)
	stored = e.messages(e.room.ID)[0]
	require.Equal(t, "both", stored.Text)
	require.ElementsMatch(t, []string{family, work}, stored.Recipients)
	require.Len(t, stored.Recipients, 2)
}

// CTX-04: the instructions explain chat_ids and room_info.
func TestInstructionsExplainChatIDsAndRoomInfo(t *testing.T) {
	require.Contains(t, Instructions, "send_message")
	require.Contains(t, Instructions, "chat_ids")
	require.Contains(t, Instructions, "room_info")
}

// insNotice is the INS-03 notice text as the spec words it.
const insNotice = "This room has instructions for its agents; call the room_info tool to read them."
