package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
)

// KindName is the connector kind name.
const KindName = "whatsapp"

// Timing of the connector.
const (
	pingInterval     = 30 * time.Second
	statusInterval   = 60 * time.Second
	instanceCacheTTL = 5 * time.Minute
	groupTitleTTL    = 10 * time.Minute
	requestTimeout   = 60 * time.Second
)

// MaxFile is the largest file WhatsApp takes from an account, for every type of media (LF-05).
const MaxFile = 2 << 30

// ErrUnauthorized means evolution-go rejected the API key or an instance token.
var ErrUnauthorized = errors.New("unauthorized")

// ErrOtherInstance means a chat belongs to a WhatsApp instance this connection does not use:
// one number per connection (CID-04).
var ErrOtherInstance = errors.New("chat of another instance")

// instanceChoiceDetail is the status detail of a connection that does not name exactly one
// instance (CID-04): until the owner chooses the number, the connection cannot run.
const instanceChoiceDetail = "choose the instance (number) of this connection"

// Kind returns the WhatsApp (evolution-go) connector kind.
func Kind() connectors.Kind {
	return connectors.Kind{
		Name: KindName, Label: "WhatsApp (evolution-go)", CanListChats: true, CanReact: true, CanSendFiles: true,
		MaxFileBytes: MaxFile,
		Fields: []connectors.Field{
			{Name: "base_url", Label: "evolution-go URL", Type: dto.FieldTypeURL, Required: true, Endpoint: true,
				Help: "e.g. https://evolution.example.com"},
			{Name: "api_key", Label: "Global API key", Type: dto.FieldTypeSecret, Required: true},
			{Name: "instances", Label: "Instances", Type: dto.FieldTypeStringList, Required: true,
				Help: "exactly one instance: the number of this connection"},
		},
		Address: address,
		New: func(cfg connectors.Config, d connectors.Deps) (connectors.Connector, error) {
			return New(cfg, d)
		},
	}
}

// Connector is a WhatsApp connector over one evolution-go server.
type Connector struct {
	base      *url.URL
	apiKey    string
	instance  string // the single evolution-go instance of this connection; empty when the config names zero or several
	userAgent string
	log       *slog.Logger
	client    *http.Client // REST calls (JSON)
	downloads *http.Client // /message/downloadmedia: JSON carrying base64 media, streamed (LF-04)
	uploads   *http.Client // /send/media: base64 media in the request, streamed; no total timeout
	stream    *http.Client // WebSocket handshakes
	maxFrame  int64        // WebSocket frames carry inline base64 media, held in memory: the single-request limit
	now       func() time.Time
	wait      func(ctx context.Context, d time.Duration) error // the pause between two checks of the instance; tests replace it

	mu        sync.Mutex
	instances map[string]instance
	fetchedAt time.Time
	titles    map[string]groupTitle // by chat id (instance|jid)
}

// groupTitle is a cached group subject.
type groupTitle struct {
	name string
	at   time.Time
}

// instance is an evolution-go instance; its token stays in memory only.
type instance struct {
	Name            string `json:"name"`
	Token           string `json:"token"`
	WebsocketEnable flag   `json:"websocketEnable"`
	// Connected tells that the WhatsApp session of the instance's number is up; nil when evolution-go
	// does not say.
	Connected *flag `json:"connected"`
	// DisconnectReason is why evolution-go says the number is not connected ("Reconnecting"); empty
	// when it is, and when evolution-go does not say.
	DisconnectReason reason `json:"disconnect_reason"`
	// JID is the WhatsApp account of a connected instance, "<number>:<device>@s.whatsapp.net";
	// empty for an instance that is not connected and for versions that do not report it.
	JID string `json:"jid"`
}

// flag reads an evolution-go switch. evolution-go 0.7.2 sends websocketEnable as a string
// ("enabled" or ""), other versions as a boolean; both are accepted.
type flag bool

func (f *flag) UnmarshalJSON(b []byte) error {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(string(b)), `"`)) {
	case "true", "enabled", "1", "yes", "on":
		*f = true
	default:
		*f = false
	}
	return nil
}

// reason reads the disconnect_reason of an instance. evolution-go sends text; any other JSON value
// is no reason, so that a field of an unexpected type never fails the whole instance list, which
// the connection cannot run without.
type reason string

func (r *reason) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*r = reason(s)
	}
	return nil
}

var (
	_ connectors.Connector  = (*Connector)(nil)
	_ connectors.ChatLister = (*Connector)(nil)
)

// New returns a connector for a validated config.
func New(cfg connectors.Config, d connectors.Deps) (*Connector, error) {
	base, err := url.Parse(strings.TrimRight(cfg.String("base_url"), "/"))
	if err != nil || base.Host == "" {
		return nil, errors.New("invalid base_url")
	}
	// CID-04: a connection is one number. A config that names zero or several instances (one
	// stored before the requirement existed) constructs the connector so Run can say what the
	// owner must do; it uses no instance at all.
	instances := cfg.Strings("instances")
	instance := ""
	if len(instances) == 1 {
		instance = instances[0]
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Connector{
		base: base, apiKey: cfg.String("api_key"), instance: instance, userAgent: d.UserAgent, log: log,
		// Media stream to disk up to the largest file; the stall watch of each transfer bounds its time,
		// not a client timeout, which would cut a large file that is still arriving (LF-04).
		client: d.JSONClient(requestTimeout), downloads: d.HTTPClient(0, mediaEnvelope(d.FileLimit())),
		uploads: d.HTTPClient(0, connectors.MaxJSONResponse),
		stream:  d.StreamClient(), maxFrame: mediaEnvelope(d.MediaLimit()), now: time.Now, wait: sleep,
	}, nil
}

// Run checks the API key, subscribes to the broadcast WebSocket and hands every Message
// event of the connection's instance to h (WA-01). It also reports whether the instance's number
// is connected: when it starts, which is whenever the WebSocket reconnects, and every
// statusInterval after (WA-06). It returns when ctx ends, the socket fails or a check of the
// instance fails. A connection that does not name exactly one instance (CID-04) reports what the
// owner must do, neither dials nor restarts, and waits for ctx.
func (c *Connector) Run(ctx context.Context, h connectors.Handler) error {
	if c.instance == "" {
		h.Status(connectors.StatusError, instanceChoiceDetail)
		<-ctx.Done()
		return ctx.Err()
	}
	insts, err := c.refreshInstances(ctx)
	if err != nil {
		return err
	}
	conn, resp, err := websocket.Dial(ctx, c.wsURL(), &websocket.DialOptions{
		HTTPClient: c.stream, HTTPHeader: http.Header{"User-Agent": {c.userAgent}},
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return ErrUnauthorized
		}
		return fmt.Errorf("websocket: %w", plainError(err))
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(c.maxFrame)
	h.Account(c.accountLabel(insts))
	first := c.state(insts)
	h.Status(first.status, first.detail)

	parent := ctx
	ctx, cancel := context.WithCancelCause(ctx)
	var watching sync.WaitGroup
	defer watching.Wait() // its last report comes before Run returns, so before the supervisor's
	defer cancel(nil)
	// Frames are handled in order by one goroutine while this one goes on reading the socket, so that
	// pongs are read while a large media downloads (LF-04). The media downloads live as long as the
	// socket: when it fails, the download in progress is aborted (its attachment is unavailable) and
	// the frames already read are handled at once, under the connector's own context, so that Run
	// returns promptly and the next connection reads what comes next, after them.
	frames := newInbox(maxInboxBytes)
	var handling sync.WaitGroup
	handling.Go(func() {
		err := connectors.Guard(c.log, "inbound", func() error {
			for {
				data, ok := frames.take(parent)
				if !ok {
					return nil
				}
				c.handle(parent, ctx, h, data)
				frames.done(data)
			}
		})
		if err != nil {
			cancel(err)
		}
	})
	// A panic in a goroutine of ours ends the process, whatever supervises Run: these two end Run instead (CON-15).
	go func() {
		if err := connectors.Guard(c.log, "ping", func() error { c.ping(ctx, conn, func() { cancel(nil) }); return nil }); err != nil {
			cancel(err)
		}
	}()
	watching.Go(func() {
		if err := connectors.Guard(c.log, "watch", func() error { return c.watch(ctx, h, first) }); err != nil {
			cancel(err)
		}
	})
	err = c.read(ctx, conn, frames)
	cancel(err) // the socket is over: so are its downloads
	frames.close()
	handling.Wait()
	return err
}

// read hands the socket's frames to the inbox until the socket or ctx ends.
func (c *Connector) read(ctx context.Context, conn *websocket.Conn, frames *inbox) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return fmt.Errorf("websocket: %w", plainError(err))
		}
		if err := frames.put(ctx, data); err != nil {
			return context.Cause(ctx)
		}
	}
}

// accountLabel is how WhatsApp names the connector's account (ACC-07): the masked phone number
// of the connection's instance, as evolution-go reports it. An instance whose jid is no phone
// number (not connected, a LID) or that evolution-go does not have adds nothing.
func (c *Connector) accountLabel(insts []instance) string {
	for _, inst := range insts {
		if inst.Name != c.instance {
			continue
		}
		if n := instanceNumber(inst.JID); n != "" {
			return maskNumber(n)
		}
	}
	return ""
}

// idleWarning explains a connection that will never deliver a message: evolution-go does not
// have the instance the connection names. It is shown with the connected status so a quiet
// connector is not mistaken for a working one; empty when messages can arrive.
func (c *Connector) idleWarning(insts []instance) string {
	if slices.ContainsFunc(insts, func(i instance) bool { return i.Name == c.instance }) {
		return ""
	}
	return "evolution-go has no instance named " + c.instance + ": messages of it cannot arrive"
}

// instanceNumber is the phone number of an instance jid ("5511999990000:7@s.whatsapp.net" is
// "5511999990000"); empty for any other jid.
func instanceNumber(jid string) string {
	user, ok := strings.CutSuffix(jid, "@s.whatsapp.net")
	if !ok {
		return ""
	}
	user, _, _ = strings.Cut(user, ":")
	for _, r := range user {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return user
}

// ping keeps the socket alive; a failed ping ends Run.
func (c *Connector) ping(ctx context.Context, conn *websocket.Conn, cancel context.CancelFunc) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, pcancel := context.WithTimeout(ctx, pingInterval)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil && ctx.Err() == nil {
				c.log.Warn("whatsapp: ping failed")
				_ = conn.CloseNow()
				cancel()
				return
			}
		}
	}
}

// handle parses one frame; events that are not chat messages or come from another instance
// of the evolution-go server are ignored.
//
// life is the socket's context: the media of the message download while it lasts.
func (c *Connector) handle(ctx, life context.Context, h connectors.Handler, data []byte) {
	ev, err := DecodeFrame(data)
	if err != nil {
		c.log.Warn("whatsapp: undecodable frame")
		return
	}
	if ev.Event != "Message" || !c.uses(ev.InstanceName) {
		return
	}
	p, err := Parse(ev)
	if err != nil {
		if !errors.Is(err, ErrNotMessage) {
			c.log.Warn("whatsapp: unparsable message", "instance", ev.InstanceName)
		}
		return
	}
	if len(p.UnknownKeys) > 0 {
		// The protocol types of a message the connector does not know; the scoped logger carries
		// the connector id. Never the chat, the sender or the text.
		c.log.Info("whatsapp: unsupported message", "keys", p.UnknownKeys)
	}
	if len(p.MentionedJIDs) > 0 {
		p.Inbound.MentionsAccount = c.mentions(p.MentionedJIDs)
	}
	if p.Media != nil {
		p.Inbound.Media = []connectors.Media{c.media(life, p.Instance, *p.Media)}
	}
	if p.Inbound.ChatKind == "group" && p.Inbound.ChatTitle == "" && !p.Inbound.FromSelf {
		p.Inbound.ChatTitle = c.groupSubject(ctx, p.Instance, p.Inbound.ChatID)
	}
	h.Inbound(ctx, p.Inbound)
}

// mentions reports whether a mentioned jid is the connection's own account (CID-12). WhatsApp
// mentions carry the account's jid without the device suffix; evolution-go may report the
// instance jid as a phone jid or, for a newer account, as its LID, so both sides are compared
// as bare jids.
func (c *Connector) mentions(jids []string) bool {
	c.mu.Lock()
	self := BareJID(c.instances[c.instance].JID)
	c.mu.Unlock()
	if self == "" {
		return false
	}
	for _, jid := range jids {
		if BareJID(jid) == self {
			return true
		}
	}
	return false
}

// groupSubject returns the group's subject (CTX-05): cached for 10 minutes per chat, else
// fetched with POST /group/info. A failure is logged and yields "" (the stored title stays).
func (c *Connector) groupSubject(ctx context.Context, inst, chatID string) string {
	c.mu.Lock()
	cached, ok := c.titles[chatID]
	c.mu.Unlock()
	if ok && c.now().Sub(cached.at) < groupTitleTTL {
		return cached.name
	}
	_, jid, _ := SplitChatID(chatID)
	var resp struct {
		Data struct {
			Name string `json:"Name"`
		} `json:"data"`
	}
	if err := c.instanceCall(ctx, inst, http.MethodPost, "/group/info", map[string]any{"groupJid": jid}, &resp); err != nil {
		c.log.Warn("whatsapp: group info failed", "instance", inst, "err", err)
		return ""
	}
	c.cacheTitle(chatID, resp.Data.Name)
	return resp.Data.Name
}

func (c *Connector) cacheTitle(chatID, name string) {
	if name == "" {
		return
	}
	c.mu.Lock()
	if c.titles == nil {
		c.titles = map[string]groupTitle{}
	}
	c.titles[chatID] = groupTitle{name: name, at: c.now()}
	c.mu.Unlock()
}

// uses reports whether name is the single instance of this connection.
func (c *Connector) uses(name string) bool {
	return name == c.instance
}

// media opens inline content, or downloads it through evolution-go on demand; both are decoded from
// base64 as they are read (LF-04).
//
// A download ends when life does, the socket the message came on: a connection that drops does not
// wait for a download of minutes before it reconnects.
func (c *Connector) media(life context.Context, inst string, ref MediaRef) connectors.Media {
	return connectors.Media{
		Name: ref.Name, Mime: ref.Mime, Size: ref.Size,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			if ref.Inline != "" {
				r, err := decodeBase64(strings.NewReader(ref.Inline))
				if err != nil {
					return nil, err
				}
				return io.NopCloser(r), nil
			}
			ctx, cancel := context.WithCancelCause(ctx)
			stop := context.AfterFunc(life, func() { cancel(errSocketClosed) })
			r, err := c.download(ctx, inst, ref.Message, ref.Size)
			if err != nil {
				stop()
				cancel(nil)
				return nil, err
			}
			return readCloser{Reader: causeReader{r: r, ctx: ctx}, Closer: closeFunc(func() error {
				err := r.Close()
				stop()
				cancel(nil)
				return err
			})}, nil
		},
	}
}

// download calls POST /message/downloadmedia and returns the media it answers, decoded from the base64
// of the JSON as it is read: memory does not grow with the file (LF-04). evolution-go fetches the file
// from WhatsApp before it answers, so the first byte may take a while: a minute and a second per MB of
// size, when it is known; after it, the answer fails when it brings nothing for StallTimeout.
func (c *Connector) download(ctx context.Context, inst string, message json.RawMessage, size int64) (io.ReadCloser, error) {
	tok, err := c.token(ctx, inst)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"message": message})
	if err != nil {
		return nil, err
	}
	ctx, stall := connectors.WatchStall(ctx, requestTimeout+time.Duration(max(size, 0)>>20)*time.Second, connectors.StallTimeout)
	resp, err := c.do(ctx, c.downloads, tok, http.MethodPost, "/message/downloadmedia", jsonPayload(body))
	if err != nil {
		stall.Stop()
		return nil, err
	}
	rc := stall.Body(resp.Body)
	media, err := mediaContent(rc, connectors.MaxJSONResponse)
	if err != nil {
		_ = rc.Close()
		return nil, err
	}
	return readCloser{Reader: media, Closer: rc}, nil
}

// errSocketClosed is why a media download stopped: the socket its message came on closed.
var errSocketClosed = errors.New("media download: the connection to evolution-go closed")

// causeReader reads from r and, once ctx was cancelled, fails with its cause: the transport reports a
// closed connection, the cause says why it closed.
type causeReader struct {
	r   io.Reader
	ctx context.Context
}

func (c causeReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && c.ctx.Err() != nil {
		err = context.Cause(c.ctx)
	}
	return n, err
}

// closeFunc is a function as an io.Closer.
type closeFunc func() error

func (f closeFunc) Close() error { return f() }

// readCloser reads from a reader and closes what it came from.
type readCloser struct {
	io.Reader
	io.Closer
}

// Send sends the text with /send/text, quoting the replied message (WA-03), and every
// file with /send/media (WA-04). It returns the id of the first sent message and tells m.Created
// the id of each, as the platform reports every one as a message of the account (ACC-05).
// A chat of another instance is refused (CID-04): only the connection's number sends.
func (c *Connector) Send(ctx context.Context, m connectors.Outbound) (string, error) {
	inst, jid, ok := SplitChatID(m.ChatID)
	if !ok {
		return "", errors.New("invalid chat id")
	}
	if inst != c.instance {
		return "", ErrOtherInstance
	}
	first := ""
	if text := formatText(m); text != "" {
		body := map[string]any{"number": jid, "text": text}
		if m.ReplyToID != "" {
			quoted := map[string]any{"messageId": m.ReplyToID}
			if m.ReplyToSenderID != "" {
				quoted["participant"] = m.ReplyToSenderID
			}
			body["quoted"] = quoted
		}
		id, err := c.send(ctx, inst, "/send/text", body)
		if err != nil {
			return "", err
		}
		created(m, id)
		first = id
	}
	for _, f := range m.Files {
		id, err := c.sendMedia(ctx, inst, jid, f)
		if err != nil {
			return "", err
		}
		created(m, id)
		if first == "" {
			first = id
		}
	}
	return first, nil
}

// created tells m.Created the id of a message the send created.
func created(m connectors.Outbound, id string) {
	if m.Created != nil {
		m.Created(id)
	}
}

// formatText prefixes the author's name in bold (WhatsApp markup).
func formatText(m connectors.Outbound) string {
	return connectors.AuthorLine(m.Signature(), "*", "*\n", m.Text)
}

// sendMedia sends a file with /send/media: its content goes as base64 in the "url" field of the JSON,
// encoded as the request is written, with the exact length of the body when the file's size is known
// (LF-04). A request the transport sends again reopens the file.
func (c *Connector) sendMedia(ctx context.Context, inst, jid string, f connectors.OutFile) (string, error) {
	tok, err := c.token(ctx, inst)
	if err != nil {
		return "", err
	}
	// The fields but the content, whose keys json sorts: "url" goes last, after them.
	head, err := json.Marshal(map[string]any{"number": jid, "type": mediaType(f.Mime), "filename": f.Name, "caption": ""})
	if err != nil {
		return "", err
	}
	prefix := append(head[:len(head)-1:len(head)-1], `,"url":"`...)
	suffix := []byte(`"}`)
	open := func() (io.ReadCloser, error) {
		if f.Open == nil {
			return nil, errors.New("open attachment: no content")
		}
		r, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open attachment: %w", err)
		}
		return readCloser{Reader: io.MultiReader(bytes.NewReader(prefix), newBase64Reader(r), bytes.NewReader(suffix)), Closer: r}, nil
	}
	body, err := open()
	if err != nil {
		return "", err
	}
	length := int64(-1)
	if f.Size > 0 {
		length = int64(len(prefix)) + base64Len(f.Size) + int64(len(suffix))
	}
	var resp sendAnswer
	if err := c.callBody(ctx, c.uploads, tok, http.MethodPost, "/send/media", &payload{r: body, length: length, reopen: open}, &resp); err != nil {
		return "", err
	}
	return resp.Data.Info.ID, nil
}

// mediaType maps a MIME type to the /send/media type.
func mediaType(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	}
	return "document"
}

// sendAnswer is what the send endpoints answer: the id of the message created.
type sendAnswer struct {
	Data struct {
		Info struct {
			ID string `json:"ID"`
		} `json:"Info"`
	} `json:"data"`
}

func (c *Connector) send(ctx context.Context, inst, path string, body map[string]any) (string, error) {
	var resp sendAnswer
	if err := c.instanceCall(ctx, inst, http.MethodPost, path, body, &resp); err != nil {
		return "", err
	}
	return resp.Data.Info.ID, nil
}

// ListChats lists the groups of the connection's instance (WA-05) and refreshes the cached
// group subjects.
func (c *Connector) ListChats(ctx context.Context) ([]connectors.ChatInfo, error) {
	insts, err := c.refreshInstances(ctx)
	if err != nil {
		return nil, err
	}
	var out []connectors.ChatInfo
	for _, inst := range insts {
		if !c.listed(inst) {
			continue
		}
		var resp struct {
			Data []struct {
				JID  string `json:"JID"`
				Name string `json:"Name"`
			} `json:"data"`
		}
		if err := c.call(ctx, inst.Token, http.MethodGet, "/group/list", nil, &resp); err != nil {
			return nil, fmt.Errorf("instance %s: %w", inst.Name, err)
		}
		for _, g := range resp.Data {
			id := ChatID(inst.Name, BareJID(g.JID))
			c.cacheTitle(id, g.Name)
			out = append(out, connectors.ChatInfo{ID: id, Title: g.Name, Kind: "group"})
		}
	}
	return out, nil
}

// listed reports whether inst is the instance this connection uses.
func (c *Connector) listed(inst instance) bool {
	return inst.Name == c.instance
}

// refreshInstances reloads the instance list with the global key.
func (c *Connector) refreshInstances(ctx context.Context) ([]instance, error) {
	var resp struct {
		Data []instance `json:"data"`
	}
	if err := c.call(ctx, c.apiKey, http.MethodGet, "/instance/all", nil, &resp); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.instances = map[string]instance{}
	for _, inst := range resp.Data {
		c.instances[inst.Name] = inst
	}
	c.fetchedAt = c.now()
	c.mu.Unlock()
	return resp.Data, nil
}

// token returns the instance token, refreshing the cached list when it is older than
// 5 minutes or misses the instance.
func (c *Connector) token(ctx context.Context, name string) (string, error) {
	if !c.uses(name) {
		return "", ErrOtherInstance
	}
	c.mu.Lock()
	inst, ok := c.instances[name]
	fresh := c.now().Sub(c.fetchedAt) < instanceCacheTTL
	c.mu.Unlock()
	if ok && fresh {
		return inst.Token, nil
	}
	if _, err := c.refreshInstances(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	inst, ok = c.instances[name]
	c.mu.Unlock()
	if !ok || inst.Token == "" {
		return "", fmt.Errorf("instance %s not found", name)
	}
	return inst.Token, nil
}

func (c *Connector) instanceCall(ctx context.Context, inst, method, path string, body, out any) error {
	tok, err := c.token(ctx, inst)
	if err != nil {
		return err
	}
	return c.call(ctx, tok, method, path, body, out)
}

// call performs a REST call with key as apikey. Errors never carry the URL, the key or
// the response body (which may echo message content).
func (c *Connector) call(ctx context.Context, key, method, path string, body, out any) error {
	return c.callWith(ctx, c.client, key, method, path, body, out)
}

// mediaEnvelope bounds a JSON answer or WebSocket frame carrying media of up to limit bytes
// as base64, plus the JSON around it (S8).
func mediaEnvelope(limit int64) int64 {
	return (limit+2)/3*4 + connectors.MaxJSONResponse
}

// payload is the body of a request: its reader, its length (-1 when unknown) and, for a body that is
// streamed, how to open it again when the transport sends the request again.
type payload struct {
	r      io.Reader
	length int64
	reopen func() (io.ReadCloser, error)
}

// jsonPayload is a body held in memory: a small JSON request.
func jsonPayload(data []byte) *payload {
	return &payload{r: bytes.NewReader(data), length: int64(len(data)), reopen: func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}}
}

// callWith is call through the given client.
func (c *Connector) callWith(ctx context.Context, client *http.Client, key, method, path string, body, out any) error {
	var p *payload
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		p = jsonPayload(data)
	}
	return c.callBody(ctx, client, key, method, path, p, out)
}

// callBody performs a call with the given body and decodes its JSON answer into out.
func (c *Connector) callBody(ctx context.Context, client *http.Client, key, method, path string, body *payload, out any) error {
	resp, err := c.do(ctx, client, key, method, path, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		if _, ok := out.(bestEffort); ok {
			return nil
		}
		return fmt.Errorf("%s %s: invalid response (%s)", method, path, decodeReason(err))
	}
	return nil
}

// do sends a request with key as apikey and returns the response of a success status, whose body the
// caller closes. Errors never carry the URL, the key or the response body (which may echo message
// content).
func (c *Connector) do(ctx context.Context, client *http.Client, key, method, path string, body *payload) (*http.Response, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	var rd io.Reader
	if body != nil {
		rd = body.r
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		if rc, ok := rd.(io.Closer); ok {
			_ = rc.Close()
		}
		return nil, errors.New("invalid request")
	}
	if body != nil {
		req.ContentLength = body.length
		if body.length < 0 {
			req.ContentLength = -1
		}
		req.GetBody = body.reopen
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("apikey", key)
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, plainError(err))
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		_ = resp.Body.Close()
		return nil, ErrUnauthorized
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s %s: status %d", method, path, resp.StatusCode)
	}
	return resp, nil
}

// bestEffort marks the destination of an answer the caller can do without: a body that is empty or
// not what is expected is then no failure of a call evolution-go answered with a success status.
type bestEffort interface{ bestEffort() }

// decodeReason describes a JSON decode failure without echoing response content: the field
// and the types for a type mismatch, otherwise only that the body is not the expected JSON.
func decodeReason(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return fmt.Sprintf("field %s: got %s, want %s", te.Field, te.Value, te.Type)
	}
	return "not the expected JSON"
}

// wsURL is the broadcast WebSocket URL: no instanceId, the global key as token.
func (c *Connector) wsURL() string {
	u := *c.base
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws"
	u.RawQuery = url.Values{"token": {c.apiKey}}.Encode()
	return u.String()
}

// plainError drops the URL (which may hold the key) from transport errors.
func plainError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
