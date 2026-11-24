package whatsapp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors/connectortest"
)

// Quotes as evolution-go reports them: the answer's content node carries contextInfo with the quoted
// message's id (stanzaID), its author (participant) and a copy of it (quotedMessage). Synthetic.
const (
	ownJID   = "5511999990000@s.whatsapp.net" // the jid of the fake instance "acme"
	otherJID = "5511999991234@s.whatsapp.net"
)

// quoting is an answer "that one" that quotes the message quoted, written by participant.
func quoting(stanza, participant, quoted string) string {
	return `{"extendedTextMessage":{"text":"that one","contextInfo":{"stanzaID":"` + stanza + `","participant":"` + participant +
		`","quotedMessage":` + quoted + `}}}`
}

// QC-01, QC-02: a quote of a text names the quoted message and previews its text.
func TestQC02QuoteOfATextPreviewsItsText(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("Q1"), quoting("ORIG1", otherJID, `{"conversation":"which color do you want?"}`)))
	require.Equal(t, "that one", p.Inbound.Text)
	require.Equal(t, "ORIG1", p.Inbound.ReplyToID)
	require.NotNil(t, p.Inbound.Quote)
	require.Equal(t, "ORIG1", p.Inbound.Quote.ID)
	require.Equal(t, "which color do you want?", p.Inbound.Quote.Preview)
	require.Equal(t, otherJID, p.QuotedParticipant)

	p = parse(t, frame(t, testInstance, groupInfo("Q2"), quoting("ORIG2", otherJID, `{"extendedTextMessage":{"text":"line one\nline two"}}`)))
	require.Equal(t, "line one line two", p.Inbound.Quote.Preview, "one line")
}

// QC-02: a quote of an image is "[image]" followed by its caption when it has one.
func TestQC02QuoteOfAnImagePreviewsItsKindAndCaption(t *testing.T) {
	p := parse(t, frame(t, testInstance, dmInfo("Q3"),
		quoting("IMG1", ownJID, `{"imageMessage":{"mimetype":"image/jpeg","caption":"option B","fileLength":"2048"}}`)))
	require.Equal(t, "[image] option B", p.Inbound.Quote.Preview)

	p = parse(t, frame(t, testInstance, dmInfo("Q4"), quoting("IMG2", ownJID, `{"imageMessage":{"mimetype":"image/jpeg","fileLength":"2048"}}`)))
	require.Equal(t, "[image]", p.Inbound.Quote.Preview, "an image without a caption")
	require.Equal(t, "IMG2", p.Inbound.Quote.ID)
}

// QC-02: every media kind has its bracketed tag; a document names its file, and a wrapped quoted
// message is unwrapped like any message.
func TestQC02QuoteOfOtherKinds(t *testing.T) {
	for quoted, want := range map[string]string{
		`{"videoMessage":{"mimetype":"video/mp4","caption":"the clip"}}`:                            "[video] the clip",
		`{"audioMessage":{"mimetype":"audio/ogg","ptt":true}}`:                                      "[audio]",
		`{"documentMessage":{"mimetype":"application/pdf","fileName":"plan.pdf"}}`:                  "[document: plan.pdf]",
		`{"stickerMessage":{"mimetype":"image/webp"}}`:                                              "[sticker]",
		`{"locationMessage":{"degreesLatitude":-23.5,"degreesLongitude":-46.6}}`:                    "[location]",
		`{"contactMessage":{"displayName":"Ana","vcard":"BEGIN:VCARD\nEND:VCARD"}}`:                 "[contact] Ana",
		`{"ephemeralMessage":{"message":{"imageMessage":{"mimetype":"image/jpeg","caption":"x"}}}}`: "[image] x",
	} {
		p := parse(t, frame(t, testInstance, groupInfo("Q5"), quoting("ORIG", otherJID, quoted)))
		require.Equal(t, want, p.Inbound.Quote.Preview, quoted)
	}
}

// QC-02: the preview is at most 200 characters, cut with an ellipsis.
func TestQC02QuotePreviewIsShort(t *testing.T) {
	long := strings.Repeat("a", 300)
	p := parse(t, frame(t, testInstance, groupInfo("Q6"), quoting("ORIG", otherJID, `{"conversation":"`+long+`"}`)))
	require.Equal(t, strings.Repeat("a", 199)+"…", p.Inbound.Quote.Preview)
}

// QC-04: a quote whose copy the event does not carry still names the quoted message, without a
// preview; a message that quotes nothing has no quote.
func TestQC04QuoteWithoutItsCopyAndNoQuote(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("Q7"), `{"extendedTextMessage":{"text":"x","contextInfo":{"stanzaId":"ORIG2"}}}`))
	require.Equal(t, "ORIG2", p.Inbound.Quote.ID)
	require.Empty(t, p.Inbound.Quote.Preview)

	p = parse(t, frame(t, testInstance, groupInfo("Q8"), `{"conversation":"plain"}`))
	require.Nil(t, p.Inbound.Quote)
}

// QC-01: a reaction, an edit and a delete name the message they act on in ReplyToID, and quote nothing.
func TestQC01ReactionsEditsAndDeletesQuoteNothing(t *testing.T) {
	for _, msg := range []string{
		`{"reactionMessage":{"key":{"ID":"ORIG"},"text":"👍"}}`,
		`{"protocolMessage":{"type":14,"key":{"ID":"ORIG"},"editedMessage":{"conversation":"fixed"}}}`,
		`{"protocolMessage":{"type":0,"key":{"ID":"ORIG"}}}`,
	} {
		p := parse(t, frame(t, testInstance, groupInfo("Q9"), msg))
		require.Equal(t, "ORIG", p.Inbound.ReplyToID, msg)
		require.Nil(t, p.Inbound.Quote, msg)
	}
}

// QC-03: a quote of a message the number itself wrote says so, whether Partyline sent it or not; a
// quote of someone else's does not.
func TestQC03QuoteOfTheNumbersOwnMessage(t *testing.T) {
	f := newFakeEvolution(t)
	rec := connectortest.NewRecorder()
	sock := start(t, newConnector(t, f), f, rec)
	push(t, sock, frame(t, testInstance, dmInfo("QS1"), quoting("MINE", ownJID, `{"imageMessage":{"mimetype":"image/jpeg","caption":"option B"}}`)))
	push(t, sock, frame(t, testInstance, groupInfo("QS2"), quoting("THEIRS", otherJID, `{"conversation":"hi"}`)))

	in := rec.NextInbound(t)
	require.Equal(t, "MINE", in.Quote.ID)
	require.Equal(t, "[image] option B", in.Quote.Preview)
	require.True(t, in.Quote.FromSelf, "the number wrote the quoted message")
	in = rec.NextInbound(t)
	require.Equal(t, "THEIRS", in.Quote.ID)
	require.False(t, in.Quote.FromSelf)
}
