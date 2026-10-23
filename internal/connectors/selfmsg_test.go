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

// ownDM is a message of the account in a direct chat.
func ownDM(chat, id, body string) connectors.Inbound {
	m := dmText(chat, id, body)
	m.FromSelf = true
	m.SenderID, m.SenderName = accountID, "Account"
	return m
}

// CID-14: a message typed on the platform with the connector's own account (FromSelf, an id
// Partyline never sent) is delivered to the operator as from_self and never trusted, even when the
// account's sender id is a verified account of the owner; its reply target is not an obligation to
// answer, and a repeat of it is queued once.
func TestOwnTypingIsDeliveredAsFromSelfAndNeverTrusted(t *testing.T) {
	e := newInboundEnv(t)
	e.link(t, e.alice, accountID, "", "Alice phone") // the account's id is a verified account of the owner
	typed := ownDM(dmA, "P1", "checking it now")
	typed.ReplyToID = "M1"
	e.emit(typed)

	got := e.queued(t)
	require.Len(t, got, 1)
	require.Equal(t, []any{"checking it now", true, false, accountID, "P1"}, []any{got[0].Text, got[0].FromSelf, got[0].Trusted, got[0].SenderID, got[0].ExternalID})
	require.Equal(t, dto.ChatKindDM, got[0].ChatKind)

	e.emit(typed)
	require.Len(t, e.queued(t), 1, "the same message twice is queued once")

	// A customer's message is not from_self.
	e.emit(dmText(dmA, "D1", "and the order?"))
	require.False(t, e.queued(t)[1].FromSelf)
}

// CID-14, CID-12: the account's own typing in a group goes through the same filters as any group
// message: without a mention (or a reply to a Partyline send) it is dropped while mentions-only is
// on, and it is delivered when it mentions the account or the setting is off.
func TestOwnTypingInAGroupFollowsTheGroupFilters(t *testing.T) {
	e := newInboundEnv(t)

	e.emit(own(groupID, "P1", "typed in the group"))
	require.Empty(t, e.queued(t), "mentions-only drops it")
	mentioned := own(groupID, "P2", "typed, mentioning the account")
	mentioned.MentionsAccount = true
	e.emit(mentioned)
	require.Equal(t, []string{"typed, mentioning the account"}, texts(e.queued(t)))

	e.patch(t, dto.UpdateConnectorRequest{GroupsMentionsOnly: ptr(false)})
	e.emit(own(groupID, "P3", "typed, setting off"))
	got := e.queued(t)
	require.Len(t, got, 2)
	require.True(t, got[1].FromSelf)
	require.False(t, got[1].Trusted)
}

// CID-14, CID-07: media typed or sent from the platform itself are stored like any other media,
// once; the message creates nothing but its queue item and its chat record.
func TestOwnTypingMediaIsStoredOnce(t *testing.T) {
	e := newInboundEnv(t)
	var opened atomic.Int32
	m := ownDM(dmA, "P1", "the invoice")
	m.Kind = "document"
	m.Media = []connectors.Media{{Name: "invoice.txt", Mime: "text/plain", Open: func(ctx context.Context) (io.ReadCloser, error) {
		opened.Add(1)
		return opener([]byte("synthetic invoice"))(ctx)
	}}}
	e.emit(m)

	got := e.queued(t)
	require.Len(t, got, 1)
	require.Equal(t, dto.MessageKindDocument, got[0].Kind)
	require.Len(t, got[0].Attachments, 1)
	require.Equal(t, []any{"invoice.txt", dto.AttachmentStatusStored}, []any{got[0].Attachments[0].Name, got[0].Attachments[0].Status})

	e.emit(m)
	require.Len(t, e.queued(t), 1)
	require.Equal(t, int32(1), opened.Load(), "a repeat downloads nothing (CON-10)")
}

// CID-14: an id belongs to the chat it was sent to, in Partyline's memory of the last minutes too;
// the same id in another chat is a different message.
func TestEchoIsMatchedInItsOwnChatOnly(t *testing.T) {
	e := newInboundEnv(t)
	e.connectionSend(t, e.dmChat(t, dmA), "on its way")
	e.dmChat(t, dmB)
	require.Equal(t, "on its way", e.inst.WaitSend(t).Text)

	e.emit(ownDM(dmB, "ext-1", "typed in B"))
	require.Equal(t, []string{"typed in B"}, texts(e.queued(t)), "ext-1 was sent to chat A, not to chat B")
}

// CID-14: a send that created several messages (the parts of a long text, its files) is echoed
// once per message; the record of the send keeps the first id only, and every one is dropped.
func TestEchoOfEveryMessageOfASendIsDropped(t *testing.T) {
	e := newInboundEnv(t)
	e.inst.SetSendParts(2)
	e.connectionSend(t, e.dmChat(t, dmA), "a long answer")
	e.inst.WaitSend(t)

	for _, id := range []string{"ext-1.p2", "ext-1", "ext-1.p1"} {
		e.emit(ownDM(dmA, id, "a long answer"))
	}
	require.Empty(t, e.queued(t), "every part's echo is dropped")

	e.emit(ownDM(dmA, "ext-1.p3", "typed"))
	require.Equal(t, []string{"typed"}, texts(e.queued(t)), "a part Partyline did not create is delivered")
}

// CID-14: a send that never ends does not hold an own-account message back for more than 10
// seconds; after that it is delivered as the account's own typing.
func TestAStuckSendHoldsAnOwnMessageForTenSecondsOnly(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	var waited atomic.Int64
	e := newInboundEnv(t, testkit.WithClock(clock.Now), testkit.WithInboundWait(func(_ context.Context, d time.Duration) error {
		waited.Add(int64(d))
		clock.Advance(d)
		return nil
	}))
	chat := e.dmChat(t, dmA)
	waited.Store(0)
	e.inst.HoldSends()
	sendDone := e.sendInBackground(chat, "never delivered")
	e.inst.WaitSending(t)

	e.emit(ownDM(dmA, "P1", "typed on the phone"))

	held := time.Duration(waited.Load())
	require.GreaterOrEqual(t, held, 10*time.Second, "held for the whole limit")
	require.Less(t, held, 11*time.Second, "and not longer")
	require.Equal(t, []string{"typed on the phone"}, texts(e.queued(t)), "then delivered")

	e.inst.ReleaseSends()
	ended(t, sendDone)
}

// CID-14: a message of the account is not held when nothing is being sent to its chat.
func TestOwnMessageIsNotHeldWithoutASend(t *testing.T) {
	var waits atomic.Int32
	e := newInboundEnv(t, testkit.WithInboundWait(func(context.Context, time.Duration) error {
		waits.Add(1)
		return nil
	}))
	e.emit(ownDM(dmA, "P1", "typed"))
	require.Len(t, e.queued(t), 1)
	require.Zero(t, waits.Load())
}

// CID-14, IDN-02: the account's own message is not a pairing command: it links nobody and does
// not spend the code, and the code is not delivered.
func TestOwnAccountPairingCommandDoesNothing(t *testing.T) {
	e := newInboundEnv(t)
	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))

	e.emit(ownDM(dmA, "P1", "!link "+code.Code))
	require.Empty(t, e.queued(t), "a pairing code is not delivered")
	require.Empty(t, testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items, "and links nobody")

	e.emit(as(dmText(dmA, "L1", "!link "+code.Code), sender2, "", "Alice on the phone"))
	e.emit(as(dmText(dmA, "M1", "linked?"), sender2, "", "Alice on the phone"))
	got := e.queued(t)
	require.Len(t, got, 1)
	require.True(t, got[0].Trusted, "the code was still good for the person who typed it")
}

// Partyline remembers what it sent for a few minutes, for the parts of a send its record does not
// hold; it forgets later, and per chat.
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
