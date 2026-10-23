package api_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// replyFixture is alice with her AIs Atlas and Notes and a running fake connection operated by
// Atlas, with a group chat ("120363000000000001@g.us") and a direct chat ("5511999990000"). Its
// messages are queued with platformWS.enqueue.
type replyFixture struct {
	*platformWS
	inst *connectortest.Instance
}

func newReplyFixture(t *testing.T, opts ...testkit.Option) *replyFixture {
	t.Helper()
	fake := connectortest.New()
	env := testkit.New(t, append([]testkit.Option{testkit.WithConnectorKinds(fake.Kind())}, opts...)...)
	alice := env.NewUser("alice")
	f := &platformWS{
		env: env, alice: alice, token: alice.Token(dto.TokenKindAgent, "laptop"),
		atlas: env.NewAI(alice, "Atlas"), notes: env.NewAI(alice, "Notes"),
	}
	f.conn = alice.OperateConnector(createFakeIdle(alice, "wa", true).ID, f.atlas)
	inst := fake.Instance(t, f.conn.ID)
	inst.WaitRun(t)
	f.group = f.chat(t, f.conn, "120363000000000001@g.us", "Team", "group")
	f.dm = f.chat(t, f.conn, "5511999990000", "Pat", "dm")
	return &replyFixture{platformWS: f, inst: inst}
}

const replyPath = "/api/v1/agent/messages/"

// reply answers the message id as the AI of the folder name.
func (f *replyFixture) reply(name, id, text string) *testkit.Response {
	f.env.T.Helper()
	return f.token.AsAgent(name).Post(replyPath+id+"/reply", dto.ReplyRequest{Text: text})
}

func (f *replyFixture) sendRow(t *testing.T, id string) db.PlatformSend {
	t.Helper()
	r, err := f.env.App.Store.R().GetPlatformSend(context.Background(), id)
	require.NoError(t, err)
	return r
}

// CID-15: the operator's reply to a message of its connection goes to that message's chat through the
// connection, unsigned, quoting the message and, in a group, its sender; the answer is 201 with the
// state of the send, and the text is gone from the record once it went out.
func TestReplyToAPlatformMessageSendsThroughTheConnectionUnsigned(t *testing.T) {
	f := newReplyFixture(t)
	asked := f.enqueue(t, f.atlas, f.group, "M1", "where is my order?")
	direct := f.enqueue(t, f.atlas, f.dm, "M2", "oi", func(it *platform.Item) { it.SenderID, it.SenderName = "5511999990000", "Pat" })

	res := f.reply("Atlas", asked, "on its way").RequireStatus(http.StatusCreated)
	require.Equal(t, []string{"id", "status"}, keysOf(t, res.Body), "no error when it was sent")
	got := testkit.Decode[dto.PlatformSendResult](res)
	require.Equal(t, dto.PlatformSendStatusSent, got.Status)
	require.NotEmpty(t, got.ID)
	sent := f.inst.WaitSend(t)
	require.Equal(t, []any{"120363000000000001@g.us", "on its way", true, "", "M1", "5511999990000"},
		[]any{sent.ChatID, sent.Text, sent.Unsigned, sent.Signature(), sent.ReplyToID, sent.ReplyToSenderID})
	row := f.sendRow(t, got.ID)
	require.Equal(t, []any{"sent", "", f.atlas, f.group.ID}, []any{row.Status, row.Text, row.AgentID, row.ChatID})

	f.reply("Atlas", direct, "oi, tudo bem?").RequireStatus(http.StatusCreated)
	sent = f.inst.WaitSend(t)
	require.Equal(t, []any{"5511999990000", "oi, tudo bem?", true, "M2"}, []any{sent.ChatID, sent.Text, sent.Unsigned, sent.ReplyToID}, "a direct chat, by its own platform id")
}

// CID-15: a message the platform gave no id is answered without a quote.
func TestReplyToAPlatformMessageWithoutAPlatformIDHasNoQuote(t *testing.T) {
	f := newReplyFixture(t)
	id := f.enqueue(t, f.atlas, f.group, "", "no id from the platform")
	f.reply("Atlas", id, "on its way").RequireStatus(http.StatusCreated)
	sent := f.inst.WaitSend(t)
	require.Equal(t, []string{"on its way", "", ""}, []string{sent.Text, sent.ReplyToID, sent.ReplyToSenderID})
}

// CID-15: a reply keeps working once the session acknowledged the message (its text is gone then)
// until the record is purged, 24 hours after the acknowledgement: a millisecond before it still
// answers, at 24 hours the message is unknown.
func TestReplyToAnAcknowledgedPlatformMessageWorksForADay(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	f := newReplyFixture(t, testkit.WithClock(clock.Now))
	ctx := context.Background()
	id := f.enqueue(t, f.atlas, f.group, "M1", "where is my order?")
	require.NoError(t, f.env.App.Platform.Ack(ctx, f.atlas, id))
	require.Empty(t, f.item(t, f.atlas, id).Text, "acknowledged: the text is gone")

	f.reply("Atlas", id, "on its way").RequireStatus(http.StatusCreated)
	require.Equal(t, "M1", f.inst.WaitSend(t).ReplyToID)

	clock.Advance(24*time.Hour - time.Millisecond)
	_, err := f.env.App.Platform.Purge(ctx, clock.Now())
	require.NoError(t, err)
	f.reply("Atlas", id, "still there").RequireStatus(http.StatusCreated)
	require.Equal(t, "M1", f.inst.WaitSend(t).ReplyToID)

	clock.Advance(time.Millisecond)
	_, err = f.env.App.Platform.Purge(ctx, clock.Now())
	require.NoError(t, err)
	f.reply("Atlas", id, "too late").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Len(t, f.inst.Sent(), 2, "nothing is sent for an expired message")
}

// CID-18: a platform that refuses is answered with 202, the state pending and the reason; the worker
// retries, the message goes out, and its text is deleted from the record.
func TestReplyToAPlatformMessageThatTheConnectionRefusesIsAcceptedAndRetried(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	f := newReplyFixture(t, testkit.WithOutbox(), testkit.WithClock(clock.Now))
	id := f.enqueue(t, f.atlas, f.group, "M1", "where is my order?")
	f.inst.SetSendError(errors.New("platform unavailable"))

	res := f.reply("Atlas", id, "on its way").RequireStatus(http.StatusAccepted)
	got := testkit.Decode[dto.PlatformSendResult](res)
	require.Equal(t, dto.PlatformSendResult{ID: got.ID, Status: dto.PlatformSendStatusPending, Error: "platform unavailable"}, got)
	require.Equal(t, "on its way", f.sendRow(t, got.ID).Text, "the text waits for the retry")
	require.Empty(t, f.inst.Sent())

	f.inst.SetSendError(nil)
	clock.Advance(2 * time.Second)
	sent := f.inst.WaitSend(t)
	require.Equal(t, []string{"on its way", "M1"}, []string{sent.Text, sent.ReplyToID})
	require.Eventually(t, func() bool { return f.sendRow(t, got.ID).Status == "sent" }, connectortest.Timeout, 10*time.Millisecond)
	row := f.sendRow(t, got.ID)
	require.Equal(t, []any{"", "", int64(2)}, []any{row.Text, row.LastError, row.Attempts}, "sent by the retry, the text deleted")
}

// A connection that is stopped is a refusal too: 202 with the reason, and the message goes out once
// it runs again.
func TestReplyToAPlatformMessageOfAStoppedConnectionIsAccepted(t *testing.T) {
	f := newReplyFixture(t)
	id := f.enqueue(t, f.atlas, f.group, "M1", "where is my order?")
	off := false
	f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{Enabled: &off}).RequireStatus(http.StatusOK)
	f.inst.WaitStop(t)

	got := testkit.Decode[dto.PlatformSendResult](f.reply("Atlas", id, "on its way").RequireStatus(http.StatusAccepted))
	require.Equal(t, dto.PlatformSendResult{ID: got.ID, Status: dto.PlatformSendStatusPending, Error: "connector is not running"}, got)
}

// CID-15: a message that is not the AI's own queue item is unknown to it: an unknown id, the queue
// item of another AI of the same owner, and an item of Atlas asked by Notes all answer 404 "message
// not found" and send nothing.
func TestReplyToAPlatformMessageOfAnotherAIIsNotFound(t *testing.T) {
	f := newReplyFixture(t)
	atlasItem := f.enqueue(t, f.atlas, f.group, "M1", "for Atlas")
	e := f.reply("Notes", atlasItem, "x").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, "message not found", e.Message)
	f.reply("Atlas", "no-such-message", "x").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, f.inst.Sent())
	require.Equal(t, "for Atlas", f.item(t, f.atlas, atlasItem).Text, "nothing was touched")
}

// Authorization of POST /agent/messages/{id}/reply for a platform message: only agent tokens, and an
// AI answers its own queue only. The operator is answered; another AI of the owner, the AI of a user
// granted use of the connection and of the operator, the AI of a stranger and of an admin (each with
// the same folder name where it can be) all get 404 and send nothing; users, with a session or a
// token, are refused with 403 and anonymous callers with 401 (L-018: each refusal has a valid
// request otherwise).
func TestPlatformReplyAuthorizationMatrix(t *testing.T) {
	f := newReplyFixture(t)
	bob, carol, root := f.env.NewUser("bob"), f.env.NewUser("carol"), f.env.NewAdmin("root")
	shareConnector(f.alice, f.conn.ID, bob.User.ID)
	shareAgent(f.alice, f.atlas, bob.User.ID)
	id := f.enqueue(t, f.atlas, f.group, "M1", "for Atlas only")
	path := replyPath + id + "/reply"
	body := dto.ReplyRequest{Text: "x"}

	f.reply("Notes", id, "x").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	for name, u := range map[string]*testkit.Client{"use grantee": bob, "stranger": carol, "admin": root} {
		res := u.Token(dto.TokenKindAgent, "desk").AsAgent("Atlas").Post(path, body)
		require.Equal(t, http.StatusNotFound, res.Status, "%s's own AI of the same folder: %s", name, res.Body)
		require.Equal(t, "message not found", testkit.Decode[dto.ErrorBody](res).Error.Message, name)
	}
	f.alice.Post(path, body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.alice.Token(dto.TokenKindUser, "script").Post(path, body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.env.Anon().AsAgent("Atlas").Post(path, body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	require.Empty(t, f.inst.Sent(), "nobody but the operator sent anything")

	f.reply("Atlas", id, "from the operator").RequireStatus(http.StatusCreated)
	require.Equal(t, []string{"from the operator"}, outboundTexts(f.inst.Sent()))
}

func outboundTexts(list []connectors.Outbound) []string {
	out := make([]string, len(list))
	for i, m := range list {
		out[i] = m.Text
	}
	return out
}

// CID-03: a reply by an AI that is no longer the connection's operator is refused with 403
// not_operator and sends nothing, even though the message is still in its queue (here the operator was
// changed without the queue following).
func TestPlatformReplyByAnAIThatNoLongerOperatesIsRefused(t *testing.T) {
	f := newReplyFixture(t)
	id := f.enqueue(t, f.atlas, f.group, "M1", "for Atlas")
	ctx := context.Background()
	require.NoError(t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetConnectorOperator(ctx, db.SetConnectorOperatorParams{OperatorIid: sql.NullString{String: f.notes, Valid: true}, ID: f.conn.ID})
	}))
	e := f.reply("Atlas", id, "x").RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	require.Equal(t, "only the AI that operates the connection can send through it", e.Message)
	require.Empty(t, f.inst.Sent())
}

// MSG-02: the text of a reply has 1 to 16000 characters; a refusal names the field and sends nothing.
func TestPlatformReplyValidatesTheText(t *testing.T) {
	f := newReplyFixture(t)
	id := f.enqueue(t, f.atlas, f.group, "M1", "for Atlas")
	for name, text := range map[string]string{"empty": "", "blank": "  \n", "too long": strings.Repeat("a", dto.MaxTextLength+1)} {
		e := f.reply("Atlas", id, text).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "text", name)
	}
	f.token.AsAgent("Atlas").DoRaw(http.MethodPost, replyPath+id+"/reply", "application/json", strings.NewReader("{")).
		RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	require.Empty(t, f.inst.Sent())
}

// TOOL-08: replies through a connection count against the AI's 30 posts a minute like every other
// post: the 31st is 429 and sends nothing.
func TestPlatformReplyRateLimit(t *testing.T) {
	f := newReplyFixture(t)
	id := f.enqueue(t, f.atlas, f.group, "M1", "for Atlas")
	for i := 1; i <= 30; i++ {
		f.reply("Atlas", id, fmt.Sprintf("answer %d", i)).RequireStatus(http.StatusCreated)
	}
	f.reply("Atlas", id, "one too many").RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	require.Len(t, f.inst.Sent(), 30)
}

// A reply to a room message is what it always was: a message of the room with reply_to set, answered
// with 201 and the message itself, never a platform result.
func TestReplyToARoomMessageIsStillAMessage(t *testing.T) {
	f := newReplyFixture(t)
	room := createRoom(f.alice, "Support")
	f.alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: f.atlas}).RequireStatus(http.StatusCreated)
	asked := testkit.Decode[dto.Message](f.alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "status?"}).RequireStatus(http.StatusCreated))

	res := f.reply("Atlas", asked.ID, "all green").RequireStatus(http.StatusCreated)
	got := testkit.Decode[dto.Message](res)
	require.Equal(t, []any{room.ID, asked.ID, "all green", dto.AuthorKindAgent, f.atlas}, []any{got.RoomID, got.ReplyTo, got.Text, got.AuthorKind, got.AuthorID})
	require.NotContains(t, keysOf(t, res.Body), "status")
	require.Empty(t, f.inst.Sent(), "a room reply sends nothing through a connection")
}
