package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// fakeConnectorEnv runs an app whose only connector kind is the controllable fake.
func fakeConnectorEnv(t *testing.T) (*testkit.Env, *connectortest.Fake) {
	t.Helper()
	fake := connectortest.New()
	return testkit.New(t, testkit.WithConnectorKinds(fake.Kind())), fake
}

func createFake(c *testkit.Client, name string, enabled bool) dto.Connector {
	c.T.Helper()
	return testkit.Decode[dto.Connector](c.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: name, Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &enabled,
	}).RequireStatus(http.StatusCreated))
}

// CON-04: a created connector starts within 2 seconds and its status changes arrive as
// connector.status events. CON-05: disabling and deleting stop it.
func TestConnectorLifecycleThroughTheAPI(t *testing.T) {
	env, fake := fakeConnectorEnv(t)
	alice := env.NewUser("alice")
	live, _ := alice.DialLive("")

	// Started at once (well within 2 s): the instance exists when the create returns.
	// Wall time is not asserted (loaded hosts).
	c := createFake(alice, "ops", true)
	inst := fake.Lookup(c.ID)
	require.NotNil(t, inst, "create starts the connector synchronously")
	inst.WaitRun(t)

	var st dto.ConnectorStatusData
	live.Expect(dto.LiveEventTypeConnectorStatus, &st, connectortest.Timeout)
	require.Equal(t, dto.ConnectorStatusData{ConnectorID: c.ID, Status: dto.ConnectorStatusConnecting}, st)
	inst.Connected()
	live.Expect(dto.LiveEventTypeConnectorStatus, &st, connectortest.Timeout)
	require.Equal(t, dto.ConnectorStatusData{ConnectorID: c.ID, Status: dto.ConnectorStatusConnected}, st)
	got := testkit.Decode[dto.Connector](alice.Get("/api/v1/connectors/" + c.ID).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ConnectorStatusConnected, got.Status)

	off, on := false, true
	alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{Enabled: &off}).RequireStatus(http.StatusOK)
	inst.WaitStop(t)
	live.Expect(dto.LiveEventTypeConnectorStatus, &st, connectortest.Timeout)
	require.Equal(t, dto.ConnectorStatusStopped, st.Status)
	require.Equal(t, dto.ConnectorStatusStopped, testkit.Decode[dto.Connector](alice.Get("/api/v1/connectors/"+c.ID).RequireStatus(http.StatusOK)).Status)

	alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{Enabled: &on}).RequireStatus(http.StatusOK)
	again := fake.Instance(t, c.ID)
	again.WaitRun(t)

	alice.Delete("/api/v1/connectors/" + c.ID).RequireStatus(http.StatusNoContent)
	again.WaitStop(t)
}

// A connector created disabled does not run.
func TestDisabledConnectorDoesNotStart(t *testing.T) {
	env, fake := fakeConnectorEnv(t)
	alice := env.NewUser("alice")
	c := createFake(alice, "ops", false)
	other := createFake(alice, "probe", true)
	fake.Instance(t, other.ID).WaitRun(t) // the manager is running
	require.Equal(t, dto.ConnectorStatusStopped, testkit.Decode[dto.Connector](alice.Get("/api/v1/connectors/"+c.ID).RequireStatus(http.StatusOK)).Status)
}
