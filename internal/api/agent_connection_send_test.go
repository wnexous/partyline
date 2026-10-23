package api_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/testkit"
)

// connectionChatPath is POST /agent/connections/{id}/chats/{chat}/messages: the chat is Partyline's id.
func connectionChatPath(connection, chat string) string {
	return "/api/v1/agent/connections/" + connection + "/chats/" + chat + "/messages"
}

// sendToChat writes text to the chat of the connection as the AI of the folder name.
func (f *replyFixture) sendToChat(name, connection, chat, text string) *testkit.Response {
	f.env.T.Helper()
	return f.token.AsAgent(name).Post(connectionChatPath(connection, chat), dto.SendToChatRequest{Text: text})
}

// CID-16: the operator writes to a chat of its connection by naming the connection and the chat
// (Partyline's ids, as a platform event carries them); the text goes to the chat's platform id through
// the connection, unsigned and without a quote, whether the chat is a group or a direct one. The answer
// is 201 with the state of the send.
func TestSendToAChatOfTheConnectionGoesOutUnsigned(t *testing.T) {
	f := newReplyFixture(t)
	res := f.sendToChat("Atlas", f.conn.ID, f.group.ID, "good morning, team").RequireStatus(http.StatusCreated)
	require.Equal(t, []string{"id", "status"}, keysOf(t, res.Body))
	got := testkit.Decode[dto.PlatformSendResult](res)
	require.Equal(t, dto.PlatformSendStatusSent, got.Status)
	require.NotEmpty(t, got.ID)
	sent := f.inst.WaitSend(t)
	require.Equal(t, []any{"120363000000000001@g.us", "good morning, team", true, "", "", ""},
		[]any{sent.ChatID, sent.Text, sent.Unsigned, sent.Signature(), sent.ReplyToID, sent.ReplyToSenderID})
	row := f.sendRow(t, got.ID)
	require.Equal(t, []any{"sent", "", f.atlas, f.group.ID, f.conn.ID}, []any{row.Status, row.Text, row.AgentID, row.ChatID, row.ConnectorID})

	f.sendToChat("Atlas", f.conn.ID, f.dm.ID, "oi, Pat").RequireStatus(http.StatusCreated)
	sent = f.inst.WaitSend(t)
	require.Equal(t, []string{"5511999990000", "oi, Pat"}, []string{sent.ChatID, sent.Text})
}

// CID-18: a platform that refuses is answered with 202, the state pending and the reason; the
// worker retries and the message goes out.
func TestSendToAChatThatTheConnectionRefusesIsAcceptedAndRetried(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	f := newReplyFixture(t, testkit.WithOutbox(), testkit.WithClock(clock.Now))
	f.inst.SetSendError(errors.New("platform unavailable"))

	got := testkit.Decode[dto.PlatformSendResult](f.sendToChat("Atlas", f.conn.ID, f.group.ID, "good morning").RequireStatus(http.StatusAccepted))
	require.Equal(t, dto.PlatformSendResult{ID: got.ID, Status: dto.PlatformSendStatusPending, Error: "platform unavailable"}, got)

	f.inst.SetSendError(nil)
	clock.Advance(2 * time.Second)
	require.Equal(t, "good morning", f.inst.WaitSend(t).Text)
	require.Eventually(t, func() bool { return f.sendRow(t, got.ID).Status == "sent" }, connectortest.Timeout, 10*time.Millisecond)
	require.Empty(t, f.sendRow(t, got.ID).Text, "the text is deleted once it went out")
}

// secondConnection is another connection of alice's that Atlas operates too, with a chat of its own.
func (f *replyFixture) secondConnection(t *testing.T) (dto.Connector, string) {
	t.Helper()
	c := f.alice.OperateConnector(createFakeIdle(f.alice, "tg", false).ID, f.atlas)
	return c, f.chat(t, c, "-1001234567890", "Elsewhere", "group").ID
}

// CID-03, CID-17: who may write to a chat of a connection, and the refusals. Every refusal is tested
// with an otherwise valid request, so that no other guard answers first (L-018), and each answers its
// own code (L-017):
//   - the operator: 201;
//   - another AI of the owner and the AI of a user granted use of the connection (the same-named AI
//     of the operator, as far as it can be): 403 not_operator, because they may see the connection;
//   - the AI of a stranger and of an admin, who may not: 404 not_found, as for an unknown connection,
//     so the answer reveals nothing about it;
//   - the owner, and a user granted use, as users with a session or a token: 403 forbidden (agent
//     tokens only); anonymous: 401;
//   - the operator naming a chat of another connection, or one nobody has: 404 unknown_chat.
func TestSendToAChatAuthorizationMatrix(t *testing.T) {
	f := newReplyFixture(t)
	bob, carol, root := f.env.NewUser("bob"), f.env.NewUser("carol"), f.env.NewAdmin("root")
	shareConnector(f.alice, f.conn.ID, bob.User.ID)
	_, foreignChat := f.secondConnection(t)
	path := connectionChatPath(f.conn.ID, f.group.ID)
	body := dto.SendToChatRequest{Text: "x"}
	asAI := func(u *testkit.Client) *testkit.Client { return u.Token(dto.TokenKindAgent, "desk").AsAgent("Atlas") }

	f.sendToChat("Notes", f.conn.ID, f.group.ID, "x").RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	asAI(bob).Post(path, body).RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	asAI(carol).Post(path, body).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	asAI(root).Post(path, body).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.alice.Post(path, body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	bob.Post(path, body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.alice.Token(dto.TokenKindUser, "script").Post(path, body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.env.Anon().AsAgent("Atlas").Post(path, body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	f.sendToChat("Atlas", f.conn.ID, foreignChat, "x").RequireError(http.StatusNotFound, dto.ErrorCodeUnknownChat)
	f.sendToChat("Atlas", f.conn.ID, store.NewID(), "x").RequireError(http.StatusNotFound, dto.ErrorCodeUnknownChat)
	f.sendToChat("Atlas", store.NewID(), f.group.ID, "x").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, f.inst.Sent(), "nothing was sent for any refusal")

	f.sendToChat("Atlas", f.conn.ID, f.group.ID, "from the operator").RequireStatus(http.StatusCreated)
	require.Equal(t, []string{"from the operator"}, outboundTexts(f.inst.Sent()))
}

// CID-03: the message of the refusals. A use grantee who is no operator is told so, and the answer of
// a stranger is the very answer for a connection that does not exist.
func TestSendToAChatRefusalMessages(t *testing.T) {
	f := newReplyFixture(t)
	carol := f.env.NewUser("carol")
	e := f.sendToChat("Notes", f.conn.ID, f.group.ID, "x").RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	require.Equal(t, "only the AI that operates the connection can send through it", e.Message)

	stranger := carol.Token(dto.TokenKindAgent, "desk").AsAgent("Atlas").Post(connectionChatPath(f.conn.ID, f.group.ID), dto.SendToChatRequest{Text: "x"})
	missing := carol.Token(dto.TokenKindAgent, "desk").AsAgent("Atlas").Post(connectionChatPath(store.NewID(), f.group.ID), dto.SendToChatRequest{Text: "x"})
	require.Equal(t, missing.Status, stranger.Status)
	require.Equal(t, testkit.Decode[dto.ErrorBody](missing), testkit.Decode[dto.ErrorBody](stranger), "a connection the caller cannot see answers as one that does not exist")

	e = f.sendToChat("Atlas", f.conn.ID, store.NewID(), "x").RequireError(http.StatusNotFound, dto.ErrorCodeUnknownChat)
	require.Equal(t, "the chat does not belong to this connection", e.Message)
}

// CID-02, CID-03: a connection nobody operates has no operator to send, not even the AI that used to
// operate it: its owner's AIs are refused with not_operator.
func TestSendToAChatOfAConnectionNobodyOperatesIsRefused(t *testing.T) {
	f := newReplyFixture(t)
	idle := createFakeIdle(f.alice, "idle", false)
	chat := f.chat(t, idle, "-1001111111111", "Idle group", "group")
	f.sendToChat("Atlas", idle.ID, chat.ID, "x").RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	require.Empty(t, f.inst.Sent())
}

// A connection that is stopped is a refusal of the platform side: 202 with the reason, and nothing is
// lost.
func TestSendToAChatOfAStoppedConnectionIsAccepted(t *testing.T) {
	f := newReplyFixture(t)
	off := false
	f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{Enabled: &off}).RequireStatus(http.StatusOK)
	f.inst.WaitStop(t)
	got := testkit.Decode[dto.PlatformSendResult](f.sendToChat("Atlas", f.conn.ID, f.group.ID, "x").RequireStatus(http.StatusAccepted))
	require.Equal(t, dto.PlatformSendResult{ID: got.ID, Status: dto.PlatformSendStatusPending, Error: "connector is not running"}, got)
}

// MSG-02: the text has 1 to 16000 characters; a refusal names the field and sends nothing; a body that
// is not JSON is a 400.
func TestSendToAChatValidatesTheText(t *testing.T) {
	f := newReplyFixture(t)
	for name, text := range map[string]string{"empty": "", "blank": " \t", "too long": strings.Repeat("a", dto.MaxTextLength+1)} {
		e := f.sendToChat("Atlas", f.conn.ID, f.group.ID, text).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "text", name)
	}
	f.token.AsAgent("Atlas").DoRaw(http.MethodPost, connectionChatPath(f.conn.ID, f.group.ID), "application/json", strings.NewReader("{")).
		RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	require.Empty(t, f.inst.Sent())
}

// TOOL-08: writes through a connection count against the AI's 30 posts a minute, together with its
// replies: the 31st is 429 and sends nothing.
func TestSendToAChatRateLimit(t *testing.T) {
	f := newReplyFixture(t)
	id := f.enqueue(t, f.atlas, f.group, "M1", "for Atlas")
	for i := 1; i <= 15; i++ {
		f.sendToChat("Atlas", f.conn.ID, f.group.ID, fmt.Sprintf("message %d", i)).RequireStatus(http.StatusCreated)
		f.reply("Atlas", id, fmt.Sprintf("reply %d", i)).RequireStatus(http.StatusCreated)
	}
	f.sendToChat("Atlas", f.conn.ID, f.group.ID, "one too many").RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	f.reply("Atlas", id, "one too many").RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	require.Len(t, f.inst.Sent(), 30)
}

// A refused request does not use up the limit: the AI that is not the operator and the AI that names
// a foreign chat still have all their posts.
func TestRefusedSendsDoNotCountAgainstTheLimit(t *testing.T) {
	f := newReplyFixture(t)
	_, foreignChat := f.secondConnection(t)
	for range 35 {
		f.sendToChat("Atlas", f.conn.ID, foreignChat, "x").RequireError(http.StatusNotFound, dto.ErrorCodeUnknownChat)
		f.sendToChat("Notes", f.conn.ID, f.group.ID, "x").RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	}
	f.sendToChat("Atlas", f.conn.ID, f.group.ID, "still allowed").RequireStatus(http.StatusCreated)
}
