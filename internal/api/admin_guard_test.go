package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/users"
)

// AUTH-10 guard over HTTP: the last active admin cannot be demoted or disabled, and the admin
// route answers 409 conflict. Through the full stack this needs two admins demoting each other
// concurrently: the loser passed authentication as an admin, then lost the role before its
// update ran. The test builds that state deterministically: the request carries the admin
// principal the middleware resolved, while the database already shows the actor demoted.
func TestLastAdminConflictOverHTTP(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "partyline.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(ctx))
	svc, err := users.NewService(st, users.Options{})
	require.NoError(t, err)
	last, err := svc.CreateUser(ctx, users.AccountInput{Username: "last", Password: "last-password-1"}, dto.RoleAdmin)
	require.NoError(t, err)
	actor, err := svc.CreateUser(ctx, users.AccountInput{Username: "actor", Password: "actor-password-1"}, dto.RoleMember)
	require.NoError(t, err)

	s := &server{Deps: Deps{Users: svc, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	mux := http.NewServeMux()
	s.routes(mux)
	staleAdmin := authn.Principal{Kind: authn.KindUser, UserID: actor.ID, Role: string(dto.RoleAdmin), SessionID: "s"}

	member, disabled := dto.RoleMember, true
	for name, body := range map[string]dto.UpdateUserRequest{
		"demote":  {Role: &member},
		"disable": {Disabled: &disabled},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(body)
			require.NoError(t, err)
			req := httptest.NewRequestWithContext(authn.WithPrincipal(ctx, staleAdmin),
				http.MethodPatch, "/api/v1/admin/users/"+last.ID, bytes.NewReader(data))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			var e dto.ErrorBody
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
			require.Equal(t, dto.ErrorCodeConflict, e.Error.Code)

			u, err := st.R().GetUser(ctx, last.ID)
			require.NoError(t, err)
			require.Equal(t, string(dto.RoleAdmin), u.Role, "the last admin keeps its role")
			require.False(t, u.DisabledAt.Valid, "the last admin stays active")
		})
	}
}
