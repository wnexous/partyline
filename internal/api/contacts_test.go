package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
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

// CONT-01, TREE-01, TREE-03, TREE-06: the contacts of a user are their own AIs as a tree per
// machine (an online machine first), the AIs of others shared with them, the other users and the
// people of the connectors they may use; every AI row carries its online state, app and last seen.
func TestContactsListWhatTheCallerCanReach(t *testing.T) {
	f := newContactsFixture(t)
	c := contactsOf(t, f.alice)

	require.Equal(t, f.alice.User.ID, c.Me.ID)
	require.Equal(t, []any{dto.IntelligenceRoleUser, "alice"}, []any{c.Me.Role, c.Me.Handle})

	require.Len(t, c.Machines, 2)
	devbox, notebook := c.Machines[0], c.Machines[1]
	require.Equal(t, []any{"devbox", "devbox", true}, []any{devbox.Hostname, devbox.Label, devbox.Online}, "the machine with an online AI comes first")
	require.Equal(t, []any{"notebook", "notebook", false}, []any{notebook.Hostname, notebook.Label, notebook.Online})
	require.Equal(t, []string{"atlas = Atlas", "  notes = Notes", "c/d › lab = Lab"}, shape(devbox.Nodes, 0), "TREE-01, TREE-02: nesting and collapsed chains")
	require.Equal(t, []string{"~ = Dev"}, shape(notebook.Nodes, 0), "the home folder is ~")

	atlas := devbox.Nodes[0].AI
	require.Equal(t, []any{f.atlas, dto.IntelligenceRoleAI, "atlas", "devbox", "/home/alice/atlas", "~/atlas", "claude", true},
		[]any{atlas.Intelligence.ID, atlas.Intelligence.Role, atlas.Intelligence.Handle, atlas.Machine, atlas.Folder, atlas.FolderLabel, atlas.App, atlas.Online})
	require.Nil(t, atlas.Owner, "your own AI names no owner")
	require.Equal(t, dto.IntelligenceKindAI, atlas.Intelligence.Kind)
	notes := devbox.Nodes[0].Children[0].AI
	require.Equal(t, []any{f.notes, "opencode", false, "~/atlas/notes"}, []any{notes.Intelligence.ID, notes.App, notes.Online, notes.FolderLabel})
	require.Nil(t, notes.LastSeenAt, "never connected: no last seen")
	lab := devbox.Nodes[1].AI
	require.Equal(t, []any{f.lab, "codex", false, "~/c/d/lab"}, []any{lab.Intelligence.ID, lab.App, lab.Online, lab.FolderLabel})
	require.NotNil(t, lab.LastSeenAt, "it was online, so it was seen")
	require.WithinDuration(t, f.clock.Now(), *lab.LastSeenAt, time.Minute, "when it went offline, by the app clock")

	require.Equal(t, []string{"old-helper"}, aiNames(c.Unlinked), "TREE-06: the identity without a folder")
	require.Equal(t, []any{f.old, "", ""}, []any{c.Unlinked[0].Intelligence.ID, c.Unlinked[0].Folder, c.Unlinked[0].FolderLabel})

	require.Equal(t, []string{"Bobby", "Public"}, aiNames(c.Shared), "bob's shared AI and carol's public one, not bob's Secret")
	require.Equal(t, []any{f.bobby, f.bob.User.ID, "bob", "bobs-pc", "~/bobby"}, []any{c.Shared[0].Intelligence.ID, c.Shared[0].Owner.ID, c.Shared[0].Owner.Handle, c.Shared[0].Machine, c.Shared[0].FolderLabel},
		"a shared AI names its owner and shows its folder as the owner's machine does")
	require.Equal(t, "/srv/public", c.Shared[1].FolderLabel, "outside the owner's home")

	require.Equal(t, []string{"Admin", "bob", "carol", "dave", "erin"}, names(c.Users), "the other enabled users, by name")
	for _, u := range c.Users {
		require.Equal(t, dto.IntelligenceRoleUser, u.Role)
		require.NotEqual(t, f.alice.User.ID, u.ID)
	}

	require.Equal(t, []dto.PeopleGroup{{Connector: dto.ConnectorRef{ID: f.conn.ID, Kind: connectortest.KindName, Name: "wa"}, Count: 1}}, c.People)
}

// CONT-01 (authorization): each user lists their own data; what is not theirs or shared with them
// is absent, and a connector is a people group for those who own or may use it.
func TestContactsOfOtherUsersHoldOnlyTheirOwnData(t *testing.T) {
	f := newContactsFixture(t)
	carolConn := createFakeIdle(f.carol, "carol-wa", true)

	bob := contactsOf(t, f.bob)
	require.Equal(t, []string{"bobs-pc"}, hostnames(bob.Machines))
	require.Equal(t, []string{"~/bobby", "~/secret"}, folderLabels(bob.Machines[0].Nodes), "bob's own AIs, shared or not")
	require.Equal(t, []string{"Public"}, aiNames(bob.Shared), "bob sees carol's public AI; his own AIs are not 'shared'")
	require.Empty(t, bob.Unlinked)
	require.Equal(t, []dto.PeopleGroup{{Connector: dto.ConnectorRef{ID: f.conn.ID, Kind: connectortest.KindName, Name: "wa"}, Count: 1}}, bob.People,
		"bob may use alice's connector")

	carol := contactsOf(t, f.carol)
	require.Equal(t, []string{"carols-pc"}, hostnames(carol.Machines))
	require.Empty(t, carol.Shared, "nobody shared an AI with carol; her own is hers")
	require.Equal(t, []dto.PeopleGroup{{Connector: dto.ConnectorRef{ID: carolConn.ID, Kind: connectortest.KindName, Name: "carol-wa"}, Count: 0}}, carol.People,
		"her connector has seen nobody; alice's is not hers to use")
	require.NotContains(t, names(carol.Users), "carol")

	dave := contactsOf(t, f.dave)
	require.NotNil(t, dave.Machines, "empty lists, not null")
	require.Empty(t, dave.Machines)
	require.NotNil(t, dave.Unlinked)
	require.Equal(t, []string{"Public"}, aiNames(dave.Shared))
	require.NotNil(t, dave.People)
	require.Empty(t, dave.People)

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

func folderLabels(nodes []dto.TreeNode) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.AI.FolderLabel)
		out = append(out, folderLabels(n.Children)...)
	}
	return out
}

// Disabled users are not contacts, and a machine whose AIs are all gone does not linger.
func TestContactsLeaveOutDisabledUsersAndEmptyMachines(t *testing.T) {
	f := newContactsFixture(t)
	f.admin.Patch("/api/v1/admin/users/"+f.erin.User.ID, dto.UpdateUserRequest{Disabled: new(true)}).RequireStatus(http.StatusOK)
	require.NotContains(t, names(contactsOf(t, f.alice).Users), "erin")
	f.alice.Delete("/api/v1/agents/" + f.dev).RequireStatus(http.StatusNoContent)
	require.Equal(t, []string{"devbox"}, hostnames(contactsOf(t, f.alice).Machines))
}

// TREE-06: the identities without a folder are listed the most recently seen first, the ones
// never seen after them by name.
func TestContactsOrderUnlinkedByLastSeen(t *testing.T) {
	f := newContactsFixture(t)
	seen := func(id string, ago time.Duration) {
		t.Helper()
		at := f.clock.Now().Add(-ago).UnixMilli()
		require.NoError(t, f.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
			return q.TouchAgent(context.Background(), db.TouchAgentParams{LastSeenAt: sql.NullInt64{Int64: at, Valid: true}, ID: id})
		}))
	}
	older := f.env.NewLegacyAI(f.alice, "alpha-helper")
	newer := f.env.NewLegacyAI(f.alice, "zeta-helper")
	f.env.NewLegacyAI(f.alice, "beta-helper")
	seen(older, 2*time.Hour)
	seen(newer, time.Hour)

	require.Equal(t, []string{"zeta-helper", "alpha-helper", "beta-helper", "old-helper"}, aiNames(contactsOf(t, f.alice).Unlinked))
}

// TREE-03: the machine order is online first, then by label (the owner's label, else the
// hostname), and the label shows in the tree.
func TestContactsOrderMachinesByStateAndLabel(t *testing.T) {
	f := newContactsFixture(t)
	f.alice.Put("/api/v1/machines/notebook", dto.SetMachineLabelRequest{Label: "Aardvark"}).RequireStatus(http.StatusOK)
	c := contactsOf(t, f.alice)
	require.Equal(t, []string{"devbox", "notebook"}, hostnames(c.Machines), "online first even though 'Aardvark' sorts before 'devbox'")
	require.Equal(t, "Aardvark", c.Machines[1].Label)

	f.atlasConn.Close()
	require.Eventually(t, func() bool { return !f.env.App.Presence.Online(f.atlas) }, wsWait, 20*time.Millisecond)
	c = contactsOf(t, f.alice)
	require.Equal(t, []string{"notebook", "devbox"}, hostnames(c.Machines), "nobody online: by label, case-insensitively")
	require.False(t, c.Machines[0].Online)
}

// TREE-04: the owner labels a machine (1 to 64 characters; empty resets it); the label replaces
// the hostname in the tree, is the owner's own, and its refusals are observable at their status
// (L-017). (An AI's profile does not show the machine any more, AD-022; T57 retires the route.)
func TestMachineLabel(t *testing.T) {
	f := newContactsFixture(t)
	put := func(c *testkit.Client, host, label string) *testkit.Response {
		return c.Put("/api/v1/machines/"+url.PathEscape(host), dto.SetMachineLabelRequest{Label: label})
	}

	got := testkit.Decode[dto.Machine](put(f.alice, "devbox", "  Alice's desk  ").RequireStatus(http.StatusOK))
	require.Equal(t, dto.Machine{Hostname: "devbox", Label: "Alice's desk"}, got, "the label is trimmed")
	c := contactsOf(t, f.alice)
	require.Equal(t, []any{"devbox", "Alice's desk"}, []any{c.Machines[0].Hostname, c.Machines[0].Label})

	longest := strings.Repeat("é", 64) // 64 code points, 128 bytes
	require.Equal(t, longest, testkit.Decode[dto.Machine](put(f.alice, "devbox", longest).RequireStatus(http.StatusOK)).Label)
	err := put(f.alice, "devbox", longest+"x").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, err.Fields, "label")
	require.Equal(t, longest, contactsOf(t, f.alice).Machines[0].Label, "a refused label changes nothing")

	require.Equal(t, dto.Machine{Hostname: "devbox"}, testkit.Decode[dto.Machine](put(f.alice, "devbox", "").RequireStatus(http.StatusOK)), "empty resets")
	require.Equal(t, "devbox", contactsOf(t, f.alice).Machines[0].Label)
	put(f.alice, "devbox", "Desk").RequireStatus(http.StatusOK)
	put(f.alice, "devbox", " \t ").RequireStatus(http.StatusOK)
	require.Equal(t, "devbox", contactsOf(t, f.alice).Machines[0].Label, "a blank label resets too")

	// Another owner's machine and an unknown one are 404; bob's machine of the same name would be his own.
	put(f.bob, "devbox", "Mine now").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	put(f.alice, "no-such-machine", "x").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	put(f.alice, "bobs-pc", "x").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, "devbox", contactsOf(t, f.alice).Machines[0].Label)
	f.env.Anon().Put("/api/v1/machines/devbox", dto.SetMachineLabelRequest{Label: "x"}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	f.alice.Token(dto.TokenKindAgent, "laptop").Put("/api/v1/machines/devbox", dto.SetMachineLabelRequest{Label: "x"}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.alice.Do(http.MethodPut, "/api/v1/machines/devbox", "not an object").RequireStatus(http.StatusBadRequest)

	// Two owners may have a machine of the same hostname: each labels their own.
	aiAt(t, f.env, f.bob, "Twin", "twin", "devbox", "/home/bob/twin", "/home/bob", "claude")
	put(f.bob, "devbox", "Bob's devbox").RequireStatus(http.StatusOK)
	require.Equal(t, "devbox", contactsOf(t, f.alice).Machines[0].Label)
	require.Equal(t, "Bob's devbox", contactsOf(t, f.bob).Machines[0].Label)
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
