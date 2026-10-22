package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

func getProfile(c *testkit.Client, id string) *testkit.Response {
	c.T.Helper()
	return c.Get("/api/v1/intelligences/" + id)
}

func patchProfile(c *testkit.Client, id string, req dto.UpdateProfileRequest) *testkit.Response {
	c.T.Helper()
	return c.Patch("/api/v1/intelligences/"+id, req)
}

func profileOf(t *testing.T, c *testkit.Client, id string) dto.IntelligenceProfile {
	t.Helper()
	return testkit.Decode[dto.IntelligenceProfile](getProfile(c, id).RequireStatus(http.StatusOK))
}

// seedPerson stores a person of a connector the way the connectors' inbound pipeline will,
// and returns its intelligence id.
func seedPerson(t *testing.T, env *testkit.Env, connectorID, externalID, name string) string {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if _, err := intelligences.Create(ctx, q, intelligences.NewParams{ID: id, Kind: handles.Person, DisplayName: name, HandleBase: name, Now: time.Now()}); err != nil {
			return err
		}
		return q.CreatePerson(ctx, db.CreatePersonParams{ID: id, ConnectorID: connectorID, ExternalID: externalID, PlatformName: name, CreatedAt: time.Now().UnixMilli()})
	}))
	return id
}

// PROF-03: a user edits their own profile; the new display name, handle and bio show on the
// profile, in GET /me (which now carries the user's intelligence) and in the users directory.
func TestUserEditsOwnProfile(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")

	before := profileOf(t, alice, alice.User.ID)
	require.Equal(t, []any{alice.User.ID, dto.IntelligenceKindHuman, dto.IntelligenceRoleUser, "alice", "alice", "", true},
		[]any{before.ID, before.Kind, before.Role, before.DisplayName, before.Handle, before.Bio, before.CanEdit})
	require.Equal(t, []dto.App{{Kind: "web", Label: "Web app"}}, before.Apps)
	require.NotNil(t, before.Conversations, "an empty list, not null")
	require.Empty(t, before.Conversations)
	require.Nil(t, before.AI)
	require.Empty(t, before.AvatarURL, "no avatar yet: the UI shows initials")

	got := testkit.Decode[dto.IntelligenceProfile](patchProfile(alice, alice.User.ID, dto.UpdateProfileRequest{
		DisplayName: ptr("  Alice A. "), Handle: ptr("Alice-Prime"), Bio: ptr("Writes the docs"),
	}).RequireStatus(http.StatusOK))
	require.Equal(t, []any{"Alice A.", "alice-prime", "Writes the docs", true},
		[]any{got.DisplayName, got.Handle, got.Bio, got.CanEdit})
	require.Equal(t, got, profileOf(t, alice, alice.User.ID), "the PATCH answers with the stored profile")

	me := testkit.Decode[dto.Me](alice.Get("/api/v1/me").RequireStatus(http.StatusOK))
	require.Equal(t, []any{alice.User.ID, dto.IntelligenceKindHuman, dto.IntelligenceRoleUser, "Alice A.", "alice-prime"},
		[]any{me.Intelligence.ID, me.Intelligence.Kind, me.Intelligence.Role, me.Intelligence.DisplayName, me.Intelligence.Handle})
	require.Equal(t, "alice", me.Username, "the username never changes")

	dir := testkit.Decode[dto.List[dto.User]](bob.Get("/api/v1/users").RequireStatus(http.StatusOK))
	var listed *dto.IntelligenceRef
	for _, u := range dir.Items {
		require.NotNil(t, u.Intelligence, "every directory entry carries its intelligence: %s", u.Username)
		require.Equal(t, u.ID, u.Intelligence.ID)
		if u.ID == alice.User.ID {
			listed = u.Intelligence
		}
	}
	require.NotNil(t, listed)
	require.Equal(t, []any{"Alice A.", "alice-prime", dto.IntelligenceRoleUser}, []any{listed.DisplayName, listed.Handle, listed.Role})

	seen := profileOf(t, bob, alice.User.ID)
	require.Equal(t, []any{"Alice A.", "Writes the docs", false}, []any{seen.DisplayName, seen.Bio, seen.CanEdit}, "other users see it, read-only")
}

// PROF-03: PATCH /me writes the account's intelligence, so the profile follows; an empty
// display name shows the username again.
func TestPatchMeWritesTheIntelligence(t *testing.T) {
	env := testkit.New(t)
	bob := env.NewUser("bob")
	bob.Patch("/api/v1/me", dto.UpdateMeRequest{DisplayName: ptr("Bobby")}).RequireStatus(http.StatusOK)
	require.Equal(t, "Bobby", profileOf(t, bob, bob.User.ID).DisplayName)
	require.Equal(t, "Bobby", testkit.Decode[dto.Me](bob.Get("/api/v1/me").RequireStatus(http.StatusOK)).Intelligence.DisplayName)

	bob.Patch("/api/v1/me", dto.UpdateMeRequest{DisplayName: ptr("")}).RequireStatus(http.StatusOK)
	require.Equal(t, "bob", profileOf(t, bob, bob.User.ID).DisplayName)

	// The profile route writes the account's display name too, for readers of GET /me.
	patchProfile(bob, bob.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Robert")}).RequireStatus(http.StatusOK)
	require.Equal(t, "Robert", testkit.Decode[dto.Me](bob.Get("/api/v1/me").RequireStatus(http.StatusOK)).DisplayName)
}

// Every response that returns the logged-in user carries its intelligence: setup, invite
// accept and login too.
func TestAccountResponsesCarryTheIntelligence(t *testing.T) {
	env := testkit.New(t)
	anon := env.Anon()
	me := testkit.Decode[dto.Me](anon.Post("/api/v1/setup", dto.SetupRequest{
		Token: env.App.SetupToken(), Username: "admin", Password: testkit.Password, DisplayName: "Admin",
	}).RequireStatus(http.StatusCreated))
	require.Equal(t, []any{me.ID, "Admin", "admin"}, []any{me.Intelligence.ID, me.Intelligence.DisplayName, me.Intelligence.Handle})

	admin := env.LoginAs("admin", testkit.Password)
	created := testkit.Decode[dto.CreatedInvite](admin.Post("/api/v1/admin/invites", dto.CreateInviteRequest{Role: dto.RoleMember}).RequireStatus(http.StatusCreated))
	token := created.URL[strings.LastIndex(created.URL, "/")+1:]
	accepted := testkit.Decode[dto.Me](env.Anon().Post("/api/v1/invites/"+token+"/accept", dto.AcceptInviteRequest{
		Username: "carol", Password: testkit.Password, DisplayName: "Carol C.",
	}).RequireStatus(http.StatusCreated))
	require.Equal(t, []any{accepted.ID, "Carol C.", "carol"}, []any{accepted.Intelligence.ID, accepted.Intelligence.DisplayName, accepted.Intelligence.Handle})

	login := testkit.Decode[dto.Me](env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "carol", Password: testkit.Password}).RequireStatus(http.StatusOK))
	require.Equal(t, accepted.Intelligence, login.Intelligence)
}

// PROF-05, AID-06: an AI's owner edits the AI's profile; the rename shows in the AI's profile
// and in the agents list. A use grantee reads it (403 on edit); a stranger finds nothing (404).
func TestOwnerEditsAIProfile(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	id := registerAgent(t, env, alice, "builder")
	shareAgent(alice, id, bob.User.ID)

	before := profileOf(t, alice, id)
	require.Equal(t, []any{dto.IntelligenceKindAI, dto.IntelligenceRoleAI, "builder", "builder", true},
		[]any{before.Kind, before.Role, before.DisplayName, before.Handle, before.CanEdit})
	require.NotNil(t, before.AI)
	require.Equal(t, []any{alice.User.ID, "alice", dto.IntelligenceRoleUser, dto.LevelManage, false},
		[]any{before.AI.Owner.ID, before.AI.Owner.Handle, before.AI.Owner.Role, before.AI.MyLevel, before.AI.Online})

	got := testkit.Decode[dto.IntelligenceProfile](patchProfile(alice, id, dto.UpdateProfileRequest{
		DisplayName: ptr("Atlas"), Handle: ptr("atlas"), Bio: ptr("Runs the builds"),
	}).RequireStatus(http.StatusOK))
	require.Equal(t, []any{"Atlas", "atlas", "Runs the builds"}, []any{got.DisplayName, got.Handle, got.Bio})
	require.Equal(t, "Atlas", listedAgent(t, alice, id).DisplayName, "the agents list shows the rename (AID-06)")
	require.Equal(t, "builder", listedAgent(t, alice, id).Name, "the internal name stays")

	grantee := profileOf(t, bob, id)
	require.Equal(t, []any{"Atlas", false, dto.LevelUse}, []any{grantee.DisplayName, grantee.CanEdit, grantee.AI.MyLevel})
	patchProfile(bob, id, dto.UpdateProfileRequest{DisplayName: ptr("Hijacked")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	getProfile(carol, id).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	patchProfile(carol, id, dto.UpdateProfileRequest{DisplayName: ptr("Hijacked")}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, "Atlas", profileOf(t, alice, id).DisplayName, "refused edits change nothing")
}

// A profile shows where the AI lives and whether it is online: machine, current app (the
// harness), and its machine label when the owner set one.
func TestAIProfileShowsItsSession(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	conn, info := dialSession(t, tok, sessionHeaders("proj", "claude", "laptop", "", ""))

	p := profileOf(t, alice, info.ID)
	require.Equal(t, []any{"Proj · Claude Code", "proj-claude", true, "laptop", "laptop", "claude", ""},
		[]any{p.DisplayName, p.Handle, p.AI.Online, p.AI.Machine, p.AI.MachineLabel, p.AI.App, p.AI.Folder})
	require.Equal(t, []dto.App{{Kind: "claude", Label: "Claude Code"}}, p.Apps)
	require.NotNil(t, p.AI.ConnectedAt)

	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.UpsertMachine(ctx, db.UpsertMachineParams{OwnerID: alice.User.ID, Hostname: "laptop", Home: "/home/alice"}); err != nil {
			return err
		}
		return q.SetMachineLabel(ctx, db.SetMachineLabelParams{Label: "Alice's laptop", OwnerID: alice.User.ID, Hostname: "laptop"})
	}))
	require.Equal(t, "Alice's laptop", profileOf(t, alice, info.ID).AI.MachineLabel)

	conn.Close()
	require.Eventually(t, func() bool { return !profileOf(t, alice, info.ID).AI.Online }, wsWait, 20*time.Millisecond)
	require.NotNil(t, profileOf(t, alice, info.ID).AI.LastSeenAt)
}

// A person's profile shows the platform of its connector; users who own or may use the
// connector read it, nobody else does (PEOP-04 starts here).
func TestPersonProfile(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	connectorID, _ := storeChat(t, env, alice.User.ID, "family")
	pat := seedPerson(t, env, connectorID, "5511999990000", "Pat")

	p := profileOf(t, alice, pat)
	require.Equal(t, []any{dto.IntelligenceKindHuman, dto.IntelligenceRolePerson, "Pat", "pat", "telegram", true},
		[]any{p.Kind, p.Role, p.DisplayName, p.Handle, p.Platform, p.CanEdit})
	getProfile(bob, pat).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// PROF-03, PROF-10 (L-017): every refused edit has its status: 422 with the offending
// fields and nothing applied, 409 handle_taken for a handle in use (case-insensitive), 401
// without credentials, 400 for a malformed body.
func TestProfileEditErrors(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	aiID := registerAgent(t, env, alice, "builder")
	path := "/api/v1/intelligences/" + alice.User.ID

	for _, c := range []struct {
		name  string
		req   dto.UpdateProfileRequest
		field string
	}{
		{"empty display name", dto.UpdateProfileRequest{DisplayName: ptr(" ")}, "display_name"},
		{"display name of 65 characters", dto.UpdateProfileRequest{DisplayName: ptr(strings.Repeat("x", 65))}, "display_name"},
		{"handle starting with a digit", dto.UpdateProfileRequest{Handle: ptr("1abc")}, "handle"},
		{"handle with an underscore", dto.UpdateProfileRequest{Handle: ptr("a_b")}, "handle"},
		{"handle of 41 characters", dto.UpdateProfileRequest{Handle: ptr("a" + strings.Repeat("b", 40))}, "handle"},
		{"reserved handle", dto.UpdateProfileRequest{Handle: ptr("partyline")}, "handle"},
		{"bio of 281 characters", dto.UpdateProfileRequest{Bio: ptr(strings.Repeat("é", 281))}, "bio"},
	} {
		req := c.req
		if c.field != "display_name" {
			req.DisplayName = ptr("Must Not Apply") // a valid field next to the invalid one
		}
		e := patchProfile(alice, alice.User.ID, req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, c.field, c.name)
		after := profileOf(t, alice, alice.User.ID)
		require.Equal(t, []any{"alice", "alice", ""}, []any{after.DisplayName, after.Handle, after.Bio}, "%s: nothing applied", c.name)
	}

	both := patchProfile(alice, alice.User.ID, dto.UpdateProfileRequest{DisplayName: ptr(""), Handle: ptr("-x"), Bio: ptr(strings.Repeat("b", 281))})
	e := both.RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Len(t, e.Fields, 3, "every invalid field is reported")

	for _, taken := range []string{"bob", "BOB", "builder"} { // another user, case-insensitive, an AI
		e := patchProfile(alice, alice.User.ID, dto.UpdateProfileRequest{Handle: ptr(taken), DisplayName: ptr("Must Not Apply")}).
			RequireError(http.StatusConflict, dto.ErrorCodeHandleTaken)
		require.NotEmpty(t, e.Message)
	}
	patchProfile(alice, aiID, dto.UpdateProfileRequest{Handle: ptr("alice")}).RequireError(http.StatusConflict, dto.ErrorCodeHandleTaken)
	patchProfile(alice, alice.User.ID, dto.UpdateProfileRequest{Handle: ptr("alice")}).RequireStatus(http.StatusOK)
	patchProfile(bob, bob.User.ID, dto.UpdateProfileRequest{Handle: ptr("robert")}).RequireStatus(http.StatusOK)
	patchProfile(alice, alice.User.ID, dto.UpdateProfileRequest{Handle: ptr("bob")}).RequireStatus(http.StatusOK)
	require.Equal(t, "alice", profileOf(t, alice, alice.User.ID).DisplayName, "taken handles applied nothing")

	env.Anon().Patch(path, dto.UpdateProfileRequest{Bio: ptr("x")}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	env.Anon().Get(path).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	alice.DoRaw(http.MethodPatch, path, "application/json", strings.NewReader("{not json")).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	getProfile(alice, store.NewID()).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	patchProfile(alice, store.NewID(), dto.UpdateProfileRequest{Bio: ptr("x")}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// Authorization matrix (owner, use grantee, poster, stranger, admin without grant) for the
// profile of an AI owned by the owner and shared with the viewer.
func TestAIProfileRoutesAuthMatrix(t *testing.T) {
	m := newMatrix(t)
	setup := func(t *testing.T, m *matrix) matrixResource {
		owner := m.as(t, roleOwner)
		id := registerAgent(t, m.env, owner, "builder")
		shareAgent(owner, id, m.clients[roleViewer].User.ID)
		return matrixResource{"ai": id}
	}
	routes := []matrixRoute{
		{"get AI profile", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return getProfile(c, res["ai"])
		}, statuses(200, 200, 404, 404, 404)},
		{"edit AI profile", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return patchProfile(c, res["ai"], dto.UpdateProfileRequest{Bio: ptr("Builds things")})
		}, statuses(200, 403, 404, 404, 404)},
	}
	m.run(t, setup, routes)
}

// Authorization matrix for the profile of the owner's own account: every user reads it, only
// the owner edits it; an admin has no extra right (ACL-08).
func TestUserProfileRoutesAuthMatrix(t *testing.T) {
	m := newMatrix(t)
	setup := func(_ *testing.T, m *matrix) matrixResource {
		return matrixResource{"user": m.clients[roleOwner].User.ID}
	}
	routes := []matrixRoute{
		{"get user profile", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return getProfile(c, res["user"])
		}, statuses(200, 200, 200, 200, 200)},
		{"edit user profile", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return patchProfile(c, res["user"], dto.UpdateProfileRequest{Bio: ptr("Hello")})
		}, statuses(200, 403, 403, 403, 403)},
	}
	m.run(t, setup, routes)
}

// Authorization matrix for a person: the connector's owner and its use grantee read and edit
// it; the others find nothing.
func TestPersonProfileRoutesAuthMatrix(t *testing.T) {
	m := newMatrix(t)
	setup := func(t *testing.T, m *matrix) matrixResource {
		owner := m.as(t, roleOwner)
		connectorID, _ := storeChat(t, m.env, owner.User.ID, "family")
		grant := m.as(t, roleViewer)
		owner.Put("/api/v1/connectors/"+connectorID+"/sharing", dto.Sharing{
			Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: grant.User.ID, Level: dto.LevelUse}},
		}).RequireStatus(http.StatusOK)
		return matrixResource{"person": seedPerson(t, m.env, connectorID, "5511999990000", "Pat")}
	}
	routes := []matrixRoute{
		{"get person profile", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return getProfile(c, res["person"])
		}, statuses(200, 200, 404, 404, 404)},
		{"edit person profile", func(_ *testing.T, _ *matrix, c *testkit.Client, res matrixResource) *testkit.Response {
			return patchProfile(c, res["person"], dto.UpdateProfileRequest{DisplayName: ptr("Patricia")})
		}, statuses(200, 200, 404, 404, 404)},
	}
	m.run(t, setup, routes)
}

// Agent tokens reach only the /agent routes (TOK-03): the profile routes refuse them.
func TestProfileRoutesRefuseAgentTokens(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	getProfile(tok, alice.User.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	patchProfile(tok, alice.User.ID, dto.UpdateProfileRequest{Bio: ptr("x")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	user := alice.Token(dto.TokenKindUser, "script")
	patchProfile(user, alice.User.ID, dto.UpdateProfileRequest{Bio: ptr("via a user token")}).RequireStatus(http.StatusOK)
}

// intelligence.changed reaches the live sockets of users who can see the intelligence, with
// its new summary, and nobody else: the owner and a use grantee see an AI's rename, a
// stranger does not; every user sees a user's rename.
func TestLiveIntelligenceChangedFollowsVisibility(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	id := registerAgent(t, env, alice, "builder")
	shareAgent(alice, id, bob.User.ID)
	aliceLive, bobLive, carolLive := dialLive(t, alice), dialLive(t, bob), dialLive(t, carol)

	patchProfile(alice, id, dto.UpdateProfileRequest{DisplayName: ptr("Atlas"), Handle: ptr("atlas")}).RequireStatus(http.StatusOK)
	for _, c := range []*testkit.LiveConn{aliceLive, bobLive} {
		var got dto.IntelligenceRef
		c.Expect(dto.LiveEventTypeIntelligenceChanged, &got, liveWait)
		require.Equal(t, []any{id, dto.IntelligenceKindAI, dto.IntelligenceRoleAI, "Atlas", "atlas"},
			[]any{got.ID, got.Kind, got.Role, got.DisplayName, got.Handle})
	}
	carolLive.ExpectNone(quiet)

	patchProfile(bob, bob.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Robert")}).RequireStatus(http.StatusOK)
	for _, c := range []*testkit.LiveConn{aliceLive, bobLive, carolLive} {
		var got dto.IntelligenceRef
		c.Expect(dto.LiveEventTypeIntelligenceChanged, &got, liveWait)
		require.Equal(t, []any{bob.User.ID, dto.IntelligenceRoleUser, "Robert"}, []any{got.ID, got.Role, got.DisplayName})
	}
}
