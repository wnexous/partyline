package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// questionMessage is a message row as the quick-reply feature writes it: a question carries
// interactive_json, an answer carries answers_json and replies to the question, every other message
// carries neither.
type questionMessage struct {
	ID          string
	RoomID      string
	Seq         int64
	IID         string
	Text        string
	ReplyTo     string
	Interactive string
	Answers     string
}

func (e *schemaEnv) insertQuestionMessage(m questionMessage) error {
	return e.exec(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_iid, kind, text, reply_to,
		interactive_json, answers_json, created_at)
		VALUES (?, ?, ?, 'user', ?, ?, 'text', ?, ?, ?, ?, 1)`,
		m.ID, m.RoomID, m.Seq, m.IID, m.IID, m.Text, m.ReplyTo, m.Interactive, m.Answers)
}

// questionRow is what the view returns for a message's text and its two interactive parts.
type questionRow struct {
	id          string
	seq         int64
	replyTo     string
	text        string
	interactive string
	answers     string
	editedAt    sql.NullInt64
	deletedAt   sql.NullInt64
}

func (e *schemaEnv) questionRow(id string) questionRow {
	e.t.Helper()
	var r questionRow
	require.NoError(e.t, e.s.write.QueryRowContext(context.Background(),
		`SELECT id, seq, reply_to, text, interactive_json, answers_json, edited_at, deleted_at
		 FROM message_views WHERE id = ?`, id).
		Scan(&r.id, &r.seq, &r.replyTo, &r.text, &r.interactive, &r.answers, &r.editedAt, &r.deletedAt))
	return r
}

func (e *schemaEnv) markMessageDeleted(id string, at int64) int64 {
	e.t.Helper()
	var n int64
	require.NoError(e.t, e.s.WithTx(context.Background(), func(q *db.Queries) error {
		var err error
		n, err = q.MarkMessageDeleted(context.Background(), db.MarkMessageDeletedParams{
			DeletedAt: sql.NullInt64{Int64: at, Valid: true}, ID: id,
		})
		return err
	}))
	return n
}

func (e *schemaEnv) quickReplyRoom() {
	e.t.Helper()
	e.user("u1", "alice")
	e.user("u2", "bob")
	e.must(`INSERT INTO rooms (id, owner_id, name, kind, created_at, updated_at)
		VALUES ('r1', 'u1', 'launch', 'group', 1, 1)`)
}

// QR-01, QR-04 (data): migration 0026 adds the two columns - NOT NULL, defaulting to the empty string
// - to a database that existed before it and to a fresh one, and recreates the view to expose them
// with the two parts empty for every message stored before it.
func TestMigration0026AddsTheQuestionColumns(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0026")}
	cols := e.columnsOf("messages")
	require.NotContains(t, cols, "interactive_json", "fixture sanity: schema before 0026")
	require.NotContains(t, cols, "answers_json", "fixture sanity: schema before 0026")

	e.quickReplyRoom()
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_iid, kind, text, created_at)
		VALUES ('m1', 'r1', 1, 'user', 'u1', 'u1', 'text', 'hello', 1)`)

	require.NoError(t, e.s.applyMigration(ctx, "0026_quick_replies.sql"))
	require.Contains(t, versions(t, e.s), "0026_quick_replies")

	for _, col := range []string{"interactive_json", "answers_json"} {
		var notNull int
		var dflt sql.NullString
		require.NoError(t, e.s.write.QueryRowContext(ctx,
			`SELECT "notnull", dflt_value FROM pragma_table_info('messages') WHERE name = ?`, col).Scan(&notNull, &dflt), col)
		require.Equal(t, 1, notNull, col+" is NOT NULL")
		require.Equal(t, "''", dflt.String, col+" defaults to the empty string")
	}
	require.Contains(t, e.columnsOf("message_views"), "interactive_json", "the view exposes the question")
	require.Contains(t, e.columnsOf("message_views"), "answers_json", "the view exposes the answer")

	row := e.questionRow("m1")
	require.Empty(t, row.interactive, "a message stored before 0026 asks nothing")
	require.Empty(t, row.answers, "nor answers anything")
	require.Equal(t, "hello", row.text, "its text is unchanged")

	require.True(t, e.indexes("messages")["messages_answer_once"], "one answer per author and question")
	var indexSQL string
	require.NoError(t, e.s.write.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'messages_answer_once'`).Scan(&indexSQL))
	require.Contains(t, indexSQL, "WHERE answers_json <> ''", "a plain reply is not indexed")

	// A fresh database gets the same columns, the same view and the same defaults.
	fresh := newSchemaEnv(t)
	fresh.quickReplyRoom()
	fresh.must(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_iid, kind, text, created_at)
		VALUES ('m2', 'r1', 1, 'user', 'u1', 'u1', 'text', 'fresh', 1)`)
	freshRow := fresh.questionRow("m2")
	require.Empty(t, freshRow.interactive)
	require.Empty(t, freshRow.answers)
	require.True(t, fresh.indexes("messages")["messages_answer_once"])
}

// QR-01: a stored question reaches every read through message_views, byte for byte.
func TestTheViewExposesAStoredQuestion(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	e.quickReplyRoom()
	require.NoError(t, e.insertQuestionMessage(questionMessage{ID: "q1", RoomID: "r1", Seq: 1, IID: "u1", Text: "Deploy hoje?"}))

	question := `{"v":1,"quick_replies":[{"id":"r1","label":"Sim"}]}`
	e.must(`UPDATE messages SET interactive_json = ? WHERE id = 'q1'`, question)

	require.Equal(t, question, e.questionRow("q1").interactive)

	view, err := e.s.R().GetMessageView(ctx, "q1")
	require.NoError(t, err)
	require.Equal(t, question, view.InteractiveJson)
	require.Empty(t, view.AnswersJson)
}

// QR-08 (data): the partial unique index allows one structured answer per author and question, any
// number of answers by other authors and any number of plain replies, and frees the slot again when
// the answer is deleted.
func TestOneStructuredAnswerPerAuthorAndQuestion(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	e.quickReplyRoom()
	question := `{"v":1,"quick_replies":[{"id":"r1","label":"Sim"}]}`
	answer := `{"v":1,"quick_reply":"Sim"}`
	require.NoError(t, e.insertQuestionMessage(questionMessage{
		ID: "q1", RoomID: "r1", Seq: 1, IID: "u1", Text: "Deploy hoje?", Interactive: question,
	}))
	require.NoError(t, e.insertQuestionMessage(questionMessage{
		ID: "a1", RoomID: "r1", Seq: 2, IID: "u1", Text: "Sim", ReplyTo: "q1", Answers: answer,
	}))
	countAnswers := func(iid string) int64 {
		e.t.Helper()
		n, err := e.s.R().CountAnswersByAuthor(ctx, db.CountAnswersByAuthorParams{QuestionID: "q1", AuthorIid: iid})
		require.NoError(e.t, err)
		return n
	}
	require.Equal(t, int64(1), countAnswers("u1"))

	err := e.insertQuestionMessage(questionMessage{
		ID: "a2", RoomID: "r1", Seq: 3, IID: "u1", Text: "Sim", ReplyTo: "q1", Answers: answer,
	})
	require.ErrorContains(t, err, "UNIQUE constraint failed: messages.reply_to, messages.author_iid",
		"a second structured answer by the same author")

	require.NoError(t, e.insertQuestionMessage(questionMessage{
		ID: "a3", RoomID: "r1", Seq: 4, IID: "u2", Text: "Sim", ReplyTo: "q1", Answers: answer,
	}), "another author answers the same question")
	require.NoError(t, e.insertQuestionMessage(questionMessage{
		ID: "a4", RoomID: "r1", Seq: 5, IID: "u1", Text: "vou responder depois", ReplyTo: "q1",
	}), "a plain reply by the same author is not an answer")
	require.Equal(t, int64(1), countAnswers("u2"))
	require.Equal(t, int64(1), countAnswers("u1"), "the plain reply is not counted")

	require.Equal(t, int64(1), e.markMessageDeleted("a1", 10))
	require.Zero(t, countAnswers("u1"), "a deleted answer frees the slot")
	require.NoError(t, e.insertQuestionMessage(questionMessage{
		ID: "a5", RoomID: "r1", Seq: 6, IID: "u1", Text: "Sim", ReplyTo: "q1", Answers: answer,
	}), "deleting the answer lets the author answer again")
	require.Equal(t, int64(1), countAnswers("u1"))
	require.Equal(t, 5, e.count(`SELECT COUNT(*) FROM messages`), "the second answer was never stored")
}

// QR-04: deleting a message clears its text and whichever part it carries, keeps its placeholder and
// does not touch the messages around it.
func TestMarkMessageDeletedClearsBothParts(t *testing.T) {
	e := newSchemaEnv(t)
	e.quickReplyRoom()
	question := `{"v":1,"quick_replies":[{"id":"r1","label":"Sim"}]}`
	answer := `{"v":1,"quick_reply":"Sim"}`
	require.NoError(t, e.insertQuestionMessage(questionMessage{
		ID: "q1", RoomID: "r1", Seq: 1, IID: "u1", Text: "Deploy hoje?", Interactive: question,
	}))
	require.NoError(t, e.insertQuestionMessage(questionMessage{
		ID: "a1", RoomID: "r1", Seq: 2, IID: "u2", Text: "Sim", ReplyTo: "q1", Answers: answer,
	}))

	require.Equal(t, int64(1), e.markMessageDeleted("q1", 1000))
	require.Zero(t, e.markMessageDeleted("q1", 2000), "a deleted message is not deleted twice")
	deletedQuestion := e.questionRow("q1")
	require.Equal(t, "q1", deletedQuestion.id)
	require.Equal(t, int64(1), deletedQuestion.seq)
	require.Empty(t, deletedQuestion.text)
	require.Empty(t, deletedQuestion.interactive, "the placeholder keeps nothing of what it asked")
	require.False(t, deletedQuestion.editedAt.Valid)
	require.Equal(t, int64(1000), deletedQuestion.deletedAt.Int64)

	require.Equal(t, int64(1), e.markMessageDeleted("a1", 3000))
	deletedAnswer := e.questionRow("a1")
	require.Equal(t, "a1", deletedAnswer.id)
	require.Equal(t, int64(2), deletedAnswer.seq)
	require.Equal(t, "q1", deletedAnswer.replyTo, "the placeholder still replies to the question")
	require.Empty(t, deletedAnswer.text)
	require.Empty(t, deletedAnswer.answers, "the placeholder keeps nothing of what it answered")
	require.Equal(t, int64(3000), deletedAnswer.deletedAt.Int64)
}
