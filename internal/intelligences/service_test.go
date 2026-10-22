package intelligences_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
	"github.com/wnexous/partyline/internal/validation"
)

var clockStart = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

// world is an app with an admin and five members; the service under test is the app's own.
type world struct {
	t     *testing.T
	ctx   context.Context
	env   *testkit.Env
	clock *testkit.Clock
	svc   *intelligences.Service

	admin, alice, bob, carol, dave, erin *testkit.Client
}

func newWorld(t *testing.T) *world {
	t.Helper()
	clock := testkit.NewClock(clockStart)
	env := testkit.New(t, testkit.WithClock(clock.Now))
	return &world{
		t: t, ctx: context.Background(), env: env, clock: clock, svc: env.App.Intelligences,
		admin: env.SetupAdmin(), alice: env.NewUser("alice"), bob: env.NewUser("bob"),
		carol: env.NewUser("carol"), dave: env.NewUser("dave"), erin: env.NewUser("erin"),
	}
}

func asUser(c *testkit.Client) authn.Principal {
	return authn.Principal{Kind: authn.KindUser, UserID: c.User.ID, Role: c.User.Role}
}

// asAgent is the principal of an agent token of owner acting as agentID.
func asAgent(owner *testkit.Client, agentID string) authn.Principal {
	return authn.Principal{Kind: authn.KindAgent, UserID: owner.User.ID, AgentID: agentID}
}

func (w *world) intelligence(id string) db.Intelligence {
	w.t.Helper()
	i, err := w.env.App.Store.R().GetIntelligence(w.ctx, id)
	require.NoError(w.t, err)
	return i
}

// agent creates owner's AI of the folder /work/<name> on the machine "laptop", as its first
// session would, and returns its id: its profile has the default names of a folder.
func (w *world) agent(owner *testkit.Client, name string) string {
	w.t.Helper()
	a, err := w.env.App.Agents.ResolveFolder(w.ctx, owner.User.ID, agents.FolderID{Machine: "laptop", Folder: "/work/" + name, Harness: "claude"}, false)
	require.NoError(w.t, err)
	return a.ID
}

func (w *world) tx(fn func(q *db.Queries) error) {
	w.t.Helper()
	require.NoError(w.t, w.env.App.Store.WithTx(w.ctx, fn))
}

// room creates a room owned by owner with the given agents as members.
func (w *world) room(owner *testkit.Client, name string, agentIDs ...string) string {
	w.t.Helper()
	r, err := w.env.App.Rooms.Create(w.ctx, asUser(owner), rooms.CreateInput{Name: name})
	require.NoError(w.t, err)
	for _, id := range agentIDs {
		_, err := w.env.App.Rooms.AddMember(w.ctx, asUser(owner), r.ID, rooms.MemberInput{Kind: dto.MemberKindAgent, MemberID: id})
		require.NoError(w.t, err)
	}
	return r.ID
}

// connector creates a connector owned by owner and shares it (use) with the given users.
func (w *world) connector(owner *testkit.Client, grantees ...*testkit.Client) string {
	w.t.Helper()
	id := store.NewID()
	w.tx(func(q *db.Queries) error {
		return q.CreateConnector(w.ctx, db.CreateConnectorParams{
			ID: id, OwnerID: owner.User.ID, Kind: "webhook", Name: "hook", ConfigJson: "{}", Enabled: true,
			CreatedAt: clockStart.UnixMilli(), UpdatedAt: clockStart.UnixMilli(),
		})
	})
	if len(grantees) > 0 {
		v := access.Visibility{Mode: dto.VisibilityShared}
		for _, g := range grantees {
			v.Users = append(v.Users, access.UserLevel{UserID: g.User.ID, Level: dto.LevelUse})
		}
		require.NoError(w.t, w.env.App.Access.SetVisibility(w.ctx, owner.User.ID, access.KindConnector, id, owner.User.ID, v))
	}
	return id
}

// person creates an external person of a connector, as the connectors' inbound pipeline will.
func (w *world) person(connectorID, externalID, name string) string {
	w.t.Helper()
	id := store.NewID()
	w.tx(func(q *db.Queries) error {
		if _, err := intelligences.Create(w.ctx, q, intelligences.NewParams{
			ID: id, Kind: handles.Person, DisplayName: name, HandleBase: name, Now: clockStart,
		}); err != nil {
			return err
		}
		return q.CreatePerson(w.ctx, db.CreatePersonParams{
			ID: id, ConnectorID: connectorID, ExternalID: externalID, PlatformName: name, CreatedAt: clockStart.UnixMilli(),
		})
	})
	return id
}

func (w *world) shareAgent(owner *testkit.Client, agentID string, grantee *testkit.Client) {
	w.t.Helper()
	require.NoError(w.t, w.env.App.Access.SetVisibility(w.ctx, owner.User.ID, access.KindAgent, agentID, owner.User.ID, access.Visibility{
		Mode: dto.VisibilityShared, Users: []access.UserLevel{{UserID: grantee.User.ID, Level: dto.LevelUse}},
	}))
}

func requireInvalid(t *testing.T, err error, field string) {
	t.Helper()
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, field)
}

// PROF-01: every user account, however it is created (first-run setup, invite, admin), has
// exactly one intelligence of kind human; its display name is the account's display name,
// else the username, and its handle derives from the username.
func TestEveryNewUserGetsOneIntelligence(t *testing.T) {
	w := newWorld(t)
	svc := w.env.App.Users
	_, secret, err := svc.CreateInvite(w.ctx, w.admin.User.ID, dto.RoleMember)
	require.NoError(t, err)
	invited, err := svc.AcceptInvite(w.ctx, secret, users.AccountInput{Username: "carol_c", Password: "carol-password-1", DisplayName: "Carol C."})
	require.NoError(t, err)
	created, err := svc.CreateUser(w.ctx, users.AccountInput{Username: "frank", Password: "frank-password-1"}, dto.RoleMember)
	require.NoError(t, err)

	for _, c := range []struct{ name, id, display, handle string }{
		{"first-run setup", w.admin.User.ID, "Admin", "admin"},
		{"invite", invited.ID, "Carol C.", "carol-c"},
		{"admin-created without a display name", created.ID, "frank", "frank"},
		{"test-kit member", w.alice.User.ID, "alice", "alice"},
	} {
		got := w.intelligence(c.id)
		require.Equal(t, []string{"human", c.display, c.handle}, []string{got.Kind, got.DisplayName, got.Handle}, c.name)
	}

	all, err := svc.ListUsers(w.ctx)
	require.NoError(t, err)
	ids := make([]string, len(all))
	for i, u := range all {
		ids[i] = u.ID
	}
	rows, err := w.env.App.Store.R().ListIntelligencesByIDs(w.ctx, ids)
	require.NoError(t, err)
	require.Len(t, rows, len(ids), "one intelligence per account")
}

// PROF-02: the handle of a new account derives from its username (lower-case a-z0-9-, a
// letter first), is unique across the instance and skips reserved words with -2, -3 suffixes.
func TestNewUserHandlesAreUniqueAndSkipReservedWords(t *testing.T) {
	w := newWorld(t)
	create := func(username string) string {
		t.Helper()
		u, err := w.env.App.Users.CreateUser(w.ctx, users.AccountInput{Username: username, Password: "some-password-1"}, dto.RoleMember)
		require.NoError(t, err)
		return w.intelligence(u.ID).Handle
	}
	require.Equal(t, "bob-smith", create("bob_smith"))
	require.Equal(t, "bob-smith-2", create("bob-smith"), "two usernames with one slug")
	require.Equal(t, "bob-smith-3", create("bob--smith"), "and a third one")
	require.Equal(t, "user-123", create("123"), "a digit first gets the user- prefix")
	require.Equal(t, "partyline-2", create("partyline"), "a reserved word is never a handle")
	require.Equal(t, "owner-2", create("owner"))
}

// A display name given at account creation is cut to 64 code points (the profile limit), and
// control characters are dropped, so every intelligence satisfies PROF-03's display rule.
func TestNewUserDisplayNameIsCleaned(t *testing.T) {
	w := newWorld(t)
	u, err := w.env.App.Users.CreateUser(w.ctx, users.AccountInput{
		Username: "longname", Password: "some-password-1", DisplayName: "  Ann\x00\u0007e " + strings.Repeat("é", 70),
	}, dto.RoleMember)
	require.NoError(t, err)
	got := w.intelligence(u.ID).DisplayName
	require.Equal(t, "Anne "+strings.Repeat("é", 59), got, "trimmed, control characters dropped, 64 code points")
}

// PROF-01: every AI, however it first shows up, has exactly one intelligence of kind ai, and
// meeting it again keeps that intelligence.
func TestEveryNewAgentGetsOneIntelligence(t *testing.T) {
	w := newWorld(t)
	reg := w.env.App.Agents

	id := w.agent(w.alice, "Billing API!")
	got := w.intelligence(id)
	require.Equal(t, []string{"ai", "Billing API!", "billing-api"}, []string{got.Kind, got.DisplayName, got.Handle})

	_, err := w.svc.Update(w.ctx, asUser(w.alice), id, intelligences.ProfileUpdate{DisplayName: ptr("Biller")})
	require.NoError(t, err)
	require.Equal(t, id, w.agent(w.alice, "Billing API!"), "the same agent")
	require.Equal(t, "Biller", w.intelligence(id).DisplayName, "meeting it again keeps the profile")

	other := w.agent(w.bob, "Billing API!")
	require.NotEqual(t, id, other, "another owner, another agent")
	require.Equal(t, "billing-api-2", w.intelligence(other).Handle, "handles are unique across owners")

	live, err := reg.ResolveFolder(w.ctx, w.alice.User.ID, agents.FolderID{Machine: "laptop", Folder: "/work/atlas", Harness: "claude"}, true)
	require.NoError(t, err)
	require.Equal(t, []string{"ai", "atlas"}, []string{w.intelligence(live.ID).Kind, w.intelligence(live.ID).Handle})

	tools, err := reg.RegisterFolder(w.ctx, w.carol.User.ID, agents.FolderID{Machine: "laptop", Folder: "/work/scribe", Harness: "codex"})
	require.NoError(t, err)
	require.Equal(t, "scribe", w.intelligence(tools.Agent.ID).Handle, "a tools-only registration creates it too")
}

// The v0.3 rename paths still in use write the intelligence, which names authors everywhere:
// the owner's agent rename and the account's own display name.
func TestLegacyRenamePathsWriteTheIntelligence(t *testing.T) {
	w := newWorld(t)
	reg := w.env.App.Agents
	codex := w.agent(w.alice, "codex-box")
	require.Equal(t, "Codex-box", w.intelligence(codex).DisplayName)

	_, err := reg.Update(w.ctx, asUser(w.alice), codex, agents.AgentUpdate{DisplayName: ptr("Codex helper")})
	require.NoError(t, err)
	require.Equal(t, "Codex helper", w.intelligence(codex).DisplayName, "the owner's agent rename")

	usersSvc := w.env.App.Users
	_, err = usersSvc.UpdateProfile(w.ctx, w.bob.User.ID, users.ProfileUpdate{DisplayName: ptr("  Bobby ")})
	require.NoError(t, err)
	require.Equal(t, "Bobby", w.intelligence(w.bob.User.ID).DisplayName, "the account's display name")
	_, err = usersSvc.UpdateProfile(w.ctx, w.bob.User.ID, users.ProfileUpdate{DisplayName: ptr("")})
	require.NoError(t, err)
	require.Equal(t, "bob", w.intelligence(w.bob.User.ID).DisplayName, "an empty display name shows the username again")
}

// PROF-03, PROF-05: a profile update trims, applies display name (1-64 code points), handle
// and bio (0-280 code points) and moves updated_at; unchanged fields stay.
func TestUpdateAppliesTheProfile(t *testing.T) {
	w := newWorld(t)
	before := w.intelligence(w.alice.User.ID)
	w.clock.Advance(time.Hour)

	got, err := w.svc.Update(w.ctx, asUser(w.alice), w.alice.User.ID, intelligences.ProfileUpdate{
		DisplayName: ptr("  Alice A.  "), Handle: ptr("Alice-Prime"), Bio: ptr("  Writes the docs.\r\nSecond line\x07 "),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"Alice A.", "alice-prime", "Writes the docs.\nSecond line"}, []string{got.DisplayName, got.Handle, got.Bio})
	require.Equal(t, intelligences.RoleUser, got.Role)
	require.True(t, got.CanEdit)

	stored := w.intelligence(w.alice.User.ID)
	require.Equal(t, got.Intelligence, stored)
	require.Equal(t, before.CreatedAt, stored.CreatedAt)
	require.Equal(t, clockStart.Add(time.Hour).UnixMilli(), stored.UpdatedAt)

	again, err := w.svc.Update(w.ctx, asUser(w.alice), w.alice.User.ID, intelligences.ProfileUpdate{Bio: ptr("")})
	require.NoError(t, err)
	require.Equal(t, []string{"Alice A.", "alice-prime", ""}, []string{again.DisplayName, again.Handle, again.Bio},
		"absent fields are unchanged; an empty bio clears it")
}

// PROF-03: the display name has 1-64 code points after trimming, the bio 0-280; the first
// invalid field refuses the whole update and nothing is applied.
func TestUpdateFieldBoundaries(t *testing.T) {
	w := newWorld(t)
	id := w.alice.User.ID
	update := func(in intelligences.ProfileUpdate) (intelligences.Profile, error) {
		return w.svc.Update(w.ctx, asUser(w.alice), id, in)
	}

	last := ""
	for _, ok := range []string{"x", strings.Repeat("x", 64), strings.Repeat("é", 64), "  " + strings.Repeat("y", 64) + "  "} {
		got, err := update(intelligences.ProfileUpdate{DisplayName: ptr(ok)})
		require.NoError(t, err, "%q", ok)
		last = strings.TrimSpace(ok)
		require.Equal(t, last, got.DisplayName)
	}
	for _, bad := range []string{"", "   ", "\x00\x07", strings.Repeat("x", 65), strings.Repeat("é", 65)} {
		_, err := update(intelligences.ProfileUpdate{DisplayName: ptr(bad), Bio: ptr("must not apply")})
		requireInvalid(t, err, "display_name")
	}
	require.Equal(t, "", w.intelligence(id).Bio, "an invalid update applies none of its fields")

	for _, ok := range []string{"", strings.Repeat("b", 280), strings.Repeat("é", 280), "tab\tand\nnewline"} {
		_, err := update(intelligences.ProfileUpdate{Bio: ptr(ok)})
		require.NoError(t, err, "%q", ok)
	}
	for _, bad := range []string{strings.Repeat("b", 281), strings.Repeat("é", 281)} {
		_, err := update(intelligences.ProfileUpdate{Bio: ptr(bad), DisplayName: ptr("Must Not Apply")})
		requireInvalid(t, err, "bio")
	}
	require.Equal(t, last, w.intelligence(id).DisplayName, "a refused bio keeps the display name too")

	_, err := update(intelligences.ProfileUpdate{DisplayName: ptr(""), Bio: ptr(strings.Repeat("b", 281))})
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.ElementsMatch(t, []string{"display_name", "bio"}, keys(verr.Fields), "every invalid field is reported")
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// PROF-02: a handle is 1-40 characters of a-z, 0-9 and "-", starts with a letter, is
// lower-cased and is not a reserved word.
func TestUpdateHandleRules(t *testing.T) {
	w := newWorld(t)
	id := w.alice.User.ID
	set := func(h string) (intelligences.Profile, error) {
		return w.svc.Update(w.ctx, asUser(w.alice), id, intelligences.ProfileUpdate{Handle: ptr(h)})
	}
	for _, c := range []struct{ in, want string }{
		{"a", "a"}, {"Atlas-2", "atlas-2"}, {"a1-b2", "a1-b2"}, {"x" + strings.Repeat("9", 39), "x" + strings.Repeat("9", 39)},
		{"ALICE", "alice"},
	} {
		got, err := set(c.in)
		require.NoError(t, err, c.in)
		require.Equal(t, c.want, got.Handle, c.in)
	}
	for _, bad := range []string{
		"", "1abc", "-abc", "a b", "a_b", "é", "a.b", "@atlas", "x" + strings.Repeat("9", 40),
		"partyline", "ALL", "everyone", "Here", "owner",
	} {
		_, err := set(bad)
		requireInvalid(t, err, "handle")
	}
	require.Equal(t, "alice", w.intelligence(id).Handle, "refused handles change nothing")
}

// PROF-10: a handle in use, compared case-insensitively, is refused with ErrHandleTaken and
// nothing in the request is applied; keeping one's own handle (also in another case) is fine.
func TestUpdateHandleTaken(t *testing.T) {
	w := newWorld(t)
	aiID := w.agent(w.alice, "atlas")
	set := func(c *testkit.Client, id string, in intelligences.ProfileUpdate) error {
		_, err := w.svc.Update(w.ctx, asUser(c), id, in)
		return err
	}

	require.ErrorIs(t, set(w.alice, w.alice.User.ID, intelligences.ProfileUpdate{Handle: ptr("bob"), DisplayName: ptr("Must Not Apply")}), intelligences.ErrHandleTaken)
	require.ErrorIs(t, set(w.alice, w.alice.User.ID, intelligences.ProfileUpdate{Handle: ptr("BOB")}), intelligences.ErrHandleTaken, "case-insensitive")
	require.ErrorIs(t, set(w.alice, w.alice.User.ID, intelligences.ProfileUpdate{Handle: ptr("atlas")}), intelligences.ErrHandleTaken, "an AI's handle is taken for users too")
	require.ErrorIs(t, set(w.alice, aiID, intelligences.ProfileUpdate{Handle: ptr("Alice")}), intelligences.ErrHandleTaken, "and the other way round")
	require.Equal(t, "alice", w.intelligence(w.alice.User.ID).DisplayName, "nothing is applied")

	require.NoError(t, set(w.alice, w.alice.User.ID, intelligences.ProfileUpdate{Handle: ptr("alice")}), "the unchanged handle")
	require.NoError(t, set(w.alice, w.alice.User.ID, intelligences.ProfileUpdate{Handle: ptr("ALICE")}), "own handle in another case")
	require.NoError(t, set(w.bob, w.bob.User.ID, intelligences.ProfileUpdate{Handle: ptr("alice-2")}))
	require.NoError(t, set(w.alice, w.alice.User.ID, intelligences.ProfileUpdate{Handle: ptr("bob")}), "a freed handle can be taken")
}

// Edit rights (PROF-03, PROF-04, PROF-05, PROF-11): a user edits their own profile; an AI's
// owner edits the AI; the AI edits itself; grantees, other users, admins and other AIs do not.
// Without view the answer is not found, with view but without edit it is forbidden.
func TestUpdateAuthorization(t *testing.T) {
	w := newWorld(t)
	atlas, nova := w.agent(w.alice, "atlas"), w.agent(w.alice, "nova")
	carolsAI := w.agent(w.carol, "scout")
	w.shareAgent(w.alice, atlas, w.bob)
	conn := w.connector(w.dave, w.erin)
	pat := w.person(conn, "5511999990000", "Pat")

	rename := intelligences.ProfileUpdate{DisplayName: ptr("Renamed")}
	cases := []struct {
		name   string
		caller authn.Principal
		target string
		want   error
	}{
		{"own user profile", asUser(w.alice), w.alice.User.ID, nil},
		{"another user's profile", asUser(w.bob), w.alice.User.ID, access.ErrForbidden},
		{"an admin on a user profile", asUser(w.admin), w.alice.User.ID, access.ErrForbidden},
		{"an agent on its owner's profile", asAgent(w.alice, atlas), w.alice.User.ID, access.ErrForbidden},
		{"AI owner", asUser(w.alice), atlas, nil},
		{"AI use grantee", asUser(w.bob), atlas, access.ErrForbidden},
		{"AI stranger", asUser(w.carol), atlas, access.ErrNotFound},
		{"admin on an AI they do not own", asUser(w.admin), atlas, access.ErrNotFound},
		{"the AI itself", asAgent(w.alice, atlas), atlas, nil},
		{"a sibling AI of the same owner", asAgent(w.alice, nova), atlas, access.ErrForbidden},
		{"an AI of a stranger", asAgent(w.carol, carolsAI), atlas, access.ErrNotFound},
		{"person: connector owner", asUser(w.dave), pat, nil},
		{"person: connector use grantee", asUser(w.erin), pat, nil},
		{"person: stranger", asUser(w.carol), pat, access.ErrNotFound},
		{"person: AI of the connector owner", asAgent(w.dave, w.agent(w.dave, "relay")), pat, access.ErrForbidden},
		{"unknown id", asUser(w.alice), store.NewID(), access.ErrNotFound},
	}
	for _, c := range cases {
		before, _ := w.env.App.Store.R().GetIntelligence(w.ctx, c.target)
		_, err := w.svc.Update(w.ctx, c.caller, c.target, rename)
		if c.want == nil {
			require.NoError(t, err, c.name)
			require.Equal(t, "Renamed", w.intelligence(c.target).DisplayName, c.name)
			continue
		}
		require.ErrorIs(t, err, c.want, c.name)
		after, _ := w.env.App.Store.R().GetIntelligence(w.ctx, c.target)
		require.Equal(t, before, after, "%s: nothing changed", c.name)
	}
}

// The design's CanSee / CanEdit cases: self, other user, AI owner, AI grantee, stranger,
// the AI itself, sibling and foreign AIs, and a person through its connector.
func TestCanSeeAndCanEdit(t *testing.T) {
	w := newWorld(t)
	atlas, nova := w.agent(w.alice, "atlas"), w.agent(w.alice, "nova")
	carolsAI, davesAI := w.agent(w.carol, "scout"), w.agent(w.dave, "relay")
	w.shareAgent(w.alice, atlas, w.bob)
	conn := w.connector(w.dave, w.erin)
	w.connector(w.alice) // alice owns a connector too, but not Pat's
	pat := w.person(conn, "5511999990000", "Pat")

	cases := []struct {
		name      string
		caller    authn.Principal
		target    string
		see, edit bool
	}{
		{"self", asUser(w.alice), w.alice.User.ID, true, true},
		{"other user", asUser(w.bob), w.alice.User.ID, true, false},
		{"admin sees a user", asUser(w.admin), w.alice.User.ID, true, false},
		{"agent sees its owner's account", asAgent(w.alice, atlas), w.alice.User.ID, true, false},
		{"agent sees another user", asAgent(w.alice, atlas), w.bob.User.ID, true, false},
		{"AI owner", asUser(w.alice), atlas, true, true},
		{"AI use grantee", asUser(w.bob), atlas, true, false},
		{"AI stranger", asUser(w.carol), atlas, false, false},
		{"admin without access", asUser(w.admin), atlas, false, false},
		{"the AI itself", asAgent(w.alice, atlas), atlas, true, true},
		{"sibling AI sees through the owner", asAgent(w.alice, nova), atlas, true, false},
		{"stranger's AI", asAgent(w.carol, carolsAI), atlas, false, false},
		{"person: connector owner", asUser(w.dave), pat, true, true},
		{"person: connector use grantee", asUser(w.erin), pat, true, true},
		{"person: stranger", asUser(w.carol), pat, false, false},
		{"person: owner of another connector", asUser(w.alice), pat, false, false},
		{"person: connector owner's AI sees, never edits", asAgent(w.dave, davesAI), pat, true, false},
		{"person: stranger's AI", asAgent(w.carol, carolsAI), pat, false, false},
		{"unknown id", asUser(w.alice), store.NewID(), false, false},
	}
	for _, c := range cases {
		see, err := w.svc.CanSee(w.ctx, c.caller, c.target)
		require.NoError(t, err, c.name)
		require.Equal(t, c.see, see, "%s: see", c.name)
		edit, err := w.svc.CanEdit(w.ctx, c.caller, c.target)
		require.NoError(t, err, c.name)
		require.Equal(t, c.edit, edit, "%s: edit", c.name)
	}
}

// A caller who shares a viewable conversation with an AI sees it, as a member or as an
// author who has left; viewing the conversation is what counts (grants and ownership), and
// seeing never allows editing.
func TestCanSeeAnAIThroughSharedConversations(t *testing.T) {
	w := newWorld(t)
	atlas := w.agent(w.alice, "atlas")
	room := w.room(w.alice, "Ops", atlas)
	require.NoError(t, w.env.App.Access.SetVisibility(w.ctx, w.alice.User.ID, access.KindRoom, room, w.alice.User.ID, access.Visibility{
		Mode: dto.VisibilityShared, Users: []access.UserLevel{{UserID: w.carol.User.ID, Level: dto.LevelView}},
	}))

	see := func(c *testkit.Client) bool {
		t.Helper()
		ok, err := w.svc.CanSee(w.ctx, asUser(c), atlas)
		require.NoError(t, err)
		return ok
	}
	require.True(t, see(w.carol), "a viewer of a room the AI is a member of")
	require.False(t, see(w.erin), "no access to the room")
	edit, err := w.svc.CanEdit(w.ctx, asUser(w.carol), atlas)
	require.NoError(t, err)
	require.False(t, edit)

	// The AI wrote in a second room and left it: its author rows still make it visible there.
	left := w.room(w.alice, "Old", atlas)
	posted, err := w.env.App.Rooms.Post(w.ctx, asAgent(w.alice, atlas), left, rooms.PostInput{Text: "hello"})
	require.NoError(t, err)
	w.tx(func(q *db.Queries) error { // rooms.insert writes author_iid from T11 on; migrated rows have it already
		return q.SetMessageAuthorIID(w.ctx, db.SetMessageAuthorIIDParams{AuthorIid: atlas, ID: posted.ID})
	})
	members, err := w.env.App.Rooms.Members(w.ctx, asUser(w.alice), left)
	require.NoError(t, err)
	require.NoError(t, w.env.App.Rooms.RemoveMember(w.ctx, asUser(w.alice), left, members[0].ID))
	require.NoError(t, w.env.App.Access.SetVisibility(w.ctx, w.alice.User.ID, access.KindRoom, left, w.alice.User.ID, access.Visibility{
		Mode: dto.VisibilityShared, Users: []access.UserLevel{{UserID: w.erin.User.ID, Level: dto.LevelPost}},
	}))
	require.True(t, see(w.erin), "a viewer of a room the AI wrote in")
	require.False(t, see(w.dave), "a stranger to both rooms")
}

// A disabled account has no access at all (AUTH-10), also to its own profile.
func TestDisabledCallerSeesNothing(t *testing.T) {
	w := newWorld(t)
	_, err := w.env.App.Users.UpdateUser(w.ctx, w.carol.User.ID, users.UserUpdate{Disabled: ptr(true)})
	require.NoError(t, err)
	for _, target := range []string{w.carol.User.ID, w.alice.User.ID} {
		see, err := w.svc.CanSee(w.ctx, asUser(w.carol), target)
		require.NoError(t, err)
		require.False(t, see)
		_, err = w.svc.Get(w.ctx, asUser(w.carol), target)
		require.ErrorIs(t, err, access.ErrNotFound)
	}
}

// Get returns the profile with its role and whether the caller can edit it; what the caller
// cannot see does not exist for them.
func TestGetProfile(t *testing.T) {
	w := newWorld(t)
	atlas := w.agent(w.alice, "atlas")
	w.shareAgent(w.alice, atlas, w.bob)
	pat := w.person(w.connector(w.dave), "5511999990000", "Pat")

	for _, c := range []struct {
		name    string
		caller  authn.Principal
		target  string
		role    intelligences.Role
		canEdit bool
	}{
		{"user, self", asUser(w.alice), w.alice.User.ID, intelligences.RoleUser, true},
		{"user, other", asUser(w.bob), w.alice.User.ID, intelligences.RoleUser, false},
		{"AI, owner", asUser(w.alice), atlas, intelligences.RoleAI, true},
		{"AI, grantee", asUser(w.bob), atlas, intelligences.RoleAI, false},
		{"person, connector owner", asUser(w.dave), pat, intelligences.RolePerson, true},
	} {
		got, err := w.svc.Get(w.ctx, c.caller, c.target)
		require.NoError(t, err, c.name)
		require.Equal(t, w.intelligence(c.target), got.Intelligence, c.name)
		require.Equal(t, []any{c.role, c.canEdit}, []any{got.Role, got.CanEdit}, c.name)
	}
	_, err := w.svc.Get(w.ctx, asUser(w.carol), atlas)
	require.ErrorIs(t, err, access.ErrNotFound, "a stranger")
	_, err = w.svc.Get(w.ctx, asUser(w.carol), store.NewID())
	require.ErrorIs(t, err, access.ErrNotFound, "an unknown id")
}

// collect subscribes to the bus now; the returned function drains what was published since.
func collect(w *world) func() []events.Event {
	ch, cancel := w.env.App.Bus.Subscribe(256)
	w.t.Cleanup(cancel)
	return func() []events.Event {
		var out []events.Event
		for {
			select {
			case e := <-ch:
				out = append(out, e)
			default:
				return out
			}
		}
	}
}

// An applied update publishes IntelligenceChanged, and MembersChanged for every room the
// intelligence is a member of when its name or handle changed (member lists show both); a bio
// change, a refused update and an update that changes nothing publish no member events or
// nothing at all.
func TestUpdatePublishesEvents(t *testing.T) {
	w := newWorld(t)
	atlas := w.agent(w.alice, "atlas")
	ops, dev := w.room(w.alice, "Ops", atlas), w.room(w.alice, "Dev", atlas)
	w.room(w.alice, "Other")
	gathered := collect(w)
	update := func(in intelligences.ProfileUpdate) error {
		_, err := w.svc.Update(w.ctx, asUser(w.alice), atlas, in)
		return err
	}

	require.NoError(t, update(intelligences.ProfileUpdate{DisplayName: ptr("Atlas Prime")}))
	got := gathered()
	require.ElementsMatch(t, []events.Event{
		events.IntelligenceChanged{ID: atlas}, events.MembersChanged{RoomID: ops}, events.MembersChanged{RoomID: dev},
	}, got, "a rename announces the intelligence and the member lists of its rooms only")

	require.NoError(t, update(intelligences.ProfileUpdate{Handle: ptr("prime")}))
	require.Len(t, gathered(), 3, "a handle change too")

	require.NoError(t, update(intelligences.ProfileUpdate{Bio: ptr("Runs the builds")}))
	require.Equal(t, []events.Event{events.IntelligenceChanged{ID: atlas}}, gathered(), "a bio shows in no member list")

	require.NoError(t, update(intelligences.ProfileUpdate{DisplayName: ptr("Atlas Prime"), Handle: ptr("PRIME"), Bio: ptr("Runs the builds")}))
	require.Empty(t, gathered(), "nothing changed, nothing announced")

	require.Error(t, update(intelligences.ProfileUpdate{DisplayName: ptr(""), Bio: ptr("x")}))
	require.ErrorIs(t, update(intelligences.ProfileUpdate{Handle: ptr("alice")}), intelligences.ErrHandleTaken)
	_, err := w.svc.Update(w.ctx, asUser(w.carol), atlas, intelligences.ProfileUpdate{Bio: ptr("x")})
	require.ErrorIs(t, err, access.ErrNotFound)
	require.Empty(t, gathered(), "refused updates announce nothing")
}

// The user's own rename through the v0.3 profile path announces too, so clients refresh names.
func TestAccountDisplayNameChangeIsAnnounced(t *testing.T) {
	w := newWorld(t)
	gathered := collect(w)
	_, err := w.env.App.Users.UpdateProfile(w.ctx, w.bob.User.ID, users.ProfileUpdate{DisplayName: ptr("Bobby")})
	require.NoError(t, err)
	require.Contains(t, gathered(), events.Event(events.IntelligenceChanged{ID: w.bob.User.ID}))

	_, err = w.env.App.Users.UpdateProfile(w.ctx, w.bob.User.ID, users.ProfileUpdate{Locale: ptr("pt-BR")})
	require.NoError(t, err)
	require.Empty(t, gathered(), "a locale change is no profile change")
}

// The profile is the only place a name lives (the v0.3 name columns are gone): the readers of an
// AI's and an account's name see a rename at once.
func TestUpdateRenamesWhatEveryReaderShows(t *testing.T) {
	w := newWorld(t)
	atlas := w.agent(w.alice, "atlas")
	_, err := w.svc.Update(w.ctx, asUser(w.alice), atlas, intelligences.ProfileUpdate{DisplayName: ptr("Atlas Prime")})
	require.NoError(t, err)
	_, err = w.svc.Update(w.ctx, asUser(w.alice), w.alice.User.ID, intelligences.ProfileUpdate{DisplayName: ptr("Alice A.")})
	require.NoError(t, err)

	row, err := w.env.App.Store.R().GetAgentWithOwner(w.ctx, atlas)
	require.NoError(t, err)
	require.Equal(t, []string{"Atlas Prime", "Alice A."}, []string{row.ProfileName, row.OwnerDisplayName})
}

// Create allocates handles across kinds from one namespace: users, AIs and people never share
// a handle, and a person's default prefix is contact-.
func TestCreateAllocatesHandlesAcrossKinds(t *testing.T) {
	w := newWorld(t)
	var got []db.Intelligence
	w.tx(func(q *db.Queries) error {
		for _, c := range []struct {
			kind handles.Kind
			base string
		}{
			{handles.AI, "alice"}, {handles.AI, "alice"}, {handles.Person, "5511999990000"}, {handles.Person, "Zoë Ñandú"}, {handles.User, ""},
		} {
			i, err := intelligences.Create(w.ctx, q, intelligences.NewParams{ID: store.NewID(), Kind: c.kind, HandleBase: c.base, Now: clockStart})
			if err != nil {
				return err
			}
			got = append(got, i)
		}
		return nil
	})
	handlesOf := make([]string, len(got))
	kinds := make([]string, len(got))
	for i, g := range got {
		handlesOf[i], kinds[i] = g.Handle, g.Kind
		require.NotEmpty(t, g.DisplayName, "every intelligence has a display name")
		require.Equal(t, g, w.intelligence(g.ID), "returned as stored")
	}
	require.Equal(t, []string{"alice-2", "alice-3", "contact-5511999990000", "zoe-nandu", "user"}, handlesOf)
	require.Equal(t, []string{"ai", "ai", "human", "human", "human"}, kinds)
}

// SetDisplayName is how the v0.3 paths that still own a name write the intelligence: it
// cleans the name, falls back to the handle for an empty one, reports whether anything
// changed, and leaves a row that has no intelligence alone.
func TestSetDisplayName(t *testing.T) {
	w := newWorld(t)
	id := w.alice.User.ID
	set := func(name string) bool {
		t.Helper()
		var changed bool
		w.tx(func(q *db.Queries) error {
			var err error
			changed, err = intelligences.SetDisplayName(w.ctx, q, id, name, clockStart.Add(time.Hour))
			return err
		})
		return changed
	}

	require.False(t, set("alice"), "the same name changes nothing")
	require.Equal(t, clockStart.UnixMilli(), w.intelligence(id).UpdatedAt, "and does not touch updated_at")
	require.True(t, set("  Alice\x00 A.  "))
	got := w.intelligence(id)
	require.Equal(t, []any{"Alice A.", clockStart.Add(time.Hour).UnixMilli()}, []any{got.DisplayName, got.UpdatedAt})
	require.True(t, set(" \x07 "), "an empty name falls back to the handle")
	require.Equal(t, "alice", w.intelligence(id).DisplayName)

	orphan := store.NewID() // an account created before intelligences existed
	w.tx(func(q *db.Queries) error {
		changed, err := intelligences.SetDisplayName(w.ctx, q, orphan, "Ghost", clockStart)
		require.NoError(t, err)
		require.False(t, changed)
		return nil
	})
}

// Announce works without a bus (services built without one) and publishes nothing then.
func TestAnnounceWithoutABus(t *testing.T) {
	w := newWorld(t)
	require.NotPanics(t, func() {
		intelligences.Announce(w.ctx, nil, w.env.App.Store.R(), w.alice.User.ID, true)
	})
}

// A handle sent with white space around it is the trimmed handle (the web form trims too).
func TestUpdateTrimsTheHandle(t *testing.T) {
	w := newWorld(t)
	got, err := w.svc.Update(w.ctx, asUser(w.alice), w.alice.User.ID, intelligences.ProfileUpdate{Handle: ptr("  Alice-X \n")})
	require.NoError(t, err)
	require.Equal(t, "alice-x", got.Handle)
}
