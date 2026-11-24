package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// QC-01, QC-04, QC-05 (data): migration 0041 adds the quote of a queued item (reply_to, quoted,
// quoted_from_self) and the later platform ids of a send. It is additive: an item and a send of
// before the upgrade keep their rows and read "no quote".
func TestQC01Migration0041AddsTheQuoteColumns(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0041")}
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u1', 'alice', 'x', 'member', 1)`)
	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u1', 'atlas', 1)`)
	e.must(`INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES ('k1', 'u1', 'whatsapp', 'wa', 1, 1)`)
	e.must(`INSERT INTO chats (id, connector_id, external_id, created_at) VALUES ('c1', 'k1', '42', 1)`)
	e.must(`INSERT INTO platform_items (id, agent_id, connector_id, chat_id, external_id, text, received_at)
		VALUES ('i1', 'a1', 'k1', 'c1', 'E1', 'before', 1)`)
	e.must(`INSERT INTO platform_sends (id, connector_id, chat_id, agent_id, status, external_id, next_attempt_at, created_at, updated_at)
		VALUES ('s1', 'k1', 'c1', 'a1', 'sent', 'E2', 1, 1, 1)`)

	require.NoError(t, e.s.applyMigration(ctx, "0041_quoted_context.sql"))

	var replyTo, quoted string
	var fromSelf bool
	require.NoError(t, e.s.write.QueryRowContext(ctx, `SELECT reply_to, quoted, quoted_from_self FROM platform_items WHERE id = 'i1'`).
		Scan(&replyTo, &quoted, &fromSelf))
	require.Equal(t, []any{"", "", false}, []any{replyTo, quoted, fromSelf}, "an item of before reads no quote")
	require.Equal(t, []string{"send_id", "chat_id", "external_id"}, e.columnsOf("platform_send_parts"))

	// A part goes with its send.
	e.must(`INSERT INTO platform_send_parts (send_id, chat_id, external_id) VALUES ('s1', 'c1', 'E3')`)
	e.must(`DELETE FROM platform_sends WHERE id = 's1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM platform_send_parts`))
}
