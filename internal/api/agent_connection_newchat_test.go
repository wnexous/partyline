package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/testkit"
)

// newNumber is an address the fake platform takes (8 to 15 digits) that no chat of the connection has.
const newNumber = "5541999990000"

// chatsOf lists the connection's chats as its members see them, by platform id.
func (f *replyFixture) chatsOf(t *testing.T) map[string]dto.Chat {
	t.Helper()
	out := map[string]dto.Chat{}
	for _, c := range testkit.Decode[dto.List[dto.Chat]](f.alice.Get("/api/v1/connectors/" + f.conn.ID + "/chats").RequireStatus(http.StatusOK)).Items {
		out[c.ExternalID] = c
	}
	return out
}

// CID-31: the operator writes to an address no chat of the connection has seen: the chat is created
// (a direct chat the members of the organization can see, with no title and no activity yet) and the
// text goes to the platform id of the address through the connection, unsigned. Writing to the same
// address again, or to the chat by its id, is the same chat.
func TestSendToANewAddressStartsTheChat(t *testing.T) {
	f := newReplyFixture(t)
	before := f.chatsOf(t)
	require.NotContains(t, before, newNumber)

	res := f.sendToChat("Atlas", f.conn.ID, newNumber, "good morning").RequireStatus(http.StatusCreated)
	require.Equal(t, []string{"id", "status"}, keysOf(t, res.Body))
	require.Equal(t, dto.PlatformSendStatusSent, testkit.Decode[dto.PlatformSendResult](res).Status)
	sent := f.inst.WaitSend(t)
	require.Equal(t, []any{newNumber, "good morning", true, ""}, []any{sent.ChatID, sent.Text, sent.Unsigned, sent.Signature()})

	after := f.chatsOf(t)
	require.Len(t, after, len(before)+1)
	chat := after[newNumber]
	require.Equal(t, []any{dto.ChatKindDM, "", f.conn.ID, "fake"}, []any{chat.Kind, chat.Title, chat.ConnectorID, chat.ConnectorKind})
	require.Nil(t, chat.LastSeenAt, "nothing was heard from it")

	f.sendToChat("Atlas", f.conn.ID, newNumber, "and another").RequireStatus(http.StatusCreated)
	f.sendToChat("Atlas", f.conn.ID, chat.ID, "by its id").RequireStatus(http.StatusCreated)
	require.Len(t, f.chatsOf(t), len(before)+1, "the same chat each time")
	require.Equal(t, []string{"good morning", "and another", "by its id"}, outboundTexts(f.inst.Sent()))
}

// CID-17, CID-31: what the platform does not take as an address stays an unknown chat, with the code
// and words it always had; nothing is created or sent.
func TestSendToSomethingThatIsNoAddressIsAnUnknownChat(t *testing.T) {
	f := newReplyFixture(t)
	before := len(f.chatsOf(t))
	_, foreignChat := f.secondConnection(t)

	for _, ref := range []string{"hello", "5541", "+5541999990000", "5541999990000@s.whatsapp.net", "-1001234567890", foreignChat, store.NewID()} {
		e := f.sendToChat("Atlas", f.conn.ID, ref, "x").RequireError(http.StatusNotFound, dto.ErrorCodeUnknownChat)
		require.Equal(t, "the chat does not belong to this connection", e.Message, ref)
	}
	require.Len(t, f.chatsOf(t), before)
	require.Empty(t, f.inst.Sent())
}

// CID-03, CID-31: starting a chat is the operator's alone, with the refusals of any send: another AI
// of the organization is not the operator, an AI that is no member does not see the connection, users
// and anonymous callers have no agent token; and none of them creates a chat.
func TestOnlyTheOperatorStartsAChatThroughTheAPI(t *testing.T) {
	f := newReplyFixture(t)
	before := len(f.chatsOf(t))
	carol := f.env.NewUser("carol")
	path := connectionChatPath(f.conn.ID, newNumber)
	body := dto.SendToChatRequest{Text: "x"}

	f.sendToChat("Notes", f.conn.ID, newNumber, "x").RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	carol.Token(dto.TokenKindAgent, "desk").AsAgent("Atlas").Post(path, body).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.alice.Post(path, body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.env.Anon().AsAgent("Atlas").Post(path, body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	require.Len(t, f.chatsOf(t), before)
	require.Empty(t, f.inst.Sent())
}

// CID-31: an AI starts at most 10 chats an hour on its connections. The 11th address is 429
// rate_limited and creates nothing; the chats it has are still free to write to, and so is an address
// it already wrote to; an hour later it may start another.
func TestStartingChatsIsLimitedToTenAnHour(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	f := newReplyFixture(t, testkit.WithClock(clock.Now))
	before := len(f.chatsOf(t))
	number := func(i int) string { return fmt.Sprintf("55419999900%02d", i) }

	for i := range 10 {
		f.sendToChat("Atlas", f.conn.ID, number(i), "good morning").RequireStatus(http.StatusCreated)
		clock.Advance(time.Minute) // under the 30 posts a minute, far from an hour
	}
	e := f.sendToChat("Atlas", f.conn.ID, number(10), "one too many").RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	require.Equal(t, "rate limited: at most 10 new chats an hour", e.Message)
	require.Len(t, f.chatsOf(t), before+10, "the refused chat was not created")
	require.Len(t, f.inst.Sent(), 10)

	f.sendToChat("Atlas", f.conn.ID, f.group.ID, "a chat it has").RequireStatus(http.StatusCreated)
	f.sendToChat("Atlas", f.conn.ID, number(3), "an address it wrote to").RequireStatus(http.StatusCreated)
	f.sendToChat("Atlas", f.conn.ID, number(10), "still one too many").RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)

	clock.Advance(time.Hour)
	f.sendToChat("Atlas", f.conn.ID, number(10), "an hour later").RequireStatus(http.StatusCreated)
	require.Len(t, f.chatsOf(t), before+11)
}

// CID-31: a request that is refused does not spend the allowance for new chats: unknown addresses,
// unusable texts and another AI's attempts leave all ten starts to the operator.
func TestRefusedRequestsDoNotSpendTheAllowanceForNewChats(t *testing.T) {
	f := newReplyFixture(t)
	for range 12 {
		f.sendToChat("Atlas", f.conn.ID, "hello", "x").RequireError(http.StatusNotFound, dto.ErrorCodeUnknownChat)
		f.sendToChat("Atlas", f.conn.ID, newNumber, " ").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		f.sendToChat("Notes", f.conn.ID, newNumber, "x").RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	}
	for i := range 10 {
		f.sendToChat("Atlas", f.conn.ID, fmt.Sprintf("55419999900%02d", i), "good morning").RequireStatus(http.StatusCreated)
	}
}
