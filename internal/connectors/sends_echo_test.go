package connectors_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// sendAs sends text to the chat as the connection's operator through the app's Sends service, the
// one the pipeline shares its record of sent ids with.
func (e *inboundEnv) sendAs(chat, text string) (dto.PlatformSendResult, error) {
	return e.env.App.Sends.Send(context.Background(), connectors.SendInput{
		AgentID: e.operator, ConnectorID: e.conn.ID, ChatID: chat, Text: text,
	})
}

func (e *inboundEnv) connectionSend(t *testing.T, chat, text string) dto.PlatformSendResult {
	t.Helper()
	res, err := e.sendAs(chat, text)
	require.NoError(t, err)
	return res
}

// sendInBackground starts a send and returns a channel that gets its error once it ended.
func (e *inboundEnv) sendInBackground(chat, text string) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := e.sendAs(chat, text)
		done <- err
	}()
	return done
}

// ended waits for a send started by sendInBackground and requires it to have succeeded.
func ended(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(connectortest.Timeout):
		t.Fatal("the send never finished")
	}
}

// dmChat records the direct chat dmA and returns its id: a direct chat is not subject to
// mentions-only, so that the tests below see what the echo rules do and nothing else.
func (e *inboundEnv) dmChat(t *testing.T, platformID string) string {
	t.Helper()
	e.emit(dmText(platformID, "hello-"+platformID, "oi"))
	e.drain(t)
	return e.chatID(t, platformID)
}

// CID-14: what an AI sent through the connection comes back from the platform as a message of the
// account itself; it is not delivered. A message of the account with another id is.
func TestEchoOfASendThroughAConnectionIsDropped(t *testing.T) {
	e := newInboundEnv(t)
	res := e.connectionSend(t, e.dmChat(t, dmA), "on its way")
	require.Equal(t, dto.PlatformSendStatusSent, res.Status)
	require.Equal(t, "on its way", e.inst.WaitSend(t).Text)

	e.emit(ownDM(dmA, "ext-1", "on its way"))
	require.Empty(t, e.queued(t), "the echo is dropped")

	e.emit(ownDM(dmA, "ext-2", "on its way"))
	require.Equal(t, []string{"on its way"}, texts(e.queued(t)), "an id Partyline did not send is the account's own typing")
	require.True(t, e.queued(t)[0].FromSelf)
}

// CID-14: the record of the sends alone recognizes the echo, also after Partyline forgot what it sent
// (a restart): a pipeline that remembers nothing drops it too, and an id of another chat is a
// different message.
func TestEchoIsRecognizedFromTheRecordOfTheSendsAlone(t *testing.T) {
	e := newInboundEnv(t)
	e.connectionSend(t, e.dmChat(t, dmA), "on its way")
	e.dmChat(t, dmB)
	forgetful := e.pipeline(connectors.NewSentIDs(nil))

	forgetful.Handle(context.Background(), e.source(), ownDM(dmA, "ext-1", "on its way"))
	require.Empty(t, e.queued(t), "the record knows ext-1")

	forgetful.Handle(context.Background(), e.source(), ownDM(dmA, "ext-2", "typed"))
	require.Equal(t, []string{"typed"}, texts(e.queued(t)), "only the id of the send is an echo")

	forgetful.Handle(context.Background(), e.source(), ownDM(dmB, "ext-1", "typed in B"))
	require.Equal(t, []string{"typed", "typed in B"}, texts(e.queued(t)), "ext-1 was sent to chat A, not to chat B")
}

// CID-14: a send that is not over has no platform id yet, and a message of the account that carries
// no id is never an echo.
func TestAMessageWithoutAnIDIsNeverTheEchoOfASend(t *testing.T) {
	e := newInboundEnv(t)
	e.connectionSend(t, e.dmChat(t, dmA), "on its way")
	e.emit(ownDM(dmA, "", "typed without an id"))
	require.Equal(t, []string{"typed without an id"}, texts(e.queued(t)))
}

// CID-14: the echo can reach the pipeline while the platform call that created it is still running,
// before the send is marked sent. The message is held until the send ends, then recognized and
// dropped.
func TestEchoBeforeTheSendReturnsIsHeldAndDropped(t *testing.T) {
	e := newInboundEnv(t)
	chat := e.dmChat(t, dmA)
	e.inst.HoldSends()
	sendDone := e.sendInBackground(chat, "on its way")
	e.inst.WaitSending(t)

	echoDone := make(chan struct{})
	go func() {
		e.emit(ownDM(dmA, "ext-1", "on its way"))
		close(echoDone)
	}()
	select {
	case <-echoDone:
		t.Fatal("an own-account message was handled while a send to its chat was running")
	case <-time.After(300 * time.Millisecond):
	}

	e.inst.ReleaseSends()
	ended(t, sendDone)
	waitFor(t, echoDone, "the held message")
	require.Empty(t, e.queued(t), "recognized as the echo once the send ended")
}

// CID-14: a message typed on the phone while a send to the chat runs waits for the send and is then
// delivered: only an echo is dropped.
func TestTypedMessageDuringAConnectionSendIsDeliveredAfterIt(t *testing.T) {
	e := newInboundEnv(t)
	chat := e.dmChat(t, dmA)
	e.inst.HoldSends()
	sendDone := e.sendInBackground(chat, "on its way")
	e.inst.WaitSending(t)

	typedDone := make(chan struct{})
	go func() {
		e.emit(ownDM(dmA, "P1", "typed meanwhile"))
		close(typedDone)
	}()
	e.inst.ReleaseSends()
	ended(t, sendDone)
	waitFor(t, typedDone, "the held message")
	require.Equal(t, []string{"typed meanwhile"}, texts(e.queued(t)))
}

// CID-14: a send in flight holds back the messages of its own chat only; a message of another chat
// is handled at once (L-042: only the chat differs).
func TestAMessageIsHeldOnlyWhileASendToItsOwnChatRuns(t *testing.T) {
	var waits atomic.Int32
	e := newInboundEnv(t, testkit.WithInboundWait(func(ctx context.Context, d time.Duration) error {
		waits.Add(1)
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	chatA := e.dmChat(t, dmA)
	e.dmChat(t, dmB)
	e.inst.HoldSends()
	sendDone := e.sendInBackground(chatA, "on its way")
	e.inst.WaitSending(t)

	e.emit(ownDM(dmB, "P1", "typed in B"))
	require.Equal(t, []string{"typed in B"}, texts(e.queued(t)))
	require.Zero(t, waits.Load(), "chat B is not held by the send to chat A")

	e.inst.ReleaseSends()
	ended(t, sendDone)
}

// CID-12: under mentions-only a group message that replies to a message the account sent is
// delivered, also long after Partyline forgot the id (the record of the sends remembers); a reply to
// any other message, and an id that was sent to another chat, are not.
func TestAReplyToAMessageTheAccountSentThroughAConnectionIsDelivered(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(text(groupID, "M0", "hello")) // mentions-only drops it; the chat is recorded
	e.emit(text(otherID, "B0", "hello"))
	res := e.connectionSend(t, e.chatID(t, groupID), "on its way")
	require.Equal(t, dto.PlatformSendStatusSent, res.Status)
	forgetful := e.pipeline(connectors.NewSentIDs(nil))
	reply := func(chat, id, to string) connectors.Inbound {
		m := text(chat, id, "thanks "+id)
		m.ReplyToID = to
		return m
	}

	forgetful.Handle(context.Background(), e.source(), reply(groupID, "R0", "ext-9"))
	forgetful.Handle(context.Background(), e.source(), reply(groupID, "R1", ""))
	forgetful.Handle(context.Background(), e.source(), reply(otherID, "R2", "ext-1"))
	require.Empty(t, e.queued(t), "a reply to another message, none, and the id of a send to another chat")

	forgetful.Handle(context.Background(), e.source(), reply(groupID, "R3", "ext-1"))
	require.Equal(t, []string{"thanks R3"}, texts(e.queued(t)))
}

func waitFor(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(connectortest.Timeout):
		t.Fatalf("%s never finished", what)
	}
}
