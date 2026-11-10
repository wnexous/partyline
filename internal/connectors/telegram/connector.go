// Package telegram is the Telegram connector: a bot that long-polls the Bot API.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
)

// KindName is the connector kind name.
const KindName = "telegram"

// Limits of the Bot API.
const (
	// MaxText is the longest sendMessage text in characters (TG-02).
	MaxText = 4096
	// MaxPhoto is the largest file sent with sendPhoto (TG-03).
	MaxPhoto = 10 << 20
	// MaxFile is the largest file a bot sends through the public Bot API (LF-05); LocalMaxFile through
	// a self-hosted Bot API server (api_base changed).
	MaxFile      = 50 << 20
	LocalMaxFile = 2000 << 20
	// DefaultAPIBase is the public Bot API server.
	DefaultAPIBase = "https://api.telegram.org"

	pollTimeout    = 30 // seconds, getUpdates long polling
	requestTimeout = 60 * time.Second
)

// ErrUnauthorized means the Bot API rejected the token.
var ErrUnauthorized = errors.New("unauthorized")

// Kind returns the Telegram connector kind.
func Kind() connectors.Kind {
	return connectors.Kind{
		Name: KindName, Label: "Telegram bot", CanReact: true, CanSendFiles: true, MaxFileBytes: MaxFile, FileLimit: fileLimit,
		Fields: []connectors.Field{
			{Name: "bot_token", Label: "Bot token", Type: dto.FieldTypeSecret, Required: true, Help: "From @BotFather"},
			{Name: "api_base", Label: "Bot API URL", Type: dto.FieldTypeURL, Default: DefaultAPIBase, Endpoint: true,
				Help: "Change only for a self-hosted Bot API server"},
		},
		Address: address,
		New: func(cfg connectors.Config, d connectors.Deps) (connectors.Connector, error) {
			return New(cfg, d)
		},
	}
}

// fileLimit is the largest file a bot sends through the Bot API server of cfg (LF-05).
func fileLimit(cfg connectors.Config) int64 {
	if base := strings.TrimRight(cfg.String("api_base"), "/"); base != "" && base != DefaultAPIBase {
		return LocalMaxFile
	}
	return MaxFile
}

// Connector is one Telegram bot.
type Connector struct {
	base      string
	token     string
	userAgent string
	log       *slog.Logger
	client    *http.Client // Bot API calls (JSON)
	media     *http.Client // file downloads, streamed to disk; each bounded by its stall (LF-04)
	// pollTimeout is the getUpdates timeout in seconds.
	pollTimeout int

	mu     sync.Mutex
	offset int64
	selfID int64
	// selfUsername is the bot's @username from getMe; empty when the bot has none.
	selfUsername string
}

var _ connectors.Connector = (*Connector)(nil)

// New returns a connector for a validated config.
func New(cfg connectors.Config, d connectors.Deps) (*Connector, error) {
	base := strings.TrimRight(cfg.String("api_base"), "/")
	if base == "" {
		base = DefaultAPIBase
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Connector{
		base: base, token: cfg.String("bot_token"), userAgent: d.UserAgent, log: log,
		client: d.JSONClient(0), media: d.MediaClient(0), pollTimeout: pollTimeout,
	}, nil
}

// Bot API types (only the fields Partyline uses).
type (
	user struct {
		ID        int64  `json:"id"`
		IsBot     bool   `json:"is_bot"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	}
	chat struct {
		ID        int64  `json:"id"`
		Type      string `json:"type"`
		Title     string `json:"title"`
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	file struct {
		FileID   string `json:"file_id"`
		FileSize int64  `json:"file_size"`
		FileName string `json:"file_name"`
		MimeType string `json:"mime_type"`
	}
	sticker struct {
		file
		IsAnimated bool `json:"is_animated"`
		IsVideo    bool `json:"is_video"`
	}
	// entity is a formatting or mention entity of a message text or caption; offsets and
	// lengths count UTF-16 code units.
	entity struct {
		Type   string `json:"type"`
		Offset int    `json:"offset"`
		Length int    `json:"length"`
		User   *user  `json:"user"`
	}
	message struct {
		MessageID      int64     `json:"message_id"`
		From           *user     `json:"from"`
		SenderChat     *chat     `json:"sender_chat"`
		Chat           chat      `json:"chat"`
		Date           int64     `json:"date"`
		Text           string    `json:"text"`
		Caption        string    `json:"caption"`
		ReplyToMessage *message  `json:"reply_to_message"`
		Photo          []file    `json:"photo"`
		Document       *file     `json:"document"`
		Video          *file     `json:"video"`
		Audio          *file     `json:"audio"`
		Voice          *file     `json:"voice"`
		Sticker        *sticker  `json:"sticker"`
		Location       *struct{} `json:"location"`
		Contact        *struct {
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		} `json:"contact"`
		Poll *struct {
			Question string `json:"question"`
		} `json:"poll"`
		Entities          []entity `json:"entities"`
		CaptionEntities   []entity `json:"caption_entities"`
		MigrateToChatID   int64    `json:"migrate_to_chat_id"`
		MigrateFromChatID int64    `json:"migrate_from_chat_id"`
	}
	update struct {
		UpdateID    int64    `json:"update_id"`
		Message     *message `json:"message"`
		ChannelPost *message `json:"channel_post"`
	}
)

// Run reads the bot identity, then long-polls getUpdates from the last offset + 1 and
// hands message and channel_post updates to h (TG-01). It returns when ctx ends or a
// call fails.
func (c *Connector) Run(ctx context.Context, h connectors.Handler) error {
	var me user
	if err := c.call(ctx, "getMe", nil, &me); err != nil {
		return ctxErr(ctx, err)
	}
	c.mu.Lock()
	c.selfID, c.selfUsername = me.ID, me.Username
	c.mu.Unlock()
	if me.Username != "" {
		h.Account("@" + me.Username) // ACC-07
	}
	h.Status(connectors.StatusConnected, "")
	for {
		c.mu.Lock()
		offset := c.offset
		c.mu.Unlock()
		var updates []update
		err := c.call(ctx, "getUpdates", map[string]any{
			"offset": offset, "timeout": c.pollTimeout, "allowed_updates": []string{"message", "channel_post"},
		}, &updates)
		if err != nil {
			return ctxErr(ctx, err)
		}
		for _, u := range updates {
			c.mu.Lock()
			c.offset = max(c.offset, u.UpdateID+1)
			c.mu.Unlock()
			msg := u.Message
			if msg == nil {
				msg = u.ChannelPost
			}
			if msg != nil {
				c.handle(ctx, h, msg)
			}
		}
	}
}

func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// handle turns one message into an inbound message, or a chat migration.
func (c *Connector) handle(ctx context.Context, h connectors.Handler, m *message) {
	chatID := strconv.FormatInt(m.Chat.ID, 10)
	if m.MigrateToChatID != 0 {
		h.MigrateChat(ctx, chatID, strconv.FormatInt(m.MigrateToChatID, 10))
		return
	}
	if m.MigrateFromChatID != 0 {
		return
	}
	in := connectors.Inbound{
		ChatID: chatID, ChatTitle: chatTitle(m.Chat), ChatKind: chatKind(m.Chat.Type),
		MessageID: strconv.FormatInt(m.MessageID, 10), Kind: string(dto.MessageKindText), Text: m.Text,
		At: time.Unix(m.Date, 0).UTC(),
	}
	if in.Text == "" {
		in.Text = m.Caption
	}
	switch {
	case m.From != nil:
		in.SenderID = strconv.FormatInt(m.From.ID, 10)
		in.SenderName = strings.TrimSpace(m.From.FirstName + " " + m.From.LastName)
		c.mu.Lock()
		in.FromSelf = m.From.ID == c.selfID
		c.mu.Unlock()
	case m.SenderChat != nil:
		in.SenderID, in.SenderName = strconv.FormatInt(m.SenderChat.ID, 10), chatTitle(*m.SenderChat)
	default:
		in.SenderID, in.SenderName = chatID, in.ChatTitle
	}
	if m.ReplyToMessage != nil {
		in.ReplyToID = strconv.FormatInt(m.ReplyToMessage.MessageID, 10)
	}
	c.mu.Lock()
	selfID, selfUsername := c.selfID, c.selfUsername
	c.mu.Unlock()
	in.MentionsAccount = mentions(m.Text, m.Entities, selfID, selfUsername) ||
		mentions(m.Caption, m.CaptionEntities, selfID, selfUsername)
	c.content(&in, m)
	h.Inbound(ctx, in)
}

// mentions tells whether the entities mention the bot (CID-12): a "mention" of its @username
// or a "text_mention" of its user id.
func mentions(text string, entities []entity, selfID int64, selfUsername string) bool {
	for _, e := range entities {
		switch e.Type {
		case "mention":
			if selfUsername != "" && strings.EqualFold(entityText(text, e), "@"+selfUsername) {
				return true
			}
		case "text_mention":
			if selfID != 0 && e.User != nil && e.User.ID == selfID {
				return true
			}
		}
	}
	return false
}

// entityText is the text an entity covers. Telegram offsets and lengths are UTF-16 code units.
func entityText(text string, e entity) string {
	units := utf16.Encode([]rune(text))
	if e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > len(units) {
		return ""
	}
	return string(utf16.Decode(units[e.Offset : e.Offset+e.Length]))
}

// content sets the kind and media of a message (TG-04: media are fetched with getFile).
func (c *Connector) content(in *connectors.Inbound, m *message) {
	media := func(kind dto.MessageKind, f file, name, mimeType string) {
		in.Kind = string(kind)
		if f.FileName != "" {
			name = f.FileName
		}
		if f.MimeType != "" {
			mimeType = f.MimeType
		}
		in.Media = []connectors.Media{{Name: name, Mime: mimeType, Size: f.FileSize, Open: c.opener(f.FileID)}}
	}
	switch {
	case len(m.Photo) > 0:
		media(dto.MessageKindImage, m.Photo[len(m.Photo)-1], "photo.jpg", "image/jpeg")
	case m.Video != nil:
		media(dto.MessageKindVideo, *m.Video, "video.mp4", "video/mp4")
	case m.Audio != nil:
		media(dto.MessageKindAudio, *m.Audio, "audio.mp3", "audio/mpeg")
	case m.Voice != nil:
		media(dto.MessageKindAudio, *m.Voice, "voice.ogg", "audio/ogg")
	case m.Document != nil:
		media(dto.MessageKindDocument, *m.Document, "document", "application/octet-stream")
	case m.Sticker != nil:
		name, mimeType := "sticker.webp", "image/webp"
		switch {
		case m.Sticker.IsAnimated:
			name, mimeType = "sticker.tgs", "application/x-tgsticker"
		case m.Sticker.IsVideo:
			name, mimeType = "sticker.webm", "video/webm"
		}
		media(dto.MessageKindSticker, m.Sticker.file, name, mimeType)
	case m.Location != nil:
		in.Kind, in.Text = string(dto.MessageKindLocation), "Location"
	case m.Contact != nil:
		in.Kind = string(dto.MessageKindContact)
		in.Text = "Contact: " + strings.TrimSpace(m.Contact.FirstName+" "+m.Contact.LastName)
	case m.Poll != nil:
		in.Kind, in.Text = string(dto.MessageKindPoll), "Poll: "+m.Poll.Question
	}
}

// opener downloads a file through getFile and the file endpoint (TG-04).
func (c *Connector) opener(fileID string) func(ctx context.Context) (io.ReadCloser, error) {
	return func(ctx context.Context) (io.ReadCloser, error) {
		var f struct {
			FilePath string `json:"file_path"`
		}
		if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
			return nil, err
		}
		if f.FilePath == "" {
			return nil, errors.New("getFile: no file path")
		}
		ctx, stall := connectors.WatchStall(ctx, requestTimeout, connectors.StallTimeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/file/bot"+c.token+"/"+f.FilePath, nil)
		if err != nil {
			stall.Stop()
			return nil, errors.New("file: invalid request")
		}
		req.Header.Set("User-Agent", c.userAgent)
		resp, err := c.media.Do(req)
		if err != nil {
			stall.Stop()
			return nil, fmt.Errorf("file download: %w", plainError(err))
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			stall.Stop()
			return nil, fmt.Errorf("file download: status %d", resp.StatusCode)
		}
		return stall.Body(resp.Body), nil
	}
}

func chatTitle(ch chat) string {
	switch {
	case ch.Title != "":
		return ch.Title
	case ch.FirstName != "" || ch.LastName != "":
		return strings.TrimSpace(ch.FirstName + " " + ch.LastName)
	case ch.Username != "":
		return "@" + ch.Username
	}
	return ""
}

func chatKind(t string) string {
	switch t {
	case "private":
		return "dm"
	case "channel":
		return "channel"
	}
	return "group"
}

// Send sends the text with sendMessage in parts of at most 4,096 characters, the first
// replying to ReplyToID (TG-02), then each file with sendPhoto (JPEG, PNG or WebP up to
// 10 MiB) or sendDocument (TG-03). It returns the id of the first sent message.
func (c *Connector) Send(ctx context.Context, m connectors.Outbound) (string, error) {
	var reply map[string]any
	if id, err := strconv.ParseInt(m.ReplyToID, 10, 64); err == nil {
		reply = map[string]any{"message_id": id, "allow_sending_without_reply": true}
	}
	first := ""
	keep := func(id int64) {
		if first == "" {
			first = strconv.FormatInt(id, 10)
		}
	}
	for _, part := range connectors.SplitText(connectors.AuthorLine(m.Signature(), "", ": ", m.Text), MaxText) {
		body := map[string]any{"chat_id": m.ChatID, "text": part}
		if reply != nil {
			body["reply_parameters"] = reply
			reply = nil
		}
		var sent message
		if err := c.call(ctx, "sendMessage", body, &sent); err != nil {
			return "", err
		}
		keep(sent.MessageID)
	}
	for _, f := range m.Files {
		sent, err := c.sendFile(ctx, m.ChatID, f, reply)
		if err != nil {
			return "", err
		}
		reply = nil
		keep(sent.MessageID)
	}
	return first, nil
}

// isPhoto reports whether the file goes with sendPhoto.
func isPhoto(f connectors.OutFile) bool {
	switch f.Mime {
	case "image/jpeg", "image/png", "image/webp":
		return f.Size <= MaxPhoto
	}
	return false
}

// sendFile uploads a file with sendPhoto or sendDocument. The multipart body is streamed from the file,
// with its exact length when the size is known, and never held in memory (LF-04); a request the
// transport sends again reopens the file. The upload gets a second for every 256 KB on top of the
// usual bound.
func (c *Connector) sendFile(ctx context.Context, chatID string, f connectors.OutFile, reply map[string]any) (message, error) {
	method, field := "sendDocument", "document"
	if isPhoto(f) {
		method, field = "sendPhoto", "photo"
	}
	var frame bytes.Buffer
	w := multipart.NewWriter(&frame)
	_ = w.WriteField("chat_id", chatID)
	if reply != nil {
		data, _ := json.Marshal(reply)
		_ = w.WriteField("reply_parameters", string(data))
	}
	if _, err := w.CreateFormFile(field, f.Name); err != nil {
		return message{}, err
	}
	head := frame.Len() // the file's bytes go between its part's header and the closing boundary
	if err := w.Close(); err != nil {
		return message{}, err
	}
	all := frame.Bytes()
	prefix, suffix := all[:head], all[head:]
	open := func() (io.ReadCloser, error) {
		if f.Open == nil {
			return nil, errors.New("open attachment: no content")
		}
		r, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open attachment: %w", err)
		}
		return readCloser{Reader: io.MultiReader(bytes.NewReader(prefix), r, bytes.NewReader(suffix)), Closer: r}, nil
	}
	body, err := open()
	if err != nil {
		return message{}, err
	}
	length := int64(-1)
	if f.Size > 0 {
		length = int64(len(all)) + f.Size
	}
	timeout := requestTimeout + time.Duration(c.pollTimeout)*time.Second + time.Duration(max(f.Size, 0)>>18)*time.Second
	var sent message
	err = c.send(ctx, method, w.FormDataContentType(), body, length, open, timeout, &sent)
	return sent, err
}

// readCloser reads from a reader and closes what it came from.
type readCloser struct {
	io.Reader
	io.Closer
}

// call invokes a Bot API method with a JSON body.
func (c *Connector) call(ctx context.Context, method string, body map[string]any, out any) error {
	data := []byte("{}")
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			return err
		}
	}
	return c.do(ctx, method, "application/json", bytes.NewReader(data), out)
}

// do performs a Bot API request. Errors never carry the URL (which holds the token).
func (c *Connector) do(ctx context.Context, method, contentType string, body io.Reader, out any) error {
	return c.send(ctx, method, contentType, body, -2, nil, requestTimeout+time.Duration(c.pollTimeout)*time.Second, out)
}

// send performs a Bot API request within timeout; length is the body's length (-1 unknown, -2: let the
// request find it) and reopen, when set, opens the body again for a request the transport resends.
func (c *Connector) send(ctx context.Context, method, contentType string, body io.Reader, length int64,
	reopen func() (io.ReadCloser, error), timeout time.Duration, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, body)
	if err != nil {
		if rc, ok := body.(io.Closer); ok {
			_ = rc.Close()
		}
		return errors.New(method + ": invalid request")
	}
	if length > -2 {
		req.ContentLength = length
		req.GetBody = reopen
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, plainError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		if resp.StatusCode == http.StatusUnauthorized {
			return ErrUnauthorized
		}
		return fmt.Errorf("%s: status %d", method, resp.StatusCode)
	}
	if !env.OK {
		if env.ErrorCode == http.StatusUnauthorized || resp.StatusCode == http.StatusUnauthorized {
			return ErrUnauthorized
		}
		// The description is platform text (any text, with a custom api_base): keep it out
		// of statuses and errors.
		return fmt.Errorf("%s: error %d", method, env.ErrorCode)
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("%s: invalid response", method)
		}
	}
	return nil
}

// plainError drops the URL (which holds the token) from transport errors.
func plainError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
