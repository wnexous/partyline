package api_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// CON-15: a connector that keeps panicking does not stop the rest of the server. /readyz says the
// server is degraded, and how many connectors are in the loop, but stays 200 so that no probe takes
// the working part out of rotation; /healthz, which the container HEALTHCHECK asks, stays ok so that
// nothing restarts the container for one bad connector.
func TestAConnectorInACrashLoopDegradesReadyzButNotHealthz(t *testing.T) {
	fake := connectortest.New()
	// The restart after the last panic of the loop is held, to read what the server says meanwhile.
	var waits atomic.Int32
	wait := func(ctx context.Context, _ time.Duration) error {
		if waits.Add(1) == connectors.CrashLoopThreshold {
			<-ctx.Done()
		}
		return ctx.Err()
	}
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()), testkit.WithConnectorWait(wait))
	alice := env.NewUser("alice")
	bad := alice.OperateConnector(createFakeIdle(alice, "bad", true).ID, alice.NewAI("Atlas"))
	good := alice.OperateConnector(createFakeIdle(alice, "good", true).ID, alice.NewAI("Boreas"))
	badInst, goodInst := fake.Instance(t, bad.ID), fake.Instance(t, good.ID)
	badInst.WaitRun(t)
	goodInst.WaitRun(t)
	goodInst.Connected()

	readyz := func() map[string]any {
		t.Helper()
		return testkit.Decode[map[string]any](env.Anon().Get("/readyz").RequireStatus(http.StatusOK))
	}
	healthz := func() map[string]any {
		t.Helper()
		return testkit.Decode[map[string]any](env.Anon().Get("/healthz").RequireStatus(http.StatusOK))
	}
	require.Equal(t, map[string]any{"status": "ok", "version": "dev"}, readyz(), "nothing is wrong yet")

	for range connectors.CrashLoopThreshold - 1 {
		badInst.Panic("bad connector")
		badInst.WaitRun(t)
	}
	require.Equal(t, "ok", readyz()["status"], "two panics in a row are not a loop yet")
	badInst.Panic("bad connector")
	require.Eventually(t, func() bool { return waits.Load() == connectors.CrashLoopThreshold }, connectortest.Timeout, 5*time.Millisecond)

	require.Equal(t, map[string]any{"status": "degraded", "version": "dev", "connectors_crash_looping": float64(1)}, readyz())
	require.Equal(t, map[string]any{"status": "ok", "version": "dev"}, healthz(), "the process is alive, whatever one connector does")

	// The connector that crashes shows why, without anything of what it was handling.
	shown := testkit.Decode[dto.Connector](alice.Get("/api/v1/connectors/" + bad.ID).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ConnectorStatusError, shown.Status)
	require.Equal(t, "the connector crashed in run: panic of type string", shown.LastError)

	// The rest of the server works: the other connector is connected and sends, the API answers.
	shown = testkit.Decode[dto.Connector](alice.Get("/api/v1/connectors/" + good.ID).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ConnectorStatusConnected, shown.Status)
	_, err := env.App.ConnectorManager.Send(context.Background(), good.ID, connectors.Outbound{ChatID: "chat-1", Text: "still here"})
	require.NoError(t, err)
	alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK)
}
