package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/orgs"
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

// PROF-05, AID-06, POS-04: whoever manages an AI edits its profile - here its creator, who also hosts it - and the
// rename shows in the AI's profile and in the agents list. A member of its organization reads it and is refused
// the edit (403); a stranger finds nothing (404).
func TestManagerEditsAIProfile(t *testing.T) {
	env := testkit.New(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	id := registerAgent(t, env, alice, "builder") // in alice's own organization, which she created: she manages it
	env.AddMember(alice.PersonalOrg().ID, bob.User.ID, orgs.PermissionMember, "")

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
	listed := listedAgent(t, alice, id).Intelligence
	require.Equal(t, []string{"Atlas", "atlas"}, []string{listed.DisplayName, listed.Handle}, "the agents list shows the rename (AID-06)")

	member := profileOf(t, bob, id)
	require.Equal(t, []any{"Atlas", false, dto.LevelUse}, []any{member.DisplayName, member.CanEdit, member.AI.MyLevel})
	patchProfile(bob, id, dto.UpdateProfileRequest{DisplayName: ptr("Hijacked")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	getProfile(carol, id).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	patchProfile(carol, id, dto.UpdateProfileRequest{DisplayName: ptr("Hijacked")}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, "Atlas", profileOf(t, alice, id).DisplayName, "refused edits change nothing")
}

// A profile shows where the AI lives and whether it is online: current app (the harness) and folder. The machine
// is not a concept of the product any more (AD-022): the profile does not carry it.
func TestAIProfileShowsItsSession(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	conn, resp := tok.DialFolder(testkit.Folder{Machine: "laptop", Path: "/home/alice/proj", Home: "/home/alice", Harness: "claude"})
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	info := conn.Welcome(wsWait).Agent

	p := profileOf(t, alice, info.ID)
	require.Equal(t, []any{"Proj", "proj", true, "claude", "/home/alice/proj", "~/proj"},
		[]any{p.DisplayName, p.Handle, p.AI.Online, p.AI.App, p.AI.Folder, p.AI.FolderLabel})
	require.Equal(t, []dto.App{{Kind: "claude", Label: "Claude Code"}}, p.Apps)
	require.NotNil(t, p.AI.ConnectedAt)

	var raw struct {
		AI map[string]any `json:"ai"`
	}
	require.NoError(t, json.Unmarshal(getProfile(alice, info.ID).RequireStatus(http.StatusOK).Body, &raw))
	require.NotContains(t, raw.AI, "machine", "machines are internal (AD-022)")
	require.NotContains(t, raw.AI, "machine_label")

	conn.Close()
	require.Eventually(t, func() bool { return !profileOf(t, alice, info.ID).AI.Online }, wsWait, 20*time.Millisecond)
	require.NotNil(t, profileOf(t, alice, info.ID).AI.LastSeenAt)
}

// A person's profile shows the platform of its connector; users who own or may use the
// connector read it, nobody else does (PEOP-04 starts here). Nobody can edit it: a person's
// profile is history (CID-21).
func TestPersonProfile(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	connectorID, _ := storeChat(t, env, alice.User.ID, "family")
	pat := seedPerson(t, env, connectorID, "5511999990000", "Pat")

	p := profileOf(t, alice, pat)
	require.Equal(t, []any{dto.IntelligenceKindHuman, dto.IntelligenceRolePerson, "Pat", "pat", "telegram", false},
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

// POS-04, CHART-02 (design, "Intelligences" matrix): the profile of an AI of the organization is read by its
// members, guests included, and edited by whoever manages it - its creator, and the admin whose position is above
// the creator's; an outsider and an admin of the instance find nothing (404).
func TestAIProfileRoutesAuthMatrix(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	setup := func(*testing.T) map[string]string { return map[string]string{"ai": roles.TargetAI} }
	runRoleRoutes(t, roles, setup, []roleRoute{
		{"get AI profile", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return getProfile(c, res["ai"])
		}, agentWant(200, 200, 404)},
		{"edit AI profile", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return patchProfile(c, res["ai"], dto.UpdateProfileRequest{Bio: ptr("Builds things")})
		}, agentWant(200, 403, 404)},
	})
}

// POS-03, POS-04: every user reads every user's profile (the directory). A user edits their own, and so does
// whoever manages them: the manager above Target and, through the root position, the admin; the inviter of a guest.
// A peer, a plain member, the guest of someone else, an outsider and an admin of the instance are told forbidden
// - they can see the user, so it is not a 404 - and an org admin does not manage a member with no position.
func TestUserProfileRoutesAuthMatrixOnOrganizations(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	read := map[string]int{"admin": 200, "manager": 200, "target": 200, "peer": 200, "member": 200, "inviter": 200, "guest": 200, "outsider": 200, "instance_admin": 200}
	editTarget := map[string]int{"admin": 200, "manager": 200, "target": 200, "peer": 403, "member": 403, "inviter": 403, "guest": 403, "outsider": 403, "instance_admin": 403}
	editGuest := map[string]int{"admin": 403, "manager": 403, "target": 403, "peer": 403, "member": 403, "inviter": 200, "guest": 200, "outsider": 403, "instance_admin": 403}
	editManager := map[string]int{"admin": 200, "manager": 200, "target": 403, "peer": 403, "member": 403, "inviter": 403, "guest": 403, "outsider": 403, "instance_admin": 403}
	for _, target := range []struct {
		name string
		id   string
		edit map[string]int
	}{
		{"a worker", roles.Target.User.ID, editTarget},
		{"a guest", roles.Guest.User.ID, editGuest},
		{"the manager", roles.Manager.User.ID, editManager},
	} {
		setup := func(*testing.T) map[string]string { return map[string]string{"user": target.id} }
		runRoleRoutes(t, roles, setup, []roleRoute{
			{"get profile of " + target.name, func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
				return getProfile(c, res["user"])
			}, read},
			{"edit profile of " + target.name, func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
				return patchProfile(c, res["user"], dto.UpdateProfileRequest{Bio: ptr("Hello")})
			}, target.edit},
		})
	}
}

// Authorization matrix for a person (design, "People" and "Connections" rows): the members of the connection's
// organization, whatever their permission, read it and are told, when they try to edit it, that it is history (422,
// CID-21); an outsider and an admin of the instance find nothing. (The profile of a user is the one above: every user
// reads it, and only whoever manages it edits it.)
func TestPersonProfileRoutesAuthMatrix(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	connectorID, _ := storeChatIn(t, env, roles.Admin.User.ID, roles.Org.ID, "family")
	person := seedPerson(t, env, connectorID, "5511999990000", "Pat")
	setup := func(*testing.T) map[string]string { return map[string]string{"person": person} }
	runRoleRoutes(t, roles, setup, []roleRoute{
		{"get person profile", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return getProfile(c, res["person"])
		}, connectionWant(200, 200, 404)},
		{"edit person profile", func(_ *testing.T, c *testkit.Client, res map[string]string) *testkit.Response {
			return patchProfile(c, res["person"], dto.UpdateProfileRequest{DisplayName: ptr("Patricia")})
		}, connectionWant(422, 422, 404)},
	})
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

// intelligence.changed reaches the live sockets of users who can see the intelligence, with its new summary, and
// nobody else: the people of an AI's organization see its rename, an outsider does not; every user sees a user's
// rename.
func TestLiveIntelligenceChangedFollowsVisibility(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	managerLive, memberLive, outsiderLive := dialLive(t, roles.Manager), dialLive(t, roles.Member), dialLive(t, roles.Outsider)
	id := roles.TargetAI

	patchProfile(roles.Manager, id, dto.UpdateProfileRequest{DisplayName: ptr("Atlas"), Handle: ptr("atlas")}).RequireStatus(http.StatusOK)
	for _, c := range []*testkit.LiveConn{managerLive, memberLive} {
		var got dto.IntelligenceRef
		c.Expect(dto.LiveEventTypeIntelligenceChanged, &got, liveWait)
		require.Equal(t, []any{id, dto.IntelligenceKindAI, dto.IntelligenceRoleAI, "Atlas", "atlas"},
			[]any{got.ID, got.Kind, got.Role, got.DisplayName, got.Handle})
	}
	outsiderLive.ExpectNone(quiet)

	patchProfile(roles.Member, roles.Member.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Robert")}).RequireStatus(http.StatusOK)
	for _, c := range []*testkit.LiveConn{managerLive, memberLive, outsiderLive} {
		var got dto.IntelligenceRef
		c.Expect(dto.LiveEventTypeIntelligenceChanged, &got, liveWait)
		require.Equal(t, []any{roles.Member.User.ID, dto.IntelligenceRoleUser, "Robert"}, []any{got.ID, got.Role, got.DisplayName})
	}
}

// OMEM-03, POS-05, POS-06, CHART-02: the profile of a user or an AI lists the organizations the caller shares with
// it - the identity's permission and position in each - and whether the caller may delete it there and what that
// does: an AI is deleted for good in its home organization and only removed from another; a user is removed. An
// identity in two organizations shows both to a caller who belongs to both, and only the shared one to the rest.
func TestProfilesListTheirMemberships(t *testing.T) {
	env := testkit.New(t)
	acme, globex := env.OrgRoles("acme"), env.OrgRoles("globex")
	env.AddGuest(globex.Org.ID, acme.Target.User.ID, globex.Inviter.User.ID, "")
	env.AddMember(globex.Org.ID, acme.TargetAI, orgs.PermissionMember, globex.WorkerPosition)
	env.AddMember(globex.Org.ID, acme.Member.User.ID, orgs.PermissionMember, "")
	env.AddMember(acme.Org.ID, globex.Admin.User.ID, orgs.PermissionAdmin, "")

	in := func(o *testkit.OrgRoles, permission, position, positionName, effect string, canDelete bool) dto.ProfileMembership {
		return dto.ProfileMembership{
			OrgID: o.Org.ID, OrgName: o.Org.Name, OrgSlug: o.Org.Slug, Permission: permission,
			PositionID: position, PositionName: positionName, CanDelete: canDelete, DeleteEffect: effect,
		}
	}
	workerAtAcme := func(canDelete bool) dto.ProfileMembership {
		return in(acme, "member", acme.WorkerPosition, "Worker", "remove", canDelete)
	}
	guestAtGlobex := func(canDelete bool) dto.ProfileMembership { return in(globex, "guest", "", "", "remove", canDelete) }

	user := acme.Target.User.ID
	for _, c := range []struct {
		name   string
		caller *testkit.Client
		want   []dto.ProfileMembership
	}{
		{"an admin of acme", acme.Admin, []dto.ProfileMembership{workerAtAcme(true)}},
		{"the inviter of the guest in globex", globex.Inviter, []dto.ProfileMembership{guestAtGlobex(true)}},
		{"a peer", acme.Peer, []dto.ProfileMembership{workerAtAcme(false)}},
		{"a member of both", acme.Member, []dto.ProfileMembership{workerAtAcme(false), guestAtGlobex(false)}},
		{"an admin of both", globex.Admin, []dto.ProfileMembership{workerAtAcme(true), guestAtGlobex(true)}},
		{"an outsider", acme.Outsider, []dto.ProfileMembership{}},
		{"an admin of the instance", acme.InstanceAdmin, []dto.ProfileMembership{}},
	} {
		got := profileOf(t, c.caller, user)
		require.NotNil(t, got.Memberships, "%s: a list, not null", c.name)
		require.Equal(t, c.want, got.Memberships, c.name)
	}
	own := profileOf(t, acme.Target, user).Memberships
	require.Len(t, own, 3, "the identity sees its own: acme, its personal organization and globex")
	for _, m := range own {
		require.False(t, m.CanDelete, "nobody deletes themself: %s", m.OrgName)
	}

	ai := acme.TargetAI
	aiAtAcme := func(canDelete bool) dto.ProfileMembership { return in(acme, "member", "", "", "delete", canDelete) }
	aiAtGlobex := func(canDelete bool) dto.ProfileMembership {
		return in(globex, "member", globex.WorkerPosition, "Worker", "remove", canDelete)
	}
	for _, c := range []struct {
		name   string
		caller *testkit.Client
		want   []dto.ProfileMembership
	}{
		{"the admin of its home organization", acme.Admin, []dto.ProfileMembership{aiAtAcme(true)}},
		{"a peer of its creator", acme.Peer, []dto.ProfileMembership{aiAtAcme(false)}},
		{"a member of both", acme.Member, []dto.ProfileMembership{aiAtAcme(false), aiAtGlobex(false)}},
		{"an admin of both", globex.Admin, []dto.ProfileMembership{aiAtAcme(true), aiAtGlobex(true)}},
	} {
		require.Equal(t, c.want, profileOf(t, c.caller, ai).Memberships, "the AI, as seen by "+c.name)
	}
	getProfile(acme.Outsider, ai).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// POS-04: an AI's profile says whether the caller may end its session, which is the right of those who manage it
// (the creator and the admin above it); a user's profile never does.
func TestAIProfileTellsWhoMayEndItsSession(t *testing.T) {
	env := testkit.New(t)
	roles := env.OrgRoles("acme")
	for _, role := range roles.All() {
		if role.Name == "outsider" || role.Name == "instance_admin" {
			getProfile(role.Client, roles.TargetAI).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			continue
		}
		got := profileOf(t, role.Client, roles.TargetAI)
		require.Equal(t, role.Name == "admin" || role.Name == "manager", got.CanEndSession, role.Name)
		require.False(t, profileOf(t, role.Client, roles.Target.User.ID).CanEndSession, "%s: a user has no session", role.Name)
	}
}
