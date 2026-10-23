package connectors_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/validation"
)

// sendsFixture is a connection operated by the AI Atlas, with a running fake platform, a group chat
// and the Sends service on a clock the test moves. Its poll interval is an hour, so that only the
// calls of a test send anything.
type sendsFixture struct {
	*managerFixture
	clock  *testkit.Clock
	sent   *connectors.SentIDs
	queue  *platform.Queue
	sends  *connectors.Sends
	atlas  string
	conn   string
	chat   db.Chat
	inst   *connectortest.Instance
	logged *strings.Builder
}

func newSendsFixture(t *testing.T, tune ...func(*connectors.SendsDeps)) *sendsFixture {
	t.Helper()
	f := &sendsFixture{managerFixture: newManagerFixture(t), clock: testkit.NewClock(time.Now()), logged: &strings.Builder{}}
	f.conn = f.insertWithoutOperator(t, true)
	f.atlas = f.ai(t)
	f.setOperator(t, f.conn, f.atlas)
	f.m.Start(context.Background())
	f.inst = f.fake.Instance(t, f.conn)
	f.inst.WaitRun(t)
	f.chat = f.newChat(t, f.conn, groupID, "Team", "group")
	f.sent = connectors.NewSentIDs(f.clock.Now)
	f.queue = platform.New(platform.Deps{Store: f.st, Bus: f.bus, Now: f.clock.Now})
	deps := connectors.SendsDeps{
		Store: f.st, Transport: f.m, Sent: f.sent, Now: f.clock.Now, PollInterval: time.Hour,
		Logger: slog.New(slog.NewTextHandler(f.logged, nil)),
	}
	for _, tn := range tune {
		tn(&deps)
	}
	f.sends = connectors.NewSends(deps)
	return f
}

func (f *sendsFixture) newChat(t *testing.T, conn, external, title, kind string) db.Chat {
	t.Helper()
	c, err := connectors.UpsertChat(context.Background(), f.st, f.bus, conn, connectors.ChatInfo{ID: external, Title: title, Kind: kind}, time.Time{}, f.clock.Now())
	require.NoError(t, err)
	return c
}

func (f *sendsFixture) input(text string) connectors.SendInput {
	return connectors.SendInput{AgentID: f.atlas, ConnectorID: f.conn, ChatID: f.chat.ID, Text: text}
}

// send sends text to the group as Atlas.
func (f *sendsFixture) send(text string) (dto.PlatformSendResult, error) {
	return f.sends.Send(context.Background(), f.input(text))
}

func (f *sendsFixture) sendRow(t *testing.T, id string) db.PlatformSend {
	t.Helper()
	r, err := f.st.R().GetPlatformSend(context.Background(), id)
	require.NoError(t, err)
	return r
}

// insertSend stores a send in the state a worker left it in, last changed at `at`.
func (f *sendsFixture) insertSend(t *testing.T, status string, at time.Time, text string) string {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
		if err := q.InsertPlatformSend(ctx, db.InsertPlatformSendParams{
			ID: id, ConnectorID: f.conn, ChatID: f.chat.ID, AgentID: f.atlas, Text: text,
			NextAttemptAt: at.UnixMilli(), CreatedAt: at.UnixMilli(), UpdatedAt: at.UnixMilli(),
		}); err != nil {
			return err
		}
		switch status {
		case "sent":
			return q.MarkPlatformSendSent(ctx, db.MarkPlatformSendSentParams{ExternalID: "x-" + id, UpdatedAt: at.UnixMilli(), ID: id})
		case "failed":
			return q.MarkPlatformSendFailed(ctx, db.MarkPlatformSendFailedParams{Attempts: 5, LastError: "gone", UpdatedAt: at.UnixMilli(), ID: id})
		case "pending":
			return q.MarkPlatformSendRetry(ctx, db.MarkPlatformSendRetryParams{Attempts: 1, NextAttemptAt: at.Add(time.Hour).UnixMilli(), LastError: "later", UpdatedAt: at.UnixMilli(), ID: id})
		}
		return nil // sending
	}))
	return id
}

// sendRows counts the rows of platform_sends, through a connection of its own to the database.
func (f *sendsFixture) sendRows(t *testing.T) int {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+f.path+"?mode=ro&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	var n int
	require.NoError(t, raw.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM platform_sends").Scan(&n))
	return n
}

func (f *sendsFixture) exists(t *testing.T, id string) bool {
	t.Helper()
	_, err := f.st.R().GetPlatformSend(context.Background(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	require.NoError(t, err)
	return true
}

// CID-16, CID-14: a send through a connection goes to the chat's own platform id at once, unsigned
// (no author line whoever the author is), and the platform's id of what it created is recorded; the
// text is gone from the row.
func TestASendGoesOutUnsignedAndItsIDIsRecorded(t *testing.T) {
	f := newSendsFixture(t)
	res, err := f.send("on its way")
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusSent}, res)
	require.NotEmpty(t, res.ID)

	sent := f.inst.Sent()
	require.Len(t, sent, 1)
	got := sent[0]
	require.Equal(t, []any{groupID, "on its way", true, "", "", ""},
		[]any{got.ChatID, got.Text, got.Unsigned, got.Signature(), got.ReplyToID, got.ReplyToSenderID})
	require.Equal(t, "Atlas", got.AuthorName, "the name still goes to platforms that carry the author as data")
	require.Equal(t, res.ID, got.MessageID)
	require.Empty(t, got.RoomID, "it belongs to no room")
	require.Empty(t, got.Files)

	row := f.sendRow(t, res.ID)
	require.Equal(t, []any{"sent", int64(1), "ext-1", "", "", f.conn, f.chat.ID, f.atlas},
		[]any{row.Status, row.Attempts, row.ExternalID, row.Text, row.LastError, row.ConnectorID, row.ChatID, row.AgentID})
	require.True(t, f.sent.Has(f.chat.ID, "ext-1"), "the echo of the send is recognized")
}

// CID-14: a send that created several messages (the parts of a long text) is echoed once per message,
// and every one of them is remembered; the row holds the first id only.
func TestEveryMessageOfASendIsRemembered(t *testing.T) {
	f := newSendsFixture(t)
	f.inst.SetSendParts(2)
	res, err := f.send("a long answer")
	require.NoError(t, err)
	require.Equal(t, "ext-1", f.sendRow(t, res.ID).ExternalID)
	for _, id := range []string{"ext-1", "ext-1.p1", "ext-1.p2"} {
		require.True(t, f.sent.Has(f.chat.ID, id), id)
	}
	require.False(t, f.sent.Has(f.chat.ID, "ext-1.p3"))
}

// CID-14: a connector that tells nothing about the messages it created leaves the id the send
// returned, which is remembered all the same.
func TestTheReturnedIDIsRememberedWhenTheConnectorTellsNothing(t *testing.T) {
	f := newSendsFixture(t)
	f.inst.QuietSends()
	_, err := f.send("on its way")
	require.NoError(t, err)
	require.True(t, f.sent.Has(f.chat.ID, "ext-1"))
}

// CID-15: a reply quotes the platform id of the message it answers and, in a group, who wrote it
// (the platform needs the participant); a message the queue no longer holds is quoted by its id alone.
func TestAReplyQuotesTheMessageAndItsSender(t *testing.T) {
	f := newSendsFixture(t)
	ctx := context.Background()
	enqueue := func(chat db.Chat, external, sender string) {
		t.Helper()
		_, queued, err := f.queue.Enqueue(ctx, platform.Item{
			AgentID: f.atlas, ConnectorID: f.conn, ChatID: chat.ID, ExternalID: external, SenderID: sender, SenderName: "Customer", Text: "where is my order?",
		})
		require.NoError(t, err)
		require.True(t, queued)
	}
	other := f.newChat(t, f.conn, otherID, "Other group", "group")
	enqueue(other, "M1", sender2) // the same platform id in another chat, from someone else
	enqueue(f.chat, "M1", senderID)
	enqueue(f.chat, "", sender3) // a message the platform gave no id: never a quote target

	in := f.input("on its way")
	in.ReplyTo = "M1"
	_, err := f.sends.Send(ctx, in)
	require.NoError(t, err)
	got := f.inst.Sent()[0]
	require.Equal(t, []string{"M1", senderID}, []string{got.ReplyToID, got.ReplyToSenderID}, "the sender of the message in this chat")

	in.ReplyTo = "M0" // purged meanwhile
	_, err = f.sends.Send(ctx, in)
	require.NoError(t, err)
	got = f.inst.Sent()[1]
	require.Equal(t, []string{"M0", ""}, []string{got.ReplyToID, got.ReplyToSenderID})

	_, err = f.send("a plain message") // no reply target: the item without an id is not it
	require.NoError(t, err)
	got = f.inst.Sent()[2]
	require.Equal(t, []string{"", ""}, []string{got.ReplyToID, got.ReplyToSenderID})
}

// CID-18: a platform that refuses leaves the send pending with the reason and its text, and the
// worker tries again after 2 s, then 4 s; the send that finally goes out loses its text.
func TestAFailedSendIsRetriedUntilItSucceeds(t *testing.T) {
	f := newSendsFixture(t)
	ctx := context.Background()
	f.inst.SetSendError(errors.New("platform unavailable"))
	start := f.clock.Now()

	res, err := f.send("on its way")
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusPending, Error: "platform unavailable"}, res)
	row := f.sendRow(t, res.ID)
	require.Equal(t, []any{"pending", int64(1), "platform unavailable", "on its way", start.Add(2 * time.Second).UnixMilli()},
		[]any{row.Status, row.Attempts, row.LastError, row.Text, row.NextAttemptAt}, "the text waits for the retry")
	require.Empty(t, f.inst.Sent())

	f.clock.Advance(2*time.Second - time.Millisecond)
	require.NoError(t, f.sends.Flush(ctx))
	require.Equal(t, int64(1), f.sendRow(t, res.ID).Attempts, "nothing is retried a millisecond early")

	f.clock.Advance(time.Millisecond)
	require.NoError(t, f.sends.Flush(ctx))
	row = f.sendRow(t, res.ID)
	require.Equal(t, []any{"pending", int64(2), f.clock.Now().Add(4 * time.Second).UnixMilli()}, []any{row.Status, row.Attempts, row.NextAttemptAt})

	f.inst.SetSendError(nil)
	f.clock.Advance(4 * time.Second)
	require.NoError(t, f.sends.Flush(ctx))
	row = f.sendRow(t, res.ID)
	require.Equal(t, []any{"sent", int64(3), "", ""}, []any{row.Status, row.Attempts, row.LastError, row.Text}, "sent, and the text is deleted")
	require.NotEmpty(t, row.ExternalID)
	require.True(t, f.sent.Has(f.chat.ID, row.ExternalID))
	sent := f.inst.Sent()
	require.Len(t, sent, 1)
	require.Equal(t, []any{groupID, "on its way", true, res.ID}, []any{sent[0].ChatID, sent[0].Text, sent[0].Unsigned, sent[0].MessageID})

	f.clock.Advance(time.Hour)
	require.NoError(t, f.sends.Flush(ctx))
	require.Len(t, f.inst.Sent(), 1, "a sent message is never sent again")
}

// CID-18: after the fifth attempt the send is failed, its text is deleted and it is never tried again.
func TestASendFailsAfterTheAttemptLimitAndItsTextIsCleared(t *testing.T) {
	f := newSendsFixture(t)
	ctx := context.Background()
	f.inst.SetSendError(errors.New("platform unavailable"))
	res, err := f.send("on its way")
	require.NoError(t, err)

	for i, wait := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second} {
		row := f.sendRow(t, res.ID)
		require.Equal(t, []any{"pending", int64(i + 1), "on its way"}, []any{row.Status, row.Attempts, row.Text}, "after attempt %d", i+1)
		require.Equal(t, f.clock.Now().Add(wait).UnixMilli(), row.NextAttemptAt, "after attempt %d", i+1)
		f.clock.Advance(wait)
		require.NoError(t, f.sends.Flush(ctx))
	}
	row := f.sendRow(t, res.ID)
	require.Equal(t, []any{"failed", int64(5), "platform unavailable", ""}, []any{row.Status, row.Attempts, row.LastError, row.Text})

	f.inst.SetSendError(nil)
	f.clock.Advance(time.Hour)
	require.NoError(t, f.sends.Flush(ctx))
	require.Equal(t, int64(5), f.sendRow(t, res.ID).Attempts)
	require.Empty(t, f.inst.Sent(), "a failed send is not tried again")
}

// CID-18: a platform that never answers fails the attempt after 15 seconds (here 100 ms) with that
// reason, and the send waits for its retry like any other failure.
func TestASendThatNeverAnswersTimesOut(t *testing.T) {
	require.Equal(t, 15*time.Second, connectors.SendAttemptTimeout, "the synchronous attempt takes at most 15 seconds")
	f := newSendsFixture(t, func(d *connectors.SendsDeps) { d.AttemptTimeout = 100 * time.Millisecond })
	f.inst.HoldSends()
	defer f.inst.ReleaseSends()

	res, err := f.send("on its way")
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusPending, Error: "the platform did not answer within 100ms"}, res)
	row := f.sendRow(t, res.ID)
	require.Equal(t, []any{"pending", int64(1), "on its way"}, []any{row.Status, row.Attempts, row.Text})
	require.Empty(t, f.inst.Sent())
}

// CID-18: the caller going away does not abandon a send in flight: the attempt finishes and its
// outcome is recorded.
func TestASendIsNotAbandonedWhenItsCallerGoesAway(t *testing.T) {
	f := newSendsFixture(t)
	f.inst.HoldSends()
	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		res dto.PlatformSendResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := f.sends.Send(ctx, f.input("on its way"))
		done <- outcome{res, err}
	}()
	f.inst.WaitSending(t)
	cancel() // the platform call is in flight when the caller goes away
	f.inst.ReleaseSends()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(connectortest.Timeout):
		t.Fatal("the send never finished")
	}
	require.NoError(t, got.err)
	require.Equal(t, dto.PlatformSendStatusSent, got.res.Status)
	require.Equal(t, "sent", f.sendRow(t, got.res.ID).Status)
	require.Len(t, f.inst.Sent(), 1)
}

// CON-05, CID-18: a connection that is not running fails the attempt, and the send waits for the
// connection: it goes out on the first retry after the connection runs again.
func TestASendWaitsForAStoppedConnection(t *testing.T) {
	f := newSendsFixture(t)
	ctx := context.Background()
	f.m.Stop(f.conn)
	f.inst.WaitStop(t)

	res, err := f.send("on its way")
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendResult{ID: res.ID, Status: dto.PlatformSendStatusPending, Error: "connector is not running"}, res)

	f.m.Apply(f.conn)
	again := f.fake.Instance(t, f.conn)
	again.WaitRun(t)
	f.clock.Advance(2 * time.Second)
	require.NoError(t, f.sends.Flush(ctx))
	require.Equal(t, "sent", f.sendRow(t, res.ID).Status)
	require.Len(t, again.Sent(), 1)
}

// CID-03: only the AI that operates the connection sends through it, also for a send that was
// waiting for a retry when the owner chose another operator: the retry fails without sending.
func TestARetryOfAnAIThatNoLongerOperatesTheConnectionFailsWithoutSending(t *testing.T) {
	f := newSendsFixture(t)
	ctx := context.Background()
	f.inst.SetSendError(errors.New("platform unavailable"))
	res, err := f.send("on its way")
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendStatusPending, res.Status)

	f.setOperator(t, f.conn, f.ai(t))
	f.inst.SetSendError(nil)
	f.clock.Advance(2 * time.Second)
	require.NoError(t, f.sends.Flush(ctx))

	row := f.sendRow(t, res.ID)
	require.Equal(t, []any{"failed", int64(1), "the AI no longer operates this connection", ""},
		[]any{row.Status, row.Attempts, row.LastError, row.Text}, "failed at once, its text deleted")
	require.Empty(t, f.inst.Sent())
}

// CID-18: what a previous process left in flight is due again when the worker starts, and what is
// not due yet waits; the worker keeps its poll interval afterwards.
func TestSendsInterruptedByARestartAreSentAfterStart(t *testing.T) {
	f := newSendsFixture(t)
	interrupted := f.insertSend(t, "sending", f.clock.Now(), "left in flight")
	later := f.insertSend(t, "pending", f.clock.Now(), "not due yet")

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.sends.Run(ctx) }()
	require.Eventually(t, f.sends.Running, connectortest.Timeout, 5*time.Millisecond)
	require.Equal(t, "sent", f.sendRow(t, interrupted).Status)
	require.Equal(t, []string{"left in flight"}, outboundTexts(f.inst.Sent()))
	require.Equal(t, "pending", f.sendRow(t, later).Status, "due in an hour")
	stop()
	require.NoError(t, <-done)
	require.False(t, f.sends.Running())
}

func outboundTexts(list []connectors.Outbound) []string {
	out := make([]string, len(list))
	for i, m := range list {
		out[i] = m.Text
	}
	return out
}

// The worker polls: a retry that became due after it started goes out on its next pass.
func TestTheWorkerRetriesOnItsPollInterval(t *testing.T) {
	f := newSendsFixture(t, func(d *connectors.SendsDeps) { d.PollInterval = 10 * time.Millisecond })
	f.inst.SetSendError(errors.New("platform unavailable"))
	res, err := f.send("on its way")
	require.NoError(t, err)

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.sends.Run(ctx) }()
	require.Eventually(t, f.sends.Running, connectortest.Timeout, 5*time.Millisecond)
	f.inst.SetSendError(nil)
	f.clock.Advance(2 * time.Second)
	require.Eventually(t, func() bool { return f.sendRow(t, res.ID).Status == "sent" }, connectortest.Timeout, 5*time.Millisecond)
	stop()
	require.NoError(t, <-done)
}

// SEC-05: nothing a send carries reaches the logs: not its text, and not the reason the platform gave.
func TestASendLogsNeitherItsTextNorTheReason(t *testing.T) {
	f := newSendsFixture(t)
	f.inst.SetSendError(errors.New("rejected: SECRET-REASON"))
	_, err := f.send("PRIVATE-TEXT of the message")
	require.NoError(t, err)
	f.clock.Advance(2 * time.Second)
	require.NoError(t, f.sends.Flush(context.Background()))
	require.NotEmpty(t, f.logged.String(), "the failed retry is logged")
	require.NotContains(t, f.logged.String(), "PRIVATE-TEXT")
	require.NotContains(t, f.logged.String(), "SECRET-REASON")
}

// A finished send keeps its row, which has no text, for 30 days: long enough for a reply to the
// account's message to be recognized (CID-12), short enough to be no send log. The age is
// counted from the last change; a send still going or waiting stays whatever its age.
func TestFinishedSendsAreDeletedAfterThirtyDays(t *testing.T) {
	require.Equal(t, 30*24*time.Hour, connectors.SendRetention)
	f := newSendsFixture(t)
	now := f.clock.Now()
	age := 30 * 24 * time.Hour
	sentOld := f.insertSend(t, "sent", now.Add(-age), "")
	failedOld := f.insertSend(t, "failed", now.Add(-age), "")
	sentYoung := f.insertSend(t, "sent", now.Add(-age+time.Millisecond), "")
	failedYoung := f.insertSend(t, "failed", now.Add(-age+time.Millisecond), "")
	pendingOld := f.insertSend(t, "pending", now.Add(-2*age), "waiting")
	sendingOld := f.insertSend(t, "sending", now.Add(-2*age), "in flight")

	n, err := f.sends.Purge(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
	for id, want := range map[string]bool{sentOld: false, failedOld: false, sentYoung: true, failedYoung: true, pendingOld: true, sendingOld: true} {
		require.Equal(t, want, f.exists(t, id), id)
	}
}

// The purge deletes a large backlog in bounded batches, not in one long write.
func TestPurgeDeletesEveryFinishedSendInBatches(t *testing.T) {
	f := newSendsFixture(t)
	old := f.clock.Now().Add(-31 * 24 * time.Hour)
	var ids []string
	for range 1005 {
		ids = append(ids, f.insertSend(t, "sent", old, ""))
	}
	n, err := f.sends.Purge(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(1005), n)
	require.False(t, f.exists(t, ids[0]))
	require.False(t, f.exists(t, ids[1004]))
}

// CID-03, CID-17: only the operator sends through a connection, and only to a chat of that
// connection; a refused send changes nothing and sends nothing, and Check says so without storing
// anything either. Each guard is tested alone, with everything else valid (L-018).
func TestSendRefusesWhoIsNotTheOperatorAndChatsOfOtherConnections(t *testing.T) {
	f := newSendsFixture(t)
	ctx := context.Background()
	other := f.ai(t) // another AI of the same owner
	elsewhere := f.insertWithoutOperator(t, false)
	f.setOperator(t, elsewhere, f.atlas)
	foreign := f.newChat(t, elsewhere, otherID, "Elsewhere", "group")

	require.NoError(t, f.sends.Check(ctx, f.input("on its way")), "a valid request passes")
	for name, tc := range map[string]struct {
		mutate func(*connectors.SendInput)
		want   error
	}{
		"another AI of the owner":          {func(in *connectors.SendInput) { in.AgentID = other }, connectors.ErrNotOperator},
		"an agent that does not exist":     {func(in *connectors.SendInput) { in.AgentID = store.NewID() }, connectors.ErrNotOperator},
		"a chat of another connection":     {func(in *connectors.SendInput) { in.ChatID = foreign.ID }, connectors.ErrUnknownChat},
		"a chat that does not exist":       {func(in *connectors.SendInput) { in.ChatID = store.NewID() }, connectors.ErrUnknownChat},
		"a connection that does not exist": {func(in *connectors.SendInput) { in.ConnectorID = store.NewID() }, access.ErrNotFound},
	} {
		in := f.input("on its way")
		tc.mutate(&in)
		require.ErrorIs(t, f.sends.Check(ctx, in), tc.want, "Check: %s", name)
		_, err := f.sends.Send(ctx, in)
		require.ErrorIs(t, err, tc.want, "Send: %s", name)
	}

	// A connection nobody operates has no operator to send: not even the AI that used to.
	f.setOperator(t, f.conn, "")
	require.ErrorIs(t, f.sends.Check(ctx, f.input("on its way")), connectors.ErrNotOperator)
	_, err := f.send("on its way")
	require.ErrorIs(t, err, connectors.ErrNotOperator)

	require.Empty(t, f.inst.Sent())
	require.Zero(t, f.sendRows(t), "nothing was stored for any refusal, nor by Check")
}

// MSG-02: the text of an answer has 1 to 16000 characters; a refusal names the field and changes
// nothing. The limit counts characters, not bytes.
func TestSendValidatesTheText(t *testing.T) {
	f := newSendsFixture(t)
	for name, text := range map[string]string{"empty": "", "blank": " \n\t ", "too long": strings.Repeat("a", dto.MaxTextLength+1)} {
		for call, run := range map[string]func() error{
			"Check": func() error { return f.sends.Check(context.Background(), f.input(text)) },
			"Send":  func() error { _, err := f.send(text); return err },
		} {
			var verr *validation.Error
			require.ErrorAs(t, run(), &verr, "%s of %s", call, name)
			require.Contains(t, verr.Fields, "text", "%s of %s", call, name)
		}
	}
	require.Empty(t, f.inst.Sent())
	require.Zero(t, f.sendRows(t))

	long := strings.Repeat("é", dto.MaxTextLength) // 32000 bytes, 16000 characters
	res, err := f.send(long)
	require.NoError(t, err)
	require.Equal(t, dto.PlatformSendStatusSent, res.Status)
	require.Equal(t, long, f.inst.Sent()[0].Text)
}
