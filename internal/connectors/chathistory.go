package connectors

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Errors of the chat history (CH-06, CH-08).
var (
	// ErrHistoryOff means the connection keeps no chat history: its owner can turn it on in its settings.
	ErrHistoryOff = errors.New("the connection keeps no chat history")
	// ErrNoHistoryAnchor means the history of the chat has no message with a platform id yet, which the
	// platform needs to know where the past to send begins.
	ErrNoHistoryAnchor = errors.New("no message of the chat is known yet")
)

// Bounds of fetch_history (CH-08).
const (
	FetchHistoryDefault = 50
	FetchHistoryMax     = 50
)

// historyFetcher is the Runner of a Service that can ask a running connector for a chat's past (CH-08);
// the Manager is one.
type historyFetcher interface {
	FetchHistory(ctx context.Context, connectorID string, r HistoryRequest) error
}

// ValidHistoryDays tells whether days is a retention a connection may have (CH-01): off, 1 to 365 days,
// or forever.
func ValidHistoryDays(days int64) bool {
	return days == platform.HistoryOff || days == platform.HistoryForever || (days >= 1 && days <= platform.MaxHistoryDays)
}

// historyDays returns the retention after the request: unchanged when none is given, else one of
// ValidHistoryDays (a validation error on history_days otherwise).
func historyDays(c db.Connector, requested *int64) (int64, error) {
	if requested == nil {
		return c.HistoryDays, nil
	}
	if !ValidHistoryDays(*requested) {
		return 0, &validation.Error{Fields: map[string]string{"history_days": "must be 0 (off), 1 to 365 days or -1 (forever)"}}
	}
	return *requested, nil
}

// applyHistoryDays deletes what a changed retention no longer keeps (CH-03): all of the connection's
// history when it is turned off, what falls outside a shorter one. Forever keeps everything.
func (s *Service) applyHistoryDays(ctx context.Context, id string, before, after int64) error {
	if s.d.History == nil || before == after || after == platform.HistoryForever {
		return nil
	}
	cutoff := time.UnixMilli(math.MaxInt64) // off: everything goes
	if after > 0 {
		if before > 0 && before < after {
			return nil // a longer retention deletes nothing
		}
		cutoff = platform.Since(after, s.d.Now())
	}
	_, err := s.d.History.Clear(ctx, id, cutoff)
	return err
}

// HistoryPage is a page of a chat's history (CH-05).
type HistoryPage struct {
	Connector db.Connector
	Chat      db.Chat
	Messages  []platform.HistoryEntry
	HasMore   bool
}

// historyConnection authorizes reading the chat history of a connection (CH-06): the AI that operates
// it, and the users who manage it (the admins of its organization). Anyone else, members who only use
// it and other AIs included, gets access.ErrNotFound, as for a connection that does not exist. A
// connection that keeps no history is ErrHistoryOff, told only to those who may read it.
func (s *Service) historyConnection(ctx context.Context, p authn.Principal, id string) (db.Connector, error) {
	switch p.Kind {
	case authn.KindAgent:
		if _, err := s.require(ctx, p, id, dto.LevelUse); err != nil {
			return db.Connector{}, access.ErrNotFound
		}
	case authn.KindUser:
		if _, err := s.authorize(ctx, p, id, dto.LevelManage); err != nil {
			if errors.Is(err, access.ErrForbidden) {
				return db.Connector{}, access.ErrNotFound
			}
			return db.Connector{}, err
		}
	default:
		return db.Connector{}, access.ErrNotFound
	}
	c, err := s.d.Store.R().GetConnector(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Connector{}, access.ErrNotFound
	}
	if err != nil {
		return db.Connector{}, err
	}
	if p.Kind == authn.KindAgent && (!c.OperatorIid.Valid || c.OperatorIid.String != p.AgentID) {
		return db.Connector{}, access.ErrNotFound
	}
	if c.HistoryDays == platform.HistoryOff || s.d.History == nil {
		return c, ErrHistoryOff
	}
	return c, nil
}

// historyChat finds the chat of the connection that ref names: its chat id, or its address on the
// platform as send_message takes it (CID-31). Anything else is ErrUnknownChat.
func (s *Service) historyChat(ctx context.Context, c db.Connector, ref string) (db.Chat, error) {
	chat, err := s.d.Store.R().GetChat(ctx, ref)
	switch {
	case err == nil && chat.ConnectorID == c.ID:
		return chat, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return db.Chat{}, err
	}
	info, ok := kindAddress(s.d.Kinds, c, ref)
	if !ok {
		return db.Chat{}, ErrUnknownChat
	}
	chat, err = s.d.Store.R().GetChatByExternalID(ctx, db.GetChatByExternalIDParams{ConnectorID: c.ID, ExternalID: info.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Chat{}, ErrUnknownChat
	}
	return chat, err
}

// ChatHistory reads a page of the history of a chat of the connection, newest last (CH-05, CH-06):
// the limit messages (20 by default, at most 100) before the message before, or the newest. Only what
// the retention keeps now is read. A before that is not a kept message of the chat is a validation error.
func (s *Service) ChatHistory(ctx context.Context, p authn.Principal, id, chatRef, before string, limit int) (HistoryPage, error) {
	c, err := s.historyConnection(ctx, p, id)
	if err != nil {
		return HistoryPage{}, err
	}
	chat, err := s.historyChat(ctx, c, chatRef)
	if err != nil {
		return HistoryPage{}, err
	}
	msgs, more, err := s.d.History.Page(ctx, chat.ID, platform.Since(c.HistoryDays, s.d.Now()), before, limit)
	if errors.Is(err, platform.ErrHistoryCursor) {
		return HistoryPage{}, &validation.Error{Fields: map[string]string{"before": "must be the message_id of a message of this chat's history"}}
	}
	if err != nil {
		return HistoryPage{}, err
	}
	return HistoryPage{Connector: c, Chat: chat, Messages: msgs, HasMore: more}, nil
}

// FetchHistory asks the platform of the connection for up to count (1 to 50, 50 when 0) messages of the
// chat older than the oldest one its history has (CH-08). They come back later and are only added to
// the history, never delivered. Only the AI that operates the connection may ask; a platform that cannot
// is an *UnsupportedError, a connection not running ErrNotRunning and a chat whose history has no message
// with a platform id ErrNoHistoryAnchor.
func (s *Service) FetchHistory(ctx context.Context, p authn.Principal, id, chatRef string, count int) (int, error) {
	if p.Kind != authn.KindAgent {
		return 0, access.ErrNotFound
	}
	if count == 0 {
		count = FetchHistoryDefault
	}
	if count < 1 || count > FetchHistoryMax {
		return 0, &validation.Error{Fields: map[string]string{"count": "must be 1 to 50"}}
	}
	c, err := s.historyConnection(ctx, p, id)
	if err != nil {
		return 0, err
	}
	chat, err := s.historyChat(ctx, c, chatRef)
	if err != nil {
		return 0, err
	}
	fetcher, ok := s.d.Runner.(historyFetcher)
	if !ok || !s.Kind(c.Kind).CanFetchHistory {
		return 0, &UnsupportedError{Reason: "the platform of this connection cannot send the past of a chat"}
	}
	anchor, found, err := s.d.History.Anchor(ctx, chat.ID)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, ErrNoHistoryAnchor
	}
	err = fetcher.FetchHistory(ctx, c.ID, HistoryRequest{
		ChatID: chat.ExternalID, AnchorID: anchor.ExternalID, AnchorFromSelf: anchor.FromSelf, AnchorAt: anchor.At, Count: count,
	})
	return count, err
}
