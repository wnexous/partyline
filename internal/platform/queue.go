// Package platform is the delivery queue of platform messages (AD-015): what arrives on a
// connection is never stored as a conversation message, it waits here for the AI that operates
// the connection until that AI's session acknowledges it. The package owns platform_items and
// platform_attachments: queueing (with the per-AI bound and the dedupe), reading, acknowledging,
// handing a connection's items to a new operator and expiring them.
package platform

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// MaxQueued is the most unacknowledged items one AI keeps: a new item over the bound drops the
// oldest and counts it (CID-11).
const MaxQueued = 5000

// ErrNotFound means the item does not exist or belongs to another AI.
var ErrNotFound = errors.New("platform item not found")

// Deps are the collaborators of the Queue.
type Deps struct {
	Store *store.Store
	Bus   *events.Bus
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Queue is the platform queue of every AI.
type Queue struct {
	d Deps
}

// New returns a Queue.
func New(d Deps) *Queue {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Queue{d: d}
}

// Attachment is a file of a queued item. SHA256 names its blob in the attachment store; it is
// empty for a file that was not stored (too large or unavailable).
type Attachment struct {
	ID     string
	Name   string
	Mime   string
	Size   int64
	SHA256 string
	Status dto.AttachmentStatus
}

// Item is a platform message to queue for the AI that operates its connection.
type Item struct {
	AgentID     string
	ConnectorID string
	// ChatID is Partyline's id of the chat (a chat of ConnectorID), not the platform's.
	ChatID string
	// ExternalID is the platform's id of the message; empty when the platform gives none.
	ExternalID string
	SenderID   string
	SenderName string
	Trusted    bool
	FromSelf   bool
	// Kind defaults to a text.
	Kind dto.MessageKind
	Text string
	// ReceivedAt defaults to now.
	ReceivedAt  time.Time
	Attachments []Attachment
}

// Event is a queued item as the AI's session receives it.
type Event struct {
	// Seq is the position in the AI's queue: delivery order, and the cursor of a session.
	Seq int64
	// ID is the Partyline id of the item, the id the AI answers with (reply).
	ID           string
	Platform     string
	ConnectionID string
	ChatID       string
	ChatTitle    string
	ChatKind     dto.ChatKind
	// ExternalID is the platform's id of the message, which a reply quotes.
	ExternalID  string
	SenderName  string
	SenderID    string
	Trusted     bool
	FromSelf    bool
	Kind        dto.MessageKind
	Text        string
	Attachments []Attachment
	ReceivedAt  time.Time
	// AckedAt is zero while the session has not acknowledged the item.
	AckedAt time.Time
}

// Enqueue queues the item for its AI and wakes the AI's live session. The same platform
// message of one chat of one connection queues once (CID-26): a repeat reports queued=false and
// changes nothing. When the AI then holds more than MaxQueued unacknowledged items, the oldest
// are deleted and counted as dropped (CID-11).
func (q *Queue) Enqueue(ctx context.Context, it Item) (id string, queued bool, err error) {
	id = store.NewID()
	received := it.ReceivedAt
	if received.IsZero() {
		received = q.d.Now()
	}
	kind := it.Kind
	if kind == "" {
		kind = dto.MessageKindText
	}
	var seq int64
	err = q.d.Store.WithTx(ctx, func(tx *db.Queries) error {
		var err error
		seq, err = tx.InsertPlatformItem(ctx, db.InsertPlatformItemParams{
			ID: id, AgentID: it.AgentID, ConnectorID: it.ConnectorID, ChatID: it.ChatID, ExternalID: it.ExternalID,
			SenderID: it.SenderID, SenderName: it.SenderName, Trusted: it.Trusted, FromSelf: it.FromSelf,
			Kind: string(kind), Text: it.Text, ReceivedAt: received.UnixMilli(),
		})
		if errors.Is(err, sql.ErrNoRows) {
			return nil // already queued
		}
		if err != nil {
			return err
		}
		queued = true
		for _, a := range it.Attachments {
			if err := tx.InsertPlatformAttachment(ctx, db.InsertPlatformAttachmentParams{
				ID: store.NewID(), ItemID: id, Sha256: a.SHA256, Name: a.Name, Mime: a.Mime, Size: a.Size, Status: string(a.Status),
			}); err != nil {
				return err
			}
		}
		return trim(ctx, tx, it.AgentID)
	})
	if err != nil || !queued {
		return "", false, err
	}
	q.d.Bus.Publish(events.PlatformQueued{AgentID: it.AgentID, ItemID: id, Seq: seq})
	return id, true, nil
}

// Has reports whether the platform message of the chat of the connection is queued, or was
// acknowledged and not yet purged: callers skip downloading the files of a repeat (CID-26). A
// message without a platform id is never a repeat.
func (q *Queue) Has(ctx context.Context, connectorID, chatID, externalID string) (bool, error) {
	if externalID == "" {
		return false, nil
	}
	return q.d.Store.R().PlatformItemExists(ctx, db.PlatformItemExistsParams{ConnectorID: connectorID, ChatID: chatID, ExternalID: externalID})
}

// trim deletes the oldest unacknowledged items of the AI over MaxQueued and adds them to its
// dropped counter.
func trim(ctx context.Context, tx *db.Queries, agentID string) error {
	n, err := tx.DeleteUnackedPlatformItemsOverCap(ctx, db.DeleteUnackedPlatformItemsOverCapParams{AgentID: agentID, Cap: MaxQueued})
	if err != nil || n == 0 {
		return err
	}
	return tx.AddPlatformDropped(ctx, db.AddPlatformDroppedParams{N: n, AgentID: agentID})
}

// Unacked returns the AI's unacknowledged items after seq, oldest first, at most limit (CID-08).
func (q *Queue) Unacked(ctx context.Context, agentID string, afterSeq int64, limit int) ([]Event, error) {
	var out []Event
	err := q.d.Store.ReadTx(ctx, func(tx *db.Queries) error {
		rows, err := tx.ListUnackedPlatformItems(ctx, db.ListUnackedPlatformItemsParams{AgentID: agentID, AfterSeq: afterSeq, Limit: int64(limit)})
		if err != nil {
			return err
		}
		out = make([]Event, len(rows))
		ids := make([]string, len(rows))
		for i, r := range rows {
			out[i] = event(r.PlatformItem, r.ChatTitle, r.ChatKind, r.Platform)
			ids[i] = r.PlatformItem.ID
		}
		return attach(ctx, tx, out, ids)
	})
	return out, err
}

// Get returns an item of the AI, acknowledged or not: ErrNotFound for an unknown item and for
// one that belongs to another AI. An acknowledged item has no text (CID-09).
func (q *Queue) Get(ctx context.Context, agentID, itemID string) (Event, error) {
	var out []Event
	err := q.d.Store.ReadTx(ctx, func(tx *db.Queries) error {
		r, err := tx.GetPlatformItemForAgent(ctx, db.GetPlatformItemForAgentParams{ID: itemID, AgentID: agentID})
		if err != nil {
			return err
		}
		out = []Event{event(r.PlatformItem, r.ChatTitle, r.ChatKind, r.Platform)}
		return attach(ctx, tx, out, []string{itemID})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	return out[0], nil
}

// Ack records that the AI's session received the item and deletes its text at once (CID-09).
// The row stays, and so do its files, until Purge removes them. It is idempotent: a second ack,
// an unknown item and another AI's item change nothing and are not errors.
func (q *Queue) Ack(ctx context.Context, agentID, itemID string) error {
	at := sql.NullInt64{Int64: q.d.Now().UnixMilli(), Valid: true}
	return q.d.Store.WithTx(ctx, func(tx *db.Queries) error {
		_, err := tx.AckPlatformItem(ctx, db.AckPlatformItemParams{AckedAt: at, ID: itemID, AgentID: agentID})
		return err
	})
}

// TakeDropped returns how many items the AI lost to the bound or to expiry since the last call
// and resets the count: the next connection of its session tells it (CID-10).
func (q *Queue) TakeDropped(ctx context.Context, agentID string) (int64, error) {
	var n int64
	err := q.d.Store.WithTx(ctx, func(tx *db.Queries) error {
		var err error
		if n, err = tx.GetPlatformDropped(ctx, agentID); errors.Is(err, sql.ErrNoRows) {
			n = 0
			return nil
		} else if err != nil || n == 0 {
			return err
		}
		return tx.ResetPlatformDropped(ctx, agentID)
	})
	return n, err
}

// MoveConnector hands every item of the connection, acknowledged or not, to the AI toAgent
// inside the caller's transaction (CID-05) and returns how many changed hands. The new operator
// keeps the bound too: the oldest unacknowledged items over MaxQueued are deleted and counted as
// dropped (CID-11). The caller announces the move after it commits.
func (q *Queue) MoveConnector(ctx context.Context, tx *db.Queries, connectorID, toAgent string) (int64, error) {
	moved, err := tx.MovePlatformItems(ctx, db.MovePlatformItemsParams{AgentID: toAgent, ConnectorID: connectorID})
	if err != nil || moved == 0 {
		return 0, err
	}
	return moved, trim(ctx, tx, toAgent)
}

// event renders a stored item.
func event(it db.PlatformItem, chatTitle, chatKind, platform string) Event {
	e := Event{
		Seq: it.Seq, ID: it.ID, Platform: platform, ConnectionID: it.ConnectorID, ChatID: it.ChatID,
		ChatTitle: chatTitle, ChatKind: dto.ChatKind(chatKind), ExternalID: it.ExternalID,
		SenderName: it.SenderName, SenderID: it.SenderID, Trusted: it.Trusted, FromSelf: it.FromSelf,
		Kind: dto.MessageKind(it.Kind), Text: it.Text, ReceivedAt: time.UnixMilli(it.ReceivedAt).UTC(),
		Attachments: []Attachment{},
	}
	if it.AckedAt.Valid {
		e.AckedAt = time.UnixMilli(it.AckedAt.Int64).UTC()
	}
	return e
}

// attach reads the files of the items with ids and sets them on the matching events (ids[i]
// belongs to events[i]).
func attach(ctx context.Context, tx *db.Queries, events []Event, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	files, err := tx.ListPlatformAttachmentsForItems(ctx, ids)
	if err != nil {
		return err
	}
	at := make(map[string]int, len(ids))
	for i, id := range ids {
		at[id] = i
	}
	for _, f := range files {
		i, ok := at[f.ItemID]
		if !ok {
			continue
		}
		events[i].Attachments = append(events[i].Attachments, Attachment{
			ID: f.ID, Name: f.Name, Mime: f.Mime, Size: f.Size, SHA256: f.Sha256, Status: dto.AttachmentStatus(f.Status),
		})
	}
	return nil
}
