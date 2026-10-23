package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
)

// v03Database writes the store's v0.3 fixture (schema 0014) to a new database file.
func v03Database(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v03.db")
	st, err := store.Open(path)
	require.NoError(t, err)
	require.NoError(t, st.MigrateBefore(ctx, "0015"))
	require.NoError(t, st.Close())
	fixture, err := os.ReadFile("../store/testdata/v03_fixture.sql")
	require.NoError(t, err)
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	_, err = raw.ExecContext(ctx, string(fixture))
	require.NoError(t, err)
	require.NoError(t, raw.Close())
	return path
}

// GRP-01, MIG-01, MIG-02: after the upgrade, the first session in a folder called atlas adopts
// the v0.3 agent atlas, and the agent's reply to an old message of the v0.3 inbox room is stored in
// the room, quoting it. It goes out to no chat, and neither does the delivery the v0.3 outbox left
// pending: a room sends nothing to a platform any more (CID-22). (The catch-all routing of new chats
// is retired, CID-07: what arrives on the connector is queued for its operator, which the upgrade
// end-to-end test pins, T37.)
func TestUpgradedAgentAdoptsItsFolderAndRepliesInTheRoom(t *testing.T) {
	ctx := context.Background()
	fake := connectortest.New()
	telegram := fake.Kind()
	telegram.Name = "telegram" // the fixture's connector kind, served by the fake
	// The app's clock starts an hour after the fixture's last message, so that the retention sweep at
	// start-up (30 days in the inbox room) keeps its history whatever today is.
	afterTheFixture := func() time.Time { return time.UnixMilli(1790003600000) }
	env := testkit.New(t, testkit.WithDatabase(v03Database(t)), testkit.WithConnectorKinds(telegram), testkit.WithOutbox(), testkit.WithClock(afterTheFixture))
	// v0.3 connectors were operated by their owner; a connection runs once an AI operates it
	// (CID-02), so here the adopted agent takes it over.
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetConnectorOperator(ctx, db.SetConnectorOperatorParams{OperatorIid: sql.NullString{String: "a-atlas", Valid: true}, ID: "k-tg"})
	}))
	env.App.ConnectorManager.Apply("k-tg")

	inst := fake.Instance(t, "k-tg")
	inst.WaitRun(t)
	inst.Connected()
	_, secret, err := env.App.Users.CreateToken(ctx, "u-alice", users.TokenInput{Kind: dto.TokenKindAgent, Name: "devbox"})
	require.NoError(t, err)
	alice := env.Anon().WithBearer(secret)
	agent, _ := alice.DialAgent("atlas")
	require.NotNil(t, agent)
	require.Equal(t, "a-atlas", agent.Welcome(connectortest.Timeout).Agent.ID, "the folder adopted the v0.3 agent")
	require.Equal(t, "r-ops", agent.Notice(connectortest.Timeout).Room.ID, "ops keeps its instructions")

	reply := testkit.Decode[dto.Message](alice.AsAgent("atlas").
		Post("/api/v1/agent/messages/m-in3/reply", dto.ReplyRequest{Text: "tem sim, às 10h"}).
		RequireStatus(http.StatusCreated))
	require.Equal(t, "a-atlas", reply.AuthorID)
	require.Equal(t, "r-inbox", reply.RoomID)
	require.Equal(t, "m-in3", reply.ReplyTo)
	require.Equal(t, "tem sim, às 10h", reply.Text)
	require.Empty(t, reply.Recipients)
	require.Empty(t, reply.Outbox)
	require.Zero(t, countRows(t, env, "SELECT COUNT(*) FROM outbox WHERE message_id = ?", reply.ID), "no delivery is planned for the reply")
	require.Never(t, func() bool { return len(inst.Sent()) > 0 }, 1500*time.Millisecond, 50*time.Millisecond,
		"neither the reply nor the delivery v0.3 left pending reaches a chat")
	require.Equal(t, 1, countRows(t, env, "SELECT COUNT(*) FROM outbox WHERE id = 'o-2' AND status = 'pending' AND attempts = 0"), "the pending v0.3 row is left alone")

	// History is left as it was: the answer v0.3 sent to a chat still names the chat it was for.
	history := testkit.Decode[dto.Page[dto.AgentMessage]](alice.AsAgent("atlas").Get("/api/v1/agent/rooms/r-inbox/messages").RequireStatus(http.StatusOK))
	var answered dto.Message
	for _, m := range history.Items {
		if m.Message.ID == "m-in2" {
			answered = m.Message
		}
	}
	require.Equal(t, []string{"c-tgdm"}, answered.Recipients, "a message from before the upgrade keeps the chats it named")
}

// MIG-01, GRP-01: after the upgrade (0015 and 0016 in one transaction, the legacy name columns
// gone) the timelines of the v0.3 rooms still name every author: users and AIs by their profile,
// the display name a member had in the room, what a project member called its session, a person
// by the name the platform reported, and a chat without a sender by its title.
func TestUpgradedTimelinesNameEveryAuthor(t *testing.T) {
	ctx := context.Background()
	// The app's clock stands an hour after the fixture's last message, so that the retention
	// sweep at start-up (30 days in the catch-all room) keeps its history whatever today is.
	afterTheFixture := func() time.Time { return time.UnixMilli(1790003600000) }
	env := testkit.New(t, testkit.WithDatabase(v03Database(t)), testkit.WithClock(afterTheFixture))
	_, secret, err := env.App.Users.CreateToken(ctx, "u-alice", users.TokenInput{Kind: dto.TokenKindUser, Name: "web"})
	require.NoError(t, err)
	alice := env.Anon().WithBearer(secret)

	for room, want := range map[string]map[string]string{
		"r-ops": {
			"m-ops1": "Alice A.", "m-ops2": "Atlas Ops", "m-ops3": "Bob.Smith", "m-ops4": "Zé", "m-ops5": "Team",
			"m-ops6": "Partyline", "m-ops7": "Proj Bot",
		},
		"r-inbox": {"m-in1": "Maria Silva", "m-in2": "atlas", "m-in3": "Maria Silva"},
		"r-dev":   {"m-dev1": "Alice A.", "m-dev2": "Mine"},
	} {
		page := testkit.Decode[dto.Page[dto.Message]](alice.Get("/api/v1/rooms/" + room + "/messages").RequireStatus(http.StatusOK))
		got := map[string]string{}
		for _, m := range page.Items {
			got[m.ID] = m.AuthorName
		}
		require.Equal(t, want, got, room)
	}
}
