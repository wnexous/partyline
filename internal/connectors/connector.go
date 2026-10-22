// Package connectors is the framework every chat platform plugs into (AD-007): the kind
// registry with its declarative config schema, the connector service and endpoints' logic,
// the manager that supervises running connectors, the inbound pipeline that turns platform
// messages into room messages and the outbound sender used by the outbox worker.
package connectors

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/wnexous/partyline/internal/connectors/safehttp"
)

// Connector is one running platform account. Implementations live in their own package
// (internal/connectors/<kind>) and are created by their Kind's New.
type Connector interface {
	// Run connects and blocks until ctx is done or the transport fails. It reports
	// StatusConnected through h once the platform accepted the connection and hands every
	// received message to h.Inbound. A returned error is shown (redacted) as the connector's
	// status and Run is called again after a backoff.
	Run(ctx context.Context, h Handler) error
	// Send delivers a message to a chat and returns the platform message id.
	Send(ctx context.Context, m Outbound) (externalID string, err error)
}

// Reactor is implemented by connectors that can react to a chat message with an emoji
// (REACT-01). Connectors without it fail reaction deliveries with ErrReactionsUnsupported.
type Reactor interface {
	React(ctx context.Context, r Reaction) error
}

// Reaction is an emoji reaction to a message of a chat.
type Reaction struct {
	// ChatID is the platform chat id.
	ChatID string
	// TargetID is the platform id of the message reacted to; TargetSenderID is its sender's
	// platform id (empty when Partyline sent that message).
	TargetID       string
	TargetSenderID string
	Emoji          string
	// MessageID, RoomID, AuthorName and CreatedAt describe the room's reaction message, for
	// platforms that show them (the webhook payload).
	MessageID  string
	RoomID     string
	AuthorName string
	CreatedAt  time.Time
}

// ChatLister is implemented by connectors whose platform can list chats (CON-09).
type ChatLister interface {
	ListChats(ctx context.Context) ([]ChatInfo, error)
}

// Handler receives what a running connector observes.
type Handler interface {
	// Inbound handles one received message; it never fails from the connector's view.
	Inbound(ctx context.Context, m Inbound)
	// Status reports a status change with a detail text (an error message for StatusError).
	Status(s Status, detail string)
	// MigrateChat moves a chat to a new platform id (a Telegram group upgraded to a
	// supergroup), keeping its rooms. It does nothing when the old id is unknown or the new
	// id is already a chat.
	MigrateChat(ctx context.Context, oldID, newID string)
	// Account reports how the platform names the connector's own account ("@bot",
	// "+•••• 1234"), which the operator's contact page shows (ACC-07). Connectors call it once
	// connected and again when the name changes; an empty label clears it.
	Account(label string)
}

// Status is the runtime state of a connector; the values match dto.ConnectorStatus.
type Status string

// Connector states.
const (
	StatusStopped    Status = "stopped"
	StatusConnecting Status = "connecting"
	StatusConnected  Status = "connected"
	StatusError      Status = "error"
)

// Inbound is a message received from a platform, normalized.
type Inbound struct {
	// ChatID is the platform chat id; ChatTitle and ChatKind (group, dm, channel) describe it.
	ChatID    string
	ChatTitle string
	ChatKind  string
	// MessageID is the platform message id, used to drop repeats (CON-10).
	MessageID string
	// SenderID, SenderAltID and SenderName identify the person inside the chat.
	SenderID    string
	SenderAltID string
	SenderName  string
	// FromSelf marks messages sent by the connector's own account (CON-12).
	FromSelf bool
	// Kind is a dto.MessageKind value (text, image, ...); empty means text.
	Kind string
	Text string
	// ReplyToID is the platform id of the message this one replies to.
	ReplyToID string
	Media     []Media
	At        time.Time
}

// Media is an inbound file. Open is called at most once, by the inbound pipeline, and only
// when the message is routed to a room.
type Media struct {
	Name string
	Mime string
	// Size is the size in bytes when known in advance, else 0.
	Size int64
	Open func(ctx context.Context) (io.ReadCloser, error)
}

// Outbound is a room message to deliver to one chat.
type Outbound struct {
	// ChatID is the platform chat id.
	ChatID string
	Text   string
	// ReplyToID and ReplyToSenderID quote a message that came from (or was sent to) the
	// same chat; both are empty otherwise.
	ReplyToID       string
	ReplyToSenderID string
	Files           []OutFile
	// MessageID, RoomID, AuthorName and CreatedAt describe the room message, for platforms
	// that show them (e.g. the webhook payload).
	MessageID  string
	RoomID     string
	AuthorName string
	CreatedAt  time.Time
	// Created, when set, is called with the platform id of each message the send created: the
	// parts of a long text and every file, not only the first. A platform that reports the
	// account's own messages echoes them all back (ACC-05).
	Created func(platformID string)
	// Unsigned tells that the author operates the connector's account, so a platform that
	// puts the author in the text leaves the author line out (ACC-03, ACC-04). AuthorName still
	// names the author for platforms that carry it as data.
	Unsigned bool
}

// Signature is the author line to put in the text: the author's name, unless the author is the
// account's operator.
func (o Outbound) Signature() string {
	if o.Unsigned {
		return ""
	}
	return o.AuthorName
}

// OutFile is an attachment of an outbound message; Open returns its content.
type OutFile struct {
	ID   string
	Name string
	Mime string
	Size int64
	Open func() (io.ReadCloser, error)
}

// ChatInfo is a chat listed by a ChatLister.
type ChatInfo struct {
	ID    string
	Title string
	Kind  string
}

// Deps are what a Kind's New receives besides the config.
type Deps struct {
	// ID is the connector id.
	ID string
	// Logger is scoped to the connector; never log message text or secrets with it.
	Logger *slog.Logger
	// UserAgent is "partyline/<version>" for platform HTTP calls.
	UserAgent string
	// FileURL returns a signed download link of a stored attachment, valid 10 minutes, for
	// platforms that fetch files by URL (the webhook payload); nil when unavailable.
	FileURL func(attachmentID string) string
	// AllowPrivateNetworks lets the connector reach non-public addresses, and
	// PrivateAllowlist the listed ones only; HTTPClient and StreamClient apply both
	// (CON-13).
	AllowPrivateNetworks bool
	PrivateAllowlist     *safehttp.Allowlist
	// MaxMediaBytes is the upload limit (PARTYLINE_MAX_UPLOAD_MB), which bounds media
	// downloads; 0 means DefaultMaxMedia.
	MaxMediaBytes int64
	// Replays remembers accepted hook signatures across restarts (HOOK-05); nil for kinds
	// without InboundHook or in tests.
	Replays Replays
}

// Response caps (CON-13, S8).
const (
	// MaxJSONResponse bounds a JSON/API answer of a platform.
	MaxJSONResponse = 4 << 20
	// DefaultMaxMedia is the media cap when Deps.MaxMediaBytes is unset (the default
	// upload limit).
	DefaultMaxMedia = 25 << 20
)

// MediaLimit is the largest media download: the upload limit.
func (d Deps) MediaLimit() int64 {
	if d.MaxMediaBytes > 0 {
		return d.MaxMediaBytes
	}
	return DefaultMaxMedia
}

// HTTPClient returns the client for request/response platform calls: the network policy
// of the deps, no redirects, the timeout (0: none) and a response cap (CON-13).
func (d Deps) HTTPClient(timeout time.Duration, maxResponse int64) *http.Client {
	return safehttp.NewClient(safehttp.Options{
		AllowPrivate: d.AllowPrivateNetworks, Allowlist: d.PrivateAllowlist, Timeout: timeout, MaxResponseBytes: maxResponse,
	})
}

// JSONClient returns a client for JSON/API calls, capped at MaxJSONResponse (S8).
func (d Deps) JSONClient(timeout time.Duration) *http.Client {
	return d.HTTPClient(timeout, MaxJSONResponse)
}

// MediaClient returns a client for media downloads, capped at the upload limit (S8).
func (d Deps) MediaClient(timeout time.Duration) *http.Client {
	return d.HTTPClient(timeout, d.MediaLimit())
}

// StreamClient returns the client for WebSocket handshakes under the same network policy.
func (d Deps) StreamClient() *http.Client {
	return safehttp.NewStreamClient(d.AllowPrivateNetworks, d.PrivateAllowlist)
}

// MaxHookBody is the largest accepted body of POST /api/v1/hooks/{connector_id}.
const MaxHookBody = 64 << 10

// HookRequest is a request to POST /api/v1/hooks/{connector_id}.
type HookRequest struct {
	// Timestamp and Signature are the X-Partyline-Timestamp and X-Partyline-Signature headers.
	Timestamp string
	Signature string
	Body      []byte
	// RemoteIP is the client address, used to rate-limit invalid requests per IP.
	RemoteIP string
}

// HookReceiver is implemented by connectors of kinds with InboundHook: they receive
// messages over HTTP instead of a platform connection.
type HookReceiver interface {
	// ReceiveHook checks and handles one request. It returns ErrBadSignature,
	// ErrRateLimited, ErrReplayed, ErrNotRunning, a *validation.Error for a bad payload, or nil when the
	// message was accepted.
	ReceiveHook(ctx context.Context, r HookRequest) error
}

// Errors of HookReceiver.
var (
	// ErrBadSignature means the signature or the timestamp of a hook request is invalid.
	ErrBadSignature = errors.New("invalid signature or timestamp")
	// ErrRateLimited means a hook received too many requests.
	ErrRateLimited = errors.New("too many requests")
	// ErrReplayed means a hook request with the same signature was already accepted.
	ErrReplayed = errors.New("request already received")
)
