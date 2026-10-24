package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// aiAt creates the owner's AI of a folder on a machine, as its first session would, and names it
// (display name and handle). It returns the AI's id.
func aiAt(t *testing.T, env *testkit.Env, owner *testkit.Client, name, handle, machine, folder, home, harness string) string {
	t.Helper()
	ctx := context.Background()
	a, err := env.App.Agents.ResolveFolder(ctx, owner.User.ID, agents.FolderID{Machine: machine, Folder: folder, Home: home, Harness: harness}, false)
	require.NoError(t, err)
	p := authn.Principal{Kind: authn.KindUser, UserID: owner.User.ID, Role: string(owner.User.Role)}
	_, err = env.App.Intelligences.Update(ctx, p, a.ID, intelligences.ProfileUpdate{DisplayName: &name, Handle: &handle})
	require.NoError(t, err)
	return a.ID
}

// aiAtIn is aiAt for an AI whose home is the organization: the owner's default is that organization while it
// is made, and their own again afterwards.
func aiAtIn(t *testing.T, env *testkit.Env, owner *testkit.Client, orgID, name, handle, machine, folder, home, harness string) string {
	t.Helper()
	ctx := context.Background()
	before, err := env.App.Store.R().GetUser(ctx, owner.User.ID)
	require.NoError(t, err)
	require.NoError(t, env.App.Orgs.SetDefault(ctx, owner.User.ID, orgID))
	defer func() {
		require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
			return q.SetUserDefaultOrg(ctx, db.SetUserDefaultOrgParams{DefaultOrgID: before.DefaultOrgID, ID: owner.User.ID})
		}))
	}()
	return aiAt(t, env, owner, name, handle, machine, folder, home, harness)
}

// contactsFixture extends the people fixture with AIs. alice owns, on the machine devbox (home
// /home/alice): Atlas (~/atlas, Claude Code, online), Notes (~/atlas/notes, OpenCode) and Lab
// (~/c/d/lab, Codex, seen once and offline); on notebook: Dev (~, Claude Code). Old is a v0.3
// identity without a folder. bob's AI Bobby is shared with alice and his Secret is not; carol's
// Public is shared with everyone.
type contactsFixture struct {
	*peopleFixture
	atlas, notes, lab, dev, old string
	bobby, secret, public       string
	atlasConn                   *testkit.AgentConn
}

func newContactsFixture(t *testing.T) *contactsFixture {
	t.Helper()
	f := &contactsFixture{peopleFixture: newPeopleFixture(t)}
	f.atlas = aiAt(t, f.env, f.alice, "Atlas", "atlas", "devbox", "/home/alice/atlas", "/home/alice", "claude")
	f.notes = aiAt(t, f.env, f.alice, "Notes", "notes", "devbox", "/home/alice/atlas/notes", "/home/alice", "opencode")
	f.lab = aiAt(t, f.env, f.alice, "Lab", "lab", "devbox", "/home/alice/c/d/lab", "/home/alice", "codex")
	f.dev = aiAt(t, f.env, f.alice, "Dev", "dev", "notebook", "/home/alice", "/home/alice", "claude")
	f.old = f.env.NewLegacyAI(f.alice, "old-helper")
	f.bobby = aiAt(t, f.env, f.bob, "Bobby", "bobby", "bobs-pc", "/home/bob/bobby", "/home/bob", "claude")
	f.secret = aiAt(t, f.env, f.bob, "Secret", "secret", "bobs-pc", "/home/bob/secret", "/home/bob", "claude")
	f.public = aiAt(t, f.env, f.carol, "Public", "public", "carols-pc", "/srv/public", "/home/carol", "claude")
	shareAgent(f.bob, f.bobby, f.alice.User.ID)
	f.carol.Put("/api/v1/agents/"+f.public+"/sharing", dto.Sharing{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelUse}).RequireStatus(http.StatusOK)

	// Lab connects once and leaves; Atlas stays online.
	lab, resp := f.alice.Token(dto.TokenKindAgent, "devbox").DialFolder(testkit.Folder{Machine: "devbox", Path: "/home/alice/c/d/lab", Home: "/home/alice", Harness: "codex"})
	require.NotNil(t, lab, "upgrade refused: %d", resp.Status)
	lab.Welcome(wsWait)
	lab.Close()
	f.atlasConn, resp = f.alice.Token(dto.TokenKindAgent, "devbox").DialFolder(testkit.Folder{Machine: "devbox", Path: "/home/alice/atlas", Home: "/home/alice", Harness: "claude"})
	require.NotNil(t, f.atlasConn, "upgrade refused: %d", resp.Status)
	f.atlasConn.Welcome(wsWait)
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(f.lab) }, wsWait, 20*time.Millisecond)
	return f
}

func contactsOf(t *testing.T, c *testkit.Client) dto.Contacts {
	t.Helper()
	return testkit.Decode[dto.Contacts](c.Get("/api/v1/contacts").RequireStatus(http.StatusOK))
}

func names(refs []dto.IntelligenceRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.DisplayName
	}
	return out
}

func aiNames(list []dto.AIContact) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.Intelligence.DisplayName
	}
	return out
}

// shape renders a tree as "Path › Name" lines indented by depth.
func shape(nodes []dto.TreeNode, depth int) []string {
	var out []string
	for _, n := range nodes {
		label := n.Name
		if n.Path != "" {
			label = n.Path + " › " + n.Name
		}
		out = append(out, strings.Repeat("  ", depth)+label+" = "+n.AI.Intelligence.DisplayName)
		out = append(out, shape(n.Children, depth+1)...)
	}
	return out
}

// rawContacts is the body of GET /contacts as the generic JSON a client reads, to say which keys it has.
func rawContacts(t *testing.T, c *testkit.Client) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(c.Get("/api/v1/contacts").RequireStatus(http.StatusOK).Body, &out))
	return out
}

// orgGroup returns the group of the organization with that name, failing the test without one.
func orgGroup(t *testing.T, c dto.Contacts, org string) dto.OrgAIs {
	t.Helper()
	for _, g := range c.OrgAIs {
		if g.Org.Name == org {
			return g
		}
	}
	require.FailNow(t, "no group for the organization", org)
	return dto.OrgAIs{}
}

func orgNames(c dto.Contacts) []string {
	out := make([]string, len(c.OrgAIs))
	for i, g := range c.OrgAIs {
		out[i] = g.Org.Name
	}
	return out
}

func aiIDs(list []dto.AIContact) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.Intelligence.ID
	}
	return out
}

// CONT-01, OMEM-03, ORG-02 (AD-016, AD-022): the contacts of a user are the AIs of each organization they
// belong to, by organization name, with the other users and the people of their connections. An AI that
// belongs to two of the user's organizations is listed under both, with its home; the user's own AIs name no
// host and the others name theirs; every AI carries its state, app and folder, and the response has no machine
// anywhere.
func TestContactsListTheAIsOfTheCallersOrganizationsByOrganization(t *testing.T) {
	env := testkit.New(t)
	acme, globex := env.OrgRoles("acme"), env.OrgRoles("globex")
	scout := env.NewAIIn(acme.Peer, acme.Org.ID, "scout")                        // acme's second AI, hosted by Peer
	rover := env.NewAIIn(globex.Manager, globex.Org.ID, "rover")                 // globex's second AI
	env.AddMember(globex.Org.ID, acme.Member.User.ID, orgs.PermissionMember, "") // Member works in both
	env.AddMember(globex.Org.ID, scout, orgs.PermissionMember, "")               // and scout works in both too

	member := contactsOf(t, acme.Member)
	require.Equal(t, []string{"acme", "acme-member", "globex"}, orgNames(member),
		"every organization of the user, by name: their personal one too, empty")
	inAcme := orgGroup(t, member, "acme")
	require.ElementsMatch(t, []string{acme.TargetAI, scout}, aiIDs(inAcme.AIs))
	require.Equal(t, acme.Org.ID, inAcme.Org.ID)
	require.Equal(t, "acme", inAcme.Org.Slug)
	require.Empty(t, orgGroup(t, member, "acme-member").AIs, "a group is listed empty")
	require.NotNil(t, orgGroup(t, member, "acme-member").AIs, "an empty list, not null")
	inGlobex := orgGroup(t, member, "globex")
	require.ElementsMatch(t, []string{globex.TargetAI, rover, scout}, aiIDs(inGlobex.AIs), "scout is listed under both")
	require.True(t, slices.IsSortedFunc(inAcme.AIs, func(a, b dto.AIContact) int {
		return strings.Compare(strings.ToLower(a.Intelligence.DisplayName), strings.ToLower(b.Intelligence.DisplayName))
	}), "by name")
	for _, ai := range append(inAcme.AIs, inGlobex.AIs...) {
		require.Equal(t, dto.IntelligenceRoleAI, ai.Intelligence.Role)
		if ai.Intelligence.ID == scout {
			require.Equal(t, acme.Org.ID, ai.OrgID, "its home is acme whichever group lists it")
		}
	}

	// A host is named when it is someone else, never for the caller's own AIs.
	peer := contactsOf(t, acme.Peer)
	var theirs, mine dto.AIContact
	for _, ai := range orgGroup(t, peer, "acme").AIs {
		switch ai.Intelligence.ID {
		case acme.TargetAI:
			theirs = ai
		case scout:
			mine = ai
		}
	}
	require.NotNil(t, theirs.Owner)
	require.Equal(t, acme.Manager.User.ID, theirs.Owner.ID, "Manager hosts the AI")
	require.Nil(t, mine.Owner, "your own AI names no host")

	// Users and the groups of connections come as before: everybody else, and the connections of my organizations.
	require.Contains(t, names(member.Users), acme.Admin.User.Username)
	require.NotContains(t, names(member.Users), acme.Member.User.Username)
	require.NotNil(t, member.People, "an empty list, not null")

	// Presence and the app show: Manager's session connects the AI, and any member sees it online.
	conn, resp := acme.Manager.Token(dto.TokenKindAgent, "laptop").DialAgent("acme-ai")
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	conn.Welcome(wsWait)
	online := func() bool {
		for _, ai := range orgGroup(t, contactsOf(t, acme.Member), "acme").AIs {
			if ai.Intelligence.ID == acme.TargetAI {
				return ai.Online
			}
		}
		return false
	}
	require.True(t, online())
	conn.Close()
	require.Eventually(t, func() bool { return !online() }, wsWait, 20*time.Millisecond)
}

// OMEM-03 (Done when): contacts list another user's AI after the user joins its organization and not before;
// leaving the organization takes the AI away again, but an AI the user hosts stays in their list, under "hosted".
func TestContactsFollowTheOrganizationsOfTheCaller(t *testing.T) {
	env := testkit.New(t)
	acme := env.OrgRoles("acme")
	listed := func(c *testkit.Client, id string) (inGroup, inHosted bool) {
		t.Helper()
		got := contactsOf(t, c)
		for _, g := range got.OrgAIs {
			if slices.Contains(aiIDs(g.AIs), id) {
				inGroup = true
			}
		}
		return inGroup, slices.Contains(aiIDs(got.Hosted), id)
	}

	inGroup, inHosted := listed(acme.Outsider, acme.TargetAI)
	require.False(t, inGroup || inHosted, "not before joining its organization")
	env.AddMember(acme.Org.ID, acme.Outsider.User.ID, orgs.PermissionMember, "")
	inGroup, inHosted = listed(acme.Outsider, acme.TargetAI)
	require.True(t, inGroup, "after joining it")
	require.False(t, inHosted, "somebody else's AI is not hosted by the outsider")
	require.NoError(t, env.App.Orgs.Leave(context.Background(), acme.Outsider.User.ID, acme.Org.ID))
	inGroup, inHosted = listed(acme.Outsider, acme.TargetAI)
	require.False(t, inGroup || inHosted, "and not after leaving it")

	// The AI Manager hosts is listed under acme while Manager belongs to it, and under "hosted" once Manager left:
	// the AIs a user runs do not vanish from their contacts.
	inGroup, inHosted = listed(acme.Manager, acme.TargetAI)
	require.True(t, inGroup)
	require.False(t, inHosted, "no group lists it twice")
	require.NoError(t, env.App.Orgs.Leave(context.Background(), acme.Manager.User.ID, acme.Org.ID))
	inGroup, inHosted = listed(acme.Manager, acme.TargetAI)
	require.False(t, inGroup, "Manager left its organization")
	require.True(t, inHosted, "but still hosts it")
	left := contactsOf(t, acme.Manager).Hosted[0]
	require.Equal(t, acme.TargetAI, left.Intelligence.ID)
	require.Nil(t, left.Owner, "hosted by the caller")
	require.Equal(t, acme.Org.ID, left.OrgID, "its home organization is the one the caller left")
}

// AD-022: the contacts show no machine: not as a grouping, not as a hostname or a label on an AI, and the
// pieces v0.5 had for them (machines, unlinked, shared) are gone from the response.
func TestContactsShowNoMachines(t *testing.T) {
	f := newContactsFixture(t)
	raw := rawContacts(t, f.alice)
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	require.ElementsMatch(t, []string{"me", "org_ais", "hosted", "users", "people"}, keys)
	body := string(f.alice.Get("/api/v1/contacts").RequireStatus(http.StatusOK).Body)
	for _, hostname := range []string{"devbox", "notebook", "bobs-pc", "machine"} {
		require.NotContains(t, body, hostname, "no hostname, label or machine key anywhere")
	}
	// The folders still read relative to the host's home folder.
	var labels []string
	for _, g := range contactsOf(t, f.alice).OrgAIs {
		for _, ai := range g.AIs {
			labels = append(labels, ai.FolderLabel)
		}
	}
	require.Contains(t, labels, "~/atlas")
	require.Contains(t, labels, "~/atlas/notes")
}

// CONT-01 (authorization): each user lists what their organizations hold and what they host; what belongs to
// other organizations is absent, and a connection is a people group for the members of its organization only.
func TestContactsOfOtherUsersHoldOnlyTheirOrganizationsData(t *testing.T) {
	f := newContactsFixture(t)
	f.bob.Put("/api/v1/agents/"+f.secret+"/sharing", dto.Sharing{Mode: dto.VisibilityPublic, PublicLevel: dto.LevelUse}).RequireStatus(http.StatusOK) // a grant gives nothing
	carolConn := createFakeIdle(f.carol, "carol-wa", true)

	// bob belongs to "line" (alice's organization with the connection "wa") and to his own: his two AIs are
	// in the second, Atlas in the first; his private one is no secret from his own organization.
	bob := contactsOf(t, f.bob)
	require.ElementsMatch(t, []string{"bob", "line"}, orgNames(bob))
	require.ElementsMatch(t, []string{f.bobby, f.secret}, aiIDs(orgGroup(t, bob, "bob").AIs), "bob's own AIs")
	require.Equal(t, []string{f.atlas}, aiIDs(orgGroup(t, bob, "line").AIs), "the AI of the organization that holds the connection")
	require.Empty(t, bob.Hosted)
	require.Equal(t, []dto.PeopleGroup{{Connector: dto.ConnectorRef{ID: f.conn.ID, Kind: connectortest.KindName, Name: "wa"}, Count: 1}}, bob.People,
		"bob belongs to the organization of alice's connection")

	carol := contactsOf(t, f.carol)
	require.Equal(t, []string{"carol"}, orgNames(carol))
	require.Equal(t, []string{f.public}, aiIDs(orgGroup(t, carol, "carol").AIs))
	require.NotContains(t, aiIDs(orgGroup(t, carol, "carol").AIs), f.secret, "bob's public grant gives carol nothing")
	require.Equal(t, []dto.PeopleGroup{{Connector: dto.ConnectorRef{ID: carolConn.ID, Kind: connectortest.KindName, Name: "carol-wa"}, Count: 0}}, carol.People,
		"her connection has seen nobody; alice's is not hers")
	require.NotContains(t, names(carol.Users), "carol")

	dave := contactsOf(t, f.dave)
	require.NotNil(t, dave.Hosted, "empty lists, not null")
	require.Empty(t, dave.Hosted)
	require.NotNil(t, dave.People)
	require.Empty(t, dave.People)
	require.Empty(t, orgGroup(t, dave, "dave").AIs, "dave's own organization holds no AI")

	f.env.Anon().Get("/api/v1/contacts").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	f.alice.Token(dto.TokenKindAgent, "laptop").Get("/api/v1/contacts").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.alice.Token(dto.TokenKindAgent, "laptop").Get("/api/v1/contacts/people?connector_id="+f.conn.ID).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
}

func hostnames(ms []dto.MachineTree) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Hostname
	}
	return out
}

// Disabled users are not contacts.
func TestContactsLeaveOutDisabledUsers(t *testing.T) {
	f := newContactsFixture(t)
	f.admin.Patch("/api/v1/admin/users/"+f.erin.User.ID, dto.UpdateUserRequest{Disabled: new(true)}).RequireStatus(http.StatusOK)
	require.NotContains(t, names(contactsOf(t, f.alice).Users), "erin")
}

// The AIs a user hosts that none of their organizations lists come by name, case-insensitively.
func TestContactsListHostedAIsByName(t *testing.T) {
	f := newContactsFixture(t)
	// A v0.3 identity belongs to no organization until the upgrade places it (OUPG-01): it is not listed by one.
	for _, name := range []string{"zeta-helper", "alpha-helper", "Beta-helper"} {
		f.env.NewLegacyAI(f.alice, name)
	}
	require.Equal(t, []string{"alpha-helper", "Beta-helper", "old-helper", "zeta-helper"}, aiNames(contactsOf(t, f.alice).Hosted))
}

// TREE-04: the owner labels a machine (1 to 64 characters; empty resets it), the label is the owner's own, and
// its refusals are observable at their status (L-017). The label is shown nowhere any more (AD-022): the
// contacts and the profiles carry no machine, and T57 retires the route; what is left to observe is the answer
// and the stored label.
func TestMachineLabel(t *testing.T) {
	f := newContactsFixture(t)
	put := func(c *testkit.Client, host, label string) *testkit.Response {
		return c.Put("/api/v1/machines/"+url.PathEscape(host), dto.SetMachineLabelRequest{Label: label})
	}
	stored := func(owner *testkit.Client, host string) string {
		t.Helper()
		m, err := f.env.App.Store.R().GetMachine(context.Background(), db.GetMachineParams{OwnerID: owner.User.ID, Hostname: host})
		require.NoError(t, err)
		return m.Label
	}

	got := testkit.Decode[dto.Machine](put(f.alice, "devbox", "  Alice's desk  ").RequireStatus(http.StatusOK))
	require.Equal(t, dto.Machine{Hostname: "devbox", Label: "Alice's desk"}, got, "the label is trimmed")
	require.Equal(t, "Alice's desk", stored(f.alice, "devbox"))

	longest := strings.Repeat("é", 64) // 64 code points, 128 bytes
	require.Equal(t, longest, testkit.Decode[dto.Machine](put(f.alice, "devbox", longest).RequireStatus(http.StatusOK)).Label)
	err := put(f.alice, "devbox", longest+"x").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, err.Fields, "label")
	require.Equal(t, longest, stored(f.alice, "devbox"), "a refused label changes nothing")

	require.Equal(t, dto.Machine{Hostname: "devbox"}, testkit.Decode[dto.Machine](put(f.alice, "devbox", "").RequireStatus(http.StatusOK)), "empty resets")
	require.Empty(t, stored(f.alice, "devbox"))
	put(f.alice, "devbox", "Desk").RequireStatus(http.StatusOK)
	put(f.alice, "devbox", " \t ").RequireStatus(http.StatusOK)
	require.Empty(t, stored(f.alice, "devbox"), "a blank label resets too")

	// Another owner's machine and an unknown one are 404; bob's machine of the same name would be his own.
	put(f.bob, "devbox", "Mine now").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	put(f.alice, "no-such-machine", "x").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	put(f.alice, "bobs-pc", "x").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Empty(t, stored(f.alice, "devbox"))
	f.env.Anon().Put("/api/v1/machines/devbox", dto.SetMachineLabelRequest{Label: "x"}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	f.alice.Token(dto.TokenKindAgent, "laptop").Put("/api/v1/machines/devbox", dto.SetMachineLabelRequest{Label: "x"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.alice.Do(http.MethodPut, "/api/v1/machines/devbox", "not an object").RequireStatus(http.StatusBadRequest)

	// Two owners may have a machine of the same hostname: each labels their own.
	aiAt(t, f.env, f.bob, "Twin", "twin", "devbox", "/home/bob/twin", "/home/bob", "claude")
	put(f.bob, "devbox", "Bob's devbox").RequireStatus(http.StatusOK)
	require.Empty(t, stored(f.alice, "devbox"))
	require.Equal(t, "Bob's devbox", stored(f.bob, "devbox"))
}

// TREE-07 (data): the agent route returns the token owner's own AIs as machines and unlinked
// ones, with no folder headers and no AI needed; sessions and user tokens are refused.
func TestAgentContactsTree(t *testing.T) {
	f := newContactsFixture(t)
	f.alice.Put("/api/v1/machines/notebook", dto.SetMachineLabelRequest{Label: "Work laptop"}).RequireStatus(http.StatusOK)
	tok := f.alice.Token(dto.TokenKindAgent, "laptop")

	tree := testkit.Decode[dto.AgentContactsTree](tok.Get("/api/v1/agent/contacts/tree").RequireStatus(http.StatusOK))
	require.Equal(t, []string{"devbox", "notebook"}, hostnames(tree.Machines))
	require.Equal(t, "Work laptop", tree.Machines[1].Label)
	require.Equal(t, []string{"atlas = Atlas", "  notes = Notes", "c/d › lab = Lab"}, shape(tree.Machines[0].Nodes, 0))
	require.True(t, tree.Machines[0].Nodes[0].AI.Online)
	require.Equal(t, []string{"old-helper"}, aiNames(tree.Unlinked))

	bobTree := testkit.Decode[dto.AgentContactsTree](f.bob.Token(dto.TokenKindAgent, "desktop").Get("/api/v1/agent/contacts/tree").RequireStatus(http.StatusOK))
	require.Equal(t, []string{"bobs-pc"}, hostnames(bobTree.Machines), "the owner's own AIs only: not alice's, not shared ones")
	empty := testkit.Decode[dto.AgentContactsTree](f.dave.Token(dto.TokenKindAgent, "laptop").Get("/api/v1/agent/contacts/tree").RequireStatus(http.StatusOK))
	require.NotNil(t, empty.Machines)
	require.NotNil(t, empty.Unlinked)
	require.Empty(t, empty.Machines)

	f.alice.Get("/api/v1/agent/contacts/tree").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.env.Anon().Get("/api/v1/agent/contacts/tree").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
}

// writeAs makes one new sender per name write in chat X, a second apart, with account ids from
// 5511997<base>.
func (f *peopleFixture) writeAs(base int, names ...string) {
	for i, name := range names {
		f.clock.Advance(time.Second)
		f.emit(peopleGroupX, fmt.Sprintf("w%d-%d", base, i), fmt.Sprintf("5511997%06d@s.whatsapp.net", base+i), name)
	}
}

func (f *peopleFixture) peoplePage(c *testkit.Client, query string) *testkit.Response {
	f.env.T.Helper()
	return c.Get("/api/v1/contacts/people?connector_id=" + f.conn.ID + query)
}

func (f *peopleFixture) people(c *testkit.Client, query string) dto.Page[dto.IntelligenceRef] {
	f.env.T.Helper()
	return testkit.Decode[dto.Page[dto.IntelligenceRef]](f.peoplePage(c, query).RequireStatus(http.StatusOK))
}

func ids(refs []dto.IntelligenceRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	return out
}

// CONT-01 (people): the people of one connector in name order, case-insensitively, paged by the
// id of the last item (no gaps or repeats even across equal names) and searchable by name, handle
// or account id.
func TestContactsPeoplePagesAndSearches(t *testing.T) {
	f := newPeopleFixture(t)
	f.writeAs(0, "dana", "Eli", "Fay", "Gus", "Ida Lopes", "Sam", "Sam")

	all := f.people(f.alice, "")
	require.Equal(t, []string{"Customer", "dana", "Eli", "Fay", "Gus", "Ida Lopes", "Sam", "Sam"}, names(all.Items))
	require.False(t, all.HasMore)
	for _, p := range all.Items {
		require.Equal(t, []any{dto.IntelligenceRolePerson, dto.IntelligenceKindHuman, connectortest.KindName}, []any{p.Role, p.Kind, p.Platform})
		require.NotEmpty(t, p.Handle)
	}
	for _, size := range []int{1, 2, 3, 7, 8} {
		var walked []string
		query := fmt.Sprintf("&limit=%d", size)
		for pages := 0; ; pages++ {
			require.Less(t, pages, 20, "paging must end")
			pg := f.people(f.alice, query)
			require.LessOrEqual(t, len(pg.Items), size)
			walked = append(walked, ids(pg.Items)...)
			if !pg.HasMore {
				break
			}
			query = fmt.Sprintf("&limit=%d&after=%s", size, pg.Items[len(pg.Items)-1].ID)
		}
		require.Equal(t, ids(all.Items), walked, "pages of %d", size)
	}
	require.False(t, f.people(f.alice, "&limit=8").HasMore, "a page that holds exactly the rest has no next page")
	require.True(t, f.people(f.alice, "&limit=7").HasMore)

	// Search: case-insensitive over the name, the handle and the account id.
	require.Equal(t, []string{"Customer"}, names(f.people(f.alice, "&q=CUST").Items))
	require.Equal(t, []string{"Ida Lopes"}, names(f.people(f.alice, "&q=lopes").Items))
	require.Equal(t, []string{"Gus"}, names(f.people(f.alice, "&q=5511997000003").Items), "by account id")
	plantProfile(t, f.env, all.Items[3].ID, "Zed", "orange-juice") // chosen before a person's profile became history (CID-21)
	require.Equal(t, []string{"Zed"}, names(f.people(f.alice, "&q=Orange").Items), "by handle")
	require.Equal(t, []string{"Customer", "dana", "Eli", "Gus", "Ida Lopes", "Sam", "Sam", "Zed"}, names(f.people(f.alice, "").Items), "Zed sorts last")
	require.Empty(t, f.people(f.alice, "&q=nobody").Items)
	require.NotNil(t, f.people(f.alice, "&q=nobody").Items, "an empty page, not null")
	require.Len(t, f.people(f.alice, "&q=%20%20").Items, 8, "a blank search matches everyone")
	require.Equal(t, []string{"Sam", "Sam"}, names(f.people(f.alice, "&q=sam").Items))
	require.Equal(t, []string{"Sam"}, names(f.people(f.alice, "&q=sam&limit=1").Items))
	require.Empty(t, f.people(f.alice, "&q=%25").Items, "% is a character, not a wildcard")
	require.Empty(t, f.people(f.alice, "&q=_").Items, "so is _")
	require.Equal(t, []string{"Sam"}, names(f.people(f.alice, "&q=sam&limit=1&after="+f.people(f.alice, "&q=sam&limit=1").Items[0].ID).Items), "paging inside a search")
}

// CONT-01 (people): the default page is 50 people and the largest 200; the connector's users
// (owner and use grantee) may read the list, nobody else.
func TestContactsPeoplePageSizeAndAuthorization(t *testing.T) {
	f := newPeopleFixture(t)
	senders := make([]string, 60)
	for i := range senders {
		senders[i] = fmt.Sprintf("P%03d", i)
	}
	f.writeAs(0, senders...)

	require.Len(t, f.people(f.alice, "").Items, 50, "the default page")
	require.True(t, f.people(f.alice, "").HasMore)
	require.Len(t, f.people(f.alice, "&limit=61").Items, 61)
	require.False(t, f.people(f.alice, "&limit=61").HasMore)
	require.True(t, f.people(f.alice, "&limit=60").HasMore)
	require.Len(t, f.people(f.alice, "&limit=200").Items, 61)
	require.Len(t, f.people(f.bob, "&limit=5").Items, 5, "a use grantee reads the list")

	for _, c := range []*testkit.Client{f.carol, f.dave, f.admin} {
		f.peoplePage(c, "").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	f.env.Anon().Get("/api/v1/contacts/people?connector_id="+f.conn.ID).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	f.alice.Get("/api/v1/contacts/people?connector_id=no-such-connector").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// CONT-01 (people, L-017): every refused request has its status, and each is refused for its own
// field.
func TestContactsPeopleRefusals(t *testing.T) {
	f := newPeopleFixture(t)
	f.writeAs(0, "Dana")
	person := f.people(f.alice, "").Items[0].ID
	const cid = "connector_id="
	for _, c := range []struct {
		name, query, field string
	}{
		{"no connector", "", "connector_id"},
		{"limit 0", cid + "&limit=0", "limit"},
		{"limit 201", cid + "&limit=201", "limit"},
		{"negative limit", cid + "&limit=-1", "limit"},
		{"limit not a number", cid + "&limit=many", "limit"},
		{"search of 101 characters", cid + "&q=" + strings.Repeat("é", 101), "q"},
		{"after an unknown id", cid + "&after=no-such-person", "after"},
		{"after a user", cid + "&after=" + f.alice.User.ID, "after"},
	} {
		query := strings.Replace(c.query, cid, cid+f.conn.ID, 1)
		if c.field == "connector_id" {
			query = ""
		}
		e := f.alice.Get("/api/v1/contacts/people?"+query).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, c.field, c.name)
	}
	require.Len(t, f.people(f.alice, "&limit=200&q="+strings.Repeat("é", 100)+"&after="+person).Items, 0, "the largest limit and search are accepted")

	// A person of another connector is no cursor here.
	other := createFakeIdle(f.alice, "second", true)
	f.alice.Get("/api/v1/contacts/people?connector_id="+other.ID+"&after="+person).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
}

// PEOP-02 (listing): a person whose account an enabled user verified is that user's app, so it is
// not listed or counted as a person; unlink it, or disable the user, and it is a person again.
func TestContactsPeopleLeaveOutLinkedAccounts(t *testing.T) {
	f := newPeopleFixture(t)
	f.writeAs(0, "Dana")
	count := func() int { return contactsOf(t, f.alice).People[0].Count }
	require.Equal(t, 2, count())
	require.Equal(t, []string{"Customer", "Dana"}, names(f.people(f.alice, "").Items))

	code := testkit.Decode[dto.PairingCode](f.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: f.conn.ID}).RequireStatus(http.StatusCreated))
	f.clock.Advance(time.Second)
	f.inst.Emit(context.Background(), connectors.Inbound{ChatID: peopleGroupX, ChatKind: "group", MessageID: "link", SenderID: peopleSender, SenderName: "Customer", Text: "!link " + code.Code, At: f.clock.Now()})
	require.Equal(t, 1, count())
	require.Equal(t, []string{"Dana"}, names(f.people(f.alice, "").Items))
	require.Empty(t, f.people(f.alice, "&q=Customer").Items)
	require.Equal(t, 1, contactsOf(t, f.bob).People[0].Count, "for every user of the connector")

	// A second account of the same user, and one linked by bob, are apps too.
	bobCode := testkit.Decode[dto.PairingCode](f.bob.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: f.conn.ID}).RequireStatus(http.StatusCreated))
	f.writeAs(100, "Bob phone")
	f.inst.Emit(context.Background(), connectors.Inbound{ChatID: peopleGroupX, ChatKind: "group", MessageID: "link-bob", SenderID: "5511997000100@s.whatsapp.net", SenderName: "Bob phone", Text: "!link " + bobCode.Code, At: f.clock.Now()})
	require.Equal(t, []string{"Dana"}, names(f.people(f.alice, "").Items))

	// Disabling bob frees his account; deleting alice's identity frees hers.
	f.admin.Patch("/api/v1/admin/users/"+f.bob.User.ID, dto.UpdateUserRequest{Disabled: new(true)}).RequireStatus(http.StatusOK)
	require.Equal(t, []string{"Bob phone", "Dana"}, names(f.people(f.alice, "").Items), "the account of a disabled user is a person")
	idents := testkit.Decode[dto.List[dto.Identity]](f.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
	require.Len(t, idents, 1)
	f.alice.Delete("/api/v1/identities/" + idents[0].ID).RequireStatus(http.StatusNoContent)
	require.Equal(t, []string{"Bob phone", "Customer", "Dana"}, names(f.people(f.alice, "").Items))
	require.Equal(t, 3, count())
}
