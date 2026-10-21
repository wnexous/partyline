package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

func tokenIDs(c *testkit.Client, path string) []string {
	c.T.Helper()
	list := testkit.Decode[dto.List[dto.Token]](c.Get(path).RequireStatus(http.StatusOK))
	ids := make([]string, len(list.Items))
	for i, tok := range list.Items {
		ids[i] = tok.ID
	}
	return ids
}

// TOKH-01, TOKH-02: revoked tokens leave the default list and show with include_revoked=true;
// DELETE /tokens/{id}/purge deletes the caller's revoked token (audited, its secret stays 401),
// answers 409 on an active token and 404 on someone else's.
func TestTokenHygieneEndpoints(t *testing.T) {
	env := testkit.New(t)
	admin := env.SetupAdmin()
	bob, carol := env.NewUser("bob"), env.NewUser("carol")
	create := func(name string) dto.CreatedToken {
		return testkit.Decode[dto.CreatedToken](bob.Post("/api/v1/tokens", dto.CreateTokenRequest{Kind: dto.TokenKindUser, Name: name}).
			RequireStatus(http.StatusCreated))
	}
	keep, old := create("keep"), create("old")

	bob.Delete("/api/v1/tokens/" + old.Token.ID).RequireStatus(http.StatusNoContent)
	require.Equal(t, []string{keep.Token.ID}, tokenIDs(bob, "/api/v1/tokens"))
	require.Equal(t, []string{keep.Token.ID}, tokenIDs(bob, "/api/v1/tokens?include_revoked=false"))
	all := testkit.Decode[dto.List[dto.Token]](bob.Get("/api/v1/tokens?include_revoked=true").RequireStatus(http.StatusOK))
	require.Len(t, all.Items, 2)
	for _, tok := range all.Items {
		require.Equal(t, tok.ID == old.Token.ID, tok.RevokedAt != nil, tok.Name)
	}

	e := bob.Delete("/api/v1/tokens/"+keep.Token.ID+"/purge").RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	require.NotEmpty(t, e.Message)
	carol.Delete("/api/v1/tokens/"+old.Token.ID+"/purge").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	env.Anon().Delete("/api/v1/tokens/"+old.Token.ID+"/purge").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	require.ElementsMatch(t, []string{keep.Token.ID, old.Token.ID}, tokenIDs(bob, "/api/v1/tokens?include_revoked=true"))

	bob.Delete("/api/v1/tokens/" + old.Token.ID + "/purge").RequireStatus(http.StatusNoContent)
	require.Equal(t, []string{keep.Token.ID}, tokenIDs(bob, "/api/v1/tokens?include_revoked=true"))
	bob.Delete("/api/v1/tokens/"+old.Token.ID+"/purge").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// Edge case: a client still holding the purged secret gets 401 like any unknown token.
	bob.WithBearer(old.Secret).Get("/api/v1/me").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	require.Equal(t, bob.User.ID, testkit.Decode[dto.Me](bob.WithBearer(keep.Secret).Get("/api/v1/me").RequireStatus(http.StatusOK)).ID)

	page := testkit.Decode[dto.Page[dto.AuditEntry]](admin.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK))
	var purge *dto.AuditEntry
	for i, entry := range page.Items {
		if entry.Action == "token.purge" {
			require.Nil(t, purge, "one purge entry")
			purge = &page.Items[i]
		}
	}
	require.NotNil(t, purge)
	require.Equal(t, old.Token.ID, purge.TargetID)
	require.Equal(t, "token", purge.TargetKind)
	require.NotNil(t, purge.Actor)
	require.Equal(t, bob.User.ID, purge.Actor.ID)
}
