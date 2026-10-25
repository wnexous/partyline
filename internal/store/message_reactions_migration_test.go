package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// RCT-05, RCT-07 (schema): migration 0024 creates the reactions of messages. Each intelligence uses an
// emoji once on a message; a reaction exists only for a message and an intelligence that exist, and
// goes with either of them, or with the room that holds the message.

// reactionEnv is a DM of two users with two messages and a third intelligence that reacts.
func reactionEnv(t *testing.T) *schemaEnv {
	t.Helper()
	e := newSchemaEnv(t)
	e.user("u1", "alice")
	e.user("u2", "bob")
	require.NoError(t, e.intelligence("ghost", "human", "ghost"))
	require.NoError(t, e.dm("r1", "u1", "u1", "u2"))
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, created_at) VALUES ('m1', 'r1', 1, 'user', 1)`)
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, created_at) VALUES ('m2', 'r1', 2, 'user', 2)`)
	return e
}

func (e *schemaEnv) react(message, iid, emoji string) error {
	return e.exec(`INSERT INTO message_reactions (message_id, intelligence_id, emoji, created_at) VALUES (?, ?, ?, 1)`, message, iid, emoji)
}

func TestMessageReactionsAreUniquePerMessageIntelligenceAndEmoji(t *testing.T) {
	e := reactionEnv(t)
	require.NoError(t, e.react("m1", "u1", "👍"))
	require.Error(t, e.react("m1", "u1", "👍"), "the same reaction twice")
	require.NoError(t, e.react("m1", "u1", "🎉"), "another emoji of the same intelligence")
	require.NoError(t, e.react("m1", "u2", "👍"), "the same emoji of another intelligence")
	require.NoError(t, e.react("m2", "u1", "👍"), "the same reaction on another message")
	require.Equal(t, 4, e.count(`SELECT COUNT(*) FROM message_reactions`))
}

func TestAMessageReactionNeedsItsMessageAndItsIntelligence(t *testing.T) {
	e := reactionEnv(t)
	require.Error(t, e.react("no-such-message", "u1", "👍"))
	require.Error(t, e.react("m1", "no-such-intelligence", "👍"))
	require.Equal(t, 0, e.count(`SELECT COUNT(*) FROM message_reactions`))
}

func TestReactionsAreListedByTheOrderTheyWereMade(t *testing.T) {
	e := reactionEnv(t)
	for _, r := range [][2]string{{"u2", "🎉"}, {"u1", "👍"}, {"u1", "🎉"}} {
		require.NoError(t, e.react("m1", r[0], r[1]))
	}
	rows, err := e.s.R().ListReactionsByMessages(t.Context(), []string{"m1"})
	require.NoError(t, err)
	var got [][2]string
	for _, r := range rows {
		got = append(got, [2]string{r.IntelligenceID, r.Emoji})
	}
	require.Equal(t, [][2]string{{"u2", "🎉"}, {"u1", "👍"}, {"u1", "🎉"}}, got)
}

func TestAReactionGoesWithItsMessage(t *testing.T) {
	e := reactionEnv(t)
	require.NoError(t, e.react("m1", "u1", "👍"))
	require.NoError(t, e.react("m2", "u1", "👍"))
	e.must(`DELETE FROM messages WHERE id = 'm1'`)
	require.Equal(t, 0, e.count(`SELECT COUNT(*) FROM message_reactions WHERE message_id = 'm1'`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM message_reactions WHERE message_id = 'm2'`), "the other message keeps its own")
}

func TestAReactionGoesWithItsRoom(t *testing.T) {
	e := reactionEnv(t)
	require.NoError(t, e.react("m1", "u1", "👍"))
	require.NoError(t, e.react("m2", "u2", "🎉"))
	e.must(`DELETE FROM rooms WHERE id = 'r1'`)
	require.Equal(t, 0, e.count(`SELECT COUNT(*) FROM message_reactions`))
}

func TestAReactionGoesWithItsIntelligence(t *testing.T) {
	e := reactionEnv(t)
	require.NoError(t, e.react("m1", "ghost", "👍"))
	require.NoError(t, e.react("m1", "u1", "👍"))
	e.must(`DELETE FROM intelligences WHERE id = 'ghost'`)
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM message_reactions`), "only the one that left")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM message_reactions WHERE intelligence_id = 'u1'`))
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM messages`), "its messages stay")
}
