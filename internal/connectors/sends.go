package connectors

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Errors of Sends.Send.
var (
	// ErrNotOperator means the AI that sends is not the operator of the connection (CID-03).
	ErrNotOperator = errors.New("only the AI that operates the connection can send through it")
	// ErrUnknownChat means the chat is not a chat of the connection (CID-17).
	ErrUnknownChat = errors.New("the chat does not belong to the connection")
)

const (
	// SendAttemptTimeout bounds one attempt to send through a connection: the synchronous one
	// Send makes and each retry of the worker (CID-18).
	SendAttemptTimeout = 15 * time.Second
	// SendRetention is how long a finished send keeps its row, which holds no text: long enough
	// for a reply to a message the account sent to be recognized under mentions-only (CID-12),
	// short enough not to be a log of what the AI sends.
	SendRetention = 30 * 24 * time.Hour

	// noLongerOperator is why a send fails without being tried again when the AI that made it was
	// replaced as the connection's operator while it waited for a retry (CID-03).
	noLongerOperator = "the AI no longer operates this connection"

	// maxSendAttempts is how many attempts a send gets before it is failed (CID-18).
	maxSendAttempts = 5

	sendPoll     = time.Second
	sendBatch    = 50
	purgeBatch   = 1000
	maxSendError = 500
)

// sendBackoff is the wait after the given failed attempt: 2 s · 2^(attempt-1) (CID-18).
func sendBackoff(attempt int) time.Duration {
	return 2 * time.Second << max(attempt-1, 0)
}

// Transport sends a message through a running connection; the Manager implements it. Its errors
// carry no secrets (the Manager redacts them).
type Transport interface {
	Send(ctx context.Context, connectorID string, m Outbound) (platformID string, err error)
}

// SendsDeps are the collaborators of Sends.
type SendsDeps struct {
	Store     *store.Store
	Transport Transport
	// Sent remembers the platform ids of what is sent, so that the inbound pipeline can tell the
	// echo of a send from something typed on the account (CID-14); nil remembers nothing, and
	// only the record of the sends then tells them apart.
	Sent *SentIDs
	// Now returns the current time; defaults to time.Now.
	Now    func() time.Time
	Logger *slog.Logger
	// AttemptTimeout bounds one attempt; defaults to SendAttemptTimeout.
	AttemptTimeout time.Duration
	// PollInterval is how often the worker looks for retries that are due; defaults to 1 s.
	PollInterval time.Duration
}

// Sends sends what an AI answers through a connection it operates (AD-015): to a chat of the
// connection, as the account itself, never signed. A send is attempted once, right away, and a
// failure keeps it pending for the worker, which retries it with a doubling backoff (2 s first) and
// gives up after five attempts. Nothing but ids, states and errors is kept: the text is deleted
// when the send is over (CID-15, CID-16, CID-18).
type Sends struct {
	d         SendsDeps
	running   atomic.Bool
	recovered atomic.Bool
}

// NewSends returns a Sends service; Run starts its worker.
func NewSends(d SendsDeps) *Sends {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.AttemptTimeout <= 0 {
		d.AttemptTimeout = SendAttemptTimeout
	}
	if d.PollInterval <= 0 {
		d.PollInterval = sendPoll
	}
	return &Sends{d: d}
}

// SendInput is an answer of an AI through its connection.
type SendInput struct {
	// AgentID is the AI that sends: it must operate the connection (CID-03).
	AgentID     string
	ConnectorID string
	// ChatID is Partyline's id of the chat, a chat of ConnectorID (CID-17).
	ChatID string
	Text   string
	// ReplyTo is the platform's id of the message to quote, empty for a plain message; a platform
	// that cannot quote sends the text alone (CID-15).
	ReplyTo string
}

// Check refuses what Send would refuse before it stores or sends anything, and changes nothing: a
// connection that does not exist is access.ErrNotFound, an AI that does not operate it (also while
// nobody does) ErrNotOperator, a chat that is not one of its chats ErrUnknownChat and an unusable text
// a *validation.Error on text. A caller that limits how much an AI sends calls it first, so that a
// refused request does not count against the limit and the AI keeps seeing why it was refused.
func (s *Sends) Check(ctx context.Context, in SendInput) error {
	conn, err := s.d.Store.R().GetConnector(ctx, in.ConnectorID)
	if errors.Is(err, sql.ErrNoRows) {
		return access.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !conn.OperatorIid.Valid || conn.OperatorIid.String != in.AgentID {
		return ErrNotOperator
	}
	chat, err := s.d.Store.R().GetChat(ctx, in.ChatID)
	switch {
	case errors.Is(err, sql.ErrNoRows), err == nil && chat.ConnectorID != in.ConnectorID:
		return ErrUnknownChat
	case err != nil:
		return err
	}
	return validateAnswer(in.Text)
}

// Send stores the answer after the checks of Check and makes one attempt at once, under
// SendAttemptTimeout, through the connection. The result is sent when the platform took the message;
// when it refused or did not answer in time the send is pending, the result carries the reason and the
// worker retries it (CID-18). The attempt is not abandoned when ctx is done: the caller going away
// does not unsend a message in flight.
func (s *Sends) Send(ctx context.Context, in SendInput) (dto.PlatformSendResult, error) {
	if err := s.Check(ctx, in); err != nil {
		return dto.PlatformSendResult{}, err
	}
	id, now := store.NewID(), s.d.Now().UnixMilli()
	// The row is created claimed (sending): the worker only takes pending sends.
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.InsertPlatformSend(ctx, db.InsertPlatformSendParams{
			ID: id, ConnectorID: in.ConnectorID, ChatID: in.ChatID, AgentID: in.AgentID, Text: in.Text,
			ReplyToExternalID: in.ReplyTo, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		return dto.PlatformSendResult{}, err
	}
	return s.attempt(context.WithoutCancel(ctx), id)
}

// validateAnswer applies the message limits (MSG-02): at least one character that is not white
// space, at most dto.MaxTextLength characters.
func validateAnswer(text string) error {
	check := validation.Check{}
	if utf8.RuneCountInString(text) > dto.MaxTextLength {
		check.Add("text", "must have at most 16000 characters")
	}
	if strings.TrimSpace(text) == "" {
		check.Add("text", "must not be empty")
	}
	return check.Err()
}

// attempt sends the claimed send once and records the outcome, also when ctx ends meanwhile, so that
// the send is not left in flight.
func (s *Sends) attempt(ctx context.Context, id string) (dto.PlatformSendResult, error) {
	row, err := s.d.Store.R().GetPlatformSendAttempt(ctx, id)
	if err != nil {
		return dto.PlatformSendResult{}, err
	}
	send := row.PlatformSend
	rec := context.WithoutCancel(ctx)
	if !row.OperatorIid.Valid || row.OperatorIid.String != send.AgentID {
		// CID-03: only the connection's operator sends through it; the owner chose another AI
		// while this send waited for its retry. Nothing was tried, so the attempts stay.
		return s.fail(rec, send, int(send.Attempts), noLongerOperator, true)
	}
	out := Outbound{
		ChatID: row.ChatExternalID, Text: send.Text, ReplyToID: send.ReplyToExternalID, ReplyToSenderID: row.ReplyToSenderID,
		MessageID: send.ID, AuthorName: row.AgentName, CreatedAt: time.UnixMilli(send.CreatedAt).UTC(),
		// The account is the AI's own: no author line, whoever the AI is (CID-15, CID-16).
		Unsigned: true,
		Created:  func(platformID string) { s.d.Sent.Add(send.ChatID, platformID) },
	}
	sendCtx, cancel := context.WithTimeout(ctx, s.d.AttemptTimeout)
	platformID, err := s.d.Transport.Send(sendCtx, send.ConnectorID, out)
	timedOut := errors.Is(sendCtx.Err(), context.DeadlineExceeded)
	cancel()

	if err == nil {
		s.d.Sent.Add(send.ChatID, platformID)
		err = s.d.Store.WithTx(rec, func(q *db.Queries) error {
			return q.MarkPlatformSendSent(rec, db.MarkPlatformSendSentParams{ExternalID: platformID, UpdatedAt: s.d.Now().UnixMilli(), ID: id})
		})
		return dto.PlatformSendResult{ID: id, Status: dto.PlatformSendStatusSent}, err
	}
	reason := err.Error()
	if timedOut {
		reason = fmt.Sprintf("the platform did not answer within %s", s.d.AttemptTimeout)
	}
	return s.fail(rec, send, int(send.Attempts)+1, reason, false)
}

// fail records that attempt number n did not send the message: the send waits for the next attempt
// after the backoff, or is failed (and its text deleted) when it was the last one or retrying cannot
// help (CID-18). The reason comes from the connector, which redacts its secrets; it is stored and
// shown to the AI, and never logged.
func (s *Sends) fail(ctx context.Context, send db.PlatformSend, n int, reason string, final bool) (dto.PlatformSendResult, error) {
	reason = shorten(reason, maxSendError)
	now := s.d.Now()
	status := dto.PlatformSendStatusPending
	if final || n >= maxSendAttempts {
		status = dto.PlatformSendStatusFailed
	}
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if status == dto.PlatformSendStatusFailed {
			return q.MarkPlatformSendFailed(ctx, db.MarkPlatformSendFailedParams{Attempts: int64(n), LastError: reason, UpdatedAt: now.UnixMilli(), ID: send.ID})
		}
		return q.MarkPlatformSendRetry(ctx, db.MarkPlatformSendRetryParams{
			Attempts: int64(n), NextAttemptAt: now.Add(sendBackoff(n)).UnixMilli(), LastError: reason, UpdatedAt: now.UnixMilli(), ID: send.ID,
		})
	})
	s.d.Logger.Warn("connectors: platform send failed", "send_id", send.ID, "connector_id", send.ConnectorID, "attempt", n, "status", status)
	return dto.PlatformSendResult{ID: send.ID, Status: status, Error: reason}, err
}

// shorten cuts s to at most n bytes without splitting a UTF-8 sequence.
func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Running reports whether Run finished its start-up pass and is waiting for work.
func (s *Sends) Running() bool { return s.running.Load() }

// Recover makes the sends a previous process left in flight due again. It runs once: the app calls
// it before the server accepts requests, so a send started by a request is never taken for one a
// previous process left behind and sent twice; Run calls it too, for a Sends started on its own.
func (s *Sends) Recover(ctx context.Context) error {
	if s.recovered.Load() {
		return nil
	}
	var ids []string
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) (err error) {
		ids, err = q.RequeueSendingPlatformSends(ctx, s.d.Now().UnixMilli())
		return err
	})
	if err != nil {
		return err
	}
	s.recovered.Store(true)
	if len(ids) > 0 {
		s.d.Logger.Info("connectors: requeued interrupted platform sends", "count", len(ids))
	}
	return nil
}

// Run makes the sends a previous process left in flight due again (Recover), sends everything that is due and
// then keeps sending every poll interval until ctx is done (CID-18).
func (s *Sends) Run(ctx context.Context) error {
	defer s.running.Store(false)
	if err := s.Recover(ctx); err != nil {
		return err
	}
	s.flushLogged(ctx)
	s.running.Store(true)

	ticker := time.NewTicker(s.d.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		s.flushLogged(ctx)
	}
}

func (s *Sends) flushLogged(ctx context.Context) {
	if err := s.Flush(ctx); err != nil && ctx.Err() == nil {
		s.d.Logger.Error("connectors: platform sends flush failed", "err", err)
	}
}

// Flush retries every send that is due now, oldest first, one at a time, and returns when none is
// left.
func (s *Sends) Flush(ctx context.Context) error {
	for {
		due, err := s.d.Store.R().ListDuePlatformSends(ctx, db.ListDuePlatformSendsParams{Now: s.d.Now().UnixMilli(), Limit: sendBatch})
		if err != nil {
			return err
		}
		for _, row := range due {
			if err := s.retry(ctx, row.ID); err != nil {
				return err
			}
		}
		if len(due) < sendBatch {
			return nil
		}
	}
}

// retry claims a due send and attempts it again. A send another pass took, or that went with its
// connection, chat or AI meanwhile, is left alone.
func (s *Sends) retry(ctx context.Context, id string) error {
	var claimed int64
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) (err error) {
		claimed, err = q.ClaimPlatformSend(ctx, db.ClaimPlatformSendParams{UpdatedAt: s.d.Now().UnixMilli(), ID: id})
		return err
	})
	if err != nil || claimed == 0 {
		return err
	}
	if _, err := s.attempt(ctx, id); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return ctx.Err()
}

// Purge deletes the finished sends (sent or failed) whose last change is SendRetention old or older,
// in bounded batches, and returns how many it removed. Sends still waiting or in flight stay,
// whatever their age. The daily retention job runs it.
func (s *Sends) Purge(ctx context.Context) (int64, error) {
	cutoff := s.d.Now().Add(-SendRetention).UnixMilli()
	var total int64
	for {
		var n int64
		err := s.d.Store.WithTx(ctx, func(q *db.Queries) (err error) {
			n, err = q.DeleteFinishedPlatformSends(ctx, db.DeleteFinishedPlatformSendsParams{Cutoff: cutoff, Limit: purgeBatch})
			return err
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < purgeBatch {
			return total, nil
		}
	}
}
