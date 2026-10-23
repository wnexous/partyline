package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// MIG-03: projects are removed with v0.3. Their routes are gone: the owner and a stranger get
// 404 (the router no longer has them, nothing is authorized), and an AI token is refused by the
// agent-endpoint scope before routing.
func TestProjectsRoutesAreGone(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	agent := alice.Token(dto.TokenKindAgent, "laptop").AsAgent("atlas")
	paths := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/projects"},
		{http.MethodPatch, "/api/v1/projects/p1"},
		{http.MethodGet, "/api/v1/projects/p1/sharing"},
		{http.MethodPut, "/api/v1/projects/p1/sharing"},
	}
	for _, c := range []*testkit.Client{alice, bob} {
		for _, p := range paths {
			c.Do(p.method, p.path, nil).RequireStatus(http.StatusNotFound)
		}
	}
	for _, p := range paths {
		agent.Do(p.method, p.path, nil).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	}
}

// MIG-03: a project is not a room member kind any more. Adding one is refused with 422 kind,
// and the unknown-kind message names the two kinds a request may add (a chat is refused on its
// own, CID-19).
func TestProjectMemberKindIsRefused(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "Ops")

	e := alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: "project", MemberID: "p1"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, "must be agent or user", e.Fields["kind"])

	e = alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: "team", MemberID: "x"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, "must be agent or user", e.Fields["kind"])

	members := testkit.Decode[dto.List[dto.RoomMember]](alice.Get("/api/v1/rooms/" + room.ID + "/members").RequireStatus(http.StatusOK)).Items
	require.Empty(t, members, "refused members were not added")
}
