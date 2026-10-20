package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel/mcp"
)

// toolTimeout bounds one tool call, file transfers included.
const toolTimeout = 2 * time.Minute

// tools implements the room tools (TOOL-01..09) over the agent REST API.
type tools struct {
	api  *client
	link *link
	// dir is where downloads are saved: <TempDir>/partyline/<agent> (TOOL-06).
	dir string
}

func schema(s string) json.RawMessage { return json.RawMessage(s) }

// register adds the six tools to srv (TOOL-01).
func (t *tools) register(srv *mcp.Server) {
	srv.HandleTool(mcp.ToolDef{
		Name:        "reply",
		Description: "Reply to a room message. The reply goes to the message's room; when the message came from a chat, only that chat receives it.",
		InputSchema: schema(`{"type":"object","properties":{"message_id":{"type":"string","description":"message_id of the message to answer"},"text":{"type":"string","description":"reply text"}},"required":["message_id","text"]}`),
	}, t.guard(t.reply))
	srv.HandleTool(mcp.ToolDef{
		Name:        "send_message",
		Description: "Post a message to a room you belong to, optionally only to some of its chats.",
		InputSchema: schema(`{"type":"object","properties":{"room":{"type":"string","description":"room id or room name"},"text":{"type":"string","description":"message text"},"recipients":{"type":"array","items":{"type":"string"},"description":"optional chat ids (member_id of chat members from list_rooms); empty sends to every chat of the room"}},"required":["room","text"]}`),
	}, t.guard(t.sendMessage))
	srv.HandleTool(mcp.ToolDef{
		Name:        "list_rooms",
		Description: "List the rooms you belong to with their members (agents show whether they are online).",
		InputSchema: schema(`{"type":"object","properties":{}}`),
	}, t.guard(t.listRooms))
	srv.HandleTool(mcp.ToolDef{
		Name:        "history",
		Description: "Read earlier messages of a room, oldest first. Page back with before = the message_id of the oldest message returned.",
		InputSchema: schema(`{"type":"object","properties":{"room":{"type":"string","description":"room id or room name"},"before":{"type":"string","description":"only messages older than this message_id"},"limit":{"type":"integer","minimum":1,"maximum":100,"description":"how many messages (default 20, at most 100)"}},"required":["room"]}`),
	}, t.guard(t.history))
	srv.HandleTool(mcp.ToolDef{
		Name:        "download_attachment",
		Description: "Download a message attachment to a local temporary file and return its absolute path.",
		InputSchema: schema(`{"type":"object","properties":{"attachment_id":{"type":"string","description":"id from an [attachment <id>: ...] line"}},"required":["attachment_id"]}`),
	}, t.guard(t.downloadAttachment))
	srv.HandleTool(mcp.ToolDef{
		Name:        "send_file",
		Description: "Upload a local file to a room, with an optional caption and reply_to.",
		InputSchema: schema(`{"type":"object","properties":{"room":{"type":"string","description":"room id or room name"},"path":{"type":"string","description":"local file path"},"caption":{"type":"string","description":"optional text sent with the file"},"reply_to":{"type":"string","description":"optional message_id this file answers"}},"required":["room","path"]}`),
	}, t.guard(t.sendFile))
}

type toolFunc func(ctx context.Context, args json.RawMessage) (string, error)

// guard reports the link's state (invalid token, replaced, deleted, unreachable) instead of
// calling the server, bounds the call and turns errors into isError results.
func (t *tools) guard(fn toolFunc) mcp.ToolFunc {
	return func(ctx context.Context, args json.RawMessage) (mcp.ToolResult, error) {
		if err := t.link.Err(); err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		ctx, cancel := context.WithTimeout(ctx, toolTimeout)
		defer cancel()
		text, err := fn(ctx, args)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		return mcp.TextResult(text), nil
	}
}

func decode(args json.RawMessage, v any) error {
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func required(pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.TrimSpace(pairs[i+1]) == "" {
			return fmt.Errorf("%s is required", pairs[i])
		}
	}
	return nil
}

func roomPath(room string, suffix string) string {
	return "agent/rooms/" + url.PathEscape(room) + suffix
}

func sent(m dto.Message) string { return "sent (message_id " + m.ID + ")" }

func (t *tools) reply(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		MessageID string `json:"message_id"`
		Text      string `json:"text"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if err := required("message_id", a.MessageID, "text", a.Text); err != nil {
		return "", err
	}
	var m dto.Message
	if err := t.api.json(ctx, http.MethodPost, "agent/messages/"+url.PathEscape(a.MessageID)+"/reply", nil, dto.ReplyRequest{Text: a.Text}, &m); err != nil {
		return "", err
	}
	return sent(m), nil
}

func (t *tools) sendMessage(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Room       string   `json:"room"`
		Text       string   `json:"text"`
		Recipients []string `json:"recipients"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if err := required("room", a.Room, "text", a.Text); err != nil {
		return "", err
	}
	var m dto.Message
	if err := t.api.json(ctx, http.MethodPost, roomPath(a.Room, "/messages"), nil, dto.PostMessageRequest{Text: a.Text, Recipients: a.Recipients}, &m); err != nil {
		return "", err
	}
	return sent(m), nil
}

// roomListing is one room in the list_rooms answer (TOOL-09).
type roomListing struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Members     []memberListing `json:"members"`
}

type memberListing struct {
	Kind     dto.MemberKind `json:"kind"`
	MemberID string         `json:"member_id"`
	Name     string         `json:"name"`
	Owner    string         `json:"owner,omitempty"`
	Online   *bool          `json:"online,omitempty"`
}

func (t *tools) listRooms(ctx context.Context, _ json.RawMessage) (string, error) {
	var rooms dto.List[dto.AgentRoom]
	if err := t.api.json(ctx, http.MethodGet, "agent/rooms", nil, nil, &rooms); err != nil {
		return "", err
	}
	out := make([]roomListing, 0, len(rooms.Items))
	for _, r := range rooms.Items {
		l := roomListing{ID: r.ID, Name: r.Name, Description: r.Description, Members: make([]memberListing, 0, len(r.Members))}
		for _, m := range r.Members {
			l.Members = append(l.Members, memberListing{Kind: m.Kind, MemberID: m.MemberID, Name: m.Name, Owner: m.Owner, Online: m.Online})
		}
		out = append(out, l)
	}
	return marshal(map[string]any{"rooms": out})
}

func (t *tools) history(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Room   string `json:"room"`
		Before string `json:"before"`
		Limit  int    `json:"limit"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if err := required("room", a.Room); err != nil {
		return "", err
	}
	q := url.Values{}
	if a.Before != "" {
		q.Set("before", a.Before)
	}
	if a.Limit > 0 {
		q.Set("limit", strconv.Itoa(a.Limit)) // the server clamps to 100 (TOOL-05)
	}
	var page dto.Page[dto.AgentMessage]
	if err := t.api.json(ctx, http.MethodGet, roomPath(a.Room, "/messages"), q, nil, &page); err != nil {
		return "", err
	}
	// The server answers newest first; the tool reads like a transcript.
	slices.Reverse(page.Items)
	msgs := make([]map[string]string, 0, len(page.Items))
	for _, m := range page.Items {
		n := Format(m)
		entry := make(map[string]string, len(n.Meta)+1)
		for k, v := range n.Meta {
			entry[k] = v
		}
		entry["content"] = n.Content
		msgs = append(msgs, entry)
	}
	return marshal(map[string]any{"messages": msgs, "has_more": page.HasMore})
}

func (t *tools) downloadAttachment(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		AttachmentID string `json:"attachment_id"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if err := required("attachment_id", a.AttachmentID); err != nil {
		return "", err
	}
	resp, err := t.api.do(ctx, http.MethodGet, "agent/attachments/"+url.PathEscape(a.AttachmentID), nil, "", nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	name := "file"
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		name = params["filename"]
	}
	if err := privateDir(t.dir); err != nil {
		return "", err
	}
	path := filepath.Join(t.dir, safeName(a.AttachmentID)+"-"+safeName(name))
	// A fresh file (O_EXCL, 0600) renamed into place: whatever sits at path, a planted
	// symlink included, is replaced and never written through.
	f, err := os.CreateTemp(t.dir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("save attachment: %w", err)
	}
	tmp := f.Name()
	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("save attachment: %w", err)
	}
	return path, nil
}

// privateDir makes dir (<tmp>/partyline/<agent>) and its parent with mode 0700 when missing
// and refuses either one when it is a symlink, not a directory, or open to other users
// (unix: owned by another user or with group/other permission bits). On a shared /tmp another
// user could otherwise pre-create the directory to read or redirect downloads (TOOL-06).
func privateDir(dir string) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(filepath.Dir(parent), 0o700); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}
	for _, d := range []string{parent, dir} {
		if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create download directory: %w", err)
		}
		info, err := os.Lstat(d)
		if err != nil {
			return fmt.Errorf("check download directory: %w", err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("download directory %s is not a plain directory; remove it or set TMPDIR", d)
		}
		if err := checkPrivate(d, info); err != nil {
			return err
		}
	}
	return nil
}

// safeName keeps a file name component: its base name with path separators and control
// characters replaced.
func safeName(s string) string {
	s = filepath.Base(strings.ReplaceAll(s, "\\", "/"))
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' {
			return '_'
		}
		return r
	}, s)
	if s == "." || s == ".." || s == "" {
		return "file"
	}
	return s
}

func (t *tools) sendFile(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Room    string `json:"room"`
		Path    string `json:"path"`
		Caption string `json:"caption"`
		ReplyTo string `json:"reply_to"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if err := required("room", a.Room, "path", a.Path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(a.Path) // #nosec G304 -- the session asks to share this local file
	if err != nil {
		return "", fmt.Errorf("read %s: %w", a.Path, errors.Unwrap(err))
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormField(dto.MultipartMessageField)
	if err != nil {
		return "", err
	}
	if err := json.NewEncoder(part).Encode(dto.PostMessageRequest{Text: a.Caption, ReplyTo: a.ReplyTo}); err != nil {
		return "", err
	}
	name := filepath.Base(a.Path)
	ct := mime.TypeByExtension(filepath.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": dto.MultipartFileField, "filename": name}))
	h.Set("Content-Type", ct)
	fw, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	resp, err := t.api.do(ctx, http.MethodPost, roomPath(a.Room, "/messages"), nil, mw.FormDataContentType(), &body)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var m dto.Message
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return "", fmt.Errorf("partyline: unexpected answer: %w", err)
	}
	return sent(m), nil
}

func marshal(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
