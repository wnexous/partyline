// Package whatsapp is the WhatsApp connector, through an evolution-go server: it receives
// message events on evolution-go's broadcast WebSocket and sends through its REST API.
package whatsapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
)

// maxEnvelopes bounds how many envelope levels are unwrapped; maxUnknownKeys how many protocol
// types an unsupported message names.
const (
	maxEnvelopes   = 5
	maxUnknownKeys = 3
)

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
	// UnknownKeys are the protocol types of an unsupported message: the top-level keys of its
	// content that hold an object, sorted and at most maxUnknownKeys. They are type names, never
	// the content of the message.
	UnknownKeys []string
	// QuotedParticipant is the jid of whoever wrote the message this one quotes, as the platform
	// sent it (contextInfo.participant); the connector matches it against its own jid (QC-03).
	QuotedParticipant string
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
	if slices.Contains(notQuotes, dto.MessageKind(out.Inbound.Kind)) {
		out.Inbound.Quote, out.QuotedParticipant = nil, "" // ReplyToID is what it acts on (QC-01)
	}
	return out, nil
}

// notQuotes are the kinds whose ReplyToID names the message they act on, not one they quote.
var notQuotes = []dto.MessageKind{dto.MessageKindReaction, dto.MessageKindEdit, dto.MessageKindRevoke}

// content fills kind, text, reply and media from the (possibly wrapped) message; it
// reports false for protocol messages other than edits and revokes (key shares, history
// sync notices, ...), which are not chat messages.
func content(p *Parsed, root map[string]json.RawMessage) bool {
	in := &p.Inbound
	inline := stripDataURL(str(root["base64"]))
	msg, wrap := unwrap(root)
	if wrap.child && wrap.assoc != associationNone && wrap.assoc != associationUnknown && wrap.assoc != associationMediaAlbum {
		// The better copy of a photo or video already delivered (the HD upload): delivered with its
		// file, which is the original quality, and named as the copy of the earlier message (HD-01).
		if !media(p, msg, inline) {
			in.Kind, in.Text = string(dto.MessageKindUnknown), "(unsupported message)"
			return true
		}
		in.Text = strings.TrimSpace(hdCopyNote + " " + in.Text)
		if in.ReplyToID == "" {
			in.ReplyToID = wrap.parent
		}
		return true
	}
	in.ReplyToID = quotedID(msg)
	p.MentionedJIDs = mentionedJIDs(msg)
	if in.ReplyToID != "" {
		in.Quote, p.QuotedParticipant = quoteOf(msg, in.ReplyToID)
	}
	if s := str(msg["conversation"]); s != "" {
		in.Kind, in.Text = string(dto.MessageKindText), s
		return true
	}
	if node := obj(msg["extendedTextMessage"]); node != nil {
		in.Kind, in.Text = string(dto.MessageKindText), str(node["text"])
		return true
	}
	if media(p, msg, inline) {
		return true
	}
	if node := obj(msg["reactionMessage"]); node != nil {
		in.Kind, in.Text = string(dto.MessageKindReaction), str(node["text"])
		in.ReplyToID = keyID(node["key"])
		if in.Text == "" {
			in.Text = connectors.ReactionRemoved
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
	if obj(msg["contactMessage"]) != nil {
		in.Kind, in.Text = string(dto.MessageKindContact), contactsText([]json.RawMessage{msg["contactMessage"]})
		return true
	}
	if node := obj(msg["contactsArrayMessage"]); node != nil {
		var list []json.RawMessage
		_ = json.Unmarshal(node["contacts"], &list)
		in.Kind, in.Text = string(dto.MessageKindContact), contactsText(list)
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
	if keys := unknownKeys(msg); len(keys) > 0 {
		p.UnknownKeys = keys
		in.Text = "(unsupported message: " + strings.Join(keys, ", ") + ")"
	}
	return true
}

// unknownKeys names what an unsupported message states: the top-level keys of its content that
// hold an object, except messageContextInfo, which is metadata. They are sorted and bounded, so
// the text of the message stays short and stable.
func unknownKeys(msg map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(msg))
	for key, raw := range msg {
		if key == "messageContextInfo" || obj(raw) == nil {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys[:min(len(keys), maxUnknownKeys)]
}

// wrapping is what unwrap found around the content of a message.
type wrapping struct {
	// child tells that one of the envelopes was an associatedChildMessage.
	child bool
	// assoc is the type of association the message or an envelope around its content states (the
	// outermost one that states any), or associationNone.
	assoc int
	// parent is the platform id of the message the association attaches this one to, when it says.
	parent string
}

// contactsText is the text of a message that shares contacts (CID-30): who they are and the numbers
// of their cards, between parentheses, under the name the sharer saw (else the card's). One contact is
// "Contact: Ana (+5511999990000)"; several are "Contacts: Ana (+5511999990000); Bruno (...)", the
// first maxContacts of them and how many more there were. A contact without a number is its name; a
// message that shares none says so. Each item is a contactMessage node (displayName and vcard).
func contactsText(contacts []json.RawMessage) string {
	if len(contacts) == 0 {
		return "Contacts: (none)"
	}
	var parts []string
	for _, raw := range contacts[:min(len(contacts), maxContacts)] {
		c := obj(raw)
		card := parseVCard(str(c["vcard"]))
		name := ellipsize(oneLine(str(c["displayName"])), maxContactField)
		if name == "" {
			name = card.Name
		}
		if name == "" {
			name = "(no name)"
		}
		if len(card.Numbers) > 0 {
			name += " (" + strings.Join(card.Numbers, ", ") + ")"
		}
		parts = append(parts, name)
	}
	text := strings.Join(parts, "; ")
	if more := len(contacts) - maxContacts; more > 0 {
		text += "; and " + strconv.Itoa(more) + " more"
	}
	if len(contacts) == 1 {
		return "Contact: " + text
	}
	return "Contacts: " + text
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
	assoc := obj(obj(msg["messageContextInfo"])["messageAssociation"])
	raw := assoc["associationType"]
	if len(raw) == 0 {
		return
	}
	w.parent = keyID(assoc["parentMessageKey"])
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
	_, id := quoteContext(msg)
	return id
}

// quoteContext returns the contextInfo of the content node that quotes a message, and the quoted
// message's id; nil and "" when no node quotes one.
func quoteContext(msg map[string]json.RawMessage) (map[string]json.RawMessage, string) {
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
				return ci, id
			}
		}
	}
	return nil, ""
}

// quoteOf is the message with this id that msg quotes (QC-01, QC-02): the preview of the copy the
// event carries in contextInfo.quotedMessage, and the jid of its author (contextInfo.participant).
func quoteOf(msg map[string]json.RawMessage, id string) (*connectors.Quote, string) {
	ci, _ := quoteContext(msg)
	q := &connectors.Quote{ID: id}
	if quoted := obj(ci["quotedMessage"]); quoted != nil {
		quoted, _ = unwrap(quoted)
		kind, text, name := quotedContent(quoted)
		q.Preview = connectors.QuotePreview(kind, text, name)
	}
	return q, str(ci["participant"])
}

// quotedContent is the kind, the text (or caption) and the file name of a quoted message; a kind
// Partyline does not know is empty.
func quotedContent(msg map[string]json.RawMessage) (dto.MessageKind, string, string) {
	if s := str(msg["conversation"]); s != "" {
		return dto.MessageKindText, s, ""
	}
	if node := obj(msg["extendedTextMessage"]); node != nil {
		return dto.MessageKindText, str(node["text"]), ""
	}
	for _, mk := range mediaKinds {
		if node := obj(msg[mk.node]); node != nil {
			return mk.kind, str(node["caption"]), str(node["fileName"])
		}
	}
	if node := obj(msg["contactMessage"]); node != nil {
		return dto.MessageKindContact, str(node["displayName"]), ""
	}
	if node := obj(msg["contactsArrayMessage"]); node != nil {
		return dto.MessageKindContact, str(node["displayName"]), ""
	}
	if node := obj(msg["locationMessage"]); node != nil {
		return dto.MessageKindLocation, str(node["name"]), ""
	}
	for _, key := range []string{"pollCreationMessage", "pollCreationMessageV3"} {
		if node := obj(msg[key]); node != nil {
			return dto.MessageKindPoll, str(node["name"]), ""
		}
	}
	return "", "", ""
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

// hdCopyNote starts the text of the better copy of a photo or video already delivered (HD-01).
const hdCopyNote = "(original-quality copy of an earlier message)"

// media fills kind, caption and the file of a photo, video, audio, document or sticker message; it reports
// false when msg holds none.
func media(p *Parsed, msg map[string]json.RawMessage, inline string) bool {
	in := &p.Inbound
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
	return false
}
