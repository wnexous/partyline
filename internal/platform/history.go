package platform

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Chat history limits (CH-01, CH-05).
const (
	// MaxHistoryDays is the longest retention of a connection's chat history in days; HistoryForever
	// keeps it with no age limit and HistoryOff keeps none (CH-01).
	MaxHistoryDays = 365
	HistoryForever = -1
	HistoryOff     = 0
	// HistoryPageDefault and HistoryPageMax bound one page of a chat's history.
	HistoryPageDefault = 20
	HistoryPageMax     = 100

	// historyBatch bounds each delete of the history, so the single writer is never held for long.
	historyBatch = 1000
	day          = 24 * time.Hour
)

// ErrHistoryCursor means the message to page before is not a kept message of the chat.
var ErrHistoryCursor = errors.New("history message not found")

// HistoryDeps are the collaborators of History.
type HistoryDeps struct {
	Store *store.Store
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// History is the chat history of the connections (CH-01..CH-08): a store of its own, per chat, apart
// from the queue and the sends, which keep clearing their text as they always did (CH-04). It keeps a
// message only while its connection's retention is on, and only for that many days unless it is forever
// (CH-03). It never logs message text.
type History struct {
	d HistoryDeps
}

// NewHistory returns a History.
func NewHistory(d HistoryDeps) *History {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &History{d: d}
}

// HistoryMessage is a message to keep in the history of a chat.
type HistoryMessage struct {
	// ID is the id the AI knows the message by: the queued item's id, the send's id, or a new one.
	ID          string
	ConnectorID string
	// ChatID is Partyline's id of the chat.
	ChatID string
	// ExternalID is the platform's id of the message; empty when the platform gave none.
	ExternalID string
	SenderID   string
	SenderName string
	FromSelf   bool
	Trusted    bool
	Kind       dto.MessageKind
	Text       string
	// ReplyTo, Quoted and QuotedFromSelf tell the message this one quotes (QC-01..QC-03).
	ReplyTo        string
	Quoted         string
	QuotedFromSelf bool
	// At is when the platform says the message was written; zero is now.
	At          time.Time
	Attachments []HistoryAttachment
}

// HistoryAttachment is a file of a kept message: its name, type, size and storage state. ID is the id
// of the queued item's file when the message was delivered with one.
type HistoryAttachment struct {
	ID     string
	Name   string
	Mime   string
	Size   int64
	Status dto.AttachmentStatus
	// Kept tells, on read, that the queue still holds the file under ID: it can be downloaded.
	Kept bool
}

// HistoryReaction is the reaction of one sender to a kept message.
type HistoryReaction struct {
	// SenderID is empty for the account itself (FromSelf).
	SenderID   string
	SenderName string
	FromSelf   bool
	// Emoji is the reaction; an empty one removes the sender's reaction.
	Emoji string
	At    time.Time
}

// HistoryEntry is a kept message as it reads now: edited, deleted and with its reactions (CH-02).
type HistoryEntry struct {
	HistoryMessage
	Edited    bool
	Deleted   bool
	Reactions []HistoryReaction
}

// HistoryAnchor is the oldest kept message of a chat with a platform id (CH-08).
type HistoryAnchor struct {
	ExternalID string
	FromSelf   bool
	At         time.Time
}

// Record keeps the message in its chat's history when the connection's retention is on and the
// message is not older than it (CH-02, CH-03). A message the history has already (the same platform
// id in the chat, or the same id) is kept once: kept is then false.
func (h *History) Record(ctx context.Context, m HistoryMessage) (kept bool, err error) {
	at := m.At
	if at.IsZero() {
		at = h.d.Now()
	}
	kind := m.Kind
	if kind == "" {
		kind = dto.MessageKindText
	}
	if m.ID == "" {
		m.ID = store.NewID()
	}
	err = h.d.Store.WithTx(ctx, func(q *db.Queries) error {
		kept = false
		days, err := q.GetConnectorHistoryDays(ctx, m.ConnectorID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !Within(at, days, h.d.Now())) {
			return nil
		}
		if err != nil {
			return err
		}
		n, err := q.InsertChatHistory(ctx, db.InsertChatHistoryParams{
			ID: m.ID, ConnectorID: m.ConnectorID, ChatID: m.ChatID, ExternalID: m.ExternalID,
			SenderID: m.SenderID, SenderName: m.SenderName, FromSelf: m.FromSelf, Trusted: m.Trusted && !m.FromSelf,
			Kind: string(kind), Text: m.Text, ReplyTo: m.ReplyTo, Quoted: m.Quoted, QuotedFromSelf: m.QuotedFromSelf,
			At: at.UnixMilli(),
		})
		if err != nil || n == 0 {
			return err
		}
		for _, a := range m.Attachments {
			id := a.ID
			if id == "" {
				id = store.NewID()
			}
			if err := q.InsertChatHistoryAttachment(ctx, db.InsertChatHistoryAttachmentParams{
				ID: id, MessageID: m.ID, Name: a.Name, Mime: a.Mime, Size: a.Size, Status: string(a.Status),
			}); err != nil {
				return err
			}
		}
		kept = true
		return nil
	})
	return kept, err
}

// Within tells whether a message written at at is inside a retention of days (0: off, -1: forever) at
// now.
func Within(at time.Time, days int64, now time.Time) bool {
	return days == HistoryForever || (days > 0 && !at.Before(Since(days, now)))
}

// Since is the oldest time a retention of days keeps at now: the zero time for forever (CH-03).
func Since(days int64, now time.Time) time.Time {
	if days <= 0 {
		return time.Time{}
	}
	return now.Add(-time.Duration(days) * day)
}

// target is the kept message of the chat the platform knows by externalID: its own platform id, or a
// later message of an AI's send (the file after its caption, QC-01), which is kept as the send.
func target(ctx context.Context, q *db.Queries, connectorID, chatID, externalID string) (db.FindChatHistoryByExternalIDRow, bool, error) {
	if externalID == "" {
		return db.FindChatHistoryByExternalIDRow{}, false, nil
	}
	row, err := q.FindChatHistoryByExternalID(ctx, db.FindChatHistoryByExternalIDParams{
		ConnectorID: connectorID, ChatID: chatID, ExternalID: externalID,
	})
	if err == nil {
		return row, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return row, false, err
	}
	send, err := q.FindChatPlatformSendByExternalID(ctx, db.FindChatPlatformSendByExternalIDParams{ChatID: chatID, ExternalID: externalID})
	if errors.Is(err, sql.ErrNoRows) {
		return row, false, nil
	}
	if err != nil {
		return row, false, err
	}
	m, err := q.GetChatHistoryMessage(ctx, send)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && m.ConnectorID != connectorID) {
		return row, false, nil
	}
	if err != nil {
		return row, false, err
	}
	return db.FindChatHistoryByExternalIDRow{ID: m.ID, Text: m.Text, Deleted: m.Deleted}, true, nil
}

// Find is the kept message of the chat the platform knows by externalID, with its text: found is false
// when the history has none.
func (h *History) Find(ctx context.Context, connectorID, chatID, externalID string) (id, text string, found bool, err error) {
	row, found, err := target(ctx, h.d.Store.R(), connectorID, chatID, externalID)
	return row.ID, row.Text, found, err
}

// Edit shows the new text of the kept message the platform knows by targetID, marked edited (CH-02); a
// deleted message stays deleted. changed is false when the history does not have the message.
func (h *History) Edit(ctx context.Context, connectorID, chatID, targetID, text string) (changed bool, err error) {
	err = h.d.Store.WithTx(ctx, func(q *db.Queries) error {
		row, found, err := target(ctx, q, connectorID, chatID, targetID)
		if err != nil || !found {
			return err
		}
		n, err := q.EditChatHistory(ctx, db.EditChatHistoryParams{Text: text, ID: row.ID})
		changed = n > 0
		return err
	})
	return changed, err
}

// Delete marks the kept message the platform knows by targetID deleted and drops its text, its quote's
// preview and its files (CH-02).
func (h *History) Delete(ctx context.Context, connectorID, chatID, targetID string) (changed bool, err error) {
	err = h.d.Store.WithTx(ctx, func(q *db.Queries) error {
		row, found, err := target(ctx, q, connectorID, chatID, targetID)
		if err != nil || !found {
			return err
		}
		if err := q.DeleteChatHistoryAttachments(ctx, row.ID); err != nil {
			return err
		}
		n, err := q.DeleteChatHistoryContent(ctx, row.ID)
		changed = n > 0
		return err
	})
	return changed, err
}

// React lists the reaction on the kept message the platform knows by targetID, replacing the sender's
// earlier one; an empty emoji removes it (CH-02). The account's reactions are one sender.
func (h *History) React(ctx context.Context, connectorID, chatID, targetID string, r HistoryReaction) (changed bool, err error) {
	sender := r.SenderID
	if r.FromSelf {
		sender = ""
	}
	at := r.At
	if at.IsZero() {
		at = h.d.Now()
	}
	err = h.d.Store.WithTx(ctx, func(q *db.Queries) error {
		row, found, err := target(ctx, q, connectorID, chatID, targetID)
		if err != nil || !found {
			return err
		}
		changed = true
		if r.Emoji == "" {
			return q.DeleteChatHistoryReaction(ctx, db.DeleteChatHistoryReactionParams{MessageID: row.ID, SenderID: sender})
		}
		return q.UpsertChatHistoryReaction(ctx, db.UpsertChatHistoryReactionParams{
			MessageID: row.ID, SenderID: sender, SenderName: r.SenderName, FromSelf: r.FromSelf, Emoji: r.Emoji, At: at.UnixMilli(),
		})
	})
	return changed, err
}

// Page reads the kept messages of a chat written at or after since, newest last: the limit newest
// before the message before (empty: the newest of all), with whether older ones remain (CH-05). limit is
// clamped to 1..HistoryPageMax, HistoryPageDefault when 0. A before that is not a message of the chat is
// ErrHistoryCursor.
func (h *History) Page(ctx context.Context, chatID string, since time.Time, before string, limit int) ([]HistoryEntry, bool, error) {
	switch {
	case limit <= 0:
		limit = HistoryPageDefault
	case limit > HistoryPageMax:
		limit = HistoryPageMax
	}
	q := h.d.Store.R()
	cursorAt, cursorSeq := int64(math.MaxInt64), int64(math.MaxInt64)
	if before != "" {
		c, err := q.GetChatHistoryCursor(ctx, db.GetChatHistoryCursorParams{ID: before, ChatID: chatID})
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, ErrHistoryCursor
		}
		if err != nil {
			return nil, false, err
		}
		cursorAt, cursorSeq = c.At, c.Seq
	}
	rows, err := q.ListChatHistory(ctx, db.ListChatHistoryParams{
		ChatID: chatID, Since: since.UnixMilli(), BeforeAt: cursorAt, BeforeSeq: cursorSeq, Limit: int64(limit) + 1,
	})
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	slices.Reverse(rows)
	ids := make([]string, len(rows))
	entries := make([]HistoryEntry, len(rows))
	at := make(map[string]int, len(rows))
	for i, r := range rows {
		ids[i], at[r.ID] = r.ID, i
		entries[i] = HistoryEntry{
			HistoryMessage: HistoryMessage{
				ID: r.ID, ConnectorID: r.ConnectorID, ChatID: r.ChatID, ExternalID: r.ExternalID,
				SenderID: r.SenderID, SenderName: r.SenderName, FromSelf: r.FromSelf, Trusted: r.Trusted,
				Kind: dto.MessageKind(r.Kind), Text: r.Text, ReplyTo: r.ReplyTo, Quoted: r.Quoted, QuotedFromSelf: r.QuotedFromSelf,
				At: time.UnixMilli(r.At).UTC(),
			},
			Edited: r.Edited, Deleted: r.Deleted,
		}
	}
	if len(ids) == 0 {
		return entries, more, nil
	}
	files, err := q.ListChatHistoryAttachments(ctx, ids)
	if err != nil {
		return nil, false, err
	}
	for _, f := range files {
		e := &entries[at[f.MessageID]]
		status := dto.AttachmentStatus(f.Status)
		if status == dto.AttachmentStatusStored && !f.Kept {
			status = dto.AttachmentStatusExpired // the queue let the file go (CID-09); the history keeps none
		}
		e.Attachments = append(e.Attachments, HistoryAttachment{
			ID: f.ID, Name: f.Name, Mime: f.Mime, Size: f.Size, Status: status, Kept: f.Kept,
		})
	}
	reactions, err := q.ListChatHistoryReactions(ctx, ids)
	if err != nil {
		return nil, false, err
	}
	for _, r := range reactions {
		e := &entries[at[r.MessageID]]
		e.Reactions = append(e.Reactions, HistoryReaction{
			SenderID: r.SenderID, SenderName: r.SenderName, FromSelf: r.FromSelf, Emoji: r.Emoji, At: time.UnixMilli(r.At).UTC(),
		})
	}
	return entries, more, nil
}

// Anchor is the oldest kept message of the chat with a platform id, the one a platform is asked for
// the messages before (CH-08); found is false when the chat has none.
func (h *History) Anchor(ctx context.Context, chatID string) (HistoryAnchor, bool, error) {
	row, err := h.d.Store.R().GetChatHistoryAnchor(ctx, chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return HistoryAnchor{}, false, nil
	}
	if err != nil {
		return HistoryAnchor{}, false, err
	}
	return HistoryAnchor{ExternalID: row.ExternalID, FromSelf: row.FromSelf, At: time.UnixMilli(row.At).UTC()}, true, nil
}

// Clear deletes the kept messages of the connection written before cutoff, in batches, and returns how
// many (CH-03): all of them when its history is turned off, those outside a lowered retention.
func (h *History) Clear(ctx context.Context, connectorID string, cutoff time.Time) (int64, error) {
	return h.batches(ctx, func(q *db.Queries) (int64, error) {
		return q.DeleteConnectorChatHistoryBefore(ctx, db.DeleteConnectorChatHistoryBeforeParams{
			ConnectorID: connectorID, Cutoff: cutoff.UnixMilli(), Limit: historyBatch,
		})
	})
}

// Purge deletes the kept messages older than their connection's retention, all of those of a
// connection whose history is off, in batches, and returns how many (CH-03).
func (h *History) Purge(ctx context.Context) (int64, error) {
	now := h.d.Now().UnixMilli()
	return h.batches(ctx, func(q *db.Queries) (int64, error) {
		return q.DeleteExpiredChatHistory(ctx, db.DeleteExpiredChatHistoryParams{Now: now, Limit: historyBatch})
	})
}

func (h *History) batches(ctx context.Context, del func(q *db.Queries) (int64, error)) (int64, error) {
	var total int64
	for {
		var n int64
		err := h.d.Store.WithTx(ctx, func(q *db.Queries) (err error) {
			n, err = del(q)
			return err
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < historyBatch {
			return total, nil
		}
	}
}

// Run purges now and then every interval until ctx ends. Failures are logged without message content.
func (h *History) Run(ctx context.Context, interval time.Duration, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		n, err := h.Purge(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("chat history purge failed", "err", err)
		case n > 0:
			log.Info("chat history purge", "deleted", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
