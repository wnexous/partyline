package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// CH-01, CH-03 (data): migration 0042 is additive. A connection of before the upgrade keeps no history
// (history_days 0); the retention is -1 (forever), 0 (off) or 1 to 365 days; a kept message, its files
// and its reactions go with their chat and their connection; a platform message of a chat is kept once.
func TestCH01Migration0042AddsTheChatHistory(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0042")}
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u1', 'alice', 'x', 'member', 1)`)
	e.must(`INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES ('k1', 'u1', 'whatsapp', 'wa', 1, 1)`)
	e.must(`INSERT INTO chats (id, connector_id, external_id, created_at) VALUES ('c1', 'k1', '42', 1)`)

	require.NoError(t, e.s.applyMigration(ctx, "0042_chat_history.sql"))

	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM connectors WHERE history_days = 0`), "off after the upgrade")
	for _, ok := range []string{"-1", "0", "1", "365"} {
		e.must(`UPDATE connectors SET history_days = ` + ok + ` WHERE id = 'k1'`)
	}
	for _, bad := range []string{"-2", "366"} {
		_, err := e.s.write.ExecContext(ctx, `UPDATE connectors SET history_days = `+bad+` WHERE id = 'k1'`)
		require.Error(t, err, bad)
	}

	e.must(`INSERT INTO chat_history (id, connector_id, chat_id, external_id, text, at) VALUES ('h1', 'k1', 'c1', 'E1', 'hello', 1)`)
	_, err := e.s.write.ExecContext(ctx, `INSERT INTO chat_history (id, connector_id, chat_id, external_id, text, at) VALUES ('h2', 'k1', 'c1', 'E1', 'again', 2)`)
	require.Error(t, err, "a platform message of a chat is kept once")
	e.must(`INSERT INTO chat_history (id, connector_id, chat_id, text, at) VALUES ('h3', 'k1', 'c1', 'no platform id', 3)`)
	e.must(`INSERT INTO chat_history (id, connector_id, chat_id, text, at) VALUES ('h4', 'k1', 'c1', 'no platform id either', 4)`)
	e.must(`INSERT INTO chat_history_attachments (id, message_id, name, mime, status) VALUES ('a1', 'h1', 'p.png', 'image/png', 'stored')`)
	e.must(`INSERT INTO chat_history_reactions (message_id, sender_id, emoji, at) VALUES ('h1', 's1', 'x', 1)`)
	e.must(`DELETE FROM chats WHERE id = 'c1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM chat_history`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM chat_history_attachments`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM chat_history_reactions`))
}
