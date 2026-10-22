package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// connected starts a channel session for the env, completes the handshake and waits until
// the agent is online.
func (e *chanEnv) connected(o Options) *session {
	e.t.Helper()
	s := startSession(e.t, o)
	s.handshake()
	require.Eventually(e.t, e.online, wait, 10*time.Millisecond)
	return s
}

// messages returns the room's stored messages, newest first.
func (e *chanEnv) messages(roomID string) []dto.Message {
	e.t.Helper()
	return testkit.Decode[dto.Page[dto.Message]](e.alice.Get("/api/v1/rooms/" + roomID + "/messages").RequireStatus(http.StatusOK)).Items
}

// chatMember adds a chat of alice's connector to the room and returns its chat id.
func (e *chanEnv) chatMember(title string) string {
	e.t.Helper()
	conn, chat := store.NewID(), store.NewID()
	ctx := context.Background()
	require.NoError(e.t, e.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateConnector(ctx, db.CreateConnectorParams{ID: conn, OwnerID: e.alice.User.ID, Kind: "telegram", Name: "tg", ConfigJson: "{}", Enabled: true}); err != nil {
			return err
		}
		return q.CreateChat(ctx, db.CreateChatParams{ID: chat, ConnectorID: conn, ExternalID: "-100" + chat[len(chat)-6:], Title: title, Kind: "group"})
	}))
	e.alice.Post("/api/v1/rooms/"+e.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: chat}).RequireStatus(http.StatusCreated)
	return chat
}

func toolArgs(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// TOOL-01, INS-02: tools/list returns exactly the room tools, each with an object input schema.
func TestToolsList(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.options())
	s.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	list := s.response()["result"].(map[string]any)["tools"].([]any)
	required := map[string][]any{
		"send_message":        {"room", "text"},
		"reply":               {"message_id", "text"},
		"list_rooms":          nil,
		"history":             {"room"},
		"download_attachment": {"attachment_id"},
		"send_file":           {"room", "path"},
		"room_info":           {"room"},
		"react":               {"message_id", "emoji"},
	}
	got := map[string]bool{}
	for _, item := range list {
		tool := item.(map[string]any)
		name := tool["name"].(string)
		got[name] = true
		require.NotEmpty(t, tool["description"], name)
		schema := tool["inputSchema"].(map[string]any)
		require.Equal(t, "object", schema["type"], name)
		if want := required[name]; want != nil {
			require.Equal(t, want, schema["required"], name)
		}
	}
	require.Len(t, list, len(required))
	for name := range required {
		require.True(t, got[name], "missing tool %s", name)
	}
}

// ROUTE-10: send_message documents the limit of 10 chats per message, and so do the
// instructions.
func TestToolsDocumentRecipientsCap(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.options())
	s.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	found := false
	for _, item := range s.response()["result"].(map[string]any)["tools"].([]any) {
		tool := item.(map[string]any)
		if tool["name"] != "send_message" {
			continue
		}
		found = true
		chatIDs := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)["chat_ids"].(map[string]any)
		require.Contains(t, chatIDs["description"], "at most 10 chats per message")
	}
	require.True(t, found, "send_message is listed")
	require.Contains(t, Instructions, "at most 10 chats per message")
}

// TOOL-02: reply posts in the message's room with reply_to; a chat-authored original is the
// only recipient, otherwise there are no explicit recipients.
func TestToolReply(t *testing.T) {
	e := newChanEnv(t)
	family := e.chatMember("Family")
	e.chatMember("Work")
	s := e.connected(e.options())

	fromChat, err := e.env.App.Rooms.PostFromChat(context.Background(), e.room.ID, rooms.ChatMessage{
		ChatID: family, SenderID: "5511999990000", SenderName: "Grandma", ExternalID: "ext-1", Text: "dinner?",
	})
	require.NoError(t, err)
	text, isErr := s.call("reply", toolArgs("message_id", fromChat.ID, "text", "at seven"))
	require.False(t, isErr, text)
	stored := e.messages(e.room.ID)[0]
	require.Equal(t, "sent (message_id "+stored.ID+")", text)
	require.Equal(t, "at seven", stored.Text)
	require.Equal(t, fromChat.ID, stored.ReplyTo)
	require.Equal(t, dto.AuthorKindAgent, stored.AuthorKind)
	require.Equal(t, e.agentID, stored.AuthorID)
	require.Equal(t, []string{family}, stored.Recipients)

	fromUser := e.post("status?")
	text, isErr = s.call("reply", toolArgs("message_id", fromUser.ID, "text", "all green"))
	require.False(t, isErr, text)
	stored = e.messages(e.room.ID)[0]
	require.Equal(t, fromUser.ID, stored.ReplyTo)
	require.Empty(t, stored.Recipients)

	text, isErr = s.call("reply", toolArgs("message_id", "unknown", "text", "x"))
	require.True(t, isErr)
	require.Equal(t, "message not found", text)
}

// TOOL-03: send_message posts as the agent to a room by id or name, optionally limited to
// the given chats.
func TestToolSendMessage(t *testing.T) {
	e := newChanEnv(t)
	family := e.chatMember("Family")
	s := e.connected(e.options())

	text, isErr := s.call("send_message", toolArgs("room", e.room.ID, "text", "by id"))
	require.False(t, isErr, text)
	stored := e.messages(e.room.ID)[0]
	require.Equal(t, "by id", stored.Text)
	require.Equal(t, e.agentID, stored.AuthorID)

	text, isErr = s.call("send_message", toolArgs("room", "Support", "text", "by name", "recipients", []string{family}))
	require.False(t, isErr, text)
	stored = e.messages(e.room.ID)[0]
	require.Equal(t, "by name", stored.Text)
	require.Equal(t, []string{family}, stored.Recipients)
}

// TOOL-04: a room the agent is not a member of → isError "not a member of room <room>".
func TestToolNotAMember(t *testing.T) {
	e := newChanEnv(t)
	other := testkit.Decode[dto.Room](e.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Private"}).RequireStatus(http.StatusCreated))
	s := e.connected(e.options())

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"send_message", toolArgs("room", "Private", "text", "x")},
		{"history", toolArgs("room", other.ID)},
		{"room_info", toolArgs("room", "Private")},
	} {
		text, isErr := s.call(tc.tool, tc.args)
		require.True(t, isErr, tc.tool)
		room := tc.args["room"].(string)
		require.Equal(t, "not a member of room "+room, text, tc.tool)
	}
}

// TOOL-05: history returns at most the limit (default 20) messages before an optional
// message id, oldest first, each with the push meta fields.
func TestToolHistory(t *testing.T) {
	e := newChanEnv(t)
	var posted []dto.Message
	for i := 1; i <= 25; i++ {
		posted = append(posted, e.post(fmt.Sprintf("msg %d", i)))
	}
	s := e.connected(e.options())

	var page struct {
		Messages []map[string]string `json:"messages"`
		HasMore  bool                `json:"has_more"`
	}
	text, isErr := s.call("history", toolArgs("room", "Support"))
	require.False(t, isErr, text)
	require.NoError(t, json.Unmarshal([]byte(text), &page))
	require.Len(t, page.Messages, 20)
	require.True(t, page.HasMore)
	require.Equal(t, posted[5].ID, page.Messages[0]["message_id"])
	require.Equal(t, posted[24].ID, page.Messages[19]["message_id"])
	last := page.Messages[19]
	require.Equal(t, map[string]string{
		"room": e.room.ID, "room_name": "Support", "message_id": posted[24].ID, "author": "alice",
		"author_kind": "user", "sender_name": "", "trusted": "true", "kind": "text", "content": "msg 25",
	}, last)

	text, isErr = s.call("history", toolArgs("room", e.room.ID, "before", posted[5].ID, "limit", 3))
	require.False(t, isErr, text)
	require.NoError(t, json.Unmarshal([]byte(text), &page))
	require.Len(t, page.Messages, 3)
	require.Equal(t, posted[2].ID, page.Messages[0]["message_id"])
	require.Equal(t, posted[4].ID, page.Messages[2]["message_id"])
	require.True(t, page.HasMore)

	text, isErr = s.call("history", toolArgs("room", e.room.ID, "limit", 101))
	require.False(t, isErr, text)
	require.NoError(t, json.Unmarshal([]byte(text), &page))
	require.Len(t, page.Messages, 25)
	require.False(t, page.HasMore)
}

// postFile posts a message with one text attachment to the room as alice.
func (e *chanEnv) postFile(name, content string) dto.Message {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormField(dto.MultipartMessageField)
	require.NoError(e.t, err)
	require.NoError(e.t, json.NewEncoder(part).Encode(dto.PostMessageRequest{Text: "the report"}))
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	h.Set("Content-Type", "text/plain")
	fw, err := mw.CreatePart(h)
	require.NoError(e.t, err)
	_, err = fw.Write([]byte(content))
	require.NoError(e.t, err)
	require.NoError(e.t, mw.Close())
	m := testkit.Decode[dto.Message](e.alice.DoRaw(http.MethodPost, "/api/v1/rooms/"+e.room.ID+"/messages", mw.FormDataContentType(), &body).RequireStatus(http.StatusCreated))
	require.Len(e.t, m.Attachments, 1)
	return m
}

// TOOL-06: download_attachment saves the file with mode 0600 under
// <TempDir>/partyline/<agent>/ and returns its absolute path.
func TestToolDownloadAttachment(t *testing.T) {
	e := newChanEnv(t)
	o := e.options()
	s := e.connected(o)
	m := e.postFile("report.txt", "all systems go")

	path, isErr := s.call("download_attachment", toolArgs("attachment_id", m.Attachments[0].ID))
	require.False(t, isErr, path)
	require.True(t, filepath.IsAbs(path), path)
	require.Equal(t, filepath.Join(o.TempDir, "partyline", "builder"), filepath.Dir(path))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "all systems go", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	text, isErr := s.call("download_attachment", toolArgs("attachment_id", "unknown"))
	require.True(t, isErr)
	require.NotEmpty(t, text)
}

// TOOL-07: send_file uploads a local file to the room with optional caption and reply_to.
func TestToolSendFile(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.options())
	question := e.post("send the log")
	local := filepath.Join(t.TempDir(), "build.log")
	require.NoError(t, os.WriteFile(local, []byte("ok\n"), 0o600))

	text, isErr := s.call("send_file", toolArgs("room", "Support", "path", local, "caption", "here it is", "reply_to", question.ID))
	require.False(t, isErr, text)
	stored := e.messages(e.room.ID)[0]
	require.Equal(t, "sent (message_id "+stored.ID+")", text)
	require.Equal(t, "here it is", stored.Text)
	require.Equal(t, question.ID, stored.ReplyTo)
	require.Equal(t, e.agentID, stored.AuthorID)
	require.Len(t, stored.Attachments, 1)
	require.Equal(t, "build.log", stored.Attachments[0].Name)
	got := e.alice.Get("/api/v1/attachments/" + stored.Attachments[0].ID).RequireStatus(http.StatusOK)
	require.Equal(t, "ok\n", string(got.Body))

	text, isErr = s.call("send_file", toolArgs("room", "Support", "path", filepath.Join(t.TempDir(), "missing.txt")))
	require.True(t, isErr)
	require.Contains(t, text, "missing.txt")
}

// TOOL-08: beyond 30 posts per minute the tool reports rate limited.
func TestToolRateLimited(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.options())
	for i := 1; i <= 30; i++ {
		_, isErr := s.call("send_message", toolArgs("room", e.room.ID, "text", fmt.Sprintf("post %d", i)))
		require.False(t, isErr, "post %d", i)
	}
	text, isErr := s.call("send_message", toolArgs("room", e.room.ID, "text", "one too many"))
	require.True(t, isErr)
	require.Contains(t, text, "rate limited")
}

// TOOL-09: list_rooms shows each room of the agent with its members; agents show presence.
func TestToolListRooms(t *testing.T) {
	e := newChanEnv(t)
	helper := e.env.NewAI(e.alice, "helper")
	e.alice.Post("/api/v1/rooms/"+e.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: helper}).RequireStatus(http.StatusCreated)
	family := e.chatMember("Family")
	s := e.connected(e.options())

	text, isErr := s.call("list_rooms", map[string]any{})
	require.False(t, isErr, text)
	var out struct {
		Rooms []roomListing `json:"rooms"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	require.Len(t, out.Rooms, 1)
	r := out.Rooms[0]
	require.Equal(t, e.room.ID, r.ID)
	require.Equal(t, "Support", r.Name)
	byName := map[string]memberListing{}
	for _, m := range r.Members {
		byName[m.Name] = m
	}
	require.Equal(t, dto.MemberKindAgent, byName["helper"].Kind)
	require.NotNil(t, byName["helper"].Online)
	require.False(t, *byName["helper"].Online)
	require.NotNil(t, byName["Builder"].Online)
	require.True(t, *byName["Builder"].Online)
	require.Equal(t, dto.MemberKindChat, byName["Family"].Kind)
	require.Equal(t, family, byName["Family"].MemberID)

	// The other agent comes online.
	conn, resp := e.alice.WithBearer(e.secret).DialAgent("helper")
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	conn.Welcome(wait)
	text, _ = s.call("list_rooms", map[string]any{})
	require.NoError(t, json.Unmarshal([]byte(text), &out))
	for _, m := range out.Rooms[0].Members {
		if m.Name == "helper" {
			require.True(t, *m.Online)
		}
	}
}

// AGT-02: with an invalid token every tool reports "partyline: not connected (invalid token)".
func TestToolsWithInvalidToken(t *testing.T) {
	e := newChanEnv(t)
	o := e.options()
	o.Token = "pl_agent_invalid"
	s := startSession(t, o)
	s.handshake()
	require.Eventually(t, func() bool { return s.stderr.String() != "" }, wait, 10*time.Millisecond)
	for tool, args := range map[string]map[string]any{
		"send_message":        toolArgs("room", "Support", "text", "x"),
		"reply":               toolArgs("message_id", "m", "text", "x"),
		"list_rooms":          {},
		"history":             toolArgs("room", "Support"),
		"download_attachment": toolArgs("attachment_id", "a"),
		"send_file":           toolArgs("room", "Support", "path", "/etc/hostname"),
	} {
		text, isErr := s.call(tool, args)
		require.True(t, isErr, tool)
		require.Equal(t, "partyline: not connected (invalid token)", text, tool)
	}
}

// SES-05: after another session takes the AI over, every tool reports it.
func TestToolsReportTakenOver(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.options())
	f := testkit.Folder{Machine: "testbox", Path: "/work/builder", Harness: "opencode", Takeover: true}
	taker, resp := e.alice.WithBearer(e.secret).DialFolder(f)
	require.NotNil(t, taker, "upgrade refused: %d %s", resp.Status, resp.Body)
	taker.Welcome(wait)

	require.Eventually(t, func() bool { // the channel has seen the takeover
		text, isErr := s.call("list_rooms", map[string]any{})
		return isErr && text == wantTakenOver
	}, wait, 20*time.Millisecond)

	for tool, args := range map[string]map[string]any{
		"send_message": toolArgs("room", "Support", "text", "x"),
		"reply":        toolArgs("message_id", "m", "text", "x"),
		"list_rooms":   {},
		"history":      toolArgs("room", "Support"),
	} {
		text, isErr := s.call(tool, args)
		require.True(t, isErr, tool)
		require.Equal(t, wantTakenOver, text, tool)
	}
	require.Empty(t, e.messages(e.room.ID), "the taken-over channel posted nothing as the AI")
}

// SES-04: while another session holds the AI, tools report that this channel is not connected
// and nothing is posted as the AI.
func TestToolsReportBusy(t *testing.T) {
	e := newChanEnv(t)
	holder, resp := e.alice.WithBearer(e.secret).DialFolder(testkit.Folder{Machine: "testbox", Path: "/work/builder", Harness: "opencode"})
	require.NotNil(t, holder, "upgrade refused: %d %s", resp.Status, resp.Body)
	holder.Welcome(wait)
	o := e.options()
	o.timing.busyRetries = []time.Duration{} // no retry: the state stays busy
	s := startSession(t, o)
	s.handshake()
	require.Eventually(t, func() bool { // the first connection attempt has been refused
		text, isErr := s.call("list_rooms", map[string]any{})
		return isErr && strings.Contains(text, "open in another session")
	}, wait, 20*time.Millisecond)

	for tool, args := range map[string]map[string]any{
		"send_message": toolArgs("room", "Support", "text", "x"),
		"list_rooms":   {},
		"history":      toolArgs("room", "Support"),
	} {
		text, isErr := s.call(tool, args)
		require.True(t, isErr, tool)
		require.Contains(t, text, "open in another session", tool)
	}
	require.Empty(t, e.messages(e.room.ID), "the busy channel posted nothing as the AI")
}

// Design (error handling): an unreachable server → "partyline: server unreachable".
func TestToolsServerUnreachable(t *testing.T) {
	e := newChanEnv(t)
	p := newProxy(t, "127.0.0.1:1")
	p.stop()
	o := e.options()
	o.URL = p.url()
	s := startSession(t, o)
	s.handshake()
	require.Eventually(t, func() bool {
		text, isErr := s.call("list_rooms", map[string]any{})
		return isErr && text == "partyline: server unreachable"
	}, wait, 50*time.Millisecond)
}

// TOOL-06: downloads go to a directory named after the folder's name, so a hostile folder
// name cannot point the download directory outside <TempDir>/partyline/.
func TestToolDownloadDirUsesTheFolderName(t *testing.T) {
	e := newChanEnv(t)
	night := agents.FolderID{Machine: "testbox", Folder: "/work/Night Shift", Harness: "claude"}
	a, err := e.env.App.Agents.ResolveFolder(context.Background(), e.alice.User.ID, night, false)
	require.NoError(t, err)
	e.alice.Post("/api/v1/rooms/"+e.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: a.ID}).RequireStatus(http.StatusCreated)
	o := e.options()
	o.Machine, o.Folder = night.Machine, night.Folder
	s := startSession(t, o)
	s.handshake()
	require.Eventually(t, func() bool { return e.env.App.Gateway.Online(a.ID) }, wait, 10*time.Millisecond)

	m := e.postFile("report.txt", "night report")
	path, isErr := s.call("download_attachment", toolArgs("attachment_id", m.Attachments[0].ID))
	require.False(t, isErr, path)
	require.Equal(t, filepath.Join(o.TempDir, "partyline", "night-shift"), filepath.Dir(path))
}
