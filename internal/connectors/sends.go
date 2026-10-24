package connectors

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Errors of Sends.Send.
var (
	// ErrNotOperator means the AI that sends is not the operator of the connection (CID-03).
	ErrNotOperator = errors.New("only the AI that operates the connection can send through it")
	// ErrUnknownChat means the chat is not a chat of the connection (CID-17), and not an address its
	// platform lets the operator start a chat at either (CID-31).
	ErrUnknownChat = errors.New("the chat does not belong to the connection")
	// ErrNewChatRateLimited means the AI started too many chats on its connections lately (CID-31).
	ErrNewChatRateLimited = errors.New("too many new chats")
	// ErrFilesUnsupported means the platform of the connection has no media send (CID-32).
	ErrFilesUnsupported = errors.New("the platform of the connection cannot be sent files")
)

const (
	// SendAttemptTimeout bounds one attempt to send through a connection: the synchronous one
	// Send makes and each retry of the worker (CID-18).
	SendAttemptTimeout = 15 * time.Second
	// SendFileTimeout bounds the one attempt to send a file: an upload takes longer than a text
	// (CID-32).
	SendFileTimeout = 90 * time.Second
	// SendRetention is how long a finished send keeps its row, which holds no text: long enough
	// for a reply to a message the account sent to be recognized under mentions-only (CID-12),
	// short enough not to be a log of what the AI sends.
	SendRetention = 30 * 24 * time.Hour

	// noLongerOperator is why a send fails without being tried again when the AI that made it was
	// replaced as the connection's operator while it waited for a retry (CID-03).
	noLongerOperator = "the AI no longer operates this connection"

	// maxSendAttempts is how many attempts a send gets before it is failed (CID-18).
	maxSendAttempts = 5

	// interruptedFile is why a send of a file fails when the process that was sending it died (CID-32).
	interruptedFile = "interrupted by a restart: the file was not kept"

	// maxFileName is the longest file name a send takes, in bytes.
	maxFileName = 255

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
	// Kinds are the connector kinds: the Address of a connection's kind says which names it takes as a
	// chat it has not heard from yet (CID-31). Without it a send goes only to chats the connection has.
	Kinds *Registry
	// Bus announces the chats a send creates; defaults to a bus nobody listens to.
	Bus *events.Bus
	// AllowNewChat says whether an AI may start one more chat now, and counts it; the API wires the
	// registry's limit of 10 an hour. Nil allows everything. It is asked only when a chat is about to
	// be created, never for a chat the connection has.
	AllowNewChat func(agentID string) bool
	// Now returns the current time; defaults to time.Now.
	Now    func() time.Time
	Logger *slog.Logger
	// AttemptTimeout bounds one attempt; defaults to SendAttemptTimeout. FileAttemptTimeout bounds the
	// attempt of a send with a file; defaults to SendFileTimeout.
	AttemptTimeout     time.Duration
	FileAttemptTimeout time.Duration
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
	if d.Bus == nil {
		d.Bus = events.NewBus()
	}
	if d.AttemptTimeout <= 0 {
		d.AttemptTimeout = SendAttemptTimeout
	}
	if d.FileAttemptTimeout <= 0 {
		d.FileAttemptTimeout = SendFileTimeout
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
	// ChatID is Partyline's id of the chat, a chat of ConnectorID (CID-17); or else the address of a
	// chat the connection has not heard from yet (a phone number, a Telegram chat id, ...), which the
	// kind of the connection accepts and which the send then creates (CID-31).
	ChatID string
	Text   string
	// ReplyTo is the platform's id of the message to quote, empty for a plain message; a platform
	// that cannot quote sends the text alone (CID-15).
	ReplyTo string
	// File, when set, is a file sent with the text as its caption (CID-32). It is held in memory for
	// the one attempt and never stored, so the send is attempted once and a platform that refuses it
	// fails it.
	File *SendFile
}

// SendFile is the file of a send (CID-32).
type SendFile struct {
	Name string
	Mime string
	Data []byte
}

// Check refuses what Send would refuse before it stores or sends anything, and changes nothing: a
// connection that does not exist is access.ErrNotFound, an AI that does not operate it (also while
// nobody does) ErrNotOperator, a chat that is neither one of its chats nor an address its platform
// accepts ErrUnknownChat and an unusable text a *validation.Error on text. A caller that limits how
// much an AI sends calls it first, so that a refused request does not count against the limit and the
// AI keeps seeing why it was refused.
func (s *Sends) Check(ctx context.Context, in SendInput) error {
	_, err := s.plan(ctx, in)
	return err
}

// sendPlan is what a send is going to do: the chat it goes to, or the chat to create first.
type sendPlan struct {
	chat   db.Chat
	create *ChatInfo
}

// plan makes the checks of Check, in its order, and says where the send goes (CID-03, CID-17, CID-31).
func (s *Sends) plan(ctx context.Context, in SendInput) (sendPlan, error) {
	conn, err := s.d.Store.R().GetConnector(ctx, in.ConnectorID)
	if errors.Is(err, sql.ErrNoRows) {
		return sendPlan{}, access.ErrNotFound
	}
	if err != nil {
		return sendPlan{}, err
	}
	if !conn.OperatorIid.Valid || conn.OperatorIid.String != in.AgentID {
		return sendPlan{}, ErrNotOperator
	}
	if in.File != nil && !s.canSendFiles(conn) {
		return sendPlan{}, ErrFilesUnsupported
	}
	p, err := s.destination(ctx, conn, in.ChatID)
	if err != nil {
		return sendPlan{}, err
	}
	return p, validateContent(in.Text, in.File)
}

// canSendFiles tells whether the connection's kind has a media send (CID-32).
func (s *Sends) canSendFiles(conn db.Connector) bool {
	if s.d.Kinds == nil {
		return false
	}
	kind, ok := s.d.Kinds.Lookup(conn.Kind)
	return ok && kind.CanSendFiles
}

// destination finds the chat a send to ref goes to: a chat of the connection by its id, else an
// address the kind of the connection accepts, which is the chat of that address when the connection
// has one and otherwise a chat to create. Anything else, the id of a chat of another connection
// included, is an unknown chat.
func (s *Sends) destination(ctx context.Context, conn db.Connector, ref string) (sendPlan, error) {
	chat, err := s.d.Store.R().GetChat(ctx, ref)
	switch {
	case err == nil && chat.ConnectorID == conn.ID:
		return sendPlan{chat: chat}, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return sendPlan{}, err
	}
	info, ok := s.address(conn, ref)
	if !ok {
		return sendPlan{}, ErrUnknownChat
	}
	chat, err = s.d.Store.R().GetChatByExternalID(ctx, db.GetChatByExternalIDParams{ConnectorID: conn.ID, ExternalID: info.ID})
	switch {
	case err == nil:
		return sendPlan{chat: chat}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return sendPlan{}, err
	}
	return sendPlan{create: &info}, nil
}

// address asks the connection's kind whether ref is an address it can start a chat at (CID-31). The
// kind sees the public fields of the connection's config only.
func (s *Sends) address(conn db.Connector, ref string) (ChatInfo, bool) {
	if s.d.Kinds == nil {
		return ChatInfo{}, false
	}
	kind, ok := s.d.Kinds.Lookup(conn.Kind)
	if !ok || kind.Address == nil {
		return ChatInfo{}, false
	}
	cfg, err := Decode(nil, conn.ConfigJson, nil)
	if err != nil {
		return ChatInfo{}, false
	}
	return kind.Address(cfg, ref)
}

// Send stores the answer after the checks of Check and makes one attempt at once, under
// SendAttemptTimeout, through the connection. The result is sent when the platform took the message;
// when it refused or did not answer in time the send is pending, the result carries the reason and the
// worker retries it (CID-18). The attempt is not abandoned when ctx is done: the caller going away
// does not unsend a message in flight. A send to an address the connection has no chat for first
// creates the chat, unless the AI started too many lately (ErrNewChatRateLimited, CID-31).
func (s *Sends) Send(ctx context.Context, in SendInput) (dto.PlatformSendResult, error) {
	p, err := s.plan(ctx, in)
	if err != nil {
		return dto.PlatformSendResult{}, err
	}
	chat, err := s.chatOf(ctx, in, p)
	if err != nil {
		return dto.PlatformSendResult{}, err
	}
	id, now := store.NewID(), s.d.Now().UnixMilli()
	// The row is created claimed (sending): the worker only takes pending sends.
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.InsertPlatformSend(ctx, db.InsertPlatformSendParams{
			ID: id, ConnectorID: in.ConnectorID, ChatID: chat.ID, AgentID: in.AgentID, Text: in.Text,
			ReplyToExternalID: in.ReplyTo, Once: in.File != nil, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		return dto.PlatformSendResult{}, err
	}
	return s.attempt(context.WithoutCancel(ctx), id, in.File)
}

// chatOf is the chat a planned send goes to, created now when it is a new one (CID-31): the AI's
// allowance for new chats is spent first, and the chat is announced like any discovered chat. The
// chat is kept whatever becomes of the first message: the platform refusing it does not unmake it.
func (s *Sends) chatOf(ctx context.Context, in SendInput, p sendPlan) (db.Chat, error) {
	if p.create == nil {
		return p.chat, nil
	}
	if s.d.AllowNewChat != nil && !s.d.AllowNewChat(in.AgentID) {
		return db.Chat{}, ErrNewChatRateLimited
	}
	return UpsertChat(ctx, s.d.Store, s.d.Bus, in.ConnectorID, *p.create, time.Time{}, s.d.Now())
}

// validateContent checks what a send holds: a text, or a file with an optional caption (CID-32).
func validateContent(text string, file *SendFile) error {
	if file == nil {
		return validateAnswer(text)
	}
	check := validation.Check{}
	if utf8.RuneCountInString(text) > dto.MaxTextLength {
		check.Add("text", "must have at most 16000 characters")
	}
	switch {
	case len(file.Data) == 0:
		check.Add("file", "must not be empty")
	case !plainFileName(file.Name):
		check.Add("file", "must have a name of at most 255 bytes without a path or control characters")
	}
	return check.Err()
}

// plainFileName reports whether name is a file name and not a path: something, at most maxFileName bytes,
// without a separator or a control character.
func plainFileName(name string) bool {
	if name == "" || len(name) > maxFileName || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return false
	}
	return strings.IndexFunc(name, unicode.IsControl) < 0
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
// the send is not left in flight. A send with a file (once) is attempted only here, with the file the
// request brought: it is never retried, so a refusal fails it (CID-32).
func (s *Sends) attempt(ctx context.Context, id string, file *SendFile) (dto.PlatformSendResult, error) {
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
	if send.Once && file == nil { // nothing keeps a file: this is a retry that should not exist
		return s.fail(rec, send, int(send.Attempts)+1, interruptedFile, true)
	}
	var created atomic.Int32
	out := Outbound{
		ChatID: row.ChatExternalID, Text: send.Text, ReplyToID: send.ReplyToExternalID, ReplyToSenderID: row.ReplyToSenderID,
		MessageID: send.ID, AuthorName: row.AgentName, CreatedAt: time.UnixMilli(send.CreatedAt).UTC(),
		// The account is the AI's own: no author line, whoever the AI is (CID-15, CID-16).
		Unsigned: true,
		Created: func(platformID string) {
			created.Add(1)
			s.d.Sent.Add(send.ChatID, platformID)
		},
	}
	timeout := s.d.AttemptTimeout
	if file != nil {
		out.Files = []OutFile{outFile(file)}
		timeout = s.d.FileAttemptTimeout
	}
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
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
		reason = fmt.Sprintf("the platform did not answer within %s", timeout)
		if file != nil {
			reason += "; the file may or may not have arrived"
		}
	}
	if file != nil && send.Text != "" && created.Load() > 0 && !timedOut {
		reason += "; the caption was sent, the file was not"
	}
	return s.fail(rec, send, int(send.Attempts)+1, reason, send.Once)
}

// outFile is the file of a send as a connector reads it: from memory, as often as it asks.
func outFile(f *SendFile) OutFile {
	mime := f.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	return OutFile{Name: f.Name, Mime: mime, Size: int64(len(f.Data)), Open: func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(f.Data)), nil
	}}
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

// Recover makes the sends a previous process left in flight due again, except those attempted once
// (a file is not kept, CID-32), which it fails. It runs once: the app calls it before the server accepts
// requests, so a send started by a request is never taken for one a previous process left behind and
// sent twice; Run calls it too, for a Sends started on its own.
func (s *Sends) Recover(ctx context.Context) error {
	if s.recovered.Load() {
		return nil
	}
	var ids []string
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) (err error) {
		now := s.d.Now().UnixMilli()
		ids, err = q.RequeueSendingPlatformSends(ctx, now)
		if err != nil {
			return err
		}
		// A file is not kept: the send that was sending one cannot be sent again (CID-32).
		_, err = q.FailSendingOncePlatformSends(ctx, db.FailSendingOncePlatformSendsParams{LastError: interruptedFile, UpdatedAt: now})
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
	if _, err := s.attempt(ctx, id, nil); err != nil && !errors.Is(err, sql.ErrNoRows) {
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
