package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/users"
)

// POS-06: an AI cannot leave the organization it belongs to - it is deleted there - so the call answers 409
// home_organization and changes nothing (L-017). No agent token reaches an organization route (the path rule
// keeps them to /api/v1/agent/), so the request carries the principal that route would resolve for an AI acting
// as itself; the deletion of an AI by someone else is the route's other branch and is tested end to end.
func TestAnAILeavingItsHomeOrganizationIsRefusedWith409(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(ctx))
	bus := events.NewBus()
	usersSvc, err := users.NewService(st, users.Options{Bus: bus})
	require.NoError(t, err)
	alice, err := usersSvc.CreateUser(ctx, users.AccountInput{Username: "alice", Password: "alice-password-1"}, dto.RoleMember)
	require.NoError(t, err)
	registry := agents.NewRegistry(agents.Deps{
		Store: st, Access: access.New(st, bus, access.Options{}), Bus: bus, Presence: agents.NewPresence(st, bus, time.Now),
	})
	atlas, err := registry.ResolveFolder(ctx, alice.ID, agents.FolderID{Machine: "devbox", Folder: "/work/atlas"}, false)
	require.NoError(t, err)
	home := atlas.OrgID.String
	require.NotEmpty(t, home, "the AI belongs to alice's organization")

	s := &server{Deps: Deps{
		Orgs: orgs.NewService(orgs.Deps{Store: st, Bus: bus}), Agents: registry, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
	mux := http.NewServeMux()
	s.routes(mux)
	itself := authn.Principal{Kind: authn.KindAgent, UserID: alice.ID, AgentID: atlas.ID}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(authn.WithPrincipal(ctx, itself), http.MethodDelete, "/api/v1/orgs/"+home+"/members/"+atlas.ID, nil))

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	var e dto.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
	require.Equal(t, dto.ErrorCodeHomeOrganization, e.Error.Code)
	require.Contains(t, e.Error.Message, "ask an admin to delete it")
	stored, err := st.R().GetAgent(ctx, atlas.ID)
	require.NoError(t, err)
	require.Equal(t, home, stored.OrgID.String, "it is still there")
	_, err = st.R().GetOrgMember(ctx, db.GetOrgMemberParams{OrgID: home, Iid: atlas.ID})
	require.NoError(t, err, "and a member of it")
}
