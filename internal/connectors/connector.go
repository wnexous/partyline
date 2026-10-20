// Package connectors is the framework every chat platform plugs into (AD-007): the kind
// registry with its declarative config schema, the connector service and endpoints' logic,
// the manager that supervises running connectors, the inbound pipeline that turns platform
// messages into room messages and the outbound sender used by the outbox worker.
package connectors

import (
	"context"
	"io"
	"log/slog"
	"time"
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
}
