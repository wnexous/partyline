package connectors_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// keep sets how long the connection keeps its chat history (CH-01). Turning it on or off restarts the
// connection (CH-10): keep waits for the new instance to run.
func (e *inboundEnv) keep(t *testing.T, days int64) dto.Connector {
	t.Helper()
	before := e.connector(t).HistoryDays
	c := e.patch(t, dto.UpdateConnectorRequest{HistoryDays: &days})
	if (before == 0) != (days == 0) {
		require.Eventually(t, func() bool { return e.fake.Lookup(e.conn.ID) != e.inst }, connectortest.Timeout, 5*time.Millisecond,
			"turning the history on or off restarts the connection")
		e.inst = e.fake.Lookup(e.conn.ID)
		e.inst.WaitRun(t)
	}
	return c
}

// history reads a page of the history of the chat (a chat id or an address) as alice, who manages the
// connection (CH-07).
func (e *inboundEnv) history(t *testing.T, chat string, query ...string) dto.ChatHistory {
	t.Helper()
	path := "/api/v1/connectors/" + e.conn.ID + "/chats/" + url.PathEscape(chat) + "/history"
	if len(query) > 0 {
		path += "?" + query[0]
	}
	return testkit.Decode[dto.ChatHistory](e.alice.Get(path).RequireStatus(http.StatusOK))
}

// exec runs a statement on the database, with its foreign keys on, as the product's store has them.
func (e *inboundEnv) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(e.env.App.Config.DataDir, app.DatabaseFile)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.ExecContext(context.Background(), query, args...)
	require.NoError(t, err)
}

func historyTexts(h dto.ChatHistory) []string {
	out := make([]string, len(h.Messages))
	for i, m := range h.Messages {
		out[i] = m.Text
	}
	return out
}

// CH-01: a connection keeps no history until its owner turns it on: off (0) is the default, and the
// retention is 1 to 365 days or -1 for forever; anything else is refused on history_days.
func TestCH01RetentionIsOffByDefaultAndValidated(t *testing.T) {
	e := newInboundEnv(t)
	require.Equal(t, int64(0), e.connector(t).HistoryDays)
	for _, days := range []int64{7, 30, 90, 1, 365, -1, 0} {
		require.Equal(t, days, e.keep(t, days).HistoryDays)
	}
	for _, days := range []int64{366, -2, 1000} {
		res := e.alice.Patch("/api/v1/connectors/"+e.conn.ID, dto.UpdateConnectorRequest{HistoryDays: &days})
		res.RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, string(res.Body), "history_days")
	}
	// Nothing is kept while it is off.
	e.emit(dmText(dmA, "D1", "hello"))
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM chat_history`))
}

// CH-02: while history is on, a chat keeps what people said (delivered or held back by mentions-only),
// what the AI sent through the connection and what the account typed on the platform, newest last.
// The messages carry the ids the AI knew them by: the queued item's, the send's.
func TestCH02KeepsWhatArrivesWhatIsSentAndWhatIsTyped(t *testing.T) {
	e := newInboundEnv(t)
	e.keep(t, 30)
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	in := dmText(dmA, "D1", "where is my order?")
	in.At = at
	e.emit(in)
	item := e.lastQueued(t).ID
	chat := e.chatID(t, dmA)
	send := e.connectionSend(t, chat, "on its way")
	e.inst.WaitSend(t)
	e.emit(ownDM(dmA, "ext-1", "on its way")) // the echo of the send: kept once, as the send
	e.emit(ownDM(dmA, "TYPED", "typed on the phone"))

	h := e.history(t, chat)
	require.Equal(t, []string{"where is my order?", "on its way", "typed on the phone"}, historyTexts(h))
	first, sent, typed := h.Messages[0], h.Messages[1], h.Messages[2]
	require.Equal(t, []any{item, "Customer", dmA, false, false, dto.MessageKindText},
		[]any{first.MessageID, first.From, first.SenderID, first.FromSelf, first.Trusted, first.Kind})
	require.True(t, first.Time.Equal(at), "the platform's time: %v", first.Time)
	require.Equal(t, []any{send.ID, "you", true, ""}, []any{sent.MessageID, sent.From, sent.FromSelf, sent.SenderID})
	require.Equal(t, []any{"you", true, false}, []any{typed.From, typed.FromSelf, typed.Trusted})
	require.Equal(t, dto.ChatKindDM, h.ChatKind)
	require.False(t, h.HasMore)

	// A group message that mentions-only keeps from the AI is still part of the group's history.
	e.emit(text(groupID, "G1", "anyone there?"))
	require.Equal(t, []string{"where is my order?", "typed on the phone"}, texts(e.queued(t)), "not delivered")
	group := e.history(t, e.chatID(t, groupID))
	require.Equal(t, []string{"anyone there?"}, historyTexts(group))
	require.NotEmpty(t, group.Messages[0].MessageID)

	// A repeat of a platform message is kept once.
	e.emit(dmText(dmA, "D1", "where is my order?"))
	require.Len(t, e.history(t, chat).Messages, 3)
}

// CH-02: a chat the deliver setting excludes is not kept: the owner chose that the AI does not deal
// with it.
func TestCH02ChatsTheConnectionDoesNotDeliverAreNotKept(t *testing.T) {
	e := newInboundEnv(t)
	e.keep(t, 30)
	dms := dto.ConnectorDeliverDMs
	e.patch(t, dto.UpdateConnectorRequest{Deliver: &dms})
	e.emit(mention(groupID, "G1", "hello all"))
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM chat_history`))
}

// CH-02: an edit shows the new text and "edited", a delete drops the text and reads "deleted", and a
// reaction is listed on its target: one per sender, the newest, removed by an empty one. The AI's own
// reaction through the connection is listed as "you".
func TestCH02EditsDeletesAndReactionsChangeTheirTarget(t *testing.T) {
	e := newInboundEnv(t)
	e.keep(t, 30)
	e.emit(dmText(dmA, "D1", "helo"))
	e.emit(dmText(dmA, "D2", "my card number is wrong"))
	chat := e.chatID(t, dmA)

	edit := dmText(dmA, "E1", "hello")
	edit.Kind, edit.ReplyToID = string(dto.MessageKindEdit), "D1"
	e.emit(edit)
	revoke := dmText(dmA, "R1", "(message deleted)")
	revoke.Kind, revoke.ReplyToID = string(dto.MessageKindRevoke), "D2"
	e.emit(revoke)
	for _, r := range []struct{ id, emoji string }{{"X1", "👍"}, {"X2", "❤️"}} {
		m := dmText(dmA, r.id, r.emoji)
		m.Kind, m.ReplyToID = string(dto.MessageKindReaction), "D1"
		e.emit(m)
	}
	gone := dmText(dmA, "X3", connectors.ReactionRemoved)
	gone.Kind, gone.ReplyToID = string(dto.MessageKindReaction), "D2"
	e.emit(gone)
	_, err := e.env.App.Sends.React(context.Background(), connectors.ReactInput{
		AgentID: e.operator, ConnectorID: e.conn.ID, ChatID: chat, TargetID: "D1", TargetSenderID: dmA, Emoji: "🙏",
	})
	require.NoError(t, err)

	h := e.history(t, chat)
	require.Len(t, h.Messages, 2, "edits, deletes and reactions are no messages of their own")
	d1, d2 := h.Messages[0], h.Messages[1]
	require.Equal(t, []any{"hello", true, false}, []any{d1.Text, d1.Edited, d1.Deleted})
	require.Equal(t, []dto.ChatHistoryReaction{
		{Emoji: "❤️", From: "Customer", SenderID: dmA},
		{Emoji: "🙏", From: "you", FromSelf: true},
	}, d1.Reactions)
	require.Equal(t, []any{"", true, 0}, []any{d2.Text, d2.Deleted, len(d2.Reactions)})

	// The edit, the delete and the reactions are still delivered as before (CH-04).
	kinds := map[dto.MessageKind]int{}
	for _, it := range e.queued(t) {
		kinds[it.Kind]++
	}
	require.Equal(t, map[dto.MessageKind]int{dto.MessageKindText: 2, dto.MessageKindEdit: 1, dto.MessageKindRevoke: 1, dto.MessageKindReaction: 3}, kinds)

	// A deleted message stays deleted, and an edit of a message the history lacks changes nothing.
	edit.ReplyToID, edit.MessageID = "D2", "E2"
	e.emit(edit)
	edit.ReplyToID, edit.MessageID = "UNKNOWN", "E3"
	e.emit(edit)
	h = e.history(t, chat)
	require.Equal(t, []any{"", true}, []any{h.Messages[1].Text, h.Messages[1].Deleted})
	require.Len(t, h.Messages, 2)
}

// CH-02: a quote of an earlier kept message names it, and the files of a message are listed by name
// and type, downloadable (with an id) while the queue holds them.
func TestCH02QuotesAndFilesOfKeptMessages(t *testing.T) {
	e := newInboundEnv(t)
	e.keep(t, 30)
	e.emit(dmText(dmA, "D1", "which one?"))
	first := e.lastQueued(t).ID
	quote := dmText(dmA, "D2", "that one")
	quote.Quote = &connectors.Quote{ID: "D1", Preview: "which one?"}
	e.emit(quote)
	file := dmText(dmA, "D3", "the receipt")
	file.Kind = string(dto.MessageKindDocument)
	file.Media = []connectors.Media{{Name: "receipt.pdf", Mime: "application/pdf", Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("%PDF-1")), nil
	}}}
	e.emit(file)

	h := e.history(t, e.chatID(t, dmA))
	require.Equal(t, []any{first, "which one?"}, []any{h.Messages[1].ReplyTo, h.Messages[1].Quoted})
	require.Len(t, h.Messages[2].Attachments, 1)
	got := h.Messages[2].Attachments[0]
	require.Equal(t, []any{"receipt.pdf", "application/pdf", dto.AttachmentStatusStored}, []any{got.Name, got.Mime, got.Status})
	require.Equal(t, e.lastQueued(t).Attachments[0].ID, got.ID, "the queued file's id")

	// Once the queue let the file go, the history names it without an id.
	require.NoError(t, e.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		_, err := q.DeletePlatformItems(context.Background(), []string{e.lastQueued(t).ID})
		return err
	}))
	got = e.history(t, e.chatID(t, dmA)).Messages[2].Attachments[0]
	require.Equal(t, []any{"", dto.AttachmentStatusExpired}, []any{got.ID, got.Status})
}

// CH-03: lowering the retention deletes what falls outside it at once; the purge deletes what grows
// older than it; forever deletes nothing for its age; turning it off deletes the connection's history
// at once; deleting a chat or the connection deletes theirs.
func TestCH03RetentionDeletes(t *testing.T) {
	e := newInboundEnv(t)
	e.keep(t, -1)
	for i, age := range []time.Duration{200 * 24 * time.Hour, 40 * 24 * time.Hour, 10 * 24 * time.Hour, time.Hour} {
		m := dmText(dmA, fmt.Sprintf("D%d", i), fmt.Sprintf("%d days old", int(age.Hours()/24)))
		m.At = time.Now().Add(-age)
		e.emit(m)
	}
	chat := e.chatID(t, dmA)
	require.Len(t, e.history(t, chat).Messages, 4, "forever keeps a message of any age")
	n, err := e.env.App.History.Purge(context.Background())
	require.NoError(t, err)
	require.Zero(t, n, "forever: the purge deletes nothing")

	e.keep(t, 90)
	require.Equal(t, []string{"40 days old", "10 days old", "0 days old"}, historyTexts(e.history(t, chat)), "a shorter retention deletes at once")
	e.keep(t, 365)
	require.Len(t, e.history(t, chat).Messages, 3, "a longer one brings nothing back")

	// The purge: the retention went down without the update's own delete (as time passes).
	require.NoError(t, e.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SetConnectorHistoryDays(context.Background(), db.SetConnectorHistoryDaysParams{HistoryDays: 7, UpdatedAt: 1, ID: e.conn.ID})
	}))
	n, err = e.env.App.History.Purge(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
	require.Equal(t, []string{"0 days old"}, historyTexts(e.history(t, chat)))

	// A message older than the retention is not kept in the first place.
	old := dmText(dmA, "OLD", "too old")
	old.At = time.Now().Add(-8 * 24 * time.Hour)
	e.emit(old)
	require.Len(t, e.history(t, chat).Messages, 1)

	e.keep(t, 0)
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM chat_history`), "off deletes the connection's history at once")
	e.alice.Get("/api/v1/connectors/"+e.conn.ID+"/chats/"+chat+"/history").RequireError(http.StatusConflict, dto.ErrorCodeHistoryOff)

	e.keep(t, 30)
	e.emit(dmText(dmA, "N1", "new"))
	e.emit(dmText(dmB, "N2", "other chat"))
	e.exec(t, `DELETE FROM chats WHERE id = ?`, chat)
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM chat_history`), "the chat's history went with it")
	e.alice.Delete("/api/v1/connectors/" + e.conn.ID).RequireStatus(http.StatusNoContent)
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM chat_history`), "and the connection's with it")
}

// CH-04: keeping history changes no delivery: an acknowledged item loses its text and a finished send
// its text, as before; only the history keeps them.
func TestCH04HistoryIsAStoreOfItsOwn(t *testing.T) {
	e := newInboundEnv(t)
	e.keep(t, 30)
	e.emit(dmText(dmA, "D1", "oi"))
	e.drain(t)
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM platform_items WHERE text = ''`), "the ack cleared the item's text")
	send := e.connectionSend(t, e.chatID(t, dmA), "olá")
	e.inst.WaitSend(t)
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM platform_sends WHERE id = ? AND text = ''`, send.ID), "the send's text is gone")
	require.Equal(t, []string{"oi", "olá"}, historyTexts(e.history(t, e.chatID(t, dmA))))
}

// CH-05: the history of a chat reads newest last, a page at a time (20 by default, at most 100), back
// with before; the chat may be named by its platform address.
func TestCH05HistoryPages(t *testing.T) {
	e := newInboundEnv(t)
	e.keep(t, 30)
	base := time.Now().Add(-time.Hour)
	for i := range 105 {
		m := dmText(dmA, fmt.Sprintf("D%03d", i), fmt.Sprintf("m%03d", i))
		m.At = base.Add(time.Duration(i) * time.Second)
		e.emit(m)
	}
	chat := e.chatID(t, dmA)
	page := e.history(t, chat)
	require.Len(t, page.Messages, 20)
	require.Equal(t, "m104", page.Messages[19].Text, "newest last")
	require.True(t, page.HasMore)
	require.Len(t, e.history(t, chat, "limit=500").Messages, 100, "at most 100")

	page = e.history(t, chat, "limit=50")
	older := e.history(t, chat, "limit=100&before="+page.Messages[0].MessageID)
	require.Len(t, older.Messages, 55)
	require.Equal(t, []string{"m000", "m054"}, []string{older.Messages[0].Text, older.Messages[54].Text})
	require.False(t, older.HasMore)

	e.alice.Get("/api/v1/connectors/"+e.conn.ID+"/chats/"+chat+"/history?before=nope").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	e.alice.Get("/api/v1/connectors/"+e.conn.ID+"/chats/hello/history").RequireError(http.StatusNotFound, dto.ErrorCodeUnknownChat)
}

// CH-08, CH-09: messages of a chat's past that the platform sends (on its own or because the AI asked)
// are added to the history only: never queued for the AI, kept once by their platform id (also when
// the live message or the AI's own send has it), within the retention; a chat the connection did not
// have yet is recorded.
func TestCH09BackfillIsKeptNeverDelivered(t *testing.T) {
	e := newInboundEnv(t)
	e.keep(t, 30)
	e.emit(dmText(dmA, "LIVE", "live message"))
	chat := e.chatID(t, dmA)
	send := e.connectionSend(t, chat, "from the AI")
	e.inst.WaitSend(t)
	queued := len(e.queued(t))

	past := func(chatID, id, body string, age time.Duration, self bool) connectors.Inbound {
		m := dmText(chatID, id, body)
		m.At, m.Backfill, m.FromSelf = time.Now().Add(-age), true, self
		return m
	}
	e.emit(past(dmA, "OLD1", "a week ago", 7*24*time.Hour, false))
	e.emit(past(dmA, "OLD2", "typed long ago", 6*24*time.Hour, true))
	e.emit(past(dmA, "LIVE", "live message", time.Minute, false)) // the live one, again
	e.emit(past(dmA, "ext-1", "from the AI", time.Minute, true))  // the AI's send, again
	e.emit(past(dmA, "OLD1", "a week ago", 7*24*time.Hour, false))
	e.emit(past(dmA, "TOOOLD", "too old", 31*24*time.Hour, false))
	e.emit(past(dmB, "B1", "a chat Partyline never saw", 24*time.Hour, false))
	e.emit(past("120363000000000099@g.us", "OLDG", "older than the retention", 40*24*time.Hour, false))

	require.Len(t, e.queued(t), queued, "nothing of the past is delivered")
	h := e.history(t, chat)
	require.Equal(t, []string{"a week ago", "typed long ago", "live message", "from the AI"}, historyTexts(h))
	require.Equal(t, send.ID, h.Messages[3].MessageID)
	require.Equal(t, "you", h.Messages[1].From)
	require.True(t, e.hasChat(t, dmB), "CH-09: a conversation of the sync becomes a chat")
	require.Equal(t, []string{"a chat Partyline never saw"}, historyTexts(e.history(t, e.chatID(t, dmB))))
	require.False(t, e.hasChat(t, "120363000000000099@g.us"), "nothing of it is kept: no chat either")

	// Forever keeps every message the sync brings, whatever its age.
	e.keep(t, -1)
	e.emit(past(dmA, "TOOOLD", "too old", 31*24*time.Hour, false))
	require.Equal(t, "too old", e.history(t, chat).Messages[0].Text)
}
