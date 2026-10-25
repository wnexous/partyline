package connectors_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// A phone number the fake platform takes as an address (8 to 15 digits), which no chat has.
const newNumber = "5541999990000"

// toAddress is a send of text to a platform address as Atlas.
func (f *sendsFixture) toAddress(address, text string) connectors.SendInput {
	return connectors.SendInput{AgentID: f.atlas, ConnectorID: f.conn, ChatID: address, Text: text}
}

// chats lists the external ids of the connection's chats.
func (f *sendsFixture) chats(t *testing.T) []string {
	t.Helper()
	rows, err := f.st.R().ListConnectorChats(context.Background(), f.conn)
	require.NoError(t, err)
	var out []string
	for _, r := range rows {
		out = append(out, r.ExternalID)
	}
	return out
}

// discovered waits for the event that announces the chat.
func (f *sendsFixture) discovered(t *testing.T, chatID string) {
	t.Helper()
	deadline := time.After(connectortest.Timeout)
	for {
		select {
		case e := <-f.statusC:
			if d, ok := e.(events.ChatDiscovered); ok && d.ChatID == chatID {
				return
			}
		case <-deadline:
			t.Fatalf("chat %s was never announced", chatID)
		}
	}
}

// CID-31: the operator writes to an address the platform accepts that no chat of the connection has
// been seen at: the chat is created (named the way the platform names it, a direct chat, announced
// like any discovered chat) and the text goes to it through the connection, unsigned.
func TestTheOperatorStartsAChatByWritingToAnAddress(t *testing.T) {
	f := newSendsFixture(t)
	before := f.chats(t)

	res, err := f.sends.Send(context.Background(), f.toAddress(newNumber, "good morning"))
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusSent}, res)

	require.ElementsMatch(t, append(before, newNumber), f.chats(t), "the chat of the address was created")
	chat, err := f.st.R().GetChatByExternalID(context.Background(), dbChatKey(f.conn, newNumber))
	require.NoError(t, err)
	require.Equal(t, []any{"dm", "", f.conn}, []any{chat.Kind, chat.Title, chat.ConnectorID})
	require.False(t, chat.LastSeenAt.Valid, "nothing was heard from it yet")
	f.discovered(t, chat.ID)

	got := f.inst.WaitSend(t)
	require.Equal(t, []any{newNumber, "good morning", true, "", ""}, []any{got.ChatID, got.Text, got.Unsigned, got.Signature(), got.ReplyToID})
	row := f.sendRow(t, res.ID)
	require.Equal(t, []any{"sent", chat.ID, f.conn, f.atlas}, []any{row.Status, row.ChatID, row.ConnectorID, row.AgentID})
	require.True(t, f.sent.Has(chat.ID, "ext-1"), "the echo of the first message to a stranger is recognized too")
}

// CID-31: writing to the address again is writing to the chat it created, whichever way it is named:
// no second chat is made and nothing is counted against the allowance for new chats.
func TestWritingToTheSameAddressAgainUsesTheChat(t *testing.T) {
	asked := 0
	f := newSendsFixture(t, func(d *connectors.SendsDeps) {
		d.AllowNewChat = func(string) bool { asked++; return true }
	})
	first, err := f.sends.Send(context.Background(), f.toAddress(newNumber, "good morning"))
	require.NoError(t, err)
	chat, err := f.st.R().GetChatByExternalID(context.Background(), dbChatKey(f.conn, newNumber))
	require.NoError(t, err)
	count := len(f.chats(t))

	second, err := f.sends.Send(context.Background(), f.toAddress(" "+newNumber+"\n", "and another"))
	require.NoError(t, err)
	third, err := f.sends.Send(context.Background(), connectors.SendInput{AgentID: f.atlas, ConnectorID: f.conn, ChatID: chat.ID, Text: "by its id"})
	require.NoError(t, err)

	require.Len(t, f.chats(t), count, "the same chat all three times")
	for _, id := range []string{first.ID, second.ID, third.ID} {
		require.Equal(t, chat.ID, f.sendRow(t, id).ChatID)
	}
	require.Equal(t, 1, asked, "only the first message started a chat")
	require.Equal(t, []string{"good morning", "and another", "by its id"}, outboundTexts(f.inst.Sent()))
}

// CID-31: an address of a chat the connection already has is that chat: its title stays, nothing is
// created and the allowance for new chats is not touched.
func TestAnAddressOfAKnownChatIsThatChat(t *testing.T) {
	asked := 0
	f := newSendsFixture(t, func(d *connectors.SendsDeps) {
		d.AllowNewChat = func(string) bool { asked++; return true }
	})
	known := f.newChat(t, f.conn, "5541999990001", "Pat", "dm")
	count := len(f.chats(t))

	res, err := f.sends.Send(context.Background(), f.toAddress("5541999990001", "hello again"))
	require.NoError(t, err)
	require.Equal(t, known.ID, f.sendRow(t, res.ID).ChatID)
	again, err := f.st.R().GetChat(context.Background(), known.ID)
	require.NoError(t, err)
	require.Equal(t, "Pat", again.Title)
	require.Len(t, f.chats(t), count)
	require.Zero(t, asked)
}

// CID-17, CID-31: what is neither a chat of the connection nor an address its platform accepts stays
// an unknown chat: a name, a number that is too short, the id of a chat of another connection and a
// chat nobody has. Nothing is created or sent, and no allowance is spent.
func TestWhatIsNoAddressIsAnUnknownChat(t *testing.T) {
	asked := 0
	f := newSendsFixture(t, func(d *connectors.SendsDeps) {
		d.AllowNewChat = func(string) bool { asked++; return true }
	})
	other := f.insertWithoutOperator(t, false)
	foreign := f.newChat(t, other, "5541999990002", "Elsewhere", "dm")
	count := len(f.chats(t))

	for _, ref := range []string{
		"", " ", "hello", "5541", "5541 99999 0000", "+5541999990000", "5541999990000@s.whatsapp.net", "-1001234567890",
		"1234567890123456", foreign.ID, store.NewID(), strings.ToUpper(f.chat.ID) + "x",
	} {
		err := f.sends.Check(context.Background(), f.toAddress(ref, "x"))
		require.ErrorIs(t, err, connectors.ErrUnknownChat, "%q", ref)
		_, err = f.sends.Send(context.Background(), f.toAddress(ref, "x"))
		require.ErrorIs(t, err, connectors.ErrUnknownChat, "%q", ref)
	}
	require.Len(t, f.chats(t), count, "no chat was created")
	require.Empty(t, f.inst.Sent())
	require.Zero(t, asked)
}

// CID-03, CID-31: only the operator starts a chat, with the same refusals as for any send: an AI that
// does not operate the connection is refused whatever it names, and nothing is created; a connection
// nobody operates has no operator, and one that does not exist is not found.
func TestOnlyTheOperatorStartsAChat(t *testing.T) {
	f := newSendsFixture(t)
	count := len(f.chats(t))
	notes := f.ai(t)

	for _, ref := range []string{newNumber, "hello", f.chat.ID} {
		in := f.toAddress(ref, "x")
		in.AgentID = notes
		require.ErrorIs(t, f.sends.Check(context.Background(), in), connectors.ErrNotOperator, "%q", ref)
		_, err := f.sends.Send(context.Background(), in)
		require.ErrorIs(t, err, connectors.ErrNotOperator, "%q", ref)
	}
	idle := f.insertWithoutOperator(t, false)
	in := f.toAddress(newNumber, "x")
	in.ConnectorID = idle
	_, err := f.sends.Send(context.Background(), in)
	require.ErrorIs(t, err, connectors.ErrNotOperator, "nobody operates it")
	in.ConnectorID = store.NewID()
	_, err = f.sends.Send(context.Background(), in)
	require.ErrorIs(t, err, access.ErrNotFound)

	require.Len(t, f.chats(t), count)
	require.Empty(t, f.inst.Sent())
}

// CID-31: a kind that has no rule for addresses (the webhook) only sends to chats it has seen: the
// same digits that start a chat elsewhere are an unknown chat there.
func TestAKindWithoutAddressesOnlySendsToKnownChats(t *testing.T) {
	f := newSendsFixture(t, func(d *connectors.SendsDeps) {
		without := connectortest.New().Kind()
		without.Address = nil
		d.Kinds = connectors.NewRegistry(without)
	})
	_, err := f.sends.Send(context.Background(), f.toAddress(newNumber, "x"))
	require.ErrorIs(t, err, connectors.ErrUnknownChat)
	_, err = f.send("a chat it knows still works")
	require.NoError(t, err)
}

// CID-31: the allowance for new chats is asked once a chat is about to be created and not before: the
// text is valid, the AI operates the connection and the address is one the platform accepts. A
// refusal creates nothing and sends nothing, answers ErrNewChatRateLimited, and does not stop the AI
// from writing to chats it has or from starting one when the allowance is back.
func TestStartingAChatIsLimited(t *testing.T) {
	var asked []string
	allow := true
	f := newSendsFixture(t, func(d *connectors.SendsDeps) {
		d.AllowNewChat = func(agent string) bool { asked = append(asked, agent); return allow }
	})
	before := f.chats(t)
	count := len(before)

	_, err := f.sends.Send(context.Background(), f.toAddress(newNumber, " \n"))
	var invalid *validation.Error
	require.ErrorAs(t, err, &invalid, "unusable text is refused before anything is counted")
	_, err = f.sends.Send(context.Background(), f.toAddress("hello", "x"))
	require.ErrorIs(t, err, connectors.ErrUnknownChat)
	stranger := f.toAddress(newNumber, "x")
	stranger.AgentID = f.ai(t)
	_, err = f.sends.Send(context.Background(), stranger)
	require.ErrorIs(t, err, connectors.ErrNotOperator)
	require.NoError(t, f.sends.Check(context.Background(), f.toAddress(newNumber, "x")), "Check changes and counts nothing")
	require.Empty(t, asked)
	require.Len(t, f.chats(t), count)

	allow = false
	_, err = f.sends.Send(context.Background(), f.toAddress(newNumber, "good morning"))
	require.ErrorIs(t, err, connectors.ErrNewChatRateLimited)
	require.Equal(t, []string{f.atlas}, asked, "asked for the AI that writes")
	require.Len(t, f.chats(t), count, "a refused chat is not created")
	require.Empty(t, f.inst.Sent())

	_, err = f.send("the group it has is still free") // a known chat: not asked
	require.NoError(t, err)
	require.Len(t, asked, 1)

	allow = true
	_, err = f.sends.Send(context.Background(), f.toAddress(newNumber, "good morning"))
	require.NoError(t, err)
	require.ElementsMatch(t, append(before, newNumber), f.chats(t))
}

// A platform that refuses the first message to a new chat leaves the chat and a pending send, which the
// worker retries like any other (CID-18): the chat was created before the attempt.
func TestAFirstMessageThePlatformRefusesIsRetried(t *testing.T) {
	f := newSendsFixture(t)
	f.inst.SetSendError(errors.New("platform unavailable"))
	res, err := f.sends.Send(context.Background(), f.toAddress(newNumber, "good morning"))
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusPending, Error: "platform unavailable"}, res)
	require.Contains(t, f.chats(t), newNumber)

	f.inst.SetSendError(nil)
	f.clock.Advance(2 * time.Second)
	require.NoError(t, f.sends.Flush(context.Background()))
	require.Equal(t, "sent", f.sendRow(t, res.ID).Status)
	require.Equal(t, []string{newNumber}, []string{f.inst.Sent()[0].ChatID})
}

// dbChatKey is the key of a chat by its platform id.
func dbChatKey(connector, external string) db.GetChatByExternalIDParams {
	return db.GetChatByExternalIDParams{ConnectorID: connector, ExternalID: external}
}
