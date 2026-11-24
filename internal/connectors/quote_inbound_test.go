package connectors_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/whatsapp"
	"github.com/wnexous/partyline/internal/platform"
)

// waPeer is the WhatsApp chat of the quote tests, as the WhatsApp connector names it (synthetic).
const waPeer = "acme|5511999990001@s.whatsapp.net"

// waQuote is what the WhatsApp connector parses from an evolution-go event of the peer that answers
// "that one" quoting the message stanza, whose copy (quotedMessage) is quoted.
func waQuote(t *testing.T, id, stanza, quoted string) connectors.Inbound {
	t.Helper()
	message := `{"extendedTextMessage":{"text":"that one","contextInfo":{"stanzaID":"` + stanza +
		`","participant":"5511999990009@s.whatsapp.net","quotedMessage":` + quoted + `}}}`
	payload, err := json.Marshal(map[string]any{
		"event": "Message", "instanceName": "acme",
		"data": map[string]any{"Info": map[string]any{
			"ID": id, "Chat": "5511999990001@s.whatsapp.net", "Sender": "5511999990001@s.whatsapp.net",
			"IsFromMe": false, "IsGroup": false, "Timestamp": "2026-11-24T12:00:00Z", "PushName": "Customer",
		}, "Message": json.RawMessage(message)},
	})
	require.NoError(t, err)
	frame, err := json.Marshal(whatsapp.Frame{Queue: "q", Payload: string(payload)})
	require.NoError(t, err)
	ev, err := whatsapp.DecodeFrame(frame)
	require.NoError(t, err)
	p, err := whatsapp.Parse(ev)
	require.NoError(t, err)
	require.Equal(t, waPeer, p.Inbound.ChatID)
	return p.Inbound
}

// lastQueued is the newest item of the operator's queue.
func (e *inboundEnv) lastQueued(t *testing.T) platform.Event {
	t.Helper()
	list := e.queued(t)
	require.NotEmpty(t, list)
	return list[len(list)-1]
}

// QC-01, QC-02, QC-03: a WhatsApp answer that quotes one of the AI's sends carries reply_to = the id
// the send tool returned, matched by the platform id of the send's first message or of a later one
// (the image after its caption), the preview of the quoted message from the event, and
// quoted_from_self. The record of the sends alone tells it: a pipeline that remembers nothing
// recent (a restart) matches as well.
func TestQC01QuoteOfAnOwnSendNamesTheSend(t *testing.T) {
	e := newInboundEnv(t)
	chat := e.dmChat(t, waPeer)
	first := e.connectionSend(t, chat, "here are the options")
	e.inst.WaitSend(t)
	e.inst.SetSendParts(1) // the caption, then the image: ext-2 and ext-2.p1
	second, err := e.env.App.Sends.Send(context.Background(), connectors.SendInput{
		AgentID: e.operator, ConnectorID: e.conn.ID, ChatID: chat, Text: "option B",
		File: connectors.BytesFile("b.jpg", "image/jpeg", []byte("jpeg")),
	})
	require.NoError(t, err)
	e.inst.WaitSend(t)
	forgetful := e.pipeline(connectors.NewSentIDs(nil))

	forgetful.Handle(context.Background(), e.source(), waQuote(t, "IN1", "ext-1", `{"conversation":"here are the options"}`))
	got := e.lastQueued(t)
	require.Equal(t, "that one", got.Text)
	require.Equal(t, first.ID, got.ReplyTo, "the send's first message")
	require.Equal(t, "here are the options", got.Quoted)
	require.True(t, got.QuotedFromSelf, "Partyline sent it")

	forgetful.Handle(context.Background(), e.source(), waQuote(t, "IN2", "ext-2.p1", `{"imageMessage":{"mimetype":"image/jpeg"}}`))
	got = e.lastQueued(t)
	require.Equal(t, second.ID, got.ReplyTo, "the image after the caption names its send")
	require.Equal(t, "[image]", got.Quoted)
	require.True(t, got.QuotedFromSelf)

	forgetful.Handle(context.Background(), e.source(), waQuote(t, "IN3", "ext-2", `{"conversation":"option B"}`))
	require.Equal(t, second.ID, e.lastQueued(t).ReplyTo, "and so does the caption")
}

// QC-01, QC-03: a quote of an earlier message of the chat names its item, also after it was
// acknowledged; a quote of a message the account typed on the platform is the account's own.
func TestQC01QuoteOfAnEarlierMessageNamesItsItem(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(dmText(waPeer, "EARLIER", "which one is cheaper?"))
	earlier := e.lastQueued(t).ID
	e.emit(ownDM(waPeer, "TYPED", "typed on the phone"))
	typed := e.lastQueued(t).ID
	e.drain(t)

	e.emit(waQuote(t, "IN1", "EARLIER", `{"conversation":"which one is cheaper?"}`))
	got := e.lastQueued(t)
	require.Equal(t, earlier, got.ReplyTo)
	require.Equal(t, "which one is cheaper?", got.Quoted)
	require.False(t, got.QuotedFromSelf, "the peer wrote it")

	e.emit(waQuote(t, "IN2", "TYPED", `{"imageMessage":{"mimetype":"image/jpeg","caption":"this one"}}`))
	got = e.lastQueued(t)
	require.Equal(t, typed, got.ReplyTo)
	require.Equal(t, "[image] this one", got.Quoted)
	require.True(t, got.QuotedFromSelf, "the account typed it")
}

// QC-03: the platform's word that the account wrote the quoted message is enough, though Partyline
// knows nothing of the message: no reply_to, the preview and quoted_from_self.
func TestQC03TheConnectorSaysTheAccountWroteIt(t *testing.T) {
	e := newInboundEnv(t)
	m := dmText(waPeer, "IN1", "that one")
	m.Quote = &connectors.Quote{ID: "UNKNOWN", Preview: "[image] option C", FromSelf: true}
	e.emit(m)
	got := e.lastQueued(t)
	require.Empty(t, got.ReplyTo)
	require.Equal(t, "[image] option C", got.Quoted)
	require.True(t, got.QuotedFromSelf)
}

// QC-04: what is not known is absent, and a message that quotes nothing has no quote fields; a quote
// never keeps a message from its AI.
func TestQC04UnknownQuoteFieldsAreAbsent(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(waQuote(t, "IN1", "NEVER-SEEN", `{"conversation":"old message"}`))
	got := e.lastQueued(t)
	require.Equal(t, "that one", got.Text, "delivered")
	require.Empty(t, got.ReplyTo)
	require.Equal(t, "old message", got.Quoted)
	require.False(t, got.QuotedFromSelf)

	quoteWithoutAnything := dmText(waPeer, "IN2", "hm")
	quoteWithoutAnything.Quote = &connectors.Quote{}
	e.emit(quoteWithoutAnything)
	e.emit(dmText(waPeer, "IN3", "plain"))
	for _, it := range e.queued(t)[1:] {
		require.Empty(t, it.ReplyTo, it.Text)
		require.Empty(t, it.Quoted, it.Text)
		require.False(t, it.QuotedFromSelf, it.Text)
	}
}

// QC-05: the preview is platform text and goes with the text on ack; reply_to stays with the item.
func TestQC05AckDeletesThePreviewWithTheText(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(dmText(waPeer, "EARLIER", "first"))
	e.emit(waQuote(t, "IN1", "EARLIER", `{"conversation":"first"}`))
	got := e.lastQueued(t)
	require.Equal(t, "first", got.Quoted)
	require.NoError(t, e.env.App.Platform.Ack(context.Background(), e.operator, got.ID))

	after, err := e.env.App.Platform.Get(context.Background(), e.operator, got.ID)
	require.NoError(t, err)
	require.Empty(t, after.Text)
	require.Empty(t, after.Quoted, "the preview is gone with the text")
	require.Equal(t, got.ReplyTo, after.ReplyTo)
}
