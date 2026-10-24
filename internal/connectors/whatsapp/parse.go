// Package whatsapp is the WhatsApp connector, through an evolution-go server: it receives
// message events on evolution-go's broadcast WebSocket and sends through its REST API.
package whatsapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strconv"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
)

// maxEnvelopes bounds how many envelope levels are unwrapped.
const maxEnvelopes = 5

// envelopes are the message wrappers that hold the real content in {"message": {...}}.
// associatedChildMessage wraps the items of an album and the HD copy of a photo or video (CID-29).
var envelopes = []string{
	"ephemeralMessage", "viewOnceMessage", "viewOnceMessageV2", "viewOnceMessageV2Extension",
	"documentWithCaptionMessage", "editedMessage", "associatedChildMessage",
}

// The types of association (messageContextInfo.messageAssociation.associationType) a message states
// when it belongs to another message, as WhatsApp numbers them. Only these two matter: a message
// attached to nothing, or to an album header, is an item of an album; any other (the HD copy of a
// photo, a motion photo, ...) is an update of a message that was delivered already.
const (
	associationUnknown    = 0
	associationMediaAlbum = 1
	// associationNone is a message that states no association; associationOther one whose type is
	// not one of the two above.
	associationNone  = -1
	associationOther = -2
)

// associationNames are the names of the types above, which evolution-go may write instead of the number.
var associationNames = map[string]int{"UNKNOWN": associationUnknown, "MEDIA_ALBUM": associationMediaAlbum}

// mediaKinds maps the media nodes to their message kind.
var mediaKinds = []struct {
	node string
	kind dto.MessageKind
}{
	{"imageMessage", dto.MessageKindImage},
	{"videoMessage", dto.MessageKindVideo},
	{"audioMessage", dto.MessageKindAudio},
	{"documentMessage", dto.MessageKindDocument},
	{"stickerMessage", dto.MessageKindSticker},
}

// Frame is one broadcast WebSocket frame of evolution-go.
type Frame struct {
	Queue   string `json:"queue"`
	Payload string `json:"payload"`
}

// Event is the payload of a frame. InstanceToken is never logged nor kept.
type Event struct {
	Event         string `json:"event"`
	InstanceName  string `json:"instanceName"`
	InstanceID    string `json:"instanceId"`
	InstanceToken string `json:"instanceToken"`
	Data          struct {
		Info    Info            `json:"Info"`
		Message json.RawMessage `json:"Message"`
	} `json:"data"`
}

// Info is the message metadata of a Message event.
type Info struct {
	ID        string `json:"ID"`
	Chat      string `json:"Chat"`
	ChatAlt   string `json:"ChatAlt"`
	Sender    string `json:"Sender"`
	SenderAlt string `json:"SenderAlt"`
	IsFromMe  bool   `json:"IsFromMe"`
	IsGroup   bool   `json:"IsGroup"`
	Timestamp string `json:"Timestamp"`
	PushName  string `json:"PushName"`
}

// DecodeFrame decodes a broadcast frame and its JSON-string payload.
func DecodeFrame(data []byte) (Event, error) {
	var f Frame
	if err := json.Unmarshal(data, &f); err != nil {
		return Event{}, fmt.Errorf("decode frame: %w", err)
	}
	var ev Event
	if err := json.Unmarshal([]byte(f.Payload), &ev); err != nil {
		return Event{}, fmt.Errorf("decode payload: %w", err)
	}
	return ev, nil
}

// MediaRef locates the content of an inbound media: Inline is the base64 the server sent
// with the event (WEBHOOK_FILES on), else Message is the unwrapped message to pass to
// POST /message/downloadmedia.
type MediaRef struct {
	Name    string
	Mime    string
	Size    int64
	Inline  string
	Message json.RawMessage
}

// Parsed is a Message event normalized: the inbound message (without Media) and the
// reference of its media, if any.
type Parsed struct {
	Instance string
	Inbound  connectors.Inbound
	Media    *MediaRef
	// MentionedJIDs are the account jids the message mentions (CID-12), as the platform sent
	// them; the connector matches them against its own jid.
	MentionedJIDs []string
}

// ErrNotMessage means the event is not a chat message Partyline handles.
var ErrNotMessage = errors.New("not a chat message")

// Parse turns a Message event into an inbound message (WA-01, WA-02). Chat ids are
// "<instance>|<bare jid>"; a group's chat is its group JID, a DM's chat is the peer's JID
// without the device suffix. Status broadcasts are not chats and yield ErrNotMessage.
func Parse(ev Event) (Parsed, error) {
	info := ev.Data.Info
	if ev.Event != "Message" || ev.InstanceName == "" || info.Chat == "" {
		return Parsed{}, ErrNotMessage
	}
	chat := BareJID(info.Chat)
	if strings.HasSuffix(chat, "@broadcast") {
		return Parsed{}, ErrNotMessage
	}
	group := info.IsGroup || strings.HasSuffix(chat, "@g.us")
	in := connectors.Inbound{
		ChatID:      ChatID(ev.InstanceName, chat),
		ChatKind:    "dm",
		MessageID:   info.ID,
		SenderID:    BareJID(info.Sender),
		SenderAltID: BareJID(info.SenderAlt),
		SenderName:  info.PushName,
		FromSelf:    info.IsFromMe,
	}
	if group {
		in.ChatKind = "group"
	} else if !info.IsFromMe {
		in.ChatTitle = strings.TrimSpace(info.PushName)
		if in.ChatTitle == "" {
			in.ChatTitle = maskNumber(phoneNumber(chat, info)) // CTX-06
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, info.Timestamp); err == nil {
		in.At = t
	}
	var root map[string]json.RawMessage
	if len(ev.Data.Message) > 0 {
		if err := json.Unmarshal(ev.Data.Message, &root); err != nil {
			return Parsed{}, fmt.Errorf("decode message: %w", err)
		}
	}
	out := Parsed{Instance: ev.InstanceName, Inbound: in}
	if !content(&out, root) {
		return Parsed{}, ErrNotMessage
	}
	return out, nil
}

// content fills kind, text, reply and media from the (possibly wrapped) message; it
// reports false for protocol messages other than edits and revokes (key shares, history
// sync notices, ...), which are not chat messages.
func content(p *Parsed, root map[string]json.RawMessage) bool {
	in := &p.Inbound
	inline := stripDataURL(str(root["base64"]))
	msg, wrap := unwrap(root)
	if wrap.child && wrap.assoc != associationNone && wrap.assoc != associationUnknown && wrap.assoc != associationMediaAlbum {
		// The better copy of a photo or video already delivered, not a message of its own (CID-29).
		in.Kind, in.Text = string(dto.MessageKindUnknown), "(unsupported message)"
		return true
	}
	in.ReplyToID = quotedID(msg)
	p.MentionedJIDs = mentionedJIDs(msg)
	if s := str(msg["conversation"]); s != "" {
		in.Kind, in.Text = string(dto.MessageKindText), s
		return true
	}
	if node := obj(msg["extendedTextMessage"]); node != nil {
		in.Kind, in.Text = string(dto.MessageKindText), str(node["text"])
		return true
	}
	for _, mk := range mediaKinds {
		node := obj(msg[mk.node])
		if node == nil {
			continue
		}
		in.Kind, in.Text = string(mk.kind), str(node["caption"])
		ref := &MediaRef{Name: str(node["fileName"]), Mime: str(node["mimetype"]), Size: num(node["fileLength"]), Inline: inline}
		if ref.Name == "" {
			ref.Name = string(mk.kind) + extension(ref.Mime)
		}
		ref.Message, _ = json.Marshal(map[string]json.RawMessage{mk.node: msg[mk.node]})
		p.Media = ref
		return true
	}
	if node := obj(msg["reactionMessage"]); node != nil {
		in.Kind, in.Text = string(dto.MessageKindReaction), str(node["text"])
		in.ReplyToID = keyID(node["key"])
		if in.Text == "" {
			in.Text = "(reaction removed)"
		}
		return true
	}
	if node := obj(msg["protocolMessage"]); node != nil {
		switch protocolType(node["type"]) {
		case 14: // MESSAGE_EDIT
			in.Kind = string(dto.MessageKindEdit)
			in.ReplyToID = keyID(node["key"])
			in.Text = editedText(obj(node["editedMessage"]))
			return true
		case 0: // REVOKE
			in.Kind, in.Text = string(dto.MessageKindRevoke), "(message deleted)"
			in.ReplyToID = keyID(node["key"])
			return true
		}
		return false
	}
	if obj(msg["albumMessage"]) != nil {
		// The header of an album only counts what is coming; its items are messages of their own, and
		// each is delivered as it arrives (CID-29).
		return false
	}
	if node := obj(msg["contactMessage"]); node != nil {
		in.Kind, in.Text = string(dto.MessageKindContact), "Contact: "+str(node["displayName"])
		return true
	}
	if node := obj(msg["locationMessage"]); node != nil {
		in.Kind, in.Text = string(dto.MessageKindLocation), "Location"
		if name := str(node["name"]); name != "" {
			in.Text += ": " + name
		}
		return true
	}
	for _, key := range []string{"pollCreationMessage", "pollCreationMessageV3"} {
		if node := obj(msg[key]); node != nil {
			in.Kind, in.Text = string(dto.MessageKindPoll), "Poll: "+str(node["name"])
			return true
		}
	}
	in.Kind, in.Text = string(dto.MessageKindUnknown), "(unsupported message)"
	return true
}

// wrapping is what unwrap found around the content of a message.
type wrapping struct {
	// child tells that one of the envelopes was an associatedChildMessage.
	child bool
	// assoc is the type of association the message or an envelope around its content states (the
	// outermost one that states any), or associationNone.
	assoc int
}

// unwrap removes up to maxEnvelopes envelope levels and tells what it found around the content.
func unwrap(msg map[string]json.RawMessage) (map[string]json.RawMessage, wrapping) {
	w := wrapping{assoc: associationNone}
	for range maxEnvelopes {
		w.state(msg)
		inner := map[string]json.RawMessage(nil)
		for _, env := range envelopes {
			if node := obj(msg[env]); node != nil {
				if m := obj(node["message"]); m != nil {
					inner = m
					w.child = w.child || env == "associatedChildMessage"
					break
				}
			}
		}
		if inner == nil {
			return msg, w
		}
		msg = inner
	}
	w.state(msg)
	return msg, w
}

// state notes the association a level of the message states, unless an outer level stated one.
func (w *wrapping) state(msg map[string]json.RawMessage) {
	if w.assoc != associationNone {
		return
	}
	raw := obj(obj(msg["messageContextInfo"])["messageAssociation"])["associationType"]
	if len(raw) == 0 {
		return
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		w.assoc = n
		return
	}
	w.assoc = associationOther
	if n, ok := associationNames[str(raw)]; ok {
		w.assoc = n
	}
}

// editedText is the new text of an edit: the edited message may itself be wrapped.
func editedText(edited map[string]json.RawMessage) string {
	if edited == nil {
		return ""
	}
	if m := obj(edited["message"]); m != nil {
		edited = m
	}
	edited, _ = unwrap(edited)
	if s := str(edited["conversation"]); s != "" {
		return s
	}
	if node := obj(edited["extendedTextMessage"]); node != nil {
		return str(node["text"])
	}
	for _, mk := range mediaKinds {
		if node := obj(edited[mk.node]); node != nil {
			return str(node["caption"])
		}
	}
	return ""
}

// quotedID returns the quoted message id from the contextInfo of any content node.
func quotedID(msg map[string]json.RawMessage) string {
	for _, raw := range msg {
		node := obj(raw)
		if node == nil {
			continue
		}
		ci := obj(node["contextInfo"])
		if ci == nil {
			continue
		}
		for _, k := range []string{"stanzaID", "stanzaId"} {
			if id := str(ci[k]); id != "" {
				return id
			}
		}
	}
	return ""
}

// mentionedJIDs returns the jids the message mentions, from the contextInfo of its content
// node (CID-12).
func mentionedJIDs(msg map[string]json.RawMessage) []string {
	for _, raw := range msg {
		node := obj(raw)
		if node == nil {
			continue
		}
		ci := obj(node["contextInfo"])
		if ci == nil {
			continue
		}
		var jids []string
		if json.Unmarshal(ci["mentionedJid"], &jids) == nil && len(jids) > 0 {
			return jids
		}
	}
	return nil
}

func keyID(raw json.RawMessage) string {
	key := obj(raw)
	if id := str(key["ID"]); id != "" {
		return id
	}
	return str(key["id"])
}

// protocolType reads the protocol message type, numeric or by enum name; -1 when unknown.
func protocolType(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	switch str(raw) {
	case "REVOKE":
		return 0
	case "MESSAGE_EDIT":
		return 14
	}
	return -1
}

// BareJID removes the device suffix: "5511999990000:12@s.whatsapp.net" →
// "5511999990000@s.whatsapp.net".
func BareJID(jid string) string {
	at := strings.IndexByte(jid, '@')
	if at < 0 {
		return jid
	}
	user, server := jid[:at], jid[at:]
	if i := strings.IndexByte(user, ':'); i >= 0 {
		user = user[:i]
	}
	return user + server
}

// phoneNumber is the DM peer's phone number: the user part of the first phone-number JID
// among the chat and its alternates, else of the chat JID.
func phoneNumber(chat string, info Info) string {
	for _, jid := range []string{chat, BareJID(info.ChatAlt), BareJID(info.Sender), BareJID(info.SenderAlt)} {
		if user, ok := strings.CutSuffix(jid, "@s.whatsapp.net"); ok && user != "" {
			return user
		}
	}
	user, _, _ := strings.Cut(chat, "@")
	return user
}

// maskNumber shows only the last 4 digits of a phone number, as "+•••• 0000", so a DM title
// does not spread the full number to every room viewer and agent (CTX-06).
func maskNumber(n string) string {
	if len(n) > 4 {
		n = n[len(n)-4:]
	}
	return "+•••• " + n
}

// ChatID is the Partyline chat id of a JID on an instance.
func ChatID(instance, jid string) string { return instance + "|" + jid }

// SplitChatID splits a chat id into instance and JID.
func SplitChatID(id string) (instance, jid string, ok bool) {
	instance, jid, ok = strings.Cut(id, "|")
	return instance, jid, ok && instance != "" && jid != ""
}

func stripDataURL(s string) string {
	if strings.HasPrefix(s, "data:") {
		if i := strings.Index(s, ";base64,"); i >= 0 {
			return s[i+len(";base64,"):]
		}
	}
	return s
}

func extension(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	switch strings.TrimSpace(base) {
	case "image/jpeg":
		return ".jpg"
	case "audio/ogg":
		return ".ogg"
	}
	if exts, _ := mime.ExtensionsByType(strings.TrimSpace(base)); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

func obj(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func str(raw json.RawMessage) string {
	var s string
	if len(raw) > 0 && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// num reads a number sent as a JSON number or string (protojson encodes 64-bit ints as strings).
func num(raw json.RawMessage) int64 {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	n, _ = strconv.ParseInt(str(raw), 10, 64)
	return n
}
