package whatsapp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// Synthetic fixtures only: no real numbers, groups or names.
const (
	testInstance = "acme"
	groupJID     = "120363000000000001@g.us"
	peerJID      = "5511999990000@s.whatsapp.net"
	peerDevice   = "5511999990000:12@s.whatsapp.net"
	peerLID      = "100000000000001@lid"
)

// frame builds a broadcast frame for a Message event of instance with the given info and
// message JSON.
func frame(t *testing.T, instance string, info map[string]any, message string) []byte {
	t.Helper()
	payload := map[string]any{
		"event": "Message", "instanceName": instance, "instanceId": "00000000-0000-0000-0000-000000000001",
		"instanceToken": "synthetic-instance-token",
		"data":          map[string]any{"Info": info, "Message": json.RawMessage(message)},
	}
	p, err := json.Marshal(payload)
	require.NoError(t, err)
	f, err := json.Marshal(Frame{Queue: "00000000-0000-0000-0000-000000000001.message", Payload: string(p)})
	require.NoError(t, err)
	return f
}

func groupInfo(id string) map[string]any {
	return map[string]any{
		"ID": id, "Chat": groupJID, "Sender": peerDevice, "SenderAlt": peerLID, "IsFromMe": false, "IsGroup": true,
		"Timestamp": "2026-10-20T12:00:00Z", "PushName": "Test Person",
	}
}

func dmInfo(id string) map[string]any {
	return map[string]any{
		"ID": id, "Chat": peerDevice, "Sender": peerDevice, "SenderAlt": peerLID, "IsFromMe": false, "IsGroup": false,
		"Timestamp": "2026-10-20T12:00:00Z", "PushName": "Test Person",
	}
}

func parse(t *testing.T, data []byte) Parsed {
	t.Helper()
	ev, err := DecodeFrame(data)
	require.NoError(t, err)
	p, err := Parse(ev)
	require.NoError(t, err)
	return p
}

// WA-01, WA-02: a group text message maps to the group chat with bare sender ids.
func TestParseGroupConversation(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("MSG1"), `{"conversation":"hello group"}`))
	in := p.Inbound
	require.Equal(t, testInstance, p.Instance)
	require.Equal(t, "acme|120363000000000001@g.us", in.ChatID)
	require.Equal(t, "group", in.ChatKind)
	require.Empty(t, in.ChatTitle, "a group's title comes from the group list, not the sender")
	require.Equal(t, "MSG1", in.MessageID)
	require.Equal(t, "5511999990000@s.whatsapp.net", in.SenderID)
	require.Equal(t, peerLID, in.SenderAltID)
	require.Equal(t, "Test Person", in.SenderName)
	require.False(t, in.FromSelf)
	require.Equal(t, string(dto.MessageKindText), in.Kind)
	require.Equal(t, "hello group", in.Text)
	require.Equal(t, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), in.At.UTC())
	require.Nil(t, p.Media)
}

// WA-02: a DM maps to the peer's JID without the device suffix.
func TestParseDirectMessageStripsDevice(t *testing.T) {
	p := parse(t, frame(t, testInstance, dmInfo("MSG2"), `{"conversation":"hi"}`))
	require.Equal(t, "acme|5511999990000@s.whatsapp.net", p.Inbound.ChatID)
	require.Equal(t, "dm", p.Inbound.ChatKind)
	require.Equal(t, "Test Person", p.Inbound.ChatTitle)
}

func TestBareJID(t *testing.T) {
	require.Equal(t, peerJID, BareJID(peerDevice))
	require.Equal(t, peerJID, BareJID(peerJID))
	require.Equal(t, groupJID, BareJID(groupJID))
}

// CON-12 input: messages from the instance's own number are flagged.
func TestParseFromSelf(t *testing.T) {
	info := groupInfo("MSG3")
	info["IsFromMe"] = true
	p := parse(t, frame(t, testInstance, info, `{"conversation":"mine"}`))
	require.True(t, p.Inbound.FromSelf)
}

func TestParseExtendedTextWithQuote(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("MSG4"),
		`{"extendedTextMessage":{"text":"replying","contextInfo":{"stanzaID":"ORIG1","participant":"5511999990000@s.whatsapp.net"}}}`))
	require.Equal(t, string(dto.MessageKindText), p.Inbound.Kind)
	require.Equal(t, "replying", p.Inbound.Text)
	require.Equal(t, "ORIG1", p.Inbound.ReplyToID)

	p = parse(t, frame(t, testInstance, groupInfo("MSG5"), `{"extendedTextMessage":{"text":"x","contextInfo":{"stanzaId":"ORIG2"}}}`))
	require.Equal(t, "ORIG2", p.Inbound.ReplyToID, "protojson spelling stanzaId")
}

// CID-12: the mentioned jids of a message come from the contextInfo of its content node, also
// inside a media caption.
func TestParseMentionedJIDs(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("MSG25"),
		`{"extendedTextMessage":{"text":"@acme look","contextInfo":{"mentionedJid":["5511999990000@s.whatsapp.net","100000000000001@lid"]}}}`))
	require.Equal(t, []string{"5511999990000@s.whatsapp.net", "100000000000001@lid"}, p.MentionedJIDs)

	p = parse(t, frame(t, testInstance, groupInfo("MSG26"),
		`{"imageMessage":{"caption":"@acme look","mimetype":"image/jpeg","contextInfo":{"mentionedJid":["5511999990000@s.whatsapp.net"]}}}`))
	require.Equal(t, []string{"5511999990000@s.whatsapp.net"}, p.MentionedJIDs)

	p = parse(t, frame(t, testInstance, groupInfo("MSG27"), `{"conversation":"no context"}`))
	require.Empty(t, p.MentionedJIDs)
}

func TestParseImageWithCaption(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("MSG6"),
		`{"imageMessage":{"caption":"a photo","mimetype":"image/jpeg","fileLength":2048},"base64":"data:image/jpeg;base64,aGVsbG8="}`))
	require.Equal(t, string(dto.MessageKindImage), p.Inbound.Kind)
	require.Equal(t, "a photo", p.Inbound.Text)
	require.NotNil(t, p.Media)
	require.Equal(t, "image/jpeg", p.Media.Mime)
	require.Equal(t, int64(2048), p.Media.Size)
	require.Equal(t, "image.jpg", p.Media.Name)
	require.Equal(t, "aGVsbG8=", p.Media.Inline, "the data URL prefix is removed")
	require.JSONEq(t, `{"imageMessage":{"caption":"a photo","mimetype":"image/jpeg","fileLength":2048}}`, string(p.Media.Message))
}

func TestParseDocumentInsideCaptionEnvelope(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("MSG7"),
		`{"documentWithCaptionMessage":{"message":{"documentMessage":{"caption":"report","mimetype":"application/pdf","fileName":"report.pdf","fileLength":"4096"}}}}`))
	require.Equal(t, string(dto.MessageKindDocument), p.Inbound.Kind)
	require.Equal(t, "report", p.Inbound.Text)
	require.Equal(t, "report.pdf", p.Media.Name)
	require.Equal(t, int64(4096), p.Media.Size, "protojson sends 64-bit sizes as strings")
	require.Empty(t, p.Media.Inline)
}

func TestParseEphemeralWrapper(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("MSG8"),
		`{"ephemeralMessage":{"message":{"extendedTextMessage":{"text":"disappearing"}}}}`))
	require.Equal(t, string(dto.MessageKindText), p.Inbound.Kind)
	require.Equal(t, "disappearing", p.Inbound.Text)
}

func TestParseViewOnceNestedInEphemeral(t *testing.T) {
	for _, env := range []string{"viewOnceMessage", "viewOnceMessageV2", "viewOnceMessageV2Extension"} {
		msg := `{"ephemeralMessage":{"message":{"` + env + `":{"message":{"videoMessage":{"mimetype":"video/mp4","caption":"once"}}}}}}`
		p := parse(t, frame(t, testInstance, groupInfo("MSG9"), msg))
		require.Equal(t, string(dto.MessageKindVideo), p.Inbound.Kind, env)
		require.Equal(t, "once", p.Inbound.Text, env)
		require.Equal(t, "video/mp4", p.Media.Mime, env)
	}
}

func TestParseEnvelopeDepthIsBounded(t *testing.T) {
	msg := `{"conversation":"deep"}`
	for range maxEnvelopes + 1 {
		msg = `{"ephemeralMessage":{"message":` + msg + `}}`
	}
	p := parse(t, frame(t, testInstance, groupInfo("MSG10"), msg))
	require.Equal(t, string(dto.MessageKindUnknown), p.Inbound.Kind, "more than 5 envelopes are not unwrapped")
}

func TestParseEdit(t *testing.T) {
	for _, typ := range []string{`14`, `"MESSAGE_EDIT"`} {
		msg := `{"editedMessage":{"message":{"protocolMessage":{"type":` + typ + `,"key":{"ID":"ORIG3"},"editedMessage":{"conversation":"fixed text"}}}}}`
		p := parse(t, frame(t, testInstance, groupInfo("MSG11"), msg))
		require.Equal(t, string(dto.MessageKindEdit), p.Inbound.Kind, typ)
		require.Equal(t, "fixed text", p.Inbound.Text, typ)
		require.Equal(t, "ORIG3", p.Inbound.ReplyToID, typ)
	}
}

func TestParseRevoke(t *testing.T) {
	for _, typ := range []string{`0`, `"REVOKE"`} {
		p := parse(t, frame(t, testInstance, groupInfo("MSG12"), `{"protocolMessage":{"type":`+typ+`,"key":{"id":"ORIG4"}}}`))
		require.Equal(t, string(dto.MessageKindRevoke), p.Inbound.Kind, typ)
		require.Equal(t, "ORIG4", p.Inbound.ReplyToID, typ)
		require.NotEmpty(t, p.Inbound.Text, "non-text kinds carry a short text so they are stored")
	}
}

func TestParseOtherProtocolMessagesAreNotChatMessages(t *testing.T) {
	ev, err := DecodeFrame(frame(t, testInstance, groupInfo("MSG13"), `{"protocolMessage":{"type":5}}`))
	require.NoError(t, err)
	_, err = Parse(ev)
	require.ErrorIs(t, err, ErrNotMessage)
}

func TestParseReaction(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("MSG14"), `{"reactionMessage":{"text":"👍","key":{"ID":"ORIG5"}}}`))
	require.Equal(t, string(dto.MessageKindReaction), p.Inbound.Kind)
	require.Equal(t, "👍", p.Inbound.Text)
	require.Equal(t, "ORIG5", p.Inbound.ReplyToID)
}

func TestParseLocationContactPoll(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("MSG15"), `{"locationMessage":{"name":"Test Plaza","degreesLatitude":1.5}}`))
	require.Equal(t, string(dto.MessageKindLocation), p.Inbound.Kind)
	require.Equal(t, "Location: Test Plaza", p.Inbound.Text)

	p = parse(t, frame(t, testInstance, groupInfo("MSG16"), `{"contactMessage":{"displayName":"Test Contact"}}`))
	require.Equal(t, string(dto.MessageKindContact), p.Inbound.Kind)
	require.Equal(t, "Contact: Test Contact", p.Inbound.Text)

	p = parse(t, frame(t, testInstance, groupInfo("MSG17"), `{"pollCreationMessageV3":{"name":"Lunch?"}}`))
	require.Equal(t, string(dto.MessageKindPoll), p.Inbound.Kind)
	require.Equal(t, "Poll: Lunch?", p.Inbound.Text)
}

func TestParseUnknown(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("MSG18"), `{"someFutureMessage":{"x":1}}`))
	require.Equal(t, string(dto.MessageKindUnknown), p.Inbound.Kind)
	require.NotEmpty(t, p.Inbound.Text)
}

// WA-02: a message of a kind the connector does not know names the protocol types it carries, so
// the operator can see which shape arrived without seeing the content. messageContextInfo is
// metadata, not a content type; the names are sorted and at most maxUnknownKeys.
func TestParseUnknownNamesItsKinds(t *testing.T) {
	p := parse(t, frame(t, testInstance, groupInfo("MSGUNK1"),
		`{"messageContextInfo":{"messageAssociation":{"associationType":1}},"someNewMessage":{"x":1}}`))
	require.Equal(t, string(dto.MessageKindUnknown), p.Inbound.Kind)
	require.Equal(t, "(unsupported message: someNewMessage)", p.Inbound.Text)
	require.Equal(t, []string{"someNewMessage"}, p.UnknownKeys)

	p = parse(t, frame(t, testInstance, groupInfo("MSGUNK2"),
		`{"zMessage":{"x":1},"aMessage":{"x":1},"bMessage":{"x":1},"cMessage":{"x":1}}`))
	require.Equal(t, string(dto.MessageKindUnknown), p.Inbound.Kind)
	require.Equal(t, "(unsupported message: aMessage, bMessage, cMessage)", p.Inbound.Text)
	require.Equal(t, []string{"aMessage", "bMessage", "cMessage"}, p.UnknownKeys)

	p = parse(t, frame(t, testInstance, groupInfo("MSGUNK3"), `{"someNewMessage":"text"}`))
	require.Equal(t, string(dto.MessageKindUnknown), p.Inbound.Kind)
	require.Equal(t, "(unsupported message)", p.Inbound.Text)
	require.Empty(t, p.UnknownKeys)
}

// WA-01: only Message events are chat messages; status broadcasts are not chats.
func TestParseIgnoresOtherEventsAndStatusBroadcasts(t *testing.T) {
	ev, err := DecodeFrame(frame(t, testInstance, groupInfo("MSG19"), `{"conversation":"x"}`))
	require.NoError(t, err)
	ev.Event = "Receipt"
	_, err = Parse(ev)
	require.ErrorIs(t, err, ErrNotMessage)

	info := dmInfo("MSG20")
	info["Chat"] = "status@broadcast"
	ev, err = DecodeFrame(frame(t, testInstance, info, `{"conversation":"story"}`))
	require.NoError(t, err)
	_, err = Parse(ev)
	require.ErrorIs(t, err, ErrNotMessage)
}

func TestChatIDRoundTrip(t *testing.T) {
	instance, jid, ok := SplitChatID(ChatID(testInstance, groupJID))
	require.True(t, ok)
	require.Equal(t, testInstance, instance)
	require.Equal(t, groupJID, jid)
	_, _, ok = SplitChatID("no-separator")
	require.False(t, ok)
}

// CTX-06: a DM without a push name is titled with the peer's phone number masked to its last
// 4 digits ("+•••• 0000"); the chat id and sender id keep the full number, so agents still
// answer the chat. When the chat is addressed by LID, the number comes from the alternate JID.
func TestParseDirectMessageTitleFallsBackToNumber(t *testing.T) {
	info := dmInfo("MSG4")
	info["PushName"] = ""
	p := parse(t, frame(t, testInstance, info, `{"conversation":"hi"}`))
	require.Equal(t, "+•••• 0000", p.Inbound.ChatTitle)
	require.Equal(t, "acme|5511999990000@s.whatsapp.net", p.Inbound.ChatID)
	require.Equal(t, "5511999990000@s.whatsapp.net", p.Inbound.SenderID)

	info = dmInfo("MSG5")
	info["PushName"] = "  "
	info["Chat"], info["ChatAlt"] = peerLID, peerDevice
	info["Sender"], info["SenderAlt"] = peerLID, peerDevice
	p = parse(t, frame(t, testInstance, info, `{"conversation":"hi"}`))
	require.Equal(t, "acme|"+peerLID, p.Inbound.ChatID)
	require.Equal(t, "+•••• 0000", p.Inbound.ChatTitle)
}

// CID-29: the header of an album only counts what is coming; it says nothing a person wrote, so it
// is no message: it used to arrive as an unsupported message of its own.
func TestParseAlbumHeaderIsNotAMessage(t *testing.T) {
	ev, err := DecodeFrame(frame(t, testInstance, groupInfo("ALBUM"), albumHeader))
	require.NoError(t, err)
	_, err = Parse(ev)
	require.ErrorIs(t, err, ErrNotMessage)

	for _, wrapped := range []string{
		`{"ephemeralMessage":{"message":` + albumHeader + `}}`,
		`{"albumMessage":{"expectedImageCount":2},"messageContextInfo":{"messageAssociation":{"associationType":"UNKNOWN"}}}`,
	} {
		ev, err := DecodeFrame(frame(t, testInstance, groupInfo("ALBUM"), wrapped))
		require.NoError(t, err)
		_, err = Parse(ev)
		require.ErrorIs(t, err, ErrNotMessage, wrapped)
	}
}

// CID-29: every item of an album is the photo, video or document it is, whichever way WhatsApp
// wraps it (not at all, or in an associatedChildMessage with the association beside the wrapper or
// inside it) and however the association type is written (a number or the enum name): its kind,
// caption, file and quote are those of any other media message. It used to arrive as an unsupported
// message when wrapped.
func TestParseAlbumItems(t *testing.T) {
	shapes := map[string]func(node, typ string) string{"plain": plainItem, "wrapped": wrappedItem, "wrapped with the association inside": wrappedItemInside}
	for shape, build := range shapes {
		for _, typ := range []string{`1`, `"MEDIA_ALBUM"`} {
			t.Run(shape+"/"+typ, func(t *testing.T) {
				p := parse(t, frame(t, testInstance, groupInfo("ITEM1"), build(`"imageMessage":`+albumPhoto("the first one"), typ)))
				require.Equal(t, "ITEM1", p.Inbound.MessageID, "an item is a message of its own")
				require.Equal(t, string(dto.MessageKindImage), p.Inbound.Kind)
				require.Equal(t, "the first one", p.Inbound.Text)
				require.NotNil(t, p.Media)
				require.Equal(t, []string{"image/jpeg", "image.jpg"}, []string{p.Media.Mime, p.Media.Name})
				require.Equal(t, int64(2048), p.Media.Size)
				require.JSONEq(t, `{"imageMessage":`+albumPhoto("the first one")+`}`, string(p.Media.Message), "what downloadmedia gets is the media node alone")

				p = parse(t, frame(t, testInstance, groupInfo("ITEM2"), build(`"videoMessage":{"mimetype":"video/mp4","fileLength":"9000"}`, typ)))
				require.Equal(t, []string{string(dto.MessageKindVideo), "video/mp4", ""}, []string{p.Inbound.Kind, p.Media.Mime, p.Inbound.Text})

				p = parse(t, frame(t, testInstance, groupInfo("ITEM3"), build(`"documentMessage":{"mimetype":"application/pdf","fileName":"menu.pdf"}`, typ)))
				require.Equal(t, []string{string(dto.MessageKindDocument), "menu.pdf"}, []string{p.Inbound.Kind, p.Media.Name})
			})
		}
	}
}

// CID-29: what is said about the item inside its media node is read there, wrapped or not: the
// mention and the quote of a caption.
func TestParseAlbumItemMentionsAndQuotes(t *testing.T) {
	node := `"imageMessage":{"mimetype":"image/jpeg","caption":"@acme look","contextInfo":{"stanzaID":"ORIG1","mentionedJid":["5511999990000@s.whatsapp.net"]}}`
	for _, build := range []func(node, typ string) string{plainItem, wrappedItem, wrappedItemInside} {
		p := parse(t, frame(t, testInstance, groupInfo("ITEM1"), build(node, `1`)))
		require.Equal(t, "ORIG1", p.Inbound.ReplyToID)
		require.Equal(t, []string{"5511999990000@s.whatsapp.net"}, p.MentionedJIDs)
	}
}

// CID-29: an item inside the other wrappers is still an item.
func TestParseAlbumItemInsideOtherWrappers(t *testing.T) {
	item := wrappedItem(`"imageMessage":`+albumPhoto("hello"), `1`)
	for name, msg := range map[string]string{
		"ephemeral": `{"ephemeralMessage":{"message":` + item + `}}`,
		"view once": `{"viewOnceMessageV2":{"message":` + item + `}}`,
	} {
		p := parse(t, frame(t, testInstance, groupInfo("ITEM1"), msg))
		require.Equal(t, []string{string(dto.MessageKindImage), "hello"}, []string{p.Inbound.Kind, p.Inbound.Text}, name)
	}
}

// CID-29: a wrapped message that does not say what it is attached to is delivered as what it holds;
// one that says it belongs to something other than an album is not an item and stays as it was, an
// unsupported message: the better copy of a photo already delivered (the HD upload) must not
// arrive as a second photo.
func TestParseAssociatedChildMessagesThatAreNotAlbumItems(t *testing.T) {
	node := `"imageMessage":` + albumPhoto("")
	p := parse(t, frame(t, testInstance, groupInfo("CHILD1"), `{"associatedChildMessage":{"message":{`+node+`}}}`))
	require.Equal(t, string(dto.MessageKindImage), p.Inbound.Kind, "no association: delivered")
	p = parse(t, frame(t, testInstance, groupInfo("CHILD2"), wrappedItem(node, `0`)))
	require.Equal(t, string(dto.MessageKindImage), p.Inbound.Kind, "association UNKNOWN: delivered")
	p = parse(t, frame(t, testInstance, groupInfo("CHILD3"), wrappedItem(node, `"UNKNOWN"`)))
	require.Equal(t, string(dto.MessageKindImage), p.Inbound.Kind)

	for _, typ := range []string{
		`10`, `"HD_IMAGE_DUAL_UPLOAD"`, `5`, `"HD_VIDEO_DUAL_UPLOAD"`, `12`, `"MOTION_PHOTO"`, `19`, `21`, `22`, `99`, `"SOMETHING_NEW"`,
	} {
		for name, build := range map[string]func(node, typ string) string{"beside": wrappedItem, "inside": wrappedItemInside} {
			p := parse(t, frame(t, testInstance, groupInfo("HD1"), build(node, typ)))
			require.Equal(t, string(dto.MessageKindUnknown), p.Inbound.Kind, "%s/%s", name, typ)
			require.Equal(t, "(unsupported message)", p.Inbound.Text)
			require.Nil(t, p.Media, "no file is fetched for it")
		}
	}

	// A plain message keeps whatever association it carries: the type only matters for a wrapped one.
	p = parse(t, frame(t, testInstance, groupInfo("PLAIN"), plainItem(node, `10`)))
	require.Equal(t, string(dto.MessageKindImage), p.Inbound.Kind)
}

// CID-29: the edit of a caption that is wrapped like an album item is still an edit.
func TestParseEditOfAWrappedItem(t *testing.T) {
	msg := `{"editedMessage":{"message":{"protocolMessage":{"type":14,"key":{"ID":"ITEM1"},"editedMessage":{"associatedChildMessage":{"message":{"imageMessage":{"caption":"fixed caption"}}}}}}}}`
	p := parse(t, frame(t, testInstance, groupInfo("EDIT1"), msg))
	require.Equal(t, string(dto.MessageKindEdit), p.Inbound.Kind)
	require.Equal(t, "fixed caption", p.Inbound.Text)
}

// CID-30: a contact arrives with the numbers of its card, not with its name only: the text names the
// contact and puts the numbers between parentheses, as the card gives them (the WhatsApp id with a
// plus). The name is the one the sharer saw, else the card's; a card without numbers, or without a
// card, gives the name alone.
func TestParseContactWithNumbers(t *testing.T) {
	for name, tc := range map[string]struct{ node, want string }{
		"one number":            {`{"displayName":"Ana Test","vcard":` + quote(cardAna) + `}`, "Contact: Ana Test (+5511999990000)"},
		"two numbers":           {`{"displayName":"Bruno Test","vcard":` + quote(cardBruno) + `}`, "Contact: Bruno Test (+5521988880000, 2125550000)"},
		"the name of the card":  {`{"vcard":` + quote(cardAna) + `}`, "Contact: Ana Test (+5511999990000)"},
		"the sharer's name":     {`{"displayName":"Aninha","vcard":` + quote(cardAna) + `}`, "Contact: Aninha (+5511999990000)"},
		"a card without number": {`{"displayName":"No Phone","vcard":"BEGIN:VCARD\nFN:No Phone\nEND:VCARD"}`, "Contact: No Phone"},
		"no card":               {`{"displayName":"Test Contact"}`, "Contact: Test Contact"},
		"nothing":               {`{}`, "Contact: (no name)"},
	} {
		t.Run(name, func(t *testing.T) {
			p := parse(t, frame(t, testInstance, dmInfo("CARD1"), `{"contactMessage":`+tc.node+`}`))
			require.Equal(t, string(dto.MessageKindContact), p.Inbound.Kind)
			require.Equal(t, tc.want, p.Inbound.Text)
			require.Nil(t, p.Media)
		})
	}
	p := parse(t, frame(t, testInstance, dmInfo("CARD2"), `{"ephemeralMessage":{"message":{"contactMessage":{"displayName":"Ana Test","vcard":`+quote(cardAna)+`}}}}`))
	require.Equal(t, "Contact: Ana Test (+5511999990000)", p.Inbound.Text, "wrapped like any message")
}

// CID-30: several contacts shared at once (a contactsArrayMessage) are one message of kind contact
// that lists them all with their numbers, each on its own, instead of an unsupported message.
func TestParseSeveralContacts(t *testing.T) {
	msg := `{"contactsArrayMessage":{"displayName":"2 contacts","contacts":[` +
		`{"displayName":"Ana Test","vcard":` + quote(cardAna) + `},{"displayName":"Bruno Test","vcard":` + quote(cardBruno) + `},{"displayName":"Carla Test"}]}}`
	p := parse(t, frame(t, testInstance, dmInfo("CARDS"), msg))
	require.Equal(t, string(dto.MessageKindContact), p.Inbound.Kind)
	require.Equal(t, "Contacts: Ana Test (+5511999990000); Bruno Test (+5521988880000, 2125550000); Carla Test", p.Inbound.Text)

	p = parse(t, frame(t, testInstance, dmInfo("CARDS"), `{"contactsArrayMessage":{"displayName":"1 contact","contacts":[{"displayName":"Ana Test","vcard":`+quote(cardAna)+`}]}}`))
	require.Equal(t, "Contact: Ana Test (+5511999990000)", p.Inbound.Text, "one contact in a list is a contact")

	p = parse(t, frame(t, testInstance, dmInfo("CARDS"), `{"contactsArrayMessage":{"displayName":"0 contacts"}}`))
	require.Equal(t, "Contacts: (none)", p.Inbound.Text, "an empty list is still a message of kind contact")
}

// CID-30: a list of contacts is bounded like a card: the first maxContacts are named and the text says
// how many more there were.
func TestParseManyContactsIsBounded(t *testing.T) {
	var contacts []string
	for i := range maxContacts + 7 {
		contacts = append(contacts, `{"displayName":"Person `+strconv.Itoa(i)+`","vcard":"TEL:+5511900000`+fmt.Sprintf("%03d", i)+`"}`)
	}
	p := parse(t, frame(t, testInstance, dmInfo("MANY"), `{"contactsArrayMessage":{"contacts":[`+strings.Join(contacts, ",")+`]}}`))
	require.Equal(t, maxContacts, strings.Count(p.Inbound.Text, "Person "))
	require.True(t, strings.HasSuffix(p.Inbound.Text, "; and 7 more"), p.Inbound.Text[len(p.Inbound.Text)-30:])
}
