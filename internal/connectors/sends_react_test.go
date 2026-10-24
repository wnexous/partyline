package connectors_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/validation"
)

// reaction is a reaction of Atlas to the message "M1" of the group, written by senderID.
func (f *sendsFixture) reaction(emoji string) connectors.ReactInput {
	return connectors.ReactInput{
		AgentID: f.atlas, ConnectorID: f.conn, ChatID: f.chat.ID, TargetID: "M1", TargetSenderID: senderID, Emoji: emoji,
	}
}

// CID-33: the operator reacts to a message of its connection: the platform gets the chat's own id, the
// platform id of the message, who wrote it and the emoji, and the platform's id of the reaction is
// remembered so that its echo is recognized (CID-14). The send is recorded as attempted once, without
// text, and ends sent.
func TestTheOperatorReactsToAMessageOfItsConnection(t *testing.T) {
	f := newSendsFixture(t)
	res, err := f.sends.React(context.Background(), f.reaction("👍"))
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusSent}, res)

	got := f.inst.WaitReact(t)
	require.Equal(t, []any{groupID, "M1", senderID, false, "👍"}, []any{got.ChatID, got.TargetID, got.TargetSenderID, got.TargetFromMe, got.Emoji})
	row := f.sendRow(t, res.ID)
	require.Equal(t, []any{"sent", true, "", "react-1", f.chat.ID, f.atlas, int64(1)},
		[]any{row.Status, row.Once, row.Text, row.ExternalID, row.ChatID, row.AgentID, row.Attempts})
	require.True(t, f.sent.Has(f.chat.ID, "react-1"), "the reaction is a message of the account")
	require.Empty(t, f.inst.Sent(), "no message was sent")

	own := f.reaction("❤️")
	own.TargetFromSelf = true
	_, err = f.sends.React(context.Background(), own)
	require.NoError(t, err)
	require.True(t, f.inst.WaitReact(t).TargetFromMe, "a message the account wrote itself")
}

// CID-33: a platform that refuses the reaction fails the send at once with the reason, and nothing tries
// again; a platform that never answers does the same after 15 seconds (here 100 ms).
func TestAReactionThePlatformRefusesIsFailedAndNotRetried(t *testing.T) {
	f := newSendsFixture(t, func(d *connectors.SendsDeps) { d.AttemptTimeout = 100 * time.Millisecond })
	f.inst.SetReactError(errors.New("setMessageReaction: emoji is not one of Telegram's allowed reactions"))
	res, err := f.sends.React(context.Background(), f.reaction("🤖"))
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusFailed, Error: "setMessageReaction: emoji is not one of Telegram's allowed reactions"}, res)
	row := f.sendRow(t, res.ID)
	require.Equal(t, []any{"failed", true, int64(1), ""}, []any{row.Status, row.Once, row.Attempts, row.Text})

	f.inst.SetReactError(nil)
	f.clock.Advance(time.Hour)
	require.NoError(t, f.sends.Flush(context.Background()))

	f.inst.HoldSends()
	defer f.inst.ReleaseSends()
	res, err = f.sends.React(context.Background(), f.reaction("👍"))
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusFailed, Error: "the platform did not answer within 100ms"}, res)
}

// CID-33: a platform without reactions (the webhook) refuses them, and so does a message the platform
// gave no id: there is nothing to react to. Nothing is recorded or sent.
func TestAReactionNeedsAPlatformThatHasThem(t *testing.T) {
	f := newSendsFixture(t, func(d *connectors.SendsDeps) {
		kind := connectortest.New().Kind()
		kind.CanReact = false
		d.Kinds = connectors.NewRegistry(kind)
	})
	_, err := f.sends.React(context.Background(), f.reaction("👍"))
	require.ErrorIs(t, err, connectors.ErrReactionsUnsupported)
	require.Zero(t, f.sendRows(t))

	g := newSendsFixture(t)
	in := g.reaction("👍")
	in.TargetID = ""
	_, err = g.sends.React(context.Background(), in)
	require.ErrorIs(t, err, connectors.ErrNoReactionTarget)
	require.Zero(t, g.sendRows(t))
	require.Empty(t, g.inst.Sent())
}

// CID-33: the emoji is one emoji: nothing, text, two emoji and a letter are refused as a field, before
// anything is recorded; a skin tone, a flag and a family each are one.
func TestAReactionIsOneEmoji(t *testing.T) {
	f := newSendsFixture(t)
	for _, emoji := range []string{"", " ", "ok", "👍👍", "a👍", "👍 ", "👍\n👍"} {
		in := f.reaction(emoji)
		err := f.sends.CheckReaction(context.Background(), in)
		if emoji == "👍 " {
			require.NoError(t, err, "surrounding white space is trimmed")
			continue
		}
		var invalid *validation.Error
		require.ErrorAs(t, err, &invalid, "%q", emoji)
		require.Equal(t, map[string]string{"emoji": "must be one emoji"}, invalid.Fields, "%q", emoji)
	}
	for _, emoji := range []string{"👍🏽", "🏳️‍🌈", "👨‍👩‍👧‍👦", "❤️"} {
		require.NoError(t, f.sends.CheckReaction(context.Background(), f.reaction(emoji)), emoji)
	}
	require.Zero(t, f.sendRows(t))
}

// CID-03, CID-33: only the operator reacts, with the refusals of any send: another AI is refused, a
// connection nobody operates has no operator, one that does not exist is not found, and a chat of
// another connection is an unknown chat.
func TestOnlyTheOperatorReacts(t *testing.T) {
	f := newSendsFixture(t)
	in := f.reaction("👍")
	in.AgentID = f.ai(t)
	_, err := f.sends.React(context.Background(), in)
	require.ErrorIs(t, err, connectors.ErrNotOperator)

	in = f.reaction("👍")
	in.ConnectorID = f.insertWithoutOperator(t, false)
	_, err = f.sends.React(context.Background(), in)
	require.ErrorIs(t, err, connectors.ErrNotOperator)
	in.ConnectorID = store.NewID()
	_, err = f.sends.React(context.Background(), in)
	require.ErrorIs(t, err, access.ErrNotFound)

	in = f.reaction("👍")
	in.ChatID = f.newChat(t, f.insertWithoutOperator(t, false), "5541999990003", "Elsewhere", "dm").ID
	_, err = f.sends.React(context.Background(), in)
	require.ErrorIs(t, err, connectors.ErrUnknownChat)
	in.ChatID = store.NewID()
	_, err = f.sends.React(context.Background(), in)
	require.ErrorIs(t, err, connectors.ErrUnknownChat)
	require.Zero(t, f.sendRows(t))
}

// ownReaction is a reaction of the account in a direct chat, as the platform reports it back.
func ownReaction(chat, id, emoji string) connectors.Inbound {
	m := ownDM(chat, id, emoji)
	m.Kind = string(dto.MessageKindReaction)
	return m
}

// reactAs reacts as the connection's operator, through the app's Sends service, the one the pipeline shares
// its record of sent ids with.
func (e *inboundEnv) reactAs(chat, target, emoji string) (dto.PlatformSendResult, error) {
	return e.env.App.Sends.React(context.Background(), connectors.ReactInput{
		AgentID: e.operator, ConnectorID: e.conn.ID, ChatID: chat, TargetID: target, TargetSenderID: dmA, Emoji: emoji,
	})
}

// CID-14, CID-33: a platform that reports the account's own messages reports its reactions too. The
// reaction Partyline sent comes back with the id the platform gave it and is not delivered, or the AI
// would be told of its own reaction and might react to it; a reaction typed on the phone is.
func TestEchoOfAReactionIsDropped(t *testing.T) {
	e := newInboundEnv(t)
	chat := e.dmChat(t, dmA)
	res, err := e.reactAs(chat, "hello-"+dmA, "👍")
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendStatusSent, res.Status)
	e.inst.WaitReact(t)

	e.emit(ownReaction(dmA, "react-1", "👍"))
	require.Empty(t, e.queued(t), "the echo is dropped")

	e.emit(ownReaction(dmA, "react-2", "👍"))
	got := e.queued(t)
	require.Len(t, got, 1, "a reaction Partyline did not send is the account's own typing")
	require.Equal(t, []any{dto.MessageKindReaction, true, "👍"}, []any{got[0].Kind, got[0].FromSelf, got[0].Text})
}

// CID-14, CID-33: the echo of a reaction can reach the pipeline before the call that created it returns: it
// is held until the reaction ends, then recognized and dropped.
func TestEchoOfAReactionBeforeItReturnsIsHeldAndDropped(t *testing.T) {
	e := newInboundEnv(t)
	chat := e.dmChat(t, dmA)
	e.inst.HoldSends()
	done := make(chan error, 1)
	go func() {
		_, err := e.reactAs(chat, "hello-"+dmA, "👍")
		done <- err
	}()
	e.inst.WaitSending(t)

	echoDone := make(chan struct{})
	go func() {
		e.emit(ownReaction(dmA, "react-1", "👍"))
		close(echoDone)
	}()
	select {
	case <-echoDone:
		t.Fatal("an own-account message was handled while a reaction to its chat was running")
	case <-time.After(300 * time.Millisecond):
	}

	e.inst.ReleaseSends()
	ended(t, done)
	waitFor(t, echoDone, "the held message")
	require.Empty(t, e.queued(t), "recognized as the echo once the reaction ended")
}
