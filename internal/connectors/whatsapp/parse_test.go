package whatsapp

import (
	"encoding/json"
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
