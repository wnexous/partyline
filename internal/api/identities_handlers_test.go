package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
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

// IDN-01 (S5): a pairing code is requested for one connector the user owns or may use;
// another user's connector answers 404 and a missing connector 422.
func TestPairingNeedsAUsableConnector(t *testing.T) {
	fake := connectortest.New()
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()))
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	off := false
	create := func(c *testkit.Client, name string) dto.Connector {
		return testkit.Decode[dto.Connector](c.Post("/api/v1/connectors", dto.CreateConnectorRequest{
			Kind: connectortest.KindName, Name: name, Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &off,
		}).RequireStatus(http.StatusCreated))
	}
	bobs := create(bob, "bob-tg")
	shared := create(bob, "bob-shared")
	bob.Put("/api/v1/connectors/"+shared.ID+"/sharing", dto.Sharing{
		Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: alice.User.ID, Level: dto.LevelUse}},
	}).RequireStatus(http.StatusOK)

	bad := alice.Post("/api/v1/identities/pairing", dto.PairingRequest{}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"connector_id": "required"}, bad.Fields)
	alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: bobs.ID}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: "missing"}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	code := testkit.Decode[dto.PairingCode](alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: shared.ID}).RequireStatus(http.StatusCreated))
	require.Equal(t, shared.ID, code.ConnectorID, "a use grantee may pair through a shared connector")
}
