package connectors_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// HOOK-05: accepted signatures are kept per connector until they expire (inclusive); a new
// signature deletes expired rows, and deleting the connector deletes its rows.
func TestStoreReplays(t *testing.T) {
	e := newInboundEnv(t)
	st := e.env.App.Store
	r := connectors.NewReplays(st)
	ctx := context.Background()
	t0 := time.Unix(1792497600, 0)
	until := t0.Add(10 * time.Minute)

	added, err := r.Remember(ctx, e.conn.ID, "sha256=aa", t0, until)
	require.NoError(t, err)
	require.True(t, added)
	added, err = r.Remember(ctx, e.conn.ID, "sha256=aa", t0.Add(time.Minute), until.Add(time.Minute))
	require.NoError(t, err)
	require.False(t, added, "a remembered signature is a replay")

	seen, err := r.Seen(ctx, e.conn.ID, "sha256=aa", until)
	require.NoError(t, err)
	require.True(t, seen, "still remembered at its expiry")
	seen, err = r.Seen(ctx, e.conn.ID, "sha256=aa", until.Add(time.Millisecond))
	require.NoError(t, err)
	require.False(t, seen, "forgotten after its expiry")

	other := testkitConnector(t, e)
	seen, err = r.Seen(ctx, other, "sha256=aa", t0)
	require.NoError(t, err)
	require.False(t, seen, "signatures are per connector")

	added, err = r.Remember(ctx, e.conn.ID, "sha256=bb", until.Add(time.Millisecond), until.Add(11*time.Minute))
	require.NoError(t, err)
	require.True(t, added)
	n, err := st.R().CountWebhookReplays(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "the expired row was deleted on insert")

	e.alice.Delete("/api/v1/connectors/" + e.conn.ID).RequireStatus(http.StatusNoContent)
	n, err = st.R().CountWebhookReplays(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "rows go with their connector")
}

// testkitConnector creates a second fake connector of alice and returns its id.
func testkitConnector(t *testing.T, e *inboundEnv) string {
	t.Helper()
	return testkit.Decode[dto.Connector](e.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "second", Config: map[string]any{"token": "fake-token-not-real"},
	}).RequireStatus(http.StatusCreated)).ID
}
