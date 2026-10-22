package channel

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// openDMWithBuilder opens alice's DM with the AI "builder" and returns its room.
func (e *chanEnv) openDMWithBuilder() dto.Room {
	e.t.Helper()
	return testkit.Decode[dto.Room](e.alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: e.agentID}).RequireStatus(http.StatusCreated))
}

// DM-03 through the real channel: a user's DM reaches the session as a DM notification that names
// the sender, the reply tool answers in that DM, and the tools address the DM by the other
// participant's @handle.
func TestChannelDeliversAndAnswersDMs(t *testing.T) {
	e := newChanEnv(t)
	dm := e.openDMWithBuilder()
	s := e.connected(e.options())

	sent := testkit.Decode[dto.Message](e.alice.Post("/api/v1/rooms/"+dm.ID+"/messages", dto.PostMessageRequest{Text: "hi builder"}).RequireStatus(http.StatusCreated))
	n := s.notification()
	require.Equal(t, "hi builder", n.Content)
	require.Equal(t, map[string]string{
		"conversation": "dm", "room": dm.ID, "room_name": "alice", "message_id": sent.ID, "author": "alice",
		"author_kind": "user", "author_id": e.alice.User.ID, "author_handle": "alice", "sender_name": "", "trusted": "true", "kind": "text",
	}, n.Meta)

	text, isErr := s.call("reply", toolArgs("message_id", sent.ID, "text", "hello alice"))
	require.False(t, isErr, text)
	timeline := e.messages(dm.ID)
	require.Len(t, timeline, 2)
	require.Equal(t, []any{"hello alice", e.agentID, sent.ID}, []any{timeline[0].Text, timeline[0].Author.ID, timeline[0].ReplyTo}, "the reply is in the DM")

	// The DM is addressed by the other participant's @handle, in any case.
	for _, tool := range []struct {
		name string
		args map[string]any
	}{
		{"send_message", toolArgs("room", "@alice", "text", "by handle")},
		{"send_message", toolArgs("room", "@Alice", "text", "by handle, any case")},
	} {
		text, isErr = s.call(tool.name, tool.args)
		require.False(t, isErr, text)
	}
	require.Equal(t, "by handle, any case", e.messages(dm.ID)[0].Text)
	require.Len(t, e.messages(e.room.ID), 0, "nothing reached the group")

	text, isErr = s.call("history", toolArgs("room", "@alice"))
	require.False(t, isErr, text)
	var page struct {
		Messages []map[string]string `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &page))
	require.Len(t, page.Messages, 4)
	for _, m := range page.Messages {
		require.Equal(t, []string{"dm", "alice"}, []string{m["conversation"], m["room_name"]})
	}

	text, isErr = s.call("room_info", toolArgs("room", "@alice"))
	require.False(t, isErr, text)
	var info struct {
		Room dto.RoomRef `json:"room"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &info))
	require.Equal(t, dto.RoomRef{ID: dm.ID, Name: "alice", Kind: dto.ConversationKindDM}, info.Room)

	text, isErr = s.call("send_message", toolArgs("room", "@nobody", "text", "hello?"))
	require.True(t, isErr)
	require.Contains(t, text, "not a member of room @nobody")
}

// TOOL-09 with DMs: list_rooms lists the AI's DMs next to its groups, each with its kind, and a DM
// with the intelligence it is with.
func TestToolListRoomsIncludesDMs(t *testing.T) {
	e := newChanEnv(t)
	dm := e.openDMWithBuilder()
	s := e.connected(e.options())

	text, isErr := s.call("list_rooms", map[string]any{})
	require.False(t, isErr, text)
	var out struct {
		Rooms []roomListing `json:"rooms"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	require.Len(t, out.Rooms, 2)
	byID := map[string]roomListing{}
	for _, r := range out.Rooms {
		byID[r.ID] = r
	}

	got := byID[dm.ID]
	require.Equal(t, dto.ConversationKindDM, got.Kind)
	require.Equal(t, "alice", got.Name)
	require.Equal(t, &contactListing{ID: e.alice.User.ID, Name: "alice", Handle: "alice", Kind: dto.IntelligenceKindHuman}, got.With)
	require.Len(t, got.Members, 2)

	group := byID[e.room.ID]
	require.Equal(t, dto.ConversationKindGroup, group.Kind)
	require.Equal(t, "Support", group.Name)
	require.Nil(t, group.With)
	require.NotContains(t, text, `"with":null`)
}

// The instructions tell a session what conversation="dm" and "group" mean and how to address a DM,
// in the words of the delivery it gets: meta keys for the channel, framed attributes elsewhere.
func TestInstructionsExplainConversations(t *testing.T) {
	e := newChanEnv(t)
	channel := instructionsOf(t, e.options())
	require.Contains(t, channel, `conversation="dm"`)
	require.Contains(t, channel, `conversation="group"`)
	require.Contains(t, channel, "author_handle")
	require.Contains(t, channel, `"@handle"`)

	o := e.options()
	o.ToolsOnly = true
	for name, text := range map[string]string{"tools-only": instructionsOf(t, o), "omp": func() string { o.ToolsOnly, o.Harness = false, HarnessOmp; return instructionsOf(t, o) }()} {
		require.Contains(t, text, `conversation="dm"`, name)
		require.Contains(t, text, `conversation="group"`, name)
		require.Contains(t, text, "list_rooms", name)
		require.NotContains(t, text, "author_handle", name+": framed messages carry no handle")
	}
}
