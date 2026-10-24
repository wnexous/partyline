package channel

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// orgCast is the cast with a second organization around the AI: Acme, which alice administers and the AI "builder"
// (the session), the AI "helper" and bob have joined as members. Builder's home is still alice's own organization,
// where helper lives too; carol belongs to neither organization, and Globex is bob's, with nobody else in it.
type orgCast struct {
	*cast
	acme, globex orgs.Membership
}

func newOrgCast(t *testing.T) *orgCast {
	t.Helper()
	c := newCast(t)
	ctx := context.Background()
	acme, err := c.env.App.Orgs.Create(ctx, c.alice.User.ID, "Acme")
	require.NoError(t, err)
	globex, err := c.env.App.Orgs.Create(ctx, c.bob.User.ID, "Globex")
	require.NoError(t, err)
	for _, iid := range []string{c.agentID, c.helper, c.bob.User.ID} {
		c.env.AddMember(acme.ID, iid, orgs.PermissionMember, "")
	}
	return &orgCast{cast: c, acme: acme, globex: globex}
}

// roomOrg reads the organization of a group as alice sees it.
func (c *orgCast) roomOrg(roomID string) string {
	c.t.Helper()
	return testkit.Decode[dto.Room](c.alice.Get("/api/v1/rooms/" + roomID).RequireStatus(http.StatusOK)).OrgID
}

// ORG-03 (T38): create_group with organization creates the group in that organization - named by its address, in
// any case and with surrounding spaces ignored - and the result names it: its id, address and name. The AI is a
// member, and the group has the members it listed from that organization.
func TestCreateGroupInAChosenOrganization(t *testing.T) {
	c := newOrgCast(t)
	s := c.connected(c.options())

	for _, ref := range []string{"acme", "ACME", "  acme  "} {
		text, isErr := s.call("create_group", toolArgs("name", "Ops "+ref, "organization", ref, "members", []string{"@alice", "@bob", c.helper}))
		require.False(t, isErr, "%q: %s", ref, text)
		group := decodeText[dto.CreatedGroup](t, text)
		require.Equal(t, []any{c.acme.ID, "acme", "Acme"}, []any{group.OrgID, group.OrgSlug, group.OrgName}, "the result names the organization")
		require.Equal(t, c.acme.ID, c.roomOrg(group.Room), "and the group belongs to it")
		members := c.members(c.alice, group.Room)
		require.Len(t, members, 4, "the AI, alice, bob and helper")
		require.Equal(t, dto.MemberKindAgent, members[c.agentID].Kind, "the AI is a member")
	}
}

// ORG-03: without organization the group is created in the AI's home organization, which the result names; the
// members must belong to it. A blank organization is no organization.
func TestCreateGroupWithoutAnOrganizationIsInTheHomeOne(t *testing.T) {
	c := newOrgCast(t)
	s := c.connected(c.options())
	home := c.alice.PersonalOrg()

	for name, args := range map[string]map[string]any{
		"left out": toolArgs("name", "Ops", "members", []string{"@alice", c.helper}),
		"blank":    toolArgs("name", "Ops blank", "organization", "   ", "members", []string{"@alice", c.helper}),
		"empty":    toolArgs("name", "Ops empty", "organization", "", "members", []string{"@alice", c.helper}),
	} {
		text, isErr := s.call("create_group", args)
		require.False(t, isErr, "%s: %s", name, text)
		group := decodeText[dto.CreatedGroup](t, text)
		require.Equal(t, []any{home.ID, home.Slug, home.Name}, []any{group.OrgID, group.OrgSlug, group.OrgName}, name)
		require.Equal(t, home.ID, c.roomOrg(group.Room), name)
	}
}

// ORG-03: the members of a group belong to its organization, and naming an organization changes which. Bob, who
// is in Acme only, is refused for the home organization; scout, an AI of alice's that lives in the home
// organization only, is refused for Acme. Each is valid for the other organization, so the choice is what decides
// (L-018), and the tool names the reason, not_org_member, as the server gives it. Nothing is created.
func TestCreateGroupMembersMustBelongToTheChosenOrganization(t *testing.T) {
	c := newOrgCast(t)
	c.env.NewAI(c.alice, "scout")
	s := c.connected(c.options())
	rooms := len(c.listRooms(s))

	text, isErr := s.call("create_group", toolArgs("name", "Ops", "members", []string{"@alice", "@bob"}))
	require.True(t, isErr, text)
	require.Equal(t, "members_refused: some members cannot be added to the group; nothing was created (refused: @bob (not_org_member))", text, "bob is not in the home organization")

	text, isErr = s.call("create_group", toolArgs("name", "Ops", "organization", "acme", "members", []string{"@alice", "@scout"}))
	require.True(t, isErr, text)
	require.Equal(t, "members_refused: some members cannot be added to the group; nothing was created (refused: @scout (not_org_member))", text, "scout is not in Acme")
	require.Len(t, c.listRooms(s), rooms, "nothing was created")

	mustCall(t, s, "create_group", toolArgs("name", "Ops", "members", []string{"@alice", "@scout"})) // scout is fine at home
	mustCall(t, s, "create_group", toolArgs("name", "Ops 2", "organization", "acme", "members", []string{"@alice", "@bob"}))
}

// ORG-03, OMEM-04: an organization the AI is not a member of, one it is only a guest of and one that does not exist
// are refused alike, and the tool says it is a 403: "forbidden (403)", the organization the AI named, and what an AI
// needs there. The server tells nothing about organizations the caller is not in, so the words are the tool's. No
// group is created. Each is the only thing different (L-018): the same valid members for all.
func TestCreateGroupInAnOrganizationTheAIMayNotUseNamesTheRefusal(t *testing.T) {
	c := newOrgCast(t)
	c.env.AddGuest(c.globex.ID, c.agentID, c.bob.User.ID, "") // builder is only a guest of Globex
	other, err := c.env.App.Orgs.Create(context.Background(), c.carol.User.ID, "Initech")
	require.NoError(t, err)
	require.NotEmpty(t, other.ID)
	s := c.connected(c.options())
	rooms := len(c.listRooms(s))

	for name, org := range map[string]string{
		"an organization it is not in":        "initech",
		"an organization it is a guest of":    "globex",
		"an organization that does not exist": "no-such-organization",
		"an organization by its id":           other.ID,
		"an address with spaces around it":    "  initech  ",
	} {
		text, isErr := s.call("create_group", toolArgs("name", "Ops", "organization", org, "members", []string{"@alice", c.helper}))
		require.True(t, isErr, "%s: %s", name, text)
		require.Equal(t, `forbidden (403): you cannot create a group in "`+strings.TrimSpace(org)+`": it must be an organization you belong to as a member or an admin, not as a guest`, text, name)
	}
	require.Len(t, c.listRooms(s), rooms, "nothing was created")
	require.Equal(t, c.alice.PersonalOrg().ID, c.roomOrg(decodeText[dto.CreatedGroup](t, mustCall(t, s, "create_group",
		toolArgs("name", "Ops", "members", []string{"@alice", c.helper}))).Room), "and the AI can still create groups where it belongs")
}

// ORG-03, OMEM-04: an AI that is only a guest of its home organization cannot create a group there; without an
// organization the tool says so in terms of the home one.
func TestCreateGroupForAGuestAIInItsHomeOrganization(t *testing.T) {
	c := newOrgCast(t)
	ctx := context.Background()
	_, err := c.env.App.Orgs.SetPermission(ctx, c.alice.User.ID, c.alice.PersonalOrg().ID, c.agentID, orgs.PermissionGuest)
	require.NoError(t, err)
	s := c.connected(c.options())

	const home = "forbidden (403): you cannot create a group in your home organization: guests cannot create groups, and you must be a member or an admin there"
	for name, args := range map[string]map[string]any{
		"left out": toolArgs("name", "Ops", "members", []string{"@alice"}),
		"blank":    toolArgs("name", "Ops", "organization", "   ", "members", []string{"@alice"}),
	} {
		text, isErr := s.call("create_group", args)
		require.True(t, isErr, "%s: %s", name, text)
		require.Equal(t, home, text, name)
	}

	// naming an organization where it is a member works
	text, isErr := s.call("create_group", toolArgs("name", "Ops", "organization", "acme", "members", []string{"@alice"}))
	require.False(t, isErr, text)
}

// An organization that is not text is an argument error; nothing is sent.
func TestCreateGroupOrganizationMustBeText(t *testing.T) {
	c := newOrgCast(t)
	s := c.connected(c.options())
	text, isErr := s.call("create_group", toolArgs("name", "Ops", "organization", 7, "members", []string{"@alice"}))
	require.True(t, isErr, text)
	require.Contains(t, text, "invalid arguments")
	require.Empty(t, c.conversations(c.alice), "nothing was created")
}

func mustCall(t *testing.T, s *session, tool string, args map[string]any) string {
	t.Helper()
	text, isErr := s.call(tool, args)
	require.False(t, isErr, text)
	return text
}
