package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/identities"
	"github.com/wnexous/partyline/internal/testkit"
)

func TestIdentityRoutesNeedAUser(t *testing.T) {
	env := testkit.New(t)
	anon := env.Anon()
	anon.Get("/api/v1/identities").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	anon.Post("/api/v1/identities/pairing", nil).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	anon.Delete("/api/v1/identities/x").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// IDN-01, IDN-02, IDN-04 through the API: a pairing code, the link (as the inbound pipeline
// performs it), the live identity.linked event to that user only, listing and deleting.
func TestIdentityEndpoints(t *testing.T) {
	clock := testkit.NewClock(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithClock(clock.Now), testkit.WithConnectorKinds(fake.Kind()))
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	off := false
	conn := testkit.Decode[dto.Connector](alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "tg", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &off,
	}).RequireStatus(http.StatusCreated))
	aliceLive, _ := alice.DialLive("")
	bobLive, _ := bob.DialLive("")

	code := testkit.Decode[dto.PairingCode](alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: conn.ID}).RequireStatus(http.StatusCreated))
	require.Regexp(t, `^[A-HJ-NP-Z2-9]{6}$`, code.Code)
	require.Equal(t, conn.ID, code.ConnectorID)
	require.Equal(t, clock.Now().Add(10*time.Minute), code.ExpiresAt)

	_, ok, err := env.App.Identities.Link(context.Background(), code.Code, identities.Account{
		ConnectorID: conn.ID, ConnectorKind: "telegram", SenderID: "4242", Display: "Alice T",
	}, "")
	require.NoError(t, err)
	require.True(t, ok)

	var linked dto.Identity
	aliceLive.Expect(dto.LiveEventTypeIdentityLinked, &linked, 10*time.Second)
	require.Equal(t, conn.ID, linked.ConnectorID)
	require.Equal(t, "telegram", linked.ConnectorKind)
	require.Equal(t, "4242", linked.ExternalID)
	require.Equal(t, "Alice T", linked.Display)
	bobLive.ExpectNone(300*time.Millisecond, dto.LiveEventTypeHello)

	list := testkit.Decode[dto.List[dto.Identity]](alice.Get("/api/v1/identities").RequireStatus(http.StatusOK))
	require.Equal(t, []dto.Identity{linked}, list.Items)
	require.Empty(t, testkit.Decode[dto.List[dto.Identity]](bob.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items)

	bob.Delete("/api/v1/identities/"+linked.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	alice.Delete("/api/v1/identities/" + linked.ID).RequireStatus(http.StatusNoContent)
	require.Empty(t, testkit.Decode[dto.List[dto.Identity]](alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items)
	user, err := env.App.Identities.ResolveSender(context.Background(), conn.ID, "4242", "")
	require.NoError(t, err)
	require.Empty(t, user, "IDN-04: a deleted identity resolves to no user")
}

// IDN-01 (S5): a pairing code is requested for one connector of an organization the user belongs to;
// another organization's connector answers 404 and a missing connector 422.
func TestPairingNeedsAUsableConnector(t *testing.T) {
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()))
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	off := false
	create := func(c *testkit.Client, org, name string) dto.Connector {
		return testkit.Decode[dto.Connector](c.Post("/api/v1/connectors", dto.CreateConnectorRequest{
			OrgID: org, Kind: connectortest.KindName, Name: name, Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &off,
		}).RequireStatus(http.StatusCreated))
	}
	private, err := env.App.Orgs.Create(context.Background(), bob.User.ID, "bob-private") // alice is not in it
	require.NoError(t, err)
	bobs := create(bob, private.ID, "bob-tg")
	shared := create(bob, "", "bob-shared")       // in bob's default organization
	shareConnector(bob, shared.ID, alice.User.ID) // alice joins it

	bad := alice.Post("/api/v1/identities/pairing", dto.PairingRequest{}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"connector_id": "required"}, bad.Fields)
	alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: bobs.ID}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: "missing"}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	code := testkit.Decode[dto.PairingCode](alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: shared.ID}).RequireStatus(http.StatusCreated))
	require.Equal(t, shared.ID, code.ConnectorID, "a member of the connection's organization may pair through it")
}

// S5: whoever controls a connector's endpoint chooses the sender ids it reports, so changing
// an endpoint field (base_url, api_base, outbound_url) deletes the identities linked through
// the connector, audited without values; other changes keep them.
func TestEndpointChangeDropsLinkedIdentities(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	alice := env.NewUser("alice")
	off := false
	conn := testkit.Decode[dto.Connector](alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: "telegram", Name: "tg", Enabled: &off,
		Config: map[string]any{"bot_token": "123456:synthetic-token", "api_base": "https://api.telegram.org"},
	}).RequireStatus(http.StatusCreated))
	link := func(sender string) {
		code := testkit.Decode[dto.PairingCode](alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: conn.ID}).RequireStatus(http.StatusCreated))
		_, ok, err := env.App.Identities.Link(context.Background(), code.Code, identities.Account{ConnectorID: conn.ID, ConnectorKind: "telegram", SenderID: sender}, "")
		require.NoError(t, err)
		require.True(t, ok)
	}
	list := func() []dto.Identity {
		return testkit.Decode[dto.List[dto.Identity]](alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
	}
	link("4242")
	link("4343")

	name := "tg-2"
	alice.Patch("/api/v1/connectors/"+conn.ID, dto.UpdateConnectorRequest{Name: &name, Config: map[string]any{"bot_token": "654321:other-token"}}).
		RequireStatus(http.StatusOK)
	require.Len(t, list(), 2, "a non-endpoint change keeps the identities")
	alice.Patch("/api/v1/connectors/"+conn.ID, dto.UpdateConnectorRequest{Config: map[string]any{"api_base": "https://api.telegram.org"}}).
		RequireStatus(http.StatusOK)
	require.Len(t, list(), 2, "the same endpoint is not a change")

	alice.Patch("/api/v1/connectors/"+conn.ID, dto.UpdateConnectorRequest{Config: map[string]any{"api_base": "https://bots.example.com"}}).
		RequireStatus(http.StatusOK)
	require.Empty(t, list())
	user, err := env.App.Identities.ResolveSender(context.Background(), conn.ID, "4242", "")
	require.NoError(t, err)
	require.Empty(t, user, "the sender resolves to no user")

	resp := admin.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK)
	require.NotContains(t, string(resp.Body), "bots.example.com", "no config values in the audit")
	var reset *dto.AuditEntry
	for _, e := range testkit.Decode[dto.Page[dto.AuditEntry]](resp).Items {
		if e.Action == "connector.identities_reset" {
			require.Nil(t, reset, "one reset entry")
			reset = &e
		}
	}
	require.NotNil(t, reset)
	require.Equal(t, conn.ID, reset.TargetID)
	require.Equal(t, alice.User.ID, reset.Actor.ID)
	require.Equal(t, map[string]any{"kind": "telegram", "name": "tg-2", "reason": "endpoint_changed", "removed": float64(2)}, reset.Detail)
}

// S5: the endpoint fields of the built-in kinds.
func TestBuiltinEndpointFields(t *testing.T) {
	got := map[string][]string{}
	for _, k := range app.BuiltinKinds() {
		got[k.Name] = []string{}
		for _, f := range k.Fields {
			if f.Endpoint {
				got[k.Name] = append(got[k.Name], f.Name)
			}
		}
	}
	require.Equal(t, map[string][]string{
		"whatsapp": {"base_url"}, "telegram": {"api_base"}, "webhook": {"outbound_url"}, "discord": {},
	}, got)
}
