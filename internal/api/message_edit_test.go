package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// Edit and delete messages (messenger-polish: EDT-01..EDT-10, DLT-01..DLT-10; AD-026) over HTTP, the live
// WebSocket and the agent WebSocket. The rules of the service are tested in package rooms, the AI tools and
// the frames the channel hands to a harness in package channel.

func roomMessagePath(roomID, id string) string { return "/api/v1/rooms/" + roomID + "/messages/" + id }
func agentMessagePath(id string) string        { return "/api/v1/agent/messages/" + id }
func editedTo(text string) dto.EditMessageRequest {
	return dto.EditMessageRequest{Text: text}
}

// editMessage edits the message as c and returns the answer.
func editMessage(t *testing.T, c *testkit.Client, roomID, id, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](c.Patch(roomMessagePath(roomID, id), editedTo(text)).RequireStatus(http.StatusOK))
}

// deleteMessage deletes the message as c and returns the placeholder.
func deleteMessage(t *testing.T, c *testkit.Client, roomID, id string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](c.Delete(roomMessagePath(roomID, id)).RequireStatus(http.StatusOK))
}

// plantMessage stores a message the API cannot create - a notice of the system, a message of a chat, an answer
// an AI gave in a DM that is history now - and returns its id.
func plantMessage(t *testing.T, env *testkit.Env, roomID string, p db.CreateMessageParams) string {
	t.Helper()
	ctx := context.Background()
	p.ID, p.RoomID = store.NewID(), roomID
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		room, err := q.NextRoomSeq(ctx, db.NextRoomSeqParams{UpdatedAt: time.Now().UnixMilli(), ID: roomID})
		if err != nil {
			return err
		}
		p.Seq, p.Kind, p.RecipientsJson, p.CreatedAt = room.NextSeq-1, "text", "[]", time.Now().UnixMilli()
		return q.CreateMessage(ctx, p)
	}))
	return p.ID
}

// groupMessageSetup is the setup of the message matrices over a group: a group of the organization made by
// the role "member", with the roles "target" and "guest" in the room, and a message of "target".
func groupMessageSetup(env *testkit.Env, roles *testkit.OrgRoles) func(t *testing.T) map[string]string {
	return func(t *testing.T) map[string]string {
		creator, target := *roles.Member, *roles.Target
		creator.T, target.T = t, t
		room := groupIn(t, env, &creator, roles.Org.ID, "messages")
		addToRoom(t, env, &creator, room.ID, roles.Target)
		addToRoom(t, env, &creator, room.ID, roles.Guest)
		return map[string]string{"room": room.ID, "message": say(&target, room.ID, "original").ID}
	}
}

// dmMessageSetup is the setup of the DM matrix: the DM between the role "target" and an AI of the role "manager",
// with a message of "target".
func dmMessageSetup(env *testkit.Env, roles *testkit.OrgRoles) func(t *testing.T) map[string]string {
	setup := dmRoomSetup(env, roles)
	return func(t *testing.T) map[string]string {
		res := setup(t)
		target := *roles.Target
		target.T = t
		page := testkit.Decode[dto.Page[dto.Message]](target.Get("/api/v1/rooms/" + res["room"] + "/messages").RequireStatus(http.StatusOK))
		require.Len(t, page.Items, 1)
		res["message"] = page.Items[0].ID
		return res
	}
}

// messageWant is the expected status of a route on a message that the role "target" wrote in the group the role
// "member" made, by role: the author, whoever manages the group (its creator and the admin of the organization),
// the other person in the room ("guest", who only posts) and everybody else.
func messageWant(author, managers, poster, other int) map[string]int {
	return map[string]int{
		"target": author, "member": managers, "admin": managers, "guest": poster,
		"manager": other, "peer": other, "inviter": other, "outsider": other, "instance_admin": other,
	}
}

// EDT-02, DLT-02, AD-016 over HTTP: only the author edits - whoever manages the group, an admin of the
// organization and the manager of the author's AI included - and a group's managers delete as well as the author;
// in a DM only the author does and whoever only views it as the manager of its AI is forbidden; whoever cannot
// view the room, an instance admin among them, gets 404.
func TestMessageRoutesAuthMatrix(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	at := func(res map[string]string) string { return roomMessagePath(res["room"], res["message"]) }
	edit := func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
		return c.Patch(at(res), editedTo("changed"))
	}
	del := func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
		return c.Delete(at(res))
	}
	t.Run("group", func(t *testing.T) {
		runRoleRoutes(t, roles, groupMessageSetup(env, roles), []roleRoute{
			{"edit", edit, messageWant(200, 403, 403, 404)},
			{"delete", del, messageWant(200, 200, 403, 404)},
		})
	})
	t.Run("dm", func(t *testing.T) {
		runRoleRoutes(t, roles, dmMessageSetup(env, roles), []roleRoute{
			{"edit", edit, dmWant(200, 403, 404)},
			{"delete", del, dmWant(200, 403, 404)},
		})
	})

	t.Run("anonymous and agent tokens", func(t *testing.T) {
		res := groupMessageSetup(env, roles)(t)
		env.Anon().Patch(at(res), editedTo("x")).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
		env.Anon().Delete(at(res)).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
		// The tools of an AI reach agent routes only.
		token := roles.Target.Token(dto.TokenKindAgent, "tools")
		token.Patch(at(res), editedTo("x")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		token.Delete(at(res)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})

	t.Run("a message of another room", func(t *testing.T) {
		res := groupMessageSetup(env, roles)(t)
		other := groupMessageSetup(env, roles)(t)
		target := *roles.Target
		target.T = t
		target.Patch(roomMessagePath(other["room"], res["message"]), editedTo("x")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		target.Delete(roomMessagePath(other["room"], res["message"])).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		target.Patch(roomMessagePath(res["room"], "no-such-message"), editedTo("x")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		target.Delete(roomMessagePath(res["room"], "no-such-message")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	})
}

// EDT-01: the author replaces the text; the message keeps its id, sequence number, author, time and place and
// gets edited_at; the next edit moves edited_at on.
func TestEditReplacesTheTextInPlace(t *testing.T) {
	env, clock := clockEnv(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := createRoom(alice, "ops")
	addToRoom(t, env, alice, room.ID, bob)
	first := say(alice, room.ID, "first")
	clock.Advance(time.Minute)
	typo := say(alice, room.ID, "helo wrld")
	last := say(bob, room.ID, "last")

	clock.Advance(24 * time.Hour)
	got := editMessage(t, alice, room.ID, typo.ID, "hello world")
	require.Equal(t, typo.ID, got.ID)
	require.Equal(t, typo.Seq, got.Seq)
	require.Equal(t, typo.CreatedAt, got.CreatedAt)
	require.Equal(t, typo.Author, got.Author)
	require.Equal(t, "hello world", got.Text)
	require.NotNil(t, got.EditedAt)
	require.True(t, got.EditedAt.Equal(clock.Now()), "edited_at is the time of the edit: %s", got.EditedAt)
	require.Nil(t, got.DeletedAt)

	read := timeline(t, bob, room.ID)
	require.Equal(t, []string{"first", "hello world", "last"}, texts(read), "the edited message keeps its place")
	require.Equal(t, []int64{first.Seq, typo.Seq, last.Seq}, []int64{read[0].Seq, read[1].Seq, read[2].Seq})
	require.Nil(t, read[0].EditedAt)
	require.NotNil(t, read[1].EditedAt)
	require.Nil(t, read[2].EditedAt)

	clock.Advance(24 * time.Hour)
	again := editMessage(t, alice, room.ID, typo.ID, "hello, world")
	require.True(t, again.EditedAt.Equal(clock.Now()), "edited_at follows the last edit")
	require.Equal(t, "hello, world", timeline(t, bob, room.ID)[1].Text)
}

// EDT-03: what is not text a person or an AI wrote is not edited: a reaction of the caller's is 422, a system
// notice and a message a chat wrote - also one linked to the caller - have no author, and a deleted message is
// 409 conflict.
func TestEditRefusesWhatIsNotAText(t *testing.T) {
	f := newAgentRoom(t)
	chat := f.roomChat(t, "group")
	linked, err := f.env.App.Rooms.PostFromChat(context.Background(), f.room.ID, rooms.ChatMessage{
		ChatID: chat, SenderID: "100000001", SenderName: "Alice on Telegram", SenderUserID: f.alice.User.ID, AuthorIID: f.alice.User.ID, Text: "from a platform",
	})
	require.NoError(t, err)
	notice := plantMessage(t, f.env, f.room.ID, db.CreateMessageParams{AuthorKind: "system", Kind: "system", Text: "Agent delivery is paused"})
	path := func(id string) string { return roomMessagePath(f.room.ID, id) }

	f.alice.Patch(path(notice), editedTo("rewritten")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.alice.Patch(path(linked.ID), editedTo("rewritten")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	// A reaction its author tries to edit (the AI reacts to a message of a chat).
	reaction := testkit.Decode[dto.Message](f.rest().Post("/api/v1/agent/messages/"+linked.ID+"/react", dto.ReactRequest{Emoji: "👍"}).RequireStatus(http.StatusCreated))
	e := f.rest().Patch(agentMessagePath(reaction.ID), editedTo("👎")).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "message")

	gone := f.post(f.alice, "soon gone")
	deleteMessage(t, f.alice, f.room.ID, gone.ID)
	f.alice.Patch(path(gone.ID), editedTo("back")).RequireError(http.StatusConflict, dto.ErrorCodeConflict)

	text := map[string]string{}
	for _, m := range timeline(t, f.alice, f.room.ID) {
		text[m.ID] = m.Text
	}
	require.Equal(t, "from a platform", text[linked.ID])
	require.Equal(t, "Agent delivery is paused", text[notice])
	require.Equal(t, "👍", text[reaction.ID])
	require.Empty(t, text[gone.ID], "a deleted message stays deleted")
}

// EDT-04: the new text follows MSG-02; a message with files keeps them and may lose its text; the same text is a
// no-op.
func TestEditValidatesTheText(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "ops")
	m := say(alice, room.ID, "plain")
	path := roomMessagePath(room.ID, m.ID)

	for name, text := range map[string]string{"empty": "", "blank": " \n\t ", "too long": strings.Repeat("x", dto.MaxTextLength+1)} {
		e := alice.Patch(path, editedTo(text)).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "text", name)
	}
	editMessage(t, alice, room.ID, m.ID, strings.Repeat("é", dto.MaxTextLength))

	file := testkit.Decode[dto.Message](postMultipart(alice, room.ID, dto.PostMessageRequest{Text: "caption"}, upload{"a.txt", "text/plain", "hello"}).RequireStatus(http.StatusCreated))
	got := editMessage(t, alice, room.ID, file.ID, "")
	require.Empty(t, got.Text, "a message with a file may have no caption")
	require.Equal(t, file.Attachments, got.Attachments, "the file stays")
	require.NotNil(t, got.EditedAt)

	same := editMessage(t, alice, room.ID, file.ID, "")
	require.Equal(t, got.EditedAt, same.EditedAt, "the same text is no edit")
	fresh := say(alice, room.ID, "fresh")
	require.Nil(t, editMessage(t, alice, room.ID, fresh.ID, "fresh").EditedAt, "and does not mark the message")
}

// EDT-05, DLT-03: a DM the connection proxy left between an AI and a person is history; edits and deletes of
// what the AI wrote there are 422 history_only.
func TestEditAndDeleteAreRefusedInHistoryDMs(t *testing.T) {
	f := newHistoryFixture(t)
	mine := plantMessage(t, f.env, f.dm, db.CreateMessageParams{AuthorKind: "agent", AuthorID: f.atlas, AuthorIid: f.atlas, Text: "an answer from before the proxy"})

	f.atlasAI.Patch(agentMessagePath(mine), editedTo("rewritten")).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)
	f.atlasAI.Delete(agentMessagePath(mine)).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)
	require.Contains(t, f.history(t, f.alice), "an answer from before the proxy", "the history is as it was")
}

// EDT-05, DLT-03: the DM of a disabled user stays readable and refuses edits like posts; the author may still
// take a message back.
func TestEditIsRefusedInADMWithADisabledUser(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := dmWith(alice, bob.User.ID, http.StatusCreated)
	m := say(alice, room.ID, "before")

	disableUser(admin, bob.User.ID, true)
	alice.Patch(roomMessagePath(room.ID, m.ID), editedTo("after")).RequireError(http.StatusForbidden, dto.ErrorCodeCounterpartDisabled)
	require.Equal(t, "before", timeline(t, alice, room.ID)[0].Text)
	require.NotNil(t, deleteMessage(t, alice, room.ID, m.ID).DeletedAt, "taking a message back is not posting")
}

// EDT-06, DLT-06, DLT-01: every live client that may view the room - the author's too - gets room.message_updated
// with the message as it is now; a client that may not gets nothing; the same text and a second delete publish
// nothing.
func TestEditAndDeleteReachLiveWebClients(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	room := createRoom(alice, "ops")
	addToRoom(t, env, alice, room.ID, bob)
	m := say(alice, room.ID, "helo")
	aliceLive, resp := alice.DialLive("")
	require.NotNil(t, aliceLive, "%d", resp.Status)
	bobLive, _ := bob.DialLive("")
	carolLive, _ := carol.DialLive("") // no access to the room
	next := func(c *testkit.LiveConn) dto.Message {
		t.Helper()
		var got dto.Message
		c.Expect(dto.LiveEventTypeRoomMessageUpdated, &got, wsWait)
		return got
	}

	editMessage(t, alice, room.ID, m.ID, "hello")
	for name, c := range map[string]*testkit.LiveConn{"the author": aliceLive, "a member": bobLive} {
		got := next(c)
		require.Equal(t, m.ID, got.ID, name)
		require.Equal(t, "hello", got.Text, name)
		require.NotNil(t, got.EditedAt, name)
		require.Nil(t, got.DeletedAt, name)
	}

	editMessage(t, alice, room.ID, m.ID, "hello") // the same text
	deleteMessage(t, alice, room.ID, m.ID)
	for name, c := range map[string]*testkit.LiveConn{"the author": aliceLive, "a member": bobLive} {
		got := next(c)
		require.Equal(t, m.ID, got.ID, name)
		require.Empty(t, got.Text, name)
		require.Empty(t, got.Attachments, name)
		require.NotNil(t, got.DeletedAt, name)
		require.Nil(t, got.EditedAt, name)
	}
	deleteMessage(t, alice, room.ID, m.ID) // deleted already
	aliceLive.ExpectNone(300 * time.Millisecond)
	bobLive.ExpectNone(300 * time.Millisecond)
	carolLive.ExpectNone(300*time.Millisecond, dto.LiveEventTypeHello)
}

// EDT-07: an edit reaches the online sessions of the other AI members of the room as a message_edited frame,
// with the room, the trusted flag of each recipient and the message with its new text. The author's own session
// hears nothing, and the connections stay usable.
func TestEditReachesTheOtherAIsOfTheRoom(t *testing.T) {
	f := newTwoAgents(t)
	m := f.post(f.alice, "helo wrld")
	f.builder.Message(wsWait)
	f.helper.Message(wsWait)

	edited := editMessage(t, f.alice, f.room.ID, m.ID, "hello world")
	got := f.builder.Edited(wsWait)
	require.Equal(t, dto.RoomRef{ID: f.room.ID, Name: "Support"}, got.Room)
	require.True(t, got.Trusted, "alice manages her own AI")
	require.Equal(t, m.ID, got.Message.ID)
	require.Equal(t, m.Seq, got.Message.Seq)
	require.Equal(t, "hello world", got.Message.Text)
	require.Equal(t, edited.EditedAt, got.Message.EditedAt)
	got = f.helper.Edited(wsWait)
	require.False(t, got.Trusted, "but she does not manage bob's")
	require.Equal(t, "hello world", got.Message.Text)

	// An AI's own edit goes to the others.
	ownerBuilder := f.rest()
	mine := testkit.Decode[dto.Message](ownerBuilder.Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "build is gren"}).RequireStatus(http.StatusCreated))
	require.Equal(t, mine.ID, f.helper.Message(wsWait).Message.ID)
	ownerBuilder.Patch(agentMessagePath(mine.ID), editedTo("build is green")).RequireStatus(http.StatusOK)
	require.Equal(t, "build is green", f.helper.Edited(wsWait).Message.Text)
	f.builder.ExpectNone(300 * time.Millisecond)

	// Nothing about the edit gets in the way of the next message.
	next := f.post(f.alice, "next")
	require.Equal(t, next.ID, f.builder.Message(wsWait).Message.ID)
	require.Equal(t, next.ID, f.helper.Message(wsWait).Message.ID)
}

// MEM-03, EDT-07, DLT-07: the frame follows the delivery mode. A session in mentions mode hears about a message
// when its text mentioned the AI before or mentions it now; a delete when the text mentioned it. Nothing is sent
// for chatter that never concerned it, or in a room the loop guard paused.
func TestUpdateFramesFollowTheDeliveryMode(t *testing.T) {
	f := newAgentRoom(t)
	setMember(t, f.alice, f.room.ID, f.agentID, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeMentions)})
	conn := f.dial(t)
	conn.Welcome(wsWait)
	plain := f.post(f.alice, "nothing for you")
	asked := f.post(f.alice, "please @builder look")
	require.Equal(t, asked.ID, conn.Message(wsWait).Message.ID, "only the mention was delivered")

	editMessage(t, f.alice, f.room.ID, plain.ID, "now it says @builder")
	require.Equal(t, "now it says @builder", conn.Edited(wsWait).Message.Text, "the new text mentions the AI")
	editMessage(t, f.alice, f.room.ID, plain.ID, "now it says @builder again")
	require.Equal(t, plain.ID, conn.Edited(wsWait).Message.ID, "both texts mention it")
	editMessage(t, f.alice, f.room.ID, plain.ID, "just chatter")
	require.Equal(t, "just chatter", conn.Edited(wsWait).Message.Text, "the old text mentioned it: it is told the mention is gone")
	editMessage(t, f.alice, f.room.ID, plain.ID, "more chatter")
	conn.ExpectNone(300 * time.Millisecond)
	deleteMessage(t, f.alice, f.room.ID, plain.ID)
	conn.ExpectNone(300 * time.Millisecond)

	deleteMessage(t, f.alice, f.room.ID, asked.ID)
	require.Equal(t, asked.ID, conn.Deleted(wsWait).Message.ID, "it had read the mention")

	// A paused room gets no frames (DLV-08).
	setMember(t, f.alice, f.room.ID, f.agentID, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeAll)})
	live := f.post(f.alice, "all messages now")
	require.Equal(t, live.ID, conn.Message(wsWait).Message.ID)
	require.NoError(t, f.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SetRoomLoopState(context.Background(), db.SetRoomLoopStateParams{AgentStreak: 25, AgentsPaused: true, ID: f.room.ID})
	}))
	editMessage(t, f.alice, f.room.ID, live.ID, "edited while paused")
	deleteMessage(t, f.alice, f.room.ID, live.ID)
	conn.ExpectNone(300 * time.Millisecond)
}

// DLT-07: a delete reaches the online sessions of the other AI members as a message_deleted frame with the
// placeholder, and the author's own session hears nothing.
func TestDeleteReachesTheOtherAIsOfTheRoom(t *testing.T) {
	f := newTwoAgents(t)
	m := f.post(f.alice, "secret")
	f.builder.Message(wsWait)
	f.helper.Message(wsWait)

	deleteMessage(t, f.alice, f.room.ID, m.ID)
	for name, c := range map[string]*testkit.AgentConn{"builder": f.builder, "helper": f.helper} {
		got := c.Deleted(wsWait)
		require.Equal(t, dto.RoomRef{ID: f.room.ID, Name: "Support"}, got.Room, name)
		require.Equal(t, m.ID, got.Message.ID, name)
		require.Empty(t, got.Message.Text, name)
		require.Empty(t, got.Message.Attachments, name)
		require.NotNil(t, got.Message.DeletedAt, name)
		require.Equal(t, name == "builder", got.Trusted, "alice manages builder only")
	}

	mine := testkit.Decode[dto.Message](f.rest().Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "oops"}).RequireStatus(http.StatusCreated))
	require.Equal(t, mine.ID, f.helper.Message(wsWait).Message.ID)
	f.rest().Delete(agentMessagePath(mine.ID)).RequireStatus(http.StatusOK)
	require.Equal(t, mine.ID, f.helper.Deleted(wsWait).Message.ID)
	f.builder.ExpectNone(300 * time.Millisecond)
}

// EDT-08, DLT-08: an AI edits and deletes its own messages through the agent routes; another member's are
// 403, unknown messages and those of rooms it is not in are 404 "message not found", user credentials are 403,
// and a deleted message cannot be edited.
func TestAgentEditsAndDeletesItsOwnMessages(t *testing.T) {
	f := newTwoAgents(t)
	builder := f.rest()
	helper := f.bob.Token(dto.TokenKindAgent, "rest").AsAgent("helper")
	mine := testkit.Decode[dto.Message](builder.Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "build is gren"}).RequireStatus(http.StatusCreated))
	alices := f.post(f.alice, "alice says")
	private := createRoom(f.alice, "Private")
	elsewhere := say(f.alice, private.ID, "not for the AIs")

	got := testkit.Decode[dto.Message](builder.Patch(agentMessagePath(mine.ID), editedTo("build is green")).RequireStatus(http.StatusOK))
	require.Equal(t, mine.ID, got.ID)
	require.Equal(t, "build is green", got.Text)
	require.NotNil(t, got.EditedAt)
	require.Equal(t, f.agentID, got.AuthorID)

	helper.Patch(agentMessagePath(mine.ID), editedTo("hijacked")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	helper.Delete(agentMessagePath(mine.ID)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	builder.Patch(agentMessagePath(alices.ID), editedTo("hijacked")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	builder.Delete(agentMessagePath(alices.ID)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	for _, id := range []string{"no-such-message", elsewhere.ID} {
		e := builder.Patch(agentMessagePath(id), editedTo("x")).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		require.Equal(t, "message not found", e.Message)
		e = builder.Delete(agentMessagePath(id)).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		require.Equal(t, "message not found", e.Message)
	}
	f.alice.Token(dto.TokenKindUser, "script").Patch(agentMessagePath(mine.ID), editedTo("x")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.alice.Patch(agentMessagePath(mine.ID), editedTo("x")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	e := builder.Patch(agentMessagePath(mine.ID), editedTo(" ")).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "text")

	placeholder := testkit.Decode[dto.Message](builder.Delete(agentMessagePath(mine.ID)).RequireStatus(http.StatusOK))
	require.NotNil(t, placeholder.DeletedAt)
	require.Empty(t, placeholder.Text)
	builder.Patch(agentMessagePath(mine.ID), editedTo("back")).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	testkit.Decode[dto.Message](builder.Delete(agentMessagePath(mine.ID)).RequireStatus(http.StatusOK))
	require.Equal(t, []string{"", "alice says"}, texts(timeline(t, f.alice, f.room.ID)), "alice's message is untouched")
}

// TOOL-08, EDT-08, DLT-08: edits and deletes use the 30 posts a minute of the AI, like the posts they follow.
func TestAgentEditAndDeleteShareThePostingLimit(t *testing.T) {
	f := newAgentRoom(t)
	builder := f.rest()
	var first dto.Message
	for i := range agents.PostLimit - 1 {
		m := testkit.Decode[dto.Message](builder.Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "post"}).RequireStatus(http.StatusCreated))
		if i == 0 {
			first = m
		}
	}
	builder.Patch(agentMessagePath(first.ID), editedTo("the last one that fits")).RequireStatus(http.StatusOK)
	builder.Patch(agentMessagePath(first.ID), editedTo("one too many")).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	builder.Delete(agentMessagePath(first.ID)).RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	require.Equal(t, "the last one that fits", timeline(t, f.alice, f.room.ID)[0].Text)
}

// DLT-01, DLT-05: a delete leaves a placeholder in the message's place - id, sequence number, author, time and
// reply_to, no text, no files, deleted_at - the file cannot be fetched and is not listed, the messages that
// replied to it keep pointing to it, and an edit is a conflict. A group member who only posts may not delete
// what another wrote.
func TestDeleteLeavesAPlaceholder(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := createRoom(alice, "ops")
	addToRoom(t, env, alice, room.ID, bob)
	question := say(bob, room.ID, "ready?")
	report := testkit.Decode[dto.Message](postMultipart(alice, room.ID, dto.PostMessageRequest{Text: "the report", ReplyTo: question.ID},
		upload{"report.txt", "text/plain", "numbers"}).RequireStatus(http.StatusCreated))
	thanks := testkit.Decode[dto.Message](bob.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "thanks", ReplyTo: report.ID}).RequireStatus(http.StatusCreated))
	file := report.Attachments[0].ID
	bob.Get("/api/v1/attachments/" + file).RequireStatus(http.StatusOK)

	bob.Delete(roomMessagePath(room.ID, report.ID)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	got := deleteMessage(t, alice, room.ID, report.ID)
	require.Equal(t, report.ID, got.ID)
	require.Equal(t, report.Seq, got.Seq)
	require.Equal(t, report.CreatedAt, got.CreatedAt)
	require.Equal(t, report.Author, got.Author)
	require.Equal(t, question.ID, got.ReplyTo)
	require.Empty(t, got.Text)
	require.NotNil(t, got.Attachments, "an empty list, not null")
	require.Empty(t, got.Attachments)
	require.NotNil(t, got.DeletedAt)
	require.Nil(t, got.EditedAt)

	read := timeline(t, bob, room.ID)
	require.Equal(t, []string{"ready?", "", "thanks"}, texts(read))
	require.Equal(t, got, read[1], "the page returns the placeholder the delete answered with")
	require.Equal(t, report.ID, read[2].ReplyTo, "the reply still points to the deleted message")
	require.Equal(t, thanks.ID, read[2].ID)
	bob.Get("/api/v1/attachments/"+file).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	files := testkit.Decode[dto.Page[dto.RoomFile]](bob.Get("/api/v1/rooms/" + room.ID + "/attachments").RequireStatus(http.StatusOK))
	require.Empty(t, files.Items, "the files list does not list the files of a deleted message")

	alice.Patch(roomMessagePath(room.ID, report.ID), editedTo("back")).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	again := deleteMessage(t, alice, room.ID, report.ID)
	require.Equal(t, got.DeletedAt, again.DeletedAt, "deleting again answers with the same placeholder")
}

// DLT-02: a group's manager deletes what an AI of the group wrote; the placeholder keeps the AI as its author.
func TestAGroupManagerDeletesAnAIsMessage(t *testing.T) {
	f := newAgentRoom(t)
	fromAI := testkit.Decode[dto.Message](f.rest().Post(agentRoomPath(f.room.ID, "/messages"), dto.PostMessageRequest{Text: "from the AI"}).RequireStatus(http.StatusCreated))
	got := deleteMessage(t, f.alice, f.room.ID, fromAI.ID) // alice created the group: she manages it
	require.NotNil(t, got.DeletedAt)
	require.Equal(t, f.agentID, got.AuthorID)
}

// DLT-04: the blobs a delete leaves unreferenced are released at once - the ones another message uses stay -
// and the retention sweep stays the backstop.
func TestDeleteReleasesTheBlobs(t *testing.T) {
	env, clock := clockEnv(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "ops")
	file := func(name, content string) dto.Message {
		return testkit.Decode[dto.Message](postMultipart(alice, room.ID, dto.PostMessageRequest{Text: name}, upload{name, "text/plain", content}).RequireStatus(http.StatusCreated))
	}
	sha := func(m dto.Message) string {
		row, err := env.App.Store.R().GetAttachmentRoom(context.Background(), m.Attachments[0].ID)
		require.NoError(t, err)
		return row.Attachment.Sha256
	}
	present := func(sha string) bool {
		f, err := env.App.Blobs.Open(sha)
		if err == nil {
			_ = f.Close()
		}
		return err == nil
	}
	only := file("only.txt", "only in this message")
	one, two := file("one.txt", "the same bytes"), file("two.txt", "the same bytes")
	onlySHA, sharedSHA := sha(only), sha(one)
	require.Equal(t, sharedSHA, sha(two), "one blob for equal content")
	clock.Advance(time.Hour) // past the minute a fresh blob is left to the sweep

	deleteMessage(t, alice, room.ID, only.ID)
	require.False(t, present(onlySHA), "a blob nothing references any more is gone")
	deleteMessage(t, alice, room.ID, one.ID)
	require.True(t, present(sharedSHA), "another message still uses it")
	deleteMessage(t, alice, room.ID, two.ID)
	require.False(t, present(sharedSHA), "and when the last one goes, so does the blob")
}

// DLT-05: every read of the room returns the placeholder in the message's place: the page of messages, the
// history tool, the feed of the latest messages.
func TestReadsOfTheRoomReturnThePlaceholder(t *testing.T) {
	f := newAgentRoom(t)
	f.post(f.alice, "one")
	secret := testkit.Decode[dto.Message](postMultipart(f.alice, f.room.ID, dto.PostMessageRequest{Text: "secret"}, upload{"s.txt", "text/plain", "hidden"}).RequireStatus(http.StatusCreated))
	f.post(f.alice, "three")
	deleteMessage(t, f.alice, f.room.ID, secret.ID)

	read := timeline(t, f.alice, f.room.ID)
	require.Equal(t, []string{"one", "", "three"}, texts(read))
	require.NotNil(t, read[1].DeletedAt)
	require.Empty(t, read[1].Attachments)

	history := testkit.Decode[dto.Page[dto.AgentMessage]](f.rest().Get(agentRoomPath(f.room.ID, "/messages")).RequireStatus(http.StatusOK))
	require.Len(t, history.Items, 3)
	mid := history.Items[1] // newest first
	require.Equal(t, secret.ID, mid.Message.ID)
	require.Empty(t, mid.Message.Text)
	require.Empty(t, mid.Message.Attachments)
	require.NotNil(t, mid.Message.DeletedAt)
	require.True(t, mid.Trusted)

	var inFeed *dto.Message
	for _, item := range testkit.Decode[dto.List[dto.ActivityItem]](f.alice.Get("/api/v1/activity").RequireStatus(http.StatusOK)).Items {
		if item.Message.ID == secret.ID {
			inFeed = &item.Message
		}
	}
	require.NotNil(t, inFeed, "the feed keeps the message")
	require.NotNil(t, inFeed.DeletedAt)
	require.Empty(t, inFeed.Text)
}

// DLT-05, DLT-07: an AI that comes online receives the messages it missed in order, a deleted one as the
// placeholder.
func TestBacklogShowsThePlaceholder(t *testing.T) {
	f := newAgentRoom(t)
	f.post(f.alice, "one")
	secret := f.post(f.alice, "secret")
	f.post(f.alice, "three")
	deleteMessage(t, f.alice, f.room.ID, secret.ID)

	conn := f.dial(t)
	conn.Welcome(wsWait)
	got := make([]dto.AgentMessage, 3)
	for i := range got {
		got[i] = conn.Message(wsWait).AgentMessage
	}
	require.Len(t, got, 3)
	require.Equal(t, "one", got[0].Message.Text)
	require.Equal(t, secret.ID, got[1].Message.ID, "the placeholder is in its place")
	require.Equal(t, secret.Seq, got[1].Message.Seq)
	require.Empty(t, got[1].Message.Text, "and carries nothing of the text")
	require.NotNil(t, got[1].Message.DeletedAt)
	require.Equal(t, "three", got[2].Message.Text)
}

// DLT-05, EDT-10: the conversation list previews the edited text, and a deleted last message as deleted.
func TestConversationPreviewFollowsEditsAndDeletes(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := createRoom(alice, "ops")
	addToRoom(t, env, alice, room.ID, bob)
	say(alice, room.ID, "first")
	last := say(alice, room.ID, "second wrld")
	preview := func() dto.MessagePreview {
		t.Helper()
		c := conversationIn(t, conversationsOf(t, bob), room.ID)
		require.NotNil(t, c.LastMessage)
		return *c.LastMessage
	}
	require.Equal(t, "second wrld", preview().Text)

	editMessage(t, alice, room.ID, last.ID, "second world")
	require.Equal(t, "second world", preview().Text)
	require.False(t, preview().Deleted)

	deleteMessage(t, alice, room.ID, last.ID)
	p := preview()
	require.True(t, p.Deleted)
	require.Empty(t, p.Text, "no text of a deleted message is previewed")
	require.Equal(t, last.Seq, p.Seq, "the deleted message stays the last one")

	// A file-only message previews its file until it is deleted.
	file := testkit.Decode[dto.Message](postMultipart(alice, room.ID, dto.PostMessageRequest{}, upload{"a.txt", "text/plain", "x"}).RequireStatus(http.StatusCreated))
	require.Equal(t, "[a.txt]", preview().Text)
	deleteMessage(t, alice, room.ID, file.ID)
	require.Equal(t, dto.MessagePreview{Seq: file.Seq, AuthorID: alice.User.ID, AuthorName: "alice", Deleted: true, CreatedAt: file.CreatedAt}, preview())
}
