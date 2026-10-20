// Package webhook is the generic webhook connector: any system posts signed messages to
// POST /api/v1/hooks/{connector_id} and receives room messages as signed POSTs.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/safehttp"
	"github.com/wnexous/partyline/internal/validation"
)

// KindName is the connector kind name.
const KindName = "webhook"

// Signing and limits (HOOK-01, HOOK-02).
const (
	// TimestampHeader carries the Unix time in seconds of the request.
	TimestampHeader = "X-Partyline-Timestamp"
	// SignatureHeader carries "sha256=" + hex(HMAC-SHA256(secret, timestamp + "." + body)).
	SignatureHeader = "X-Partyline-Signature"
	// MaxSkew is the largest accepted distance between the timestamp and the clock.
	MaxSkew = 5 * time.Minute
	// MaxBody is the largest accepted request body.
	MaxBody = connectors.MaxHookBody
	// RateLimit is the number of authenticated requests accepted per connector and minute
	// (HOOK-04).
	RateLimit = 60
	// InvalidRateLimit is the number of requests with a bad signature or timestamp accepted
	// per client IP, connector and minute; beyond it they get ErrRateLimited (HOOK-04).
	InvalidRateLimit = 30
	// maxTrackedIPs bounds the per-IP windows kept in memory.
	maxTrackedIPs = 10000

	requestTimeout = 30 * time.Second
)

// Kind returns the webhook connector kind. An empty secret is generated on create.
func Kind() connectors.Kind {
	return connectors.Kind{
		Name: KindName, Label: "Webhook", InboundHook: true, LinkOwnerOnly: true,
		Fields: []connectors.Field{
			{Name: "secret", Label: "Signing secret", Type: dto.FieldTypeSecret, Required: true,
				Help: "Leave empty to generate one; it is shown once"},
			{Name: "outbound_url", Label: "Outbound URL", Type: dto.FieldTypeURL,
				Help: "Room messages for this connector's chats are POSTed here"},
		},
		Prepare: prepare,
		New: func(cfg connectors.Config, d connectors.Deps) (connectors.Connector, error) {
			return New(cfg, d), nil
		},
	}
}

// prepare generates the secret when it is left empty.
func prepare(cfg connectors.Config) (connectors.Config, []string) {
	if s, ok := cfg["secret"].(string); ok && strings.TrimSpace(s) != "" {
		return cfg, nil
	}
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	cfg["secret"] = hex.EncodeToString(buf)
	return cfg, []string{"secret"}
}

// Sign returns the signature header value of body sent at timestamp.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Connector is one webhook endpoint pair.
type Connector struct {
	id          string
	secret      string
	outboundURL string
	userAgent   string
	fileURL     func(string) string
	log         *slog.Logger
	client      *http.Client
	now         func() time.Time

	mu      sync.Mutex
	run     *running
	window  window            // authenticated requests
	invalid map[string]window // invalid requests by client IP
}

// window counts requests in a one-minute window.
type window struct {
	start time.Time
	count int
}

// add counts one request at now and reports whether it is within limit.
func (w *window) add(now time.Time, limit int) bool {
	if now.Sub(w.start) >= time.Minute {
		w.start, w.count = now, 0
	}
	w.count++
	return w.count <= limit
}

type running struct {
	ctx context.Context
	h   connectors.Handler
}

var (
	_ connectors.Connector    = (*Connector)(nil)
	_ connectors.HookReceiver = (*Connector)(nil)
)

// New returns a connector for a validated config.
func New(cfg connectors.Config, d connectors.Deps) *Connector {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Connector{
		id: d.ID, secret: cfg.String("secret"), outboundURL: cfg.String("outbound_url"), userAgent: d.UserAgent,
		fileURL: d.FileURL, log: log, client: safehttp.NewClient(safehttp.Options{AllowPrivate: d.AllowPrivateNetworks, Timeout: requestTimeout}), now: time.Now,
		invalid: map[string]window{},
	}
}

// Run accepts hook requests until ctx ends; there is no connection to keep.
func (c *Connector) Run(ctx context.Context, h connectors.Handler) error {
	c.mu.Lock()
	c.run = &running{ctx: ctx, h: h}
	c.mu.Unlock()
	h.Status(connectors.StatusConnected, "")
	<-ctx.Done()
	c.mu.Lock()
	c.run = nil
	c.mu.Unlock()
	return ctx.Err()
}

// payload is an inbound hook message (HOOK-01).
type payload struct {
	ChatID     string `json:"chat_id"`
	ChatTitle  string `json:"chat_title"`
	SenderID   string `json:"sender_id"`
	SenderName string `json:"sender_name"`
	Text       string `json:"text"`
	MessageID  string `json:"message_id"`
}

// ReceiveHook verifies the timestamp (±5 minutes) and the signature with a constant-time
// compare (HOOK-02), then counts the request toward the connector's budget of authenticated
// requests (HOOK-04) and hands the message to the running handler (HOOK-01). Invalid
// requests never touch that budget; they have their own per-IP budget, beyond which they
// get ErrRateLimited instead of ErrBadSignature. Nothing is handled when a check fails.
func (c *Connector) ReceiveHook(ctx context.Context, r connectors.HookRequest) error {
	if !c.validTimestamp(r.Timestamp) ||
		!hmac.Equal([]byte(Sign(c.secret, r.Timestamp, r.Body)), []byte(strings.TrimSpace(r.Signature))) {
		if !c.allowInvalid(r.RemoteIP) {
			return connectors.ErrRateLimited
		}
		return connectors.ErrBadSignature
	}
	if !c.allow() {
		return connectors.ErrRateLimited
	}
	var p payload
	if err := json.Unmarshal(r.Body, &p); err != nil {
		return &validation.Error{Fields: map[string]string{"body": "must be a JSON object"}}
	}
	check := validation.Check{}
	for field, v := range map[string]string{"chat_id": p.ChatID, "sender_id": p.SenderID, "text": p.Text} {
		if strings.TrimSpace(v) == "" {
			check.Add(field, "required")
		}
	}
	if err := check.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	run := c.run
	c.mu.Unlock()
	if run == nil {
		return connectors.ErrNotRunning
	}
	run.h.Inbound(ctx, connectors.Inbound{
		ChatID: p.ChatID, ChatTitle: p.ChatTitle, ChatKind: string(dto.ChatKindChannel), MessageID: p.MessageID,
		SenderID: p.SenderID, SenderName: p.SenderName, Kind: string(dto.MessageKindText), Text: p.Text, At: c.now(),
	})
	return nil
}

func (c *Connector) validTimestamp(ts string) bool {
	sec, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil {
		return false
	}
	d := c.now().Sub(time.Unix(sec, 0))
	return d <= MaxSkew && d >= -MaxSkew
}

// allow counts an authenticated request in the connector's one-minute window.
func (c *Connector) allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.window.add(c.now(), RateLimit)
}

// allowInvalid counts an invalid request of the client IP in its one-minute window.
func (c *Connector) allowInvalid(ip string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if _, ok := c.invalid[ip]; !ok && len(c.invalid) >= maxTrackedIPs {
		for k, w := range c.invalid {
			if now.Sub(w.start) >= time.Minute {
				delete(c.invalid, k)
			}
		}
		if len(c.invalid) >= maxTrackedIPs {
			return false
		}
	}
	w := c.invalid[ip]
	ok := w.add(now, InvalidRateLimit)
	c.invalid[ip] = w
	return ok
}

// outMessage is the outbound payload (HOOK-03).
type outMessage struct {
	Event       string          `json:"event"`
	ConnectorID string          `json:"connector_id"`
	ChatID      string          `json:"chat_id"`
	Message     outMessageBody  `json:"message"`
	Attachments []outAttachment `json:"attachments"`
}

type outMessageBody struct {
	ID                string    `json:"id"`
	RoomID            string    `json:"room_id"`
	Text              string    `json:"text"`
	ReplyToExternalID string    `json:"reply_to_external_id,omitempty"`
	AuthorName        string    `json:"author_name"`
	CreatedAt         time.Time `json:"created_at"`
}

type outAttachment struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

// Send POSTs the message as signed JSON to the outbound URL; any non-2xx answer is a
// failure (HOOK-03). Attachments are expiring signed links. The returned id is the
// "message_id" of the answer when it has one, else the room message id.
func (c *Connector) Send(ctx context.Context, m connectors.Outbound) (string, error) {
	if c.outboundURL == "" {
		return "", errors.New("no outbound URL configured")
	}
	msg := outMessage{
		Event: "message", ConnectorID: c.id, ChatID: m.ChatID, Attachments: []outAttachment{},
		Message: outMessageBody{
			ID: m.MessageID, RoomID: m.RoomID, Text: m.Text, ReplyToExternalID: m.ReplyToID,
			AuthorName: m.AuthorName, CreatedAt: m.CreatedAt,
		},
	}
	for _, f := range m.Files {
		link := ""
		if c.fileURL != nil {
			link = c.fileURL(f.ID)
		}
		msg.Attachments = append(msg.Attachments, outAttachment{Name: f.Name, Mime: f.Mime, Size: f.Size, URL: link})
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	ts := strconv.FormatInt(c.now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.outboundURL, bytes.NewReader(body))
	if err != nil {
		return "", errors.New("outbound: invalid URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set(TimestampHeader, ts)
	req.Header.Set(SignatureHeader, Sign(c.secret, ts, body))
	resp, err := c.client.Do(req)
	if errors.Is(err, safehttp.ErrBlockedAddress) {
		return "", errors.New("outbound: destination address not allowed")
	}
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", fmt.Errorf("outbound: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("outbound: status %d", resp.StatusCode)
	}
	var ack struct {
		MessageID string `json:"message_id"`
	}
	if data, _ := io.ReadAll(io.LimitReader(resp.Body, MaxBody)); json.Unmarshal(data, &ack) == nil && ack.MessageID != "" {
		return ack.MessageID, nil
	}
	return m.MessageID, nil
}
