package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// agentMember returns the member row of agentID in the room.
func agentMember(t *testing.T, c *testkit.Client, roomID, agentID string) dto.RoomMember {
	t.Helper()
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](c.Get("/api/v1/rooms/" + roomID + "/members").RequireStatus(http.StatusOK)).Items {
		if m.Kind == dto.MemberKindAgent && m.MemberID == agentID {
			return m
		}
	}
	t.Fatalf("agent %s is not a member", agentID)
	return dto.RoomMember{}
}

// setMember patches the agent's member row as c.
func setMember(t *testing.T, c *testkit.Client, roomID, agentID string, req dto.UpdateMemberRequest) {
	t.Helper()
	m := agentMember(t, c, roomID, agentID)
	c.Patch("/api/v1/rooms/"+roomID+"/members/"+m.ID, req).RequireStatus(http.StatusOK)
}

// roomChat stores a chat of the given kind on a connector of alice and adds it to the room.
func (f *agentRoom) roomChat(t *testing.T, kind string) string {
	t.Helper()
	conn, chat := store.NewID(), store.NewID()
	ctx := context.Background()
	require.NoError(t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateConnector(ctx, db.CreateConnectorParams{ID: conn, OwnerID: f.alice.User.ID, Kind: "telegram", Name: "tg-" + kind, ConfigJson: "{}"}); err != nil {
			return err
		}
		return q.CreateChat(ctx, db.CreateChatParams{ID: chat, ConnectorID: conn, ExternalID: "ext-" + kind, Title: kind + " chat", Kind: kind})
	}))
	f.alice.SeedChatMember(f.room.ID, chat)
	return chat
}

func (f *agentRoom) postFromChat(t *testing.T, chatID, text string) rooms.Message {
	t.Helper()
	m, err := f.env.App.Rooms.PostFromChat(context.Background(), f.room.ID, rooms.ChatMessage{
		ChatID: chatID, SenderID: "100000001", SenderName: "Customer", Text: text,
	})
	require.NoError(t, err)
	return m
}

// MEM-03 (live): an agent member in mentions mode gets pushed only messages that mention it:
// "@<agent name>" and "@<display name>" in any case on word boundaries, keywords on word
// boundaries, every DM chat message and replies to its own messages. Everything is still
// stored in the room. Order proves the skips: each expected frame is the next one.
func TestMentionsModePushesOnlyMentions(t *testing.T) {
	f := newAgentRoom(t)
	group, dm := f.roomChat(t, "group"), f.roomChat(t, "dm")
	setMember(t, f.alice, f.room.ID, f.agentID, dto.UpdateMemberRequest{
		Delivery: ptr(dto.DeliveryModeMentions), DisplayName: ptr("Vega"), Keywords: &[]string{"deploy"},
	})
	conn := f.dial(t)
	conn.Welcome(wsWait)

	var want []string
	f.post(f.alice, "plain message")
	want = append(want, f.post(f.alice, "hey @BUILDER, ship it").ID)
	want = append(want, f.post(f.alice, "ping @vega").ID)
	f.post(f.alice, "mail x@builder now")
	f.post(f.alice, "ask @builders")
	want = append(want, f.postFromChat(t, group, "deploy now please").ID)
	f.postFromChat(t, group, "redeploy later")
	f.postFromChat(t, group, "no mention in the group")
	want = append(want, f.postFromChat(t, dm, "oi, tudo bem?").ID)
	own := f.postAsAgent(t, "build is green")
	reply := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages",
		dto.PostMessageRequest{Text: "thanks", ReplyTo: own.ID}).RequireStatus(http.StatusCreated))
	want = append(want, reply.ID)
	f.post(f.alice, "after the reply")
	want = append(want, f.post(f.alice, "@builder last").ID)

	for i, id := range want {
		require.Equal(t, id, conn.Message(wsWait).Message.ID, "frame %d", i)
	}
	page := testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + f.room.ID + "/messages?limit=50").RequireStatus(http.StatusOK))
	require.Len(t, page.Items, 13, "every message is stored")
}

// MEM-04 (live): an agent member in trusted mode gets only messages trusted for it (authored
// by its owner); the other agent, in the default all mode, gets both.
func TestTrustedModePushesOnlyTrusted(t *testing.T) {
	f := newTwoAgents(t)
	setMember(t, f.alice, f.room.ID, f.helperID, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeTrusted)})

	fromAlice := f.post(f.alice, "from alice")
	fromBob := f.post(f.bob, "from bob")
	got := f.helper.Message(wsWait)
	require.Equal(t, fromBob.ID, got.Message.ID, "alice's message is not trusted for bob's agent")
	require.True(t, got.Trusted)
	require.Equal(t, fromAlice.ID, f.builder.Message(wsWait).Message.ID)
	require.Equal(t, fromBob.ID, f.builder.Message(wsWait).Message.ID)
}

// MEM-03 (backlog): on connect the backlog leaves out messages the mode does not want (the
// member's keywords and display name count as mentions there too); skipped
// messages before the first delivered one advance the cursor, never past a delivered message
// that was not acknowledged; a backlog of only skipped messages moves the cursor past them.
func TestMentionsModeBacklogSkipsAndAdvancesCursor(t *testing.T) {
	f := newAgentRoom(t)
	setMember(t, f.alice, f.room.ID, f.agentID, dto.UpdateMemberRequest{
		Delivery: ptr(dto.DeliveryModeMentions), DisplayName: ptr("Vega"), Keywords: &[]string{"deploy"},
	})

	a := f.post(f.alice, "plain a")
	b := f.post(f.alice, "deploy b")
	f.post(f.alice, "plain c")
	conn := f.dial(t)
	conn.Welcome(wsWait)
	require.Equal(t, b.ID, conn.Message(wsWait).Message.ID)
	require.Equal(t, a.Seq, f.cursor(t), "skipped a advanced the cursor; b awaits its ack")
	d := f.post(f.alice, "@builder d")
	require.Equal(t, d.ID, conn.Message(wsWait).Message.ID, "c was skipped")
	conn.Ack(d.ID)
	require.Eventually(t, func() bool { return f.cursor(t) == d.Seq }, wsWait, 20*time.Millisecond)
	conn.Close()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(f.agentID) }, wsWait, 20*time.Millisecond)

	f.post(f.alice, "plain e")
	last := f.post(f.alice, "plain f")
	again := f.dial(t)
	again.Welcome(wsWait)
	g := f.post(f.alice, "@vega g")
	require.Equal(t, g.ID, again.Message(wsWait).Message.ID)
	require.Equal(t, last.Seq, f.cursor(t), "a backlog of skipped messages moves the cursor past them")
}

// MEM-03 (reply to me, two agents): with two agents in mentions mode, a reply to agent A's
// message reaches A only, live and in the backlog. Each agent's next frame proves the skip.
func TestMentionsModeReplyReachesOnlyTheRepliedAgent(t *testing.T) {
	f := newTwoAgents(t)
	for _, id := range []string{f.agentID, f.helperID} {
		setMember(t, f.alice, f.room.ID, id, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeMentions)})
	}
	own := f.postAsAgent(t, "build is green")
	reply := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages",
		dto.PostMessageRequest{Text: "thanks", ReplyTo: own.ID}).RequireStatus(http.StatusCreated))
	marker := f.post(f.alice, "@helper marker")

	require.Equal(t, reply.ID, f.builder.Message(wsWait).Message.ID, "the reply reaches the replied agent")
	require.Equal(t, marker.ID, f.helper.Message(wsWait).Message.ID, "the reply to builder skips helper")

	f.helper.Ack(marker.ID)
	require.Eventually(t, func() bool {
		seq, err := f.env.App.Store.R().GetAgentCursor(context.Background(), dbCursorParams(f.helperID, f.room.ID))
		return err == nil && seq == marker.Seq
	}, wsWait, 20*time.Millisecond)
	f.helper.Close()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(f.helperID) }, wsWait, 20*time.Millisecond)
	f.post(f.alice, "again, thanks")
	f.alice.Post("/api/v1/rooms/"+f.room.ID+"/messages",
		dto.PostMessageRequest{Text: "one more", ReplyTo: own.ID}).RequireStatus(http.StatusCreated)
	last := f.post(f.alice, "@helper back")
	again, resp := f.bob.Token(dto.TokenKindAgent, "desk").DialAgent("helper")
	require.NotNil(t, again, "upgrade refused: %d", resp.Status)
	again.Welcome(wsWait)
	require.Equal(t, last.ID, again.Message(wsWait).Message.ID, "the backlog skips the reply to builder")
}
