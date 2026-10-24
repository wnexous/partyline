package api_test

import (
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// react reacts to the message id as the AI of the folder name.
func (f *replyFixture) react(name, id, emoji string) *testkit.Response {
	f.env.T.Helper()
	return f.token.AsAgent(name).Post(replyPath+id+"/react", dto.ReactRequest{Emoji: emoji})
}

// CID-33: the operator reacts to a message of its connection, whether the session acknowledged it or not:
// the reaction goes through the platform from the account, to the platform id of the message in its chat,
// naming who wrote it; the answer is 201 with the state of the send, and nothing is posted to a room.
func TestReactToAMessageOfAConnectionReactsOnThePlatform(t *testing.T) {
	f := newReplyFixture(t)
	waiting := f.enqueue(t, f.atlas, f.group, "M1", "where is my order?")
	delivered := f.enqueue(t, f.atlas, f.dm, "M2", "oi", func(it *platform.Item) { it.SenderID = "5511999990000" })
	typed := f.enqueue(t, f.atlas, f.group, "M3", "typed on the phone", func(it *platform.Item) { it.FromSelf = true })
	require.NoError(t, f.env.App.Platform.Ack(t.Context(), f.atlas, delivered))

	res := f.react("Atlas", waiting, "👍").RequireStatus(http.StatusCreated)
	require.Equal(t, []string{"id", "status"}, keysOf(t, res.Body))
	got := testkit.Decode[dto.PlatformSendResult](res)
	require.Equal(t, dto.PlatformSendStatusSent, got.Status)
	r := f.inst.WaitReact(t)
	require.Equal(t, []any{"120363000000000001@g.us", "M1", "5511999990000", false, "👍"}, []any{r.ChatID, r.TargetID, r.TargetSenderID, r.TargetFromMe, r.Emoji})
	row := f.sendRow(t, got.ID)
	require.Equal(t, []any{"sent", true, "", f.group.ID, f.atlas}, []any{row.Status, row.Once, row.Text, row.ChatID, row.AgentID})

	f.react("Atlas", delivered, " ❤️ ").RequireStatus(http.StatusCreated)
	r = f.inst.WaitReact(t)
	require.Equal(t, []string{"5511999990000", "M2", "❤️"}, []string{r.ChatID, r.TargetID, r.Emoji}, "a direct chat, the emoji trimmed")

	f.react("Atlas", typed, "🔥").RequireStatus(http.StatusCreated)
	require.True(t, f.inst.WaitReact(t).TargetFromMe, "a message the account wrote itself")
	require.Empty(t, f.inst.Sent(), "a reaction is no message")
}

// CID-33, REACT-05: the emoji is one emoji; anything else is refused as a field before anything is sent, and
// a refused request does not use up the AI's 30 posts a minute.
func TestReactionsAreOneEmoji(t *testing.T) {
	f := newReplyFixture(t)
	id := f.enqueue(t, f.atlas, f.group, "M1", "where is my order?")
	for _, emoji := range []string{"", "  ", "ok", "👍👍", "a👍"} {
		e := f.react("Atlas", id, emoji).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, map[string]string{"emoji": "must be one emoji"}, e.Fields, "%q", emoji)
	}
	f.token.AsAgent("Atlas").DoRaw(http.MethodPost, replyPath+id+"/react", "application/json", nil).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	for range 35 {
		f.react("Atlas", id, "no").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	}
	f.react("Atlas", id, "👍").RequireStatus(http.StatusCreated)
	require.Equal(t, "👍", f.inst.WaitReact(t).Emoji)
}

// CID-33, TOOL-08: reactions count against the AI's 30 posts a minute, like every other post.
func TestReactionsCountAgainstThePostLimit(t *testing.T) {
	f := newReplyFixture(t)
	id := f.enqueue(t, f.atlas, f.group, "M1", "where is my order?")
	for i := 1; i <= 30; i++ {
		f.react("Atlas", id, "👍").RequireStatus(http.StatusCreated)
		f.inst.WaitReact(t)
	}
	f.react("Atlas", id, "👍").RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
}

// CID-33: what the AI cannot react to is refused in words that say why: a platform without reactions (the
// webhook) is 422 unsupported, a message the platform gave no id has nothing to react to (422 unsupported
// too), and neither sends anything.
func TestWhatCannotGetAReactionIsUnsupported(t *testing.T) {
	f := newReplyFixture(t)
	noID := f.enqueue(t, f.atlas, f.group, "", "the platform gave this no id")
	e := f.react("Atlas", noID, "👍").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported)
	require.Equal(t, "the platform gave this message no id, so it cannot get a reaction: answer it with reply", e.Message)
	require.Empty(t, f.inst.Sent())

	h := newReplyFixtureOfKind(t, func(k *connectors.Kind) { k.CanReact = false })
	id := h.enqueue(t, h.atlas, h.group, "M1", "where is my order?")
	e = h.react("Atlas", id, "👍").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported)
	require.Equal(t, "the platform of this connection does not take reactions: answer the message with reply", e.Message)
}

// CID-33: a platform that refuses the reaction (Telegram, for an emoji outside its set) is 502 with the
// reason, and nothing sends it again.
func TestAReactionThePlatformRefusesIsBadGateway(t *testing.T) {
	f := newReplyFixture(t)
	id := f.enqueue(t, f.atlas, f.group, "M1", "where is my order?")
	f.inst.SetReactError(errors.New("setMessageReaction: emoji is not one of Telegram's allowed reactions"))
	e := f.react("Atlas", id, "🤖").RequireError(http.StatusBadGateway, dto.ErrorCodeUnavailable)
	require.Equal(t, "the platform did not take the reaction: setMessageReaction: emoji is not one of Telegram's allowed reactions", e.Message)
}

// CID-03, CID-33: a message that is not in the AI's own queue is unknown to it, whoever the AI is
// (another AI of the owner, the same-named AI of a colleague of the organization, of a stranger, of an
// admin), users and anonymous callers are refused before anything is looked up, and an AI that no longer
// operates the connection is refused with not_operator although the message is still in its queue.
func TestReactionAuthorizationMatrix(t *testing.T) {
	f := newReplyFixture(t)
	bob, carol, root := f.env.NewUser("bob"), f.env.NewUser("carol"), f.env.NewAdmin("root")
	shareConnector(f.alice, f.conn.ID, bob.User.ID)
	id := f.enqueue(t, f.atlas, f.group, "M1", "for Atlas only")
	path := replyPath + id + "/react"
	body := dto.ReactRequest{Emoji: "👍"}

	e := f.react("Notes", id, "👍").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, "message not found", e.Message)
	f.react("Atlas", "no-such-message", "👍").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	for name, u := range map[string]*testkit.Client{"colleague": bob, "stranger": carol, "admin": root} {
		res := u.Token(dto.TokenKindAgent, "desk").AsAgent("Atlas").Post(path, body)
		require.Equal(t, http.StatusNotFound, res.Status, "%s's own AI of the same folder: %s", name, res.Body)
		require.Equal(t, "message not found", testkit.Decode[dto.ErrorBody](res).Error.Message, name)
	}
	f.alice.Post(path, body).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.env.Anon().AsAgent("Atlas").Post(path, body).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	ctx := t.Context()
	require.NoError(t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetConnectorOperator(ctx, db.SetConnectorOperatorParams{OperatorIid: sql.NullString{String: f.notes, Valid: true}, ID: f.conn.ID})
	}))
	e = f.react("Atlas", id, "👍").RequireError(http.StatusForbidden, dto.ErrorCodeNotOperator)
	require.Equal(t, "only the AI that operates the connection can send through it", e.Message)
	require.Empty(t, f.inst.Reactions(), "nothing reached the platform for any refusal")
}
