package connectors_test

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// accountID is the platform id of the connector's own account (synthetic).
const accountID = "5511999990009@s.whatsapp.net"

// own is a message the connector's own account wrote from the platform itself, e.g. its phone.
func own(chat, id, body string) connectors.Inbound {
	m := text(chat, id, body)
	m.FromSelf = true
	m.SenderID, m.SenderAltID, m.SenderName = accountID, "", "Account"
	return m
}

// stored returns the number of messages of the room.
func (e *outboundEnv) stored(t *testing.T) int {
	t.Helper()
	return len(e.messages(t, e.room.ID))
}

// ACC-05: a message typed on the platform with the connector's own account (FromSelf, an id
// Partyline never sent), in a chat that sits in a room, is authored by the operator (the owner by
// default, else the AI that operates the account) and trusted by the owner's AIs as the owner's
// own account. A repeat of it is stored once, and its reply target is kept.
func TestOwnAccountMessageIsAuthoredByTheOperator(t *testing.T) {
	e := newOutboundEnv(t)
	e.emit(text(groupID, "M1", "where is my order?"))
	asked := e.helper.Message(connectortest.Timeout)
	require.False(t, asked.Trusted, "a customer of the group is not the owner")

	typed := own(groupID, "P1", "checking it now")
	typed.ReplyToID = "M1"
	e.emit(typed)
	got := e.helper.Message(connectortest.Timeout)
	require.Equal(t, "checking it now", got.Message.Text)
	require.True(t, got.Trusted, "the owner's own account is trusted by the owner's AIs")
	author := got.Message.Author
	require.Equal(t, []any{e.alice.User.ID, dto.IntelligenceRoleUser, dto.IntelligenceKindHuman}, []any{author.ID, author.Role, author.Kind},
		"the operator is the owner")
	require.Equal(t, e.alice.User.ID, got.Message.AuthorUserID)
	require.Equal(t, asked.Message.ID, got.Message.ReplyTo, "the quoted message is kept")
	require.Equal(t, "P1", got.Message.ExternalID)
	require.Equal(t, 2, e.stored(t))

	e.emit(typed)
	require.Equal(t, 2, e.stored(t), "the same message twice is stored once")

	// An AI operates the account now: it is the author, and the account is still the owner's.
	atlas := e.env.NewAI(e.alice, "atlas")
	e.patch(t, dto.UpdateConnectorRequest{OperatorID: &atlas})
	e.emit(own(groupID, "P2", "typed on Atlas's number"))
	got = e.helper.Message(connectortest.Timeout)
	author = got.Message.Author
	require.Equal(t, []any{atlas, dto.IntelligenceRoleAI, dto.IntelligenceKindAI}, []any{author.ID, author.Role, author.Kind})
	require.True(t, got.Trusted, "an AI's account is the owner's")
	require.Equal(t, e.alice.User.ID, got.Message.AuthorUserID)
	require.Equal(t, 3, e.stored(t))
}

// ACC-05: media typed or sent from the platform itself are stored like any other chat media.
func TestOwnAccountMediaIsStored(t *testing.T) {
	e := newOutboundEnv(t)
	var opened atomic.Int32
	m := own(groupID, "P1", "the invoice")
	m.Kind = "document"
	m.Media = []connectors.Media{{Name: "invoice.txt", Mime: "text/plain", Open: func(ctx context.Context) (io.ReadCloser, error) {
		opened.Add(1)
		return opener([]byte("synthetic invoice"))(ctx)
	}}}
	e.emit(m)

	msgs := e.messages(t, e.room.ID)
	require.Len(t, msgs, 1)
	require.Equal(t, dto.MessageKindDocument, msgs[0].Kind)
	require.Len(t, msgs[0].Attachments, 1)
	require.Equal(t, "invoice.txt", msgs[0].Attachments[0].Name)
	require.Equal(t, dto.AttachmentStatusStored, msgs[0].Attachments[0].Status)

	e.emit(m)
	require.Len(t, e.messages(t, e.room.ID), 1)
	require.Equal(t, int32(1), opened.Load(), "a repeat downloads nothing (CON-10)")
}

// ACC-05: what Partyline sent comes back from the platform as a message of the account itself;
// it is not stored a second time. A message of the account with another id is.
func TestEchoOfAPartylineSendIsDropped(t *testing.T) {
	e := newOutboundEnv(t)
	reply := e.agentPost(t, e.room.ID, "on its way")
	require.Equal(t, "on its way", e.inst.WaitSend(t).Text)
	e.outbox(t, reply.ID, func(o dto.OutboxItem) bool { return o.Status == dto.OutboxStatusSent })
	require.Equal(t, 1, e.stored(t))

	e.emit(own(groupID, "ext-1", "on its way"))
	require.Equal(t, 1, e.stored(t), "the echo is dropped")

	e.emit(own(groupID, "ext-2", "on its way"))
	require.Equal(t, 2, e.stored(t), "an id Partyline did not send is the operator's own message")
}

// ACC-05: the outbox records the first id of a send, which is enough to recognize its echo when
// the connector reports nothing else, also after Partyline forgot what it sent.
func TestEchoIsRecognizedFromTheOutboxAlone(t *testing.T) {
	e := newOutboundEnv(t)
	e.inst.QuietSends()
	reply := e.agentPost(t, e.room.ID, "on its way")
	e.inst.WaitSend(t)
	e.outbox(t, reply.ID, func(o dto.OutboxItem) bool { return o.Status == dto.OutboxStatusSent })

	e.emit(own(groupID, "ext-1", "on its way"))
	require.Equal(t, 1, e.stored(t), "the outbox knows ext-1")
	e.emit(own(groupID, "ext-2", "typed"))
	require.Equal(t, 2, e.stored(t))
}

// ACC-05: an id belongs to the chat it was sent to; the same id in another chat is a different
// message.
func TestEchoIsMatchedInItsOwnChatOnly(t *testing.T) {
	e := newOutboundEnv(t)
	e.emit(text(otherID, "B0", "hello from B"))
	chatB := e.chatID(t, otherID)
	roomB := e.routedRoom(t, "B", chatB)
	reply := e.agentPost(t, e.room.ID, "on its way")
	e.inst.WaitSend(t)
	e.outbox(t, reply.ID, func(o dto.OutboxItem) bool { return o.Status == dto.OutboxStatusSent })

	e.emit(own(otherID, "ext-1", "typed in B"))
	require.Len(t, e.messages(t, roomB.ID), 1, "ext-1 was sent to chat A, not to chat B")
}

// ACC-05: the echo can reach the pipeline while the platform call that created it is still
// running, before the outbox row is marked sent. The message is held until the send ends, then
// recognized and dropped.
func TestEchoBeforeTheSendReturnsIsDropped(t *testing.T) {
	e := newOutboundEnv(t)
	e.inst.HoldSends()
	reply := e.agentPost(t, e.room.ID, "on its way")
	e.inst.WaitSending(t)

	done := make(chan struct{})
	go func() {
		e.emit(own(groupID, "ext-1", "on its way"))
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("an own-account message was handled while a send to its chat was running")
	case <-time.After(300 * time.Millisecond):
	}
	require.Equal(t, 1, e.stored(t), "held: nothing stored yet")

	e.inst.ReleaseSends()
	e.inst.WaitSend(t)
	select {
	case <-done:
	case <-time.After(connectortest.Timeout):
		t.Fatal("the held message was never handled")
	}
	e.outbox(t, reply.ID, func(o dto.OutboxItem) bool { return o.Status == dto.OutboxStatusSent })
	require.Equal(t, 1, e.stored(t), "recognized as the echo once the send ended")
}

// ACC-05: a message typed on the phone while Partyline sends to the chat waits for the send and
// is then stored: only an echo is dropped.
func TestTypedMessageDuringASendIsStoredAfterIt(t *testing.T) {
	e := newOutboundEnv(t)
	e.inst.HoldSends()
	e.agentPost(t, e.room.ID, "on its way")
	e.inst.WaitSending(t)

	done := make(chan struct{})
	go func() {
		e.emit(own(groupID, "P1", "typed meanwhile"))
		close(done)
	}()
	e.inst.ReleaseSends()
	e.inst.WaitSend(t)
	select {
	case <-done:
	case <-time.After(connectortest.Timeout):
		t.Fatal("the held message was never handled")
	}
	require.Equal(t, 2, e.stored(t))
	require.Equal(t, "typed meanwhile", e.messages(t, e.room.ID)[0].Text)
}

// ACC-05: a send that created several messages (the parts of a long text, its files) is echoed
// once per message; the outbox keeps the first id only, and every one is dropped.
func TestEchoOfEveryMessageOfASendIsDropped(t *testing.T) {
	e := newOutboundEnv(t)
	e.inst.SetSendParts(2)
	reply := e.agentPost(t, e.room.ID, "a long answer")
	e.inst.WaitSend(t)
	e.outbox(t, reply.ID, func(o dto.OutboxItem) bool { return o.Status == dto.OutboxStatusSent })

	for _, id := range []string{"ext-1.p2", "ext-1", "ext-1.p1"} {
		e.emit(own(groupID, id, "a long answer"))
	}
	require.Equal(t, 1, e.stored(t), "every part's echo is dropped")

	e.emit(own(groupID, "ext-1.p3", "typed"))
	require.Equal(t, 2, e.stored(t), "a part Partyline did not create is stored")
}

// ACC-05: a send that never ends does not hold an own-account message back for more than 10
// seconds; after that it is stored as the operator's.
func TestAStuckSendHoldsAnOwnMessageForTenSecondsOnly(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	var waited atomic.Int64
	e := newOutboundEnv(t, testkit.WithClock(clock.Now), testkit.WithInboundWait(func(_ context.Context, d time.Duration) error {
		waited.Add(int64(d))
		clock.Advance(d)
		return nil
	}))
	e.inst.HoldSends()
	e.agentPost(t, e.room.ID, "never delivered")
	e.inst.WaitSending(t)

	e.emit(own(groupID, "P1", "typed on the phone"))

	held := time.Duration(waited.Load())
	require.GreaterOrEqual(t, held, 10*time.Second, "held for the whole limit")
	require.Less(t, held, 11*time.Second, "and not longer")
	require.Equal(t, 2, e.stored(t), "then stored")
	require.Equal(t, "typed on the phone", e.messages(t, e.room.ID)[0].Text)
}

// ACC-05: a message of the account is not held when nothing is being sent to its chat.
func TestOwnMessageIsNotHeldWithoutASend(t *testing.T) {
	var waits atomic.Int32
	e := newOutboundEnv(t, testkit.WithInboundWait(func(context.Context, time.Duration) error {
		waits.Add(1)
		return nil
	}))
	e.emit(own(groupID, "P1", "typed"))
	require.Equal(t, 1, e.stored(t))
	require.Zero(t, waits.Load())
}

// ACC-05: an own-account message creates nothing: no chat, no conversation, no person, and
// nothing is stored for a chat that is in no room.
func TestOwnAccountMessageCreatesNothing(t *testing.T) {
	pc := newPerChat(t)
	before := pc.messageCount(t)

	pc.emit(own(dmA, "P1", "typed to someone new"))
	require.False(t, pc.hasChat(t, dmA), "no chat from a message of the account")
	require.Zero(t, pc.dmRooms(t), "no conversation")
	require.Empty(t, pc.people(t))
	require.Equal(t, before, pc.messageCount(t))

	// A group the connector knows but routes nowhere (the scope is DMs only).
	pc.emit(text(groupID, "G1", "hello"))
	require.True(t, pc.hasChat(t, groupID))
	group := pc.chatID(t, groupID)
	pc.emit(own(groupID, "P2", "typed in the group"))
	require.Empty(t, pc.roomsOf(t, group))
	require.Equal(t, before, pc.messageCount(t))
	require.Empty(t, pc.people(t))
	require.Zero(t, pc.dmRooms(t))
}

// ACC-05, IDN-02: the account's own message is not a pairing command: it links nobody and does
// not spend the code, and the code is not kept in the room.
func TestOwnAccountPairingCommandDoesNothing(t *testing.T) {
	e := newOutboundEnv(t)
	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))

	e.emit(own(groupID, "P1", "!link "+code.Code))
	require.Zero(t, e.stored(t), "a pairing code is not kept in the room")

	e.emit(as(text(groupID, "L1", "!link "+code.Code), sender2, "", "Alice on the phone"))
	e.emit(as(text(groupID, "M1", "linked?"), sender2, "", "Alice on the phone"))
	got := e.helper.Message(connectortest.Timeout)
	require.True(t, got.Trusted, "the code was still good for the person who typed it")
}

// Partyline remembers what it sent for a few minutes, for the parts of a send the outbox does not
// record; it forgets later, and per chat.
func TestSentIDsRememberForAFewMinutesPerChat(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	s := connectors.NewSentIDs(clock.Now)
	s.Add("chat-a", "id-1")
	s.Add("chat-a", "")
	require.True(t, s.Has("chat-a", "id-1"))
	require.False(t, s.Has("chat-b", "id-1"), "another chat's message")
	require.False(t, s.Has("chat-a", "id-2"))
	require.False(t, s.Has("chat-a", ""), "an empty id matches nothing")

	clock.Advance(5*time.Minute - time.Second)
	require.True(t, s.Has("chat-a", "id-1"))
	clock.Advance(time.Second)
	require.False(t, s.Has("chat-a", "id-1"), "forgotten after five minutes")

	var none *connectors.SentIDs
	none.Add("chat-a", "id-1")
	require.False(t, none.Has("chat-a", "id-1"), "a missing registry remembers nothing")
}

// The registry does not grow without bound: ids past their time are dropped as new ones come.
func TestSentIDsDropExpiredEntries(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	s := connectors.NewSentIDs(clock.Now)
	for i := range 600 {
		s.Add("chat-a", "old-"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	clock.Advance(connectors.Remembered + time.Second)
	s.Add("chat-a", "new")
	require.True(t, s.Has("chat-a", "new"))
	require.Equal(t, 1, connectors.SentIDsLen(s), "expired entries were pruned")
}
