package whatsapp

import (
	"bytes"
	"context"
	"encoding/base64"
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
		Name: KindName, Label: "WhatsApp (evolution-go)", CanListChats: true,
		Fields: []connectors.Field{
			{Name: "base_url", Label: "evolution-go URL", Type: dto.FieldTypeURL, Required: true, Endpoint: true,
				Help: "e.g. https://evolution.example.com"},
			{Name: "api_key", Label: "Global API key", Type: dto.FieldTypeSecret, Required: true},
			{Name: "instances", Label: "Instances", Type: dto.FieldTypeStringList, Required: true,
				Help: "exactly one instance: the number of this connection"},
		},
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
	downloads *http.Client // /message/downloadmedia: JSON carrying base64 media
	stream    *http.Client // WebSocket handshakes
	maxFrame  int64        // WebSocket frames carry inline base64 media
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
		client: d.JSONClient(requestTimeout), downloads: d.HTTPClient(requestTimeout, mediaEnvelope(d.MediaLimit())),
		stream: d.StreamClient(), maxFrame: mediaEnvelope(d.MediaLimit()), now: time.Now, wait: sleep,
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

	ctx, cancel := context.WithCancelCause(ctx)
	var watching sync.WaitGroup
	defer watching.Wait() // its last report comes before Run returns, so before the supervisor's
	defer cancel(nil)
	go c.ping(ctx, conn, func() { cancel(nil) })
	watching.Go(func() {
		if err := c.watch(ctx, h, first); err != nil {
			cancel(err)
		}
	})
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			return fmt.Errorf("websocket: %w", plainError(err))
		}
		c.handle(ctx, h, data)
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
func (c *Connector) handle(ctx context.Context, h connectors.Handler, data []byte) {
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
	if len(p.MentionedJIDs) > 0 {
		p.Inbound.MentionsAccount = c.mentions(p.MentionedJIDs)
	}
	if p.Media != nil {
		p.Inbound.Media = []connectors.Media{c.media(p.Instance, *p.Media)}
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

// media opens inline content, or downloads it through evolution-go on demand.
func (c *Connector) media(inst string, ref MediaRef) connectors.Media {
	return connectors.Media{
		Name: ref.Name, Mime: ref.Mime, Size: ref.Size,
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			content := ref.Inline
			if content == "" {
				var err error
				if content, err = c.download(ctx, inst, ref.Message); err != nil {
					return nil, err
				}
			}
			data, err := base64.StdEncoding.DecodeString(stripDataURL(content))
			if err != nil {
				return nil, errors.New("media: invalid base64")
			}
			return io.NopCloser(bytes.NewReader(data)), nil
		},
	}
}

// download calls POST /message/downloadmedia and returns the base64 content.
func (c *Connector) download(ctx context.Context, inst string, message json.RawMessage) (string, error) {
	var resp struct {
		Data json.RawMessage `json:"data"`
	}
	tok, err := c.token(ctx, inst)
	if err != nil {
		return "", err
	}
	if err := c.callWith(ctx, c.downloads, tok, http.MethodPost, "/message/downloadmedia", map[string]any{"message": message}, &resp); err != nil {
		return "", err
	}
	if s := str(resp.Data); s != "" {
		return s, nil
	}
	if s := str(obj(resp.Data)["base64"]); s != "" {
		return s, nil
	}
	return "", errors.New("downloadmedia: no content")
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
		content, err := readFile(f)
		if err != nil {
			return "", err
		}
		id, err := c.send(ctx, inst, "/send/media", map[string]any{
			"number": jid, "type": mediaType(f.Mime), "filename": f.Name, "caption": "",
			"url": base64.StdEncoding.EncodeToString(content),
		})
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

func readFile(f connectors.OutFile) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open attachment: %w", err)
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
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

func (c *Connector) send(ctx context.Context, inst, path string, body map[string]any) (string, error) {
	var resp struct {
		Data struct {
			Info struct {
				ID string `json:"ID"`
			} `json:"Info"`
		} `json:"data"`
	}
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

// callWith is call through the given client.
func (c *Connector) callWith(ctx context.Context, client *http.Client, key, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return errors.New("invalid request")
	}
	req.Header.Set("apikey", key)
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, plainError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("%s %s: status %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: invalid response (%s)", method, path, decodeReason(err))
	}
	return nil
}

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
