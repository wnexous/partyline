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
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/store"
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

// GRP-01, MIG-01: after the upgrade, the v0.3 catch-all room of the telegram connector still
// routes both ways: a DM from a chat already in the room and a DM from a new chat reach the
// agent member, and the agent's reply goes out to the chat through the outbox.
func TestUpgradedCatchAllRouteDeliversBothWays(t *testing.T) {
	ctx := context.Background()
	fake := connectortest.New()
	telegram := fake.Kind()
	telegram.Name = "telegram" // the fixture's connector kind, served by the fake
	env := testkit.New(t, testkit.WithDatabase(v03Database(t)), testkit.WithConnectorKinds(telegram), testkit.WithOutbox())

	inst := fake.Instance(t, "k-tg")
	inst.WaitRun(t)
	inst.Connected()
	_, secret, err := env.App.Users.CreateToken(ctx, "u-alice", users.TokenInput{Kind: dto.TokenKindAgent, Name: "devbox"})
	require.NoError(t, err)
	alice := env.Anon().WithBearer(secret)
	agent, _ := alice.DialAgent("atlas")
	require.NotNil(t, agent)
	agent.Welcome(connectortest.Timeout)
	require.Equal(t, "r-ops", agent.Notice(connectortest.Timeout).Room.ID, "ops keeps its instructions")

	inst.Emit(ctx, connectors.Inbound{
		ChatID: "100", ChatTitle: "Maria", ChatKind: "dm", MessageID: "tg-4",
		SenderID: "100", SenderName: "Maria Silva", Text: "ainda tem horário?", At: time.Now(),
	})
	got := agent.Message(connectortest.Timeout)
	require.Equal(t, "r-inbox", got.Room.ID)
	require.Equal(t, "ainda tem horário?", got.Message.Text)
	require.Equal(t, dto.AuthorKindChat, got.Message.AuthorKind)
	require.Equal(t, "c-tgdm", got.Message.AuthorID)
	agent.Ack(got.Message.ID)

	reply := testkit.Decode[dto.Message](alice.WithHeader(dto.HeaderAgent, "atlas").
		Post("/api/v1/agent/messages/"+got.Message.ID+"/reply", dto.ReplyRequest{Text: "tem sim, às 10h"}).
		RequireStatus(http.StatusCreated))
	require.Equal(t, "a-atlas", reply.AuthorID)
	// The reply and the fixture's pending outbox row both go out; the failed row stays failed.
	sent := map[string]connectors.Outbound{}
	for range 2 {
		out := inst.WaitSend(t)
		sent[out.MessageID] = out
	}
	require.Equal(t, "100", sent[reply.ID].ChatID)
	require.Contains(t, sent[reply.ID].Text, "tem sim, às 10h")
	require.Equal(t, "-1001", sent["m-ops1"].ChatID, "pending v0.3 outbox rows are still delivered")

	// A DM from a new chat still joins the catch-all room (scope dms) and reaches the agent.
	inst.Emit(ctx, connectors.Inbound{
		ChatID: "102", ChatTitle: "João", ChatKind: "dm", MessageID: "tg-5",
		SenderID: "102", SenderName: "João", Text: "oi", At: time.Now(),
	})
	got = agent.Message(connectortest.Timeout)
	require.Equal(t, "r-inbox", got.Room.ID)
	require.Equal(t, "oi", got.Message.Text)
}
