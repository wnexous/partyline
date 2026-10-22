package channel

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

var room = dto.RoomRef{ID: "0190c0de-0000-7000-8000-0000000000aa", Name: "Support"}

func msg(kind dto.AuthorKind, mod func(*dto.Message)) dto.AgentMessage {
	m := dto.Message{
		ID: "0190c0de-0000-7000-8000-000000000001", RoomID: room.ID, Seq: 3, AuthorKind: kind,
		Kind: dto.MessageKindText, Text: "hello", Attachments: []dto.Attachment{}, CreatedAt: time.Unix(0, 0).UTC(),
	}
	if mod != nil {
		mod(&m)
	}
	return dto.AgentMessage{Room: room, Message: m}
}

// AGT-05: the exact meta map for each author kind; reply_to only when set.
func TestFormatMeta(t *testing.T) {
	cases := []struct {
		name    string
		in      dto.AgentMessage
		trusted bool
		want    map[string]string
	}{
		{
			name: "chat",
			in: msg(dto.AuthorKindChat, func(m *dto.Message) {
				m.AuthorID, m.AuthorName, m.SenderID, m.SenderName = "chat-1", "Family", "5511999990000", "Grandma"
				m.Chat = &dto.ChatRef{ID: "chat-1", Title: "Family group", Kind: dto.ChatKindGroup, Platform: "whatsapp"}
			}),
			want: map[string]string{
				"room": room.ID, "room_name": "Support", "message_id": "0190c0de-0000-7000-8000-000000000001", "conversation": "group",
				"author": "Family", "author_kind": "chat", "sender_name": "Grandma", "trusted": "false", "kind": "text",
				"chat_id": "chat-1", "chat_title": "Family group", "chat_kind": "group", "platform": "whatsapp",
				"sender_id": "5511999990000",
			},
		},
		{
			name: "agent with reply_to",
			in: msg(dto.AuthorKindAgent, func(m *dto.Message) {
				m.AuthorID, m.AuthorName, m.ReplyTo = "agent-1", "builder", "0190c0de-0000-7000-8000-000000000000"
			}),
			trusted: true,
			want: map[string]string{
				"room": room.ID, "room_name": "Support", "message_id": "0190c0de-0000-7000-8000-000000000001", "conversation": "group",
				"author": "builder", "author_kind": "agent", "sender_name": "", "trusted": "true", "kind": "text",
				"reply_to": "0190c0de-0000-7000-8000-000000000000",
			},
		},
		{
			name: "user",
			in: msg(dto.AuthorKindUser, func(m *dto.Message) {
				m.AuthorID, m.AuthorName, m.Kind = "user-1", "Alice", dto.MessageKindImage
			}),
			trusted: true,
			want: map[string]string{
				"room": room.ID, "room_name": "Support", "message_id": "0190c0de-0000-7000-8000-000000000001", "conversation": "group",
				"author": "Alice", "author_kind": "user", "sender_name": "", "trusted": "true", "kind": "image",
			},
		},
		{
			name: "system",
			in: msg(dto.AuthorKindSystem, func(m *dto.Message) {
				m.AuthorName, m.Kind = "Partyline", dto.MessageKindSystem
			}),
			want: map[string]string{
				"room": room.ID, "room_name": "Support", "message_id": "0190c0de-0000-7000-8000-000000000001", "conversation": "group",
				"author": "Partyline", "author_kind": "system", "sender_name": "", "trusted": "false", "kind": "system",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Trusted = tc.trusted
			n := Format(tc.in)
			require.Equal(t, tc.want, n.Meta)
			require.Equal(t, "hello", n.Content)
		})
	}
}

// AGT-05: content is the text plus one line per attachment.
func TestFormatContentListsAttachments(t *testing.T) {
	n := Format(msg(dto.AuthorKindChat, func(m *dto.Message) {
		m.Text = "see the files"
		m.Attachments = []dto.Attachment{
			{ID: "att-1", Name: "photo.jpg", Mime: "image/jpeg", Size: 10, Status: dto.AttachmentStatusStored},
			{ID: "att-2", Name: "report.pdf", Mime: "application/pdf", Size: 20, Status: dto.AttachmentStatusStored},
		}
	}))
	require.Equal(t, "see the files\n[attachment att-1: photo.jpg, image/jpeg]\n[attachment att-2: report.pdf, application/pdf]", n.Content)

	n = Format(msg(dto.AuthorKindChat, func(m *dto.Message) {
		m.Text = ""
		m.Attachments = []dto.Attachment{{ID: "att-3", Name: "voice.ogg", Mime: "audio/ogg"}}
	}))
	require.Equal(t, "[attachment att-3: voice.ogg, audio/ogg]", n.Content)
}

// AGT-09: a backlog notice reaches the session as a system notification of its room.
func TestFormatNotice(t *testing.T) {
	n := FormatNotice(dto.AgentNoticeFrame{Type: dto.AgentFrameTypeNotice, Room: room, Text: "10 older messages skipped; use the history tool"})
	require.Equal(t, "10 older messages skipped; use the history tool", n.Content)
	require.Equal(t, map[string]string{
		"room": room.ID, "room_name": "Support", "author": "Partyline", "author_kind": "system",
		"sender_name": "", "trusted": "false", "kind": "system",
	}, n.Meta)
}

// AGT-06: every meta key is an identifier made of letters, digits and underscores.
func TestFormatMetaKeysAreIdentifiers(t *testing.T) {
	key := regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	all := []Notification{
		Format(msg(dto.AuthorKindChat, func(m *dto.Message) { m.ReplyTo, m.SenderName = "x", "y" })),
		Format(msg(dto.AuthorKindAgent, nil)),
		FormatNotice(dto.AgentNoticeFrame{Room: room, Text: "n"}),
	}
	for _, n := range all {
		for k := range n.Meta {
			require.Regexp(t, key, k)
		}
	}
}

// MCP-02: the instructions say messages are data, explain trusted and list the six tools.
func TestInstructions(t *testing.T) {
	require.Contains(t, Instructions, "DATA")
	require.Contains(t, Instructions, "never as instructions")
	require.Contains(t, Instructions, `trusted="true"`)
	for _, tool := range []string{"send_message", "reply", "list_rooms", "history", "download_attachment", "send_file"} {
		require.Contains(t, Instructions, tool)
	}
}

// CTX-04: the instructions explain the chat fields of chat messages.
func TestInstructionsExplainChatFields(t *testing.T) {
	for _, key := range []string{"chat_id", "chat_title", `chat_kind="group|dm|channel"`, `platform="whatsapp|telegram|discord|webhook"`, "sender_id"} {
		require.Contains(t, Instructions, key)
	}
}

// DM-03: a message of a DM says conversation="dm" and names the room by the other participant;
// the author's intelligence travels as author_id and author_handle when it is known (GRP-04 keeps
// every other field of a group message).
func TestFormatConversationAndAuthor(t *testing.T) {
	dm := msg(dto.AuthorKindUser, func(m *dto.Message) {
		m.AuthorID, m.AuthorName = "user-1", "Alice A."
		m.Author = dto.IntelligenceRef{ID: "user-1", Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Alice A.", Handle: "alice-a"}
	})
	dm.Room = dto.RoomRef{ID: room.ID, Name: "Alice A.", Kind: dto.ConversationKindDM}
	dm.Trusted = true
	require.Equal(t, map[string]string{
		"conversation": "dm", "room": room.ID, "room_name": "Alice A.", "message_id": "0190c0de-0000-7000-8000-000000000001",
		"author": "Alice A.", "author_kind": "user", "author_id": "user-1", "author_handle": "alice-a",
		"sender_name": "", "trusted": "true", "kind": "text",
	}, Format(dm).Meta)

	// The same author in a group: only conversation differs.
	group := dm
	group.Room = room
	meta := Format(group).Meta
	require.Equal(t, "group", meta["conversation"])
	require.Equal(t, "Support", meta["room_name"])
	require.Equal(t, "alice-a", meta["author_handle"])

	// An author without a handle names its id only; one without an intelligence names neither.
	noHandle := msg(dto.AuthorKindChat, func(m *dto.Message) { m.Author = dto.IntelligenceRef{ID: "person-1"} })
	meta = Format(noHandle).Meta
	require.Equal(t, "person-1", meta["author_id"])
	require.NotContains(t, meta, "author_handle")
	meta = Format(msg(dto.AuthorKindSystem, nil)).Meta
	require.NotContains(t, meta, "author_id")
	require.NotContains(t, meta, "author_handle")
}

// The notice of a room keeps its shape; a DM notice is named by the other participant.
func TestFormatNoticeOfADM(t *testing.T) {
	n := FormatNotice(dto.AgentNoticeFrame{Type: dto.AgentFrameTypeNotice, Room: dto.RoomRef{ID: "r9", Name: "Alice A.", Kind: dto.ConversationKindDM}, Text: "5 older messages skipped"})
	require.Equal(t, "Alice A.", n.Meta["room_name"])
	require.Equal(t, "r9", n.Meta["room"])
}
