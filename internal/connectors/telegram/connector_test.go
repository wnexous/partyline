package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/connectors/safehttp"
)

// Synthetic values.
const (
	botToken  = "123456:SYNTHETIC-token-abcdef"
	botID     = 123456
	userAgent = "partyline/test"
	groupChat = -1001000000001
	wait      = 30 * time.Second
)

// call is a Bot API request received by the fake server.
type call struct {
	Method string
	JSON   map[string]any
	Form   map[string]string
	// FileField is the multipart file field name ("photo" or "document").
	FileField, FileName, FileBody string
	UserAgent                     string
}

// fakeBotAPI serves /bot<token>/<method> and /file/bot<token>/<path>.
type fakeBotAPI struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	updates []map[string]any
	nextID  int
	failing map[string]int // method → HTTP status

	calls  chan call
	queued chan struct{}
}

func newFakeBotAPI(t *testing.T) *fakeBotAPI {
	f := &fakeBotAPI{t: t, calls: make(chan call, 256), queued: make(chan struct{}, 1), failing: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBotAPI) serve(w http.ResponseWriter, r *http.Request) {
	if path, ok := strings.CutPrefix(r.URL.Path, "/file/bot"+botToken+"/"); ok {
		_, _ = io.WriteString(w, "content-of-"+path)
		return
	}
	method, ok := strings.CutPrefix(r.URL.Path, "/bot"+botToken+"/")
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		return
	}
	c := call{Method: method, UserAgent: r.Header.Get("User-Agent"), Form: map[string]string{}}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		require.NoError(f.t, r.ParseMultipartForm(32<<20))
		for k, v := range r.MultipartForm.Value {
			c.Form[k] = v[0]
		}
		for k, fhs := range r.MultipartForm.File {
			fh, _ := fhs[0].Open()
			data, _ := io.ReadAll(fh)
			c.FileField, c.FileName, c.FileBody = k, fhs[0].Filename, string(data)
		}
	} else {
		_ = json.NewDecoder(r.Body).Decode(&c.JSON)
	}
	f.calls <- c
	f.mu.Lock()
	status := f.failing[method]
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":`+strconv.Itoa(status)+`,"description":"Bad Request: chat not found"}`)
		return
	}
	var result any = true
	switch method {
	case "getMe":
		result = map[string]any{"id": botID, "is_bot": true, "first_name": "Test Bot", "username": "test_bot"}
	case "getUpdates":
		result = f.pending(r.Context(), int64(c.JSON["offset"].(float64)))
	case "getFile":
		result = map[string]any{"file_id": c.JSON["file_id"], "file_path": "media/" + c.JSON["file_id"].(string)}
	case "sendMessage", "sendPhoto", "sendDocument":
		f.mu.Lock()
		f.nextID++
		result = map[string]any{"message_id": 1000 + f.nextID, "chat": map[string]any{"id": groupChat}}
		f.mu.Unlock()
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

// pending returns the queued updates with update_id >= offset, waiting briefly for one.
func (f *fakeBotAPI) pending(ctx context.Context, offset int64) []map[string]any {
	deadline := time.After(200 * time.Millisecond)
	for {
		f.mu.Lock()
		out := []map[string]any{}
		for _, u := range f.updates {
			if int64(u["update_id"].(int)) >= offset {
				out = append(out, u)
			}
		}
		f.mu.Unlock()
		if len(out) > 0 {
			return out
		}
		select {
		case <-f.queued:
		case <-deadline:
			return out
		case <-ctx.Done():
			return out
		}
	}
}

func (f *fakeBotAPI) queue(updates ...map[string]any) {
	f.mu.Lock()
	f.updates = append(f.updates, updates...)
	f.mu.Unlock()
	select {
	case f.queued <- struct{}{}:
	default:
	}
}

func (f *fakeBotAPI) fail(method string, status int) {
	f.mu.Lock()
	f.failing[method] = status
	f.mu.Unlock()
}

// next returns the next call of method.
func (f *fakeBotAPI) next(method string) call {
	f.t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case c := <-f.calls:
			if c.Method == method {
				return c
			}
		case <-deadline:
			f.t.Fatalf("no %s call", method)
		}
	}
}

func newConnector(t *testing.T, f *fakeBotAPI) *Connector {
	t.Helper()
	cfg, errs := Kind().Validate(connectors.Config{"bot_token": botToken, "api_base": f.srv.URL})
	require.Empty(t, errs)
	c, err := New(cfg, connectors.Deps{AllowPrivateNetworks: true, ID: "c1", UserAgent: userAgent})
	require.NoError(t, err)
	c.pollTimeout = 0
	return c
}

func start(t *testing.T, c *Connector) *connectortest.Recorder {
	t.Helper()
	rec := connectortest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, rec) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled, "Run returns on cancel")
		case <-time.After(wait):
			t.Error("Run did not return after cancel")
		}
	})
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))
	return rec
}

func groupMessage(updateID, messageID int, extra map[string]any) map[string]any {
	msg := map[string]any{
		"message_id": messageID, "date": 1792497600,
		"from": map[string]any{"id": 5550001, "is_bot": false, "first_name": "Test", "last_name": "Person"},
		"chat": map[string]any{"id": groupChat, "type": "supergroup", "title": "Test Group"},
	}
	for k, v := range extra {
		msg[k] = v
	}
	return map[string]any{"update_id": updateID, "message": msg}
}

// TG-01: getUpdates is long-polled from the last offset + 1 for message and channel_post.
func TestRunPollsWithIncreasingOffset(t *testing.T) {
	f := newFakeBotAPI(t)
	f.queue(groupMessage(10, 1, map[string]any{"text": "first"}))
	rec := start(t, newConnector(t, f))

	in := rec.NextInbound(t)
	require.Equal(t, strconv.Itoa(groupChat), in.ChatID)
	require.Equal(t, "Test Group", in.ChatTitle)
	require.Equal(t, "group", in.ChatKind)
	require.Equal(t, "1", in.MessageID)
	require.Equal(t, "5550001", in.SenderID)
	require.Equal(t, "Test Person", in.SenderName)
	require.Equal(t, "first", in.Text)
	require.Equal(t, string(dto.MessageKindText), in.Kind)
	require.Equal(t, time.Unix(1792497600, 0).UTC(), in.At)
	require.False(t, in.FromSelf)

	first := f.next("getUpdates")
	require.Equal(t, []any{"message", "channel_post"}, first.JSON["allowed_updates"])
	require.Contains(t, first.JSON, "timeout")
	require.Equal(t, userAgent, first.UserAgent)

	f.queue(map[string]any{"update_id": 11, "channel_post": map[string]any{
		"message_id": 7, "date": 1792497601, "text": "news",
		"chat": map[string]any{"id": -1002000000001, "type": "channel", "title": "Test Channel"},
	}})
	in = rec.NextInbound(t)
	require.Equal(t, "channel", in.ChatKind)
	require.Equal(t, "news", in.Text)
	require.Equal(t, "-1002000000001", in.SenderID, "a channel post without author comes from the channel")

	for {
		c := f.next("getUpdates")
		if c.JSON["offset"].(float64) == 12 {
			break // the offset moved past update 11
		}
		require.LessOrEqual(t, c.JSON["offset"].(float64), 12.0)
	}
	require.True(t, rec.NoInbound(), "acknowledged updates are not delivered again")
}

// CON-12 input: the bot's own messages are flagged.
func TestRunFlagsOwnMessages(t *testing.T) {
	f := newFakeBotAPI(t)
	f.queue(map[string]any{"update_id": 1, "message": map[string]any{
		"message_id": 2, "date": 1792497600, "text": "from me",
		"from": map[string]any{"id": botID, "is_bot": true, "first_name": "Test Bot"},
		"chat": map[string]any{"id": 5550001, "type": "private", "first_name": "Test", "last_name": "Person"},
	}})
	in := start(t, newConnector(t, f)).NextInbound(t)
	require.True(t, in.FromSelf)
	require.Equal(t, "dm", in.ChatKind)
	require.Equal(t, "Test Person", in.ChatTitle)
}

// TG-01 edge case: a group upgraded to a supergroup moves the chat to its new id.
func TestRunMigratesChat(t *testing.T) {
	f := newFakeBotAPI(t)
	f.queue(map[string]any{"update_id": 1, "message": map[string]any{
		"message_id": 3, "date": 1792497600, "migrate_to_chat_id": groupChat,
		"chat": map[string]any{"id": -4000000001, "type": "group", "title": "Test Group"},
	}})
	rec := start(t, newConnector(t, f))
	require.Equal(t, connectortest.Migration{OldID: "-4000000001", NewID: strconv.Itoa(groupChat)}, rec.NextMigration(t))
	require.True(t, rec.NoInbound(), "the migration notice is not a message")
}

// TG-04: inbound media is fetched with getFile and the file endpoint (largest photo size).
func TestInboundMediaUsesGetFile(t *testing.T) {
	f := newFakeBotAPI(t)
	f.queue(groupMessage(1, 4, map[string]any{"caption": "look", "photo": []map[string]any{
		{"file_id": "small", "file_size": 100, "width": 90}, {"file_id": "large", "file_size": 2048, "width": 1280},
	}}), groupMessage(2, 5, map[string]any{"document": map[string]any{
		"file_id": "doc1", "file_name": "report.pdf", "mime_type": "application/pdf", "file_size": 4096,
	}}))
	rec := start(t, newConnector(t, f))

	in := rec.NextInbound(t)
	require.Equal(t, string(dto.MessageKindImage), in.Kind)
	require.Equal(t, "look", in.Text)
	require.Len(t, in.Media, 1)
	require.Equal(t, int64(2048), in.Media[0].Size)
	require.Equal(t, "image/jpeg", in.Media[0].Mime)
	require.Equal(t, "content-of-media/large", readMedia(t, in.Media[0]))
	require.Equal(t, "large", f.next("getFile").JSON["file_id"])

	in = rec.NextInbound(t)
	require.Equal(t, string(dto.MessageKindDocument), in.Kind)
	require.Equal(t, "report.pdf", in.Media[0].Name)
	require.Equal(t, "application/pdf", in.Media[0].Mime)
	require.Equal(t, "content-of-media/doc1", readMedia(t, in.Media[0]))
}

func readMedia(t *testing.T, m connectors.Media) string {
	t.Helper()
	r, err := m.Open(context.Background())
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(data)
}

// TG-02: long text is split into ordered parts of at most 4,096 characters; the first
// part replies to the quoted message.
func TestSendSplitsLongTextAndReplies(t *testing.T) {
	f := newFakeBotAPI(t)
	c := newConnector(t, f)
	text := strings.Repeat("x", 9000)
	id, err := c.Send(context.Background(), connectors.Outbound{ChatID: strconv.Itoa(groupChat), Text: text, ReplyToID: "42"})
	require.NoError(t, err)
	require.Equal(t, "1001", id, "the id of the first part")

	var joined string
	for i := range 3 {
		sent := f.next("sendMessage")
		part := sent.JSON["text"].(string)
		require.LessOrEqual(t, len([]rune(part)), MaxText)
		require.Equal(t, strconv.Itoa(groupChat), sent.JSON["chat_id"])
		if i == 0 {
			require.Equal(t, 42.0, sent.JSON["reply_parameters"].(map[string]any)["message_id"])
		} else {
			require.NotContains(t, sent.JSON, "reply_parameters")
		}
		joined += part
	}
	require.Equal(t, text, joined, "parts are sent in order")
}

func TestSendPrefixesAuthor(t *testing.T) {
	f := newFakeBotAPI(t)
	_, err := newConnector(t, f).Send(context.Background(), connectors.Outbound{ChatID: "5550001", Text: "hi", AuthorName: "Agent"})
	require.NoError(t, err)
	sent := f.next("sendMessage")
	require.Equal(t, "Agent: hi", sent.JSON["text"])
	require.NotContains(t, sent.JSON, "reply_parameters")
}

// TG-03: images go with sendPhoto, everything else (and images over 10 MiB) with sendDocument.
func TestSendFiles(t *testing.T) {
	f := newFakeBotAPI(t)
	c := newConnector(t, f)
	file := func(name, mime string, size int64, content string) connectors.OutFile {
		return connectors.OutFile{Name: name, Mime: mime, Size: size, Open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(content)), nil
		}}
	}
	_, err := c.Send(context.Background(), connectors.Outbound{ChatID: strconv.Itoa(groupChat), Files: []connectors.OutFile{
		file("a.png", "image/png", 9, "png-bytes"), file("b.pdf", "application/pdf", 9, "pdf-bytes"),
		file("huge.png", "image/png", MaxPhoto+1, "big"),
	}})
	require.NoError(t, err)

	photo := f.next("sendPhoto")
	require.Equal(t, "photo", photo.FileField)
	require.Equal(t, "a.png", photo.FileName)
	require.Equal(t, "png-bytes", photo.FileBody)
	require.Equal(t, strconv.Itoa(groupChat), photo.Form["chat_id"])

	doc := f.next("sendDocument")
	require.Equal(t, "document", doc.FileField)
	require.Equal(t, "b.pdf", doc.FileName)
	require.Equal(t, "pdf-bytes", doc.FileBody)

	require.Equal(t, "huge.png", f.next("sendDocument").FileName, "photos over 10 MiB go as documents")
}

// Edge case: a send to a missing chat fails; the token never appears in errors.
func TestErrorsNeverContainTheToken(t *testing.T) {
	f := newFakeBotAPI(t)
	f.fail("sendMessage", http.StatusBadRequest)
	c := newConnector(t, f)
	_, err := c.Send(context.Background(), connectors.Outbound{ChatID: "1", Text: "x"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), botToken)
	require.Equal(t, "sendMessage: error 400", err.Error(), "CON-13: the platform's description text is not echoed")

	f.srv.Close()
	_, err = c.Send(context.Background(), connectors.Outbound{ChatID: "1", Text: "x"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), botToken)
	require.NotContains(t, err.Error(), escaped(botToken))
	err = c.Run(context.Background(), connectortest.NewRecorder())
	require.Error(t, err)
	require.NotContains(t, err.Error(), botToken)
}

func escaped(s string) string { return strings.ReplaceAll(s, ":", "%3A") }

// CON-06: a rejected token is reported as unauthorized.
func TestRunUnauthorized(t *testing.T) {
	f := newFakeBotAPI(t)
	cfg := connectors.Config{"bot_token": "999:wrong", "api_base": f.srv.URL}
	c, err := New(cfg, connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent})
	require.NoError(t, err)
	require.ErrorIs(t, c.Run(context.Background(), connectortest.NewRecorder()), ErrUnauthorized)
}

func TestKindSchema(t *testing.T) {
	k := Kind()
	require.Equal(t, KindName, k.Name)
	require.False(t, k.CanListChats, "the Bot API cannot list chats")
	cfg, errs := k.Validate(connectors.Config{"bot_token": botToken})
	require.Empty(t, errs)
	require.Equal(t, DefaultAPIBase, cfg.String("api_base"))
	_, errs = k.Validate(connectors.Config{})
	require.Equal(t, map[string]string{"bot_token": "required"}, errs)
}

// CON-13: by default the connector refuses an api_base on a private address.
func TestPrivateAPIBaseIsRefusedByDefault(t *testing.T) {
	f := newFakeBotAPI(t)
	cfg, errs := Kind().Validate(connectors.Config{"bot_token": botToken, "api_base": f.srv.URL})
	require.Empty(t, errs)
	c, err := New(cfg, connectors.Deps{UserAgent: userAgent})
	require.NoError(t, err)
	err = c.Run(context.Background(), connectortest.NewRecorder())
	require.ErrorIs(t, err, safehttp.ErrBlockedAddress)
	require.NotContains(t, err.Error(), botToken)
}

// capServer is a Bot API answering getMe with a JSON body of exactly ?size bytes (set per
// request through want) and file downloads with that many bytes.
func capServer(t *testing.T, size *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_path":"f"}}`))
		case strings.Contains(r.URL.Path, "/file/bot"):
			_, _ = w.Write([]byte(strings.Repeat("x", *size)))
		default: // a JSON answer of exactly *size bytes
			head, tail := `{"ok":true,"result":"`, `"}`
			_, _ = w.Write([]byte(head + strings.Repeat("x", *size-len(head)-len(tail)) + tail))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// S8: Bot API answers are capped at 4 MiB and file downloads at the upload limit.
func TestResponseCaps(t *testing.T) {
	size := 0
	srv := capServer(t, &size)
	cfg, errs := Kind().Validate(connectors.Config{"bot_token": botToken, "api_base": srv.URL})
	require.Empty(t, errs)
	c, err := New(cfg, connectors.Deps{AllowPrivateNetworks: true, MaxMediaBytes: 1000, UserAgent: userAgent})
	require.NoError(t, err)
	ctx := context.Background()

	size = connectors.MaxJSONResponse
	require.NoError(t, c.call(ctx, "getMe", nil, nil), "an answer of exactly 4 MiB")
	size = connectors.MaxJSONResponse + 1
	require.Error(t, c.call(ctx, "getMe", nil, nil), "an answer over 4 MiB")

	open := c.opener("file-id")
	for n, want := range map[int]error{1000: nil, 1001: safehttp.ErrResponseTooLarge} {
		size = n
		r, err := open(ctx)
		require.NoError(t, err)
		data, err := io.ReadAll(r)
		require.NoError(t, r.Close())
		if want == nil {
			require.NoError(t, err)
			require.Len(t, data, n)
		} else {
			require.ErrorIs(t, err, want, "a download over the upload limit")
		}
	}
}
