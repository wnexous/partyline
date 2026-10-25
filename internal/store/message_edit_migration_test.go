package store

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// messageViewRow reads the columns of a message the editing feature adds and the author's name the view has
// always had.
func (e *schemaEnv) messageViewRow(id string) (edited, deleted *int64, author, text string) {
	e.t.Helper()
	require.NoError(e.t, e.s.write.QueryRowContext(context.Background(),
		`SELECT edited_at, deleted_at, author_name, text FROM message_views WHERE id = ?`, id).Scan(&edited, &deleted, &author, &text))
	return edited, deleted, author, text
}

// EDT-01, DLT-01 (AD-026): migration 0023 adds edited_at and deleted_at to messages and to message_views, both
// NULL for a message that was never edited or deleted - every message stored before it - and changes nothing
// else: the view keeps every column it had, in its order, and the message keeps every value.
func TestMigration0023AddsEditedAndDeletedAt(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0023")}
	require.NotContains(t, e.columnsOf("messages"), "edited_at", "fixture sanity: schema before 0023")
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u-alice', 'alice', 'x', 'member', 1)`)
	require.NoError(t, e.intelligence("u-alice", "human", "alice"))
	e.must(`INSERT INTO rooms (id, owner_id, name, kind, created_at, updated_at) VALUES ('r-ops', 'u-alice', 'ops', 'group', 1, 1)`)
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_user_id, author_iid, kind, text, reply_to, created_at)
		VALUES ('m-1', 'r-ops', 1, 'user', 'u-alice', 'u-alice', 'u-alice', 'text', 'hello', '', 1790000000000)`)
	viewBefore := e.columnsOf("message_views")
	rowBefore := e.rowsOf("messages", e.columnsOf("messages"))

	require.NoError(t, e.s.applyMigration(ctx, "0023_message_edit_delete.sql"))
	require.Contains(t, versions(t, e.s), "0023_message_edit_delete")

	cols := e.columnsOf("messages")
	require.Equal(t, []string{"edited_at", "deleted_at"}, cols[len(cols)-2:], "the two columns are added at the end of messages")
	require.Equal(t, rowBefore, e.rowsOf("messages", columnsWithout(cols, "edited_at", "deleted_at")), "no value of the message changed")
	viewAfter := e.columnsOf("message_views")
	require.Equal(t, viewBefore, columnsWithout(viewAfter, "edited_at", "deleted_at"), "the view keeps every column it had, in its order")
	require.Contains(t, viewAfter, "edited_at")
	require.Contains(t, viewAfter, "deleted_at")

	edited, deleted, author, text := e.messageViewRow("m-1")
	require.Nil(t, edited, "a message stored before 0023 was never edited")
	require.Nil(t, deleted, "nor deleted")
	require.Equal(t, "alice", author, "the view still names the author")
	require.Equal(t, "hello", text)

	// A statement that does not know the columns - the posts of a CLI of an older build run the same SQL - still
	// inserts, and an edit and a delete are plain updates.
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_iid, kind, text, created_at)
		VALUES ('m-2', 'r-ops', 2, 'user', 'u-alice', 'u-alice', 'text', 'second', 1790000000001)`)
	e.must(`UPDATE messages SET text = 'hello!', edited_at = 1790000000500 WHERE id = 'm-1'`)
	e.must(`UPDATE messages SET text = '', edited_at = NULL, deleted_at = 1790000000600 WHERE id = 'm-2'`)
	edited, deleted, _, text = e.messageViewRow("m-1")
	require.Equal(t, int64(1790000000500), *edited)
	require.Nil(t, deleted)
	require.Equal(t, "hello!", text)
	edited, deleted, _, text = e.messageViewRow("m-2")
	require.Nil(t, edited)
	require.Equal(t, int64(1790000000600), *deleted)
	require.Empty(t, text)
}

// columnsWithout returns cols without the named columns, in order.
func columnsWithout(cols []string, names ...string) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		if !slices.Contains(names, c) {
			out = append(out, c)
		}
	}
	return out
}
