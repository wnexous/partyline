package whatsapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/connectors/safehttp"
)

// Synthetic credentials.
const (
	globalKey  = "global-key-0123456789"
	acmeToken  = "acme-token-0123456789"
	otherToken = "other-token-0123456789"
	userAgent  = "partyline/test"
	// wait bounds every wait; generous because the test host may be loaded.
	wait = 30 * time.Second
)

// request is a REST call received by the fake server.
type request struct {
	Method, Path, APIKey, UserAgent string
	Body                            map[string]any
}

// fakeEvolution is an evolution-go stand-in: REST endpoints and the broadcast WebSocket.
type fakeEvolution struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	requests []request
	wsUA     string
	wsToken  string
	sendErr  int // status for /send/* and /message/react when not zero
	// groupName is the group subject /group/info answers; groupInfoErr fails it when set.
	groupName    string
	groupInfoErr int

	conns    chan *websocket.Conn
	received chan request
}

func newFakeEvolution(t *testing.T) *fakeEvolution {
	f := &fakeEvolution{t: t, conns: make(chan *websocket.Conn, 4), received: make(chan request, 64)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /instance/all", f.rest(globalKey, func(request) any {
		return map[string]any{"data": []map[string]any{
			{"name": "acme", "token": acmeToken, "websocketEnable": true, "connected": true},
			{"name": "other", "token": otherToken, "websocketEnable": true, "connected": true},
			{"name": "quiet", "token": "quiet-token", "websocketEnable": false, "connected": true},
		}}
	}))
	mux.HandleFunc("GET /group/list", f.rest("", func(r request) any {
		name := map[string]string{acmeToken: "Acme Team", otherToken: "Other Team"}[r.APIKey]
		return map[string]any{"data": []map[string]any{{"JID": groupJID, "Name": name, "ParticipantCount": 3}}}
	}))
	// POST /group/info answers the shape captured from evolution-go 0.7.2 (synthetic values).
	mux.HandleFunc("POST /group/info", f.rest("", func(request) any {
		f.mu.Lock()
		name := f.groupName
		f.mu.Unlock()
		return json.RawMessage(`{"data":{"JID":"` + groupJID + `","OwnerJID":"` + peerJID + `","Name":` + quote(name) +
			`,"Topic":"Synthetic topic","IsLocked":false,"IsAnnounce":false,"Participants":[` +
			`{"JID":"` + peerJID + `","PhoneNumber":"` + peerJID + `","LID":"` + peerLID + `","IsAdmin":true,"DisplayName":"Test Person"}],` +
			`"ParticipantCount":1},"message":"success"}`)
	}))
	mux.HandleFunc("POST /send/text", f.rest("", func(request) any {
		return map[string]any{"data": map[string]any{"Info": map[string]any{"ID": "SENT-TEXT"}}}
	}))
	mux.HandleFunc("POST /send/media", f.rest("", func(request) any {
		return map[string]any{"data": map[string]any{"Info": map[string]any{"ID": "SENT-MEDIA"}}}
	}))
	// POST /message/react answers like the other send endpoints of evolution-go 0.7.2.
	mux.HandleFunc("POST /message/react", f.rest("", func(request) any {
		return json.RawMessage(`{"data":{"Info":{"ID":"SENT-REACTION"}},"message":"success"}`)
	}))
	mux.HandleFunc("POST /message/downloadmedia", f.rest("", func(r request) any {
		if _, ok := r.Body["message"].(map[string]any)["audioMessage"]; ok {
			return map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("voice-bytes"))}
		}
		return map[string]any{"data": map[string]any{"base64": base64.StdEncoding.EncodeToString([]byte("downloaded-bytes"))}}
	}))
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.wsUA, f.wsToken = r.Header.Get("User-Agent"), r.URL.Query().Get("token")
		f.mu.Unlock()
		if r.URL.Query().Get("token") != globalKey || r.URL.Query().Has("instanceId") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		f.conns <- c
		_, _, _ = c.Read(context.Background()) // hold the socket until the client leaves
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// rest wraps a JSON handler; key, when set, is the required apikey (else any known
// instance token is accepted).
func (f *fakeEvolution) rest(key string, answer func(request) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := request{Method: r.Method, Path: r.URL.Path, APIKey: r.Header.Get("apikey"), UserAgent: r.Header.Get("User-Agent")}
		if r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &req.Body)
		}
		f.mu.Lock()
		f.requests = append(f.requests, req)
		sendErr, infoErr := f.sendErr, f.groupInfoErr
		f.mu.Unlock()
		f.received <- req
		valid := req.APIKey == key || (key == "" && (req.APIKey == acmeToken || req.APIKey == otherToken))
		if !valid {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/group/info" && infoErr != 0 {
			http.Error(w, "group info failed", infoErr)
			return
		}
		if sendErr != 0 && (strings.HasPrefix(r.URL.Path, "/send/") || r.URL.Path == "/message/react") {
			http.Error(w, "send failed for "+req.APIKey, sendErr)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer(req))
	}
}

// next returns the next REST call to path.
func (f *fakeEvolution) next(path string) request {
	f.t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case r := <-f.received:
			if r.Path == path {
				return r
			}
		case <-deadline:
			f.t.Fatalf("no request to %s", path)
		}
	}
}

func (f *fakeEvolution) socket() *websocket.Conn {
	f.t.Helper()
	select {
	case c := <-f.conns:
		return c
	case <-time.After(wait):
		f.t.Fatal("the connector never opened the WebSocket")
		return nil
	}
}

func newConnector(t *testing.T, f *fakeEvolution, instances ...string) *Connector {
	t.Helper()
	cfg := connectors.Config{"base_url": f.srv.URL, "api_key": globalKey}
	if len(instances) > 0 {
		cfg["instances"] = instances
	}
	norm, errs := Kind().Validate(cfg)
	require.Empty(t, errs)
	c, err := New(norm, connectors.Deps{AllowPrivateNetworks: true, ID: "c1", UserAgent: userAgent})
	require.NoError(t, err)
	return c
}

// start runs the connector until the test ends and returns the server side of its socket.
func start(t *testing.T, c *Connector, f *fakeEvolution, rec *connectortest.Recorder) *websocket.Conn {
	t.Helper()
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
	sock := f.socket()
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))
	return sock
}

func push(t *testing.T, sock *websocket.Conn, data []byte) {
	t.Helper()
	require.NoError(t, sock.Write(context.Background(), websocket.MessageText, data))
}

// WA-01: Message events of allowed instances arrive as inbound messages; the broadcast
// socket uses the global key without instanceId and sends the User-Agent.
func TestRunDeliversMessagesOfAllowedInstances(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f, "acme")
	rec := connectortest.NewRecorder()
	sock := start(t, c, f, rec)

	f.mu.Lock()
	require.Equal(t, userAgent, f.wsUA, "the WebSocket handshake carries partyline/<version>")
	require.Equal(t, globalKey, f.wsToken)
	f.mu.Unlock()
	require.Equal(t, userAgent, f.next("/instance/all").UserAgent, "REST calls carry partyline/<version>")

	push(t, sock, frame(t, "other", groupInfo("NOT-ALLOWED"), `{"conversation":"ignored"}`))
	push(t, sock, frame(t, testInstance, groupInfo("ALLOWED"), `{"conversation":"hello"}`))
	in := rec.NextInbound(t)
	require.Equal(t, "ALLOWED", in.MessageID, "the instance outside the allowlist was ignored")
	require.Equal(t, "acme|120363000000000001@g.us", in.ChatID)
	require.Equal(t, "hello", in.Text)
}

// WA-01: an empty allowlist accepts every instance the broadcast socket delivers.
func TestRunWithoutAllowlistAcceptsEveryInstance(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	rec := connectortest.NewRecorder()
	sock := start(t, c, f, rec)
	push(t, sock, frame(t, "other", groupInfo("FROM-OTHER"), `{"conversation":"x"}`))
	require.Equal(t, "other|120363000000000001@g.us", rec.NextInbound(t).ChatID)
}

// CON-06: a rejected global key fails Run with "unauthorized"; the key is not in the error.
func TestRunUnauthorized(t *testing.T) {
	f := newFakeEvolution(t)
	cfg := connectors.Config{"base_url": f.srv.URL, "api_key": "wrong-key-0123456789"}
	c, err := New(cfg, connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent})
	require.NoError(t, err)
	err = c.Run(context.Background(), connectortest.NewRecorder())
	require.ErrorIs(t, err, ErrUnauthorized)
	require.Equal(t, "unauthorized", err.Error())
}

func TestRunFailureDoesNotLeakTheKey(t *testing.T) {
	f := newFakeEvolution(t)
	f.srv.Close()
	c := newConnector(t, f)
	err := c.Run(context.Background(), connectortest.NewRecorder())
	require.Error(t, err)
	require.NotContains(t, err.Error(), globalKey)
}

// Media: inline base64 (WEBHOOK_FILES) is decoded without a request; otherwise the
// unwrapped message goes to /message/downloadmedia with the instance token.
func TestMediaOpensInlineOrDownloads(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	rec := connectortest.NewRecorder()
	sock := start(t, c, f, rec)

	push(t, sock, frame(t, testInstance, groupInfo("IMG"),
		`{"imageMessage":{"mimetype":"image/png","fileLength":6},"base64":"data:image/png;base64,`+base64.StdEncoding.EncodeToString([]byte("inline"))+`"}`))
	in := rec.NextInbound(t)
	require.Len(t, in.Media, 1)
	require.Equal(t, "image/png", in.Media[0].Mime)
	require.Equal(t, int64(6), in.Media[0].Size)
	require.Equal(t, "inline", readMedia(t, in.Media[0]))

	push(t, sock, frame(t, testInstance, groupInfo("DOC"),
		`{"ephemeralMessage":{"message":{"documentMessage":{"mimetype":"application/pdf","fileName":"a.pdf"}}}}`))
	in = rec.NextInbound(t)
	require.Equal(t, "downloaded-bytes", readMedia(t, in.Media[0]))
	req := f.next("/message/downloadmedia")
	require.Equal(t, acmeToken, req.APIKey)
	require.Contains(t, req.Body["message"], "documentMessage", "the unwrapped message is sent")

	push(t, sock, frame(t, testInstance, groupInfo("AUD"), `{"audioMessage":{"mimetype":"audio/ogg"}}`))
	in = rec.NextInbound(t)
	require.Equal(t, "voice-bytes", readMedia(t, in.Media[0]), "data may be the base64 string itself")
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

// WA-03: text goes to /send/text with the instance token and quotes the replied message.
func TestSendTextQuotesReply(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	id, err := c.Send(context.Background(), connectors.Outbound{
		ChatID: ChatID(testInstance, groupJID), Text: "on it", AuthorName: "Agent",
		ReplyToID: "ORIG1", ReplyToSenderID: peerJID,
	})
	require.NoError(t, err)
	require.Equal(t, "SENT-TEXT", id)
	req := f.next("/send/text")
	require.Equal(t, acmeToken, req.APIKey)
	require.Equal(t, userAgent, req.UserAgent)
	require.Equal(t, groupJID, req.Body["number"])
	require.Equal(t, "*Agent*\non it", req.Body["text"])
	require.Equal(t, map[string]any{"messageId": "ORIG1", "participant": peerJID}, req.Body["quoted"])
}

func TestSendTextWithoutReplyHasNoQuote(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	_, err := c.Send(context.Background(), connectors.Outbound{ChatID: ChatID(testInstance, peerJID), Text: "hi"})
	require.NoError(t, err)
	req := f.next("/send/text")
	require.Equal(t, peerJID, req.Body["number"])
	require.Equal(t, "hi", req.Body["text"])
	require.NotContains(t, req.Body, "quoted")
}

// WA-04: files go to /send/media as pure base64 with the type taken from the MIME type.
func TestSendMediaTypes(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	file := func(name, mime, content string) connectors.OutFile {
		return connectors.OutFile{Name: name, Mime: mime, Size: int64(len(content)), Open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(content)), nil
		}}
	}
	id, err := c.Send(context.Background(), connectors.Outbound{ChatID: ChatID(testInstance, groupJID), Files: []connectors.OutFile{
		file("a.png", "image/png", "png-bytes"), file("b.mp4", "video/mp4", "mp4"), file("c.ogg", "audio/ogg", "ogg"),
		file("d.pdf", "application/pdf", "pdf"),
	}})
	require.NoError(t, err)
	require.Equal(t, "SENT-MEDIA", id)
	for _, want := range []struct{ typ, name, content string }{
		{"image", "a.png", "png-bytes"}, {"video", "b.mp4", "mp4"}, {"audio", "c.ogg", "ogg"}, {"document", "d.pdf", "pdf"},
	} {
		req := f.next("/send/media")
		require.Equal(t, acmeToken, req.APIKey)
		require.Equal(t, groupJID, req.Body["number"])
		require.Equal(t, want.typ, req.Body["type"])
		require.Equal(t, want.name, req.Body["filename"])
		require.Equal(t, base64.StdEncoding.EncodeToString([]byte(want.content)), req.Body["url"])
	}
}

// CON-06 / edge case: a failed send is an error without the token or the response body.
func TestSendFailure(t *testing.T) {
	f := newFakeEvolution(t)
	f.sendErr = http.StatusInternalServerError
	c := newConnector(t, f)
	_, err := c.Send(context.Background(), connectors.Outbound{ChatID: ChatID(testInstance, groupJID), Text: "x"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), acmeToken)
	require.Contains(t, err.Error(), "500")

	_, err = c.Send(context.Background(), connectors.Outbound{ChatID: ChatID("other", groupJID), Text: "x"})
	require.Error(t, err)

	c = newConnector(t, f, "acme")
	_, err = c.Send(context.Background(), connectors.Outbound{ChatID: ChatID("other", groupJID), Text: "x"})
	require.ErrorContains(t, err, "not allowed", "an instance outside the allowlist is not used for sends")
}

// WA-05: groups are listed per allowed instance with GET /group/list.
func TestListChats(t *testing.T) {
	f := newFakeEvolution(t)
	chats, err := newConnector(t, f, "acme").ListChats(context.Background())
	require.NoError(t, err)
	require.Equal(t, []connectors.ChatInfo{{ID: "acme|" + groupJID, Title: "Acme Team", Kind: "group"}}, chats)

	chats, err = newConnector(t, f).ListChats(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t, []connectors.ChatInfo{
		{ID: "acme|" + groupJID, Title: "Acme Team", Kind: "group"},
		{ID: "other|" + groupJID, Title: "Other Team", Kind: "group"},
	}, chats, "without allowlist: every instance with WebSocket events enabled")
}

func TestKindSchema(t *testing.T) {
	k := Kind()
	require.Equal(t, KindName, k.Name)
	require.True(t, k.CanListChats)
	_, errs := k.Validate(connectors.Config{})
	require.Equal(t, map[string]string{"base_url": "required", "api_key": "required"}, errs)
}

// CON-13: by default the connector refuses a base_url on a private address.
func TestPrivateBaseURLIsRefusedByDefault(t *testing.T) {
	f := newFakeEvolution(t)
	cfg := connectors.Config{"base_url": f.srv.URL, "api_key": globalKey}
	c, err := New(cfg, connectors.Deps{UserAgent: userAgent})
	require.NoError(t, err)
	err = c.Run(context.Background(), connectortest.NewRecorder())
	require.ErrorIs(t, err, safehttp.ErrBlockedAddress)
	require.NotContains(t, err.Error(), globalKey)
}

// CON-13: the REST client refuses a private base_url on its own, before any request
// reaches the server.
func TestRESTClientRefusesPrivateBaseURL(t *testing.T) {
	f := newFakeEvolution(t)
	c, err := New(connectors.Config{"base_url": f.srv.URL, "api_key": globalKey}, connectors.Deps{UserAgent: userAgent})
	require.NoError(t, err)

	_, err = c.ListChats(context.Background())
	require.ErrorIs(t, err, safehttp.ErrBlockedAddress)
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Empty(t, f.requests, "no REST call reached the private server")
}

// CON-13: the WebSocket client refuses a private base_url on its own: with REST calls
// allowed, Run still fails at the WebSocket dial and the socket is never opened.
func TestWebSocketClientRefusesPrivateBaseURL(t *testing.T) {
	f := newFakeEvolution(t)
	c, err := New(connectors.Config{"base_url": f.srv.URL, "api_key": globalKey}, connectors.Deps{UserAgent: userAgent})
	require.NoError(t, err)
	c.client = safehttp.NewClient(safehttp.Options{AllowPrivate: true, Timeout: requestTimeout}) // REST only

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, connectortest.NewRecorder()) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, safehttp.ErrBlockedAddress)
	case <-f.conns:
		t.Fatal("the WebSocket reached the private server")
	case <-time.After(wait):
		t.Fatal("Run neither failed nor connected")
	}
	require.Equal(t, "/instance/all", f.next("/instance/all").Path, "the REST call was allowed")
}

// S8: REST answers are capped at 4 MiB; /message/downloadmedia, a JSON answer carrying
// base64 media, and WebSocket frames, which carry inline media, at the base64 size of the
// upload limit plus 4 MiB.
func TestResponseCaps(t *testing.T) {
	const limit = 1000
	envelope := (limit+2)/3*4 + connectors.MaxJSONResponse
	size := 0
	pad := func(w http.ResponseWriter, head, tail string) {
		_, _ = w.Write([]byte(head + strings.Repeat(" ", size-len(head)-len(tail)) + tail))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /instance/all", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"name":"acme","token":"` + acmeToken + `","websocketEnable":true}]}`))
	})
	mux.HandleFunc("GET /group/list", func(w http.ResponseWriter, _ *http.Request) { pad(w, `{"data":[]`, `}`) })
	mux.HandleFunc("POST /message/downloadmedia", func(w http.ResponseWriter, _ *http.Request) {
		pad(w, `{"data":"`+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", limit)))+`"`, `}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(connectors.Config{"base_url": srv.URL, "api_key": globalKey},
		connectors.Deps{AllowPrivateNetworks: true, MaxMediaBytes: limit, UserAgent: userAgent})
	require.NoError(t, err)
	ctx := context.Background()

	size = connectors.MaxJSONResponse
	_, err = c.ListChats(ctx)
	require.NoError(t, err, "a REST answer of exactly 4 MiB")
	size = connectors.MaxJSONResponse + 1
	_, err = c.ListChats(ctx)
	require.Error(t, err, "a REST answer over 4 MiB")

	size = envelope
	content, err := c.download(ctx, "acme", json.RawMessage(`{}`))
	require.NoError(t, err, "media at the upload limit")
	require.Len(t, content, (limit+2)/3*4)
	size = envelope + 1
	_, err = c.download(ctx, "acme", json.RawMessage(`{}`))
	require.Error(t, err, "an answer over the media envelope")

	require.Equal(t, int64(envelope), c.maxFrame, "WebSocket frames share the media envelope")
}

// S8: a WebSocket frame over the media envelope ends Run; one at the envelope does not.
func TestWebSocketFrameCap(t *testing.T) {
	const limit = 1000
	envelope := (limit+2)/3*4 + connectors.MaxJSONResponse
	f := newFakeEvolution(t)
	c, err := New(connectors.Config{"base_url": f.srv.URL, "api_key": globalKey},
		connectors.Deps{AllowPrivateNetworks: true, MaxMediaBytes: limit, UserAgent: userAgent})
	require.NoError(t, err)
	rec := connectortest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, rec) }()
	sock := f.socket()
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))

	push(t, sock, []byte(strings.Repeat(" ", envelope))) // undecodable, ignored
	push(t, sock, frame(t, testInstance, groupInfo("AFTER"), `{"conversation":"still here"}`))
	require.Equal(t, "still here", rec.NextInbound(t).Text)

	_ = sock.Write(context.Background(), websocket.MessageText, []byte(strings.Repeat(" ", envelope+1)))
	select {
	case err := <-done:
		require.Error(t, err)
		require.NotErrorIs(t, err, context.Canceled)
	case <-time.After(wait):
		t.Fatal("a frame over the cap did not end Run")
	}
}

// WA-05 / WA-01 against the real evolution-go 0.7.2 shape: /instance/all returns
// websocketEnable as a string ("enabled" or "") plus many other fields; the connector must
// read it, list groups of the enabled instance and skip the disabled one.
func TestInstanceListRealShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /instance/all", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[` +
			`{"id":"i1","name":"acme","token":"` + acmeToken + `","webhook":"","rabbitmqEnable":"","websocketEnable":"enabled","natsEnable":"","jid":"5511999990000:7@s.whatsapp.net","qrcode":"","connected":true,"expiration":0,"disconnect_reason":"","events":"MESSAGE","os_name":"","proxy":"","client_name":"","createdAt":"2026-10-01T00:00:00Z","alwaysOnline":false,"rejectCall":false,"msgRejectCall":"","readMessages":false,"ignoreGroups":false,"ignoreStatus":true},` +
			`{"id":"i2","name":"quiet","token":"quiet-token","webhook":"","rabbitmqEnable":"","websocketEnable":"","natsEnable":"","jid":"","qrcode":"","connected":true,"expiration":0,"disconnect_reason":"","events":"","os_name":"","proxy":"","client_name":"","createdAt":"2026-10-01T00:00:00Z","alwaysOnline":false,"rejectCall":false,"msgRejectCall":"","readMessages":false,"ignoreGroups":false,"ignoreStatus":false}` +
			`],"message":"success"}`))
	})
	listed := make(chan string, 4)
	mux.HandleFunc("GET /group/list", func(w http.ResponseWriter, r *http.Request) {
		listed <- r.Header.Get("apikey")
		_, _ = w.Write([]byte(`{"data":[{"JID":"120363000000000001@g.us","Name":"Acme Support","ParticipantCount":3}],"message":"success"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(connectors.Config{"base_url": srv.URL, "api_key": globalKey},
		connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent})
	require.NoError(t, err)

	chats, err := c.ListChats(context.Background())
	require.NoError(t, err)
	require.Len(t, chats, 1)
	require.Equal(t, "acme|120363000000000001@g.us", chats[0].ID)
	require.Equal(t, acmeToken, <-listed, "only the websocket-enabled instance is listed")
	require.Empty(t, listed)
}

// The flag parser accepts the string forms evolution-go uses and plain booleans.
func TestFlagParsing(t *testing.T) {
	for raw, want := range map[string]bool{
		`"enabled"`: true, `"true"`: true, `"1"`: true, `true`: true,
		`""`: false, `"disabled"`: false, `"false"`: false, `false`: false, `null`: false,
	} {
		var f flag
		require.NoError(t, json.Unmarshal([]byte(raw), &f), raw)
		require.Equal(t, want, bool(f), raw)
	}
}

// A response of the wrong shape names the field and types, never the content.
func TestInvalidResponseNamesTheField(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /instance/all", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"name":"acme","token":"` + acmeToken + `","websocketEnable":"enabled","connected":true}]}`))
	})
	mux.HandleFunc("GET /group/list", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"JID":42,"Name":"secret-group-name"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(connectors.Config{"base_url": srv.URL, "api_key": globalKey},
		connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent})
	require.NoError(t, err)
	_, err = c.ListChats(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid response (field data.0.JID: got number, want string)")
	require.NotContains(t, err.Error(), "secret-group-name")
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (f *fakeEvolution) setGroup(name string, failWith int) {
	f.mu.Lock()
	f.groupName, f.groupInfoErr = name, failWith
	f.mu.Unlock()
}

// count returns how many REST calls reached path.
func (f *fakeEvolution) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.Path == path {
			n++
		}
	}
	return n
}

// CTX-05, CTX-08: a group message arrives titled with the group subject from /group/info
// (instance token, body {"groupJid": jid}); the subject is cached for 10 minutes per
// instance, so a renamed group shows its new name once the cache expired.
func TestGroupMessagesCarryTheGroupSubject(t *testing.T) {
	f := newFakeEvolution(t)
	f.setGroup("Lojinha Sintética", 0)
	c := newConnector(t, f, "acme")
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	rec := connectortest.NewRecorder()
	sock := start(t, c, f, rec)

	push(t, sock, frame(t, testInstance, groupInfo("G1"), `{"conversation":"hello"}`))
	in := rec.NextInbound(t)
	require.Equal(t, "G1", in.MessageID)
	require.Equal(t, "Lojinha Sintética", in.ChatTitle)
	req := f.next("/group/info")
	require.Equal(t, acmeToken, req.APIKey)
	require.Equal(t, map[string]any{"groupJid": groupJID}, req.Body)

	f.setGroup("Renamed Group", 0)
	now = now.Add(9 * time.Minute)
	push(t, sock, frame(t, testInstance, groupInfo("G2"), `{"conversation":"again"}`))
	require.Equal(t, "Lojinha Sintética", rec.NextInbound(t).ChatTitle, "cached for 10 minutes")
	require.Equal(t, 1, f.count("/group/info"))

	now = now.Add(2 * time.Minute)
	push(t, sock, frame(t, testInstance, groupInfo("G3"), `{"conversation":"later"}`))
	require.Equal(t, "Renamed Group", rec.NextInbound(t).ChatTitle)
	require.Equal(t, 2, f.count("/group/info"))

	// DMs never ask for group info.
	push(t, sock, frame(t, testInstance, dmInfo("D1"), `{"conversation":"dm"}`))
	require.Equal(t, "Test Person", rec.NextInbound(t).ChatTitle)
	require.Equal(t, 2, f.count("/group/info"))
}

// CTX-05: the group list refresh also fills the subject cache; a failing /group/info leaves
// the title empty (the stored one is kept) and the message still arrives.
func TestGroupSubjectFromListAndFailure(t *testing.T) {
	f := newFakeEvolution(t)
	f.setGroup("", http.StatusInternalServerError)
	c := newConnector(t, f, "acme")
	rec := connectortest.NewRecorder()
	sock := start(t, c, f, rec)

	push(t, sock, frame(t, testInstance, groupInfo("G1"), `{"conversation":"hello"}`))
	in := rec.NextInbound(t)
	require.Equal(t, "G1", in.MessageID)
	require.Empty(t, in.ChatTitle)
	require.Equal(t, 1, f.count("/group/info"))

	_, err := c.ListChats(context.Background())
	require.NoError(t, err)
	push(t, sock, frame(t, testInstance, groupInfo("G2"), `{"conversation":"after the refresh"}`))
	require.Equal(t, "Acme Team", rec.NextInbound(t).ChatTitle)
	require.Equal(t, 1, f.count("/group/info"), "the list filled the cache")
}
