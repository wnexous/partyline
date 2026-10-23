package api_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/testkit"
)

// The words of the two refusals: reactions and files toward a connection are not part of this
// release (the design: "send_file and react work in rooms only and answer unsupported for a platform
// id").
const (
	unsupportedReaction = "reactions work on room messages only; a message from a connection cannot get one, answer it with reply"
	unsupportedQuote    = "a message from a connection cannot be quoted in a room (files go to rooms only); answer it with reply, or with send_message naming the connection and the chat"
)

// supportRoom is a room Atlas belongs to, where a post may quote a message.
func (f *replyFixture) supportRoom(t *testing.T) dto.Room {
	t.Helper()
	room := createRoom(f.alice, "Support")
	f.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.atlas}).RequireStatus(http.StatusCreated)
	return room
}

// CID-22: a reaction to a message that arrived on the AI's connection is refused with 422 unsupported
// and the way out, whether the session acknowledged the message or not; nothing is sent. A message the
// AI does not hold, another AI's included, and an unknown id are the same 404 "message not found" as
// before, so the answer reveals nothing about other queues (L-017: the status of the new code is
// observed; L-018: the request is otherwise valid).
func TestReactToAMessageOfAConnectionIsUnsupported(t *testing.T) {
	f := newReplyFixture(t)
	waiting := f.enqueue(t, f.atlas, f.group, "M1", "where is my order?")
	delivered := f.enqueue(t, f.atlas, f.dm, "M2", "oi")
	require.NoError(t, f.env.App.Platform.Ack(t.Context(), f.atlas, delivered))
	react := func(name, id string) *testkit.Response {
		return f.token.AsAgent(name).Post(replyPath+id+"/react", dto.ReactRequest{Emoji: "👍"})
	}

	for _, id := range []string{waiting, delivered} {
		e := react("Atlas", id).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported)
		require.Equal(t, unsupportedReaction, e.Message)
	}
	e := react("Notes", waiting).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, "message not found", e.Message, "another AI's message")
	react("Atlas", "no-such-message").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	// The same-named AI of a user granted use of the connection and of the operator, of a stranger and
	// of an admin holds no such message: the id is unknown to each of them.
	bob, carol, root := f.env.NewUser("bob"), f.env.NewUser("carol"), f.env.NewAdmin("root")
	shareConnector(f.alice, f.conn.ID, bob.User.ID)
	shareAgent(f.alice, f.atlas, bob.User.ID)
	for name, u := range map[string]*testkit.Client{"use grantee": bob, "stranger": carol, "admin": root} {
		res := u.Token(dto.TokenKindAgent, "desk").AsAgent("Atlas").Post(replyPath+waiting+"/react", dto.ReactRequest{Emoji: "👍"})
		require.Equal(t, http.StatusNotFound, res.Status, "%s's own AI of the same folder: %s", name, res.Body)
		require.Equal(t, "message not found", testkit.Decode[dto.ErrorBody](res).Error.Message, name)
	}
	require.Empty(t, f.inst.Sent())

	// Users and anonymous callers are refused before anything is looked up.
	f.alice.Post(replyPath+waiting+"/react", dto.ReactRequest{Emoji: "👍"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.env.Anon().AsAgent("Atlas").Post(replyPath+waiting+"/react", dto.ReactRequest{Emoji: "👍"}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// CID-22: a post in a room that quotes (reply_to) a message that arrived on the AI's connection, with
// or without files, is refused with 422 unsupported: a quote is a room message of the room. The other
// AIs' messages and unknown ids keep the answer every unknown quote has (422 on reply_to), so the
// answer reveals nothing about other queues; a quote of a message of the room works as before.
func TestQuotingAMessageOfAConnectionInARoomIsUnsupported(t *testing.T) {
	f := newReplyFixture(t)
	room := f.supportRoom(t)
	asked := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "status?"}).RequireStatus(http.StatusCreated))
	mine := f.enqueue(t, f.atlas, f.group, "M1", "where is my order?")
	// Notes's message comes from a connection Notes operates: the queue gives an item to the
	// connection's current operator (CID-05), so it cannot be queued for Notes on Atlas's one.
	notesConn := f.alice.OperateConnector(createFakeIdle(f.alice, "tg", false).ID, f.notes)
	notes := f.enqueue(t, f.notes, f.chat(t, notesConn, "-1009876543210", "Notes group", "group"), "M2", "for Notes")
	path := "/api/v1/agent/rooms/" + room.ID + "/messages"
	post := func(replyTo string) *testkit.Response {
		return f.token.AsAgent("Atlas").Post(path, dto.PostMessageRequest{Text: "the report", ReplyTo: replyTo})
	}
	multipartPost := func(replyTo string) *testkit.Response {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, err := w.CreateFormField(dto.MultipartMessageField)
		require.NoError(t, err)
		require.NoError(t, json.NewEncoder(part).Encode(dto.PostMessageRequest{Text: "the report", ReplyTo: replyTo}))
		file, err := w.CreateFormFile(dto.MultipartFileField, "report.txt")
		require.NoError(t, err)
		_, err = file.Write([]byte("synthetic report"))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		return f.token.AsAgent("Atlas").DoRaw(http.MethodPost, path, w.FormDataContentType(), &body)
	}

	for name, res := range map[string]*testkit.Response{"json": post(mine), "multipart": multipartPost(mine)} {
		e := res.RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported)
		require.Equal(t, unsupportedQuote, e.Message, name)
	}
	for name, id := range map[string]string{"another AI's message": notes, "an unknown id": store.NewID()} {
		e := post(id).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, map[string]string{"reply_to": "must be a message of the room"}, e.Fields, name)
	}
	reply := testkit.Decode[dto.Message](post(asked.ID).RequireStatus(http.StatusCreated))
	require.Equal(t, asked.ID, reply.ReplyTo, "a quote of a room message works as before")
	require.Len(t, testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/"+room.ID+"/messages").RequireStatus(http.StatusOK)).Items, 2, "only the status and its reply: nothing was posted for the refusals")

	// A user posting the same quote is not touched by the AI's rule: the message is not in the room.
	e := f.alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "x", ReplyTo: mine}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "reply_to")
}
