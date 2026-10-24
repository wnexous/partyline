package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// welcomeV05 is the welcome frame as a v0.5 CLI reads it: no organization, position or managers, and the
// owner as the one who runs the AI.
type welcomeV05 struct {
	Type         dto.AgentFrameType    `json:"type"`
	Agent        dto.AgentInfo         `json:"agent"`
	Intelligence dto.IntelligenceRef   `json:"intelligence"`
	Owner        dto.IntelligenceRef   `json:"owner"`
	Connections  []dto.AgentConnection `json:"connections"`
}

// welcomeOf starts the session of folder f with the token and returns its welcome frame, decoded and
// raw, then closes the session. Every call is the same process (one live id), so dialing an AI again is a
// reconnect and never identity_busy.
func welcomeOf(t *testing.T, tok *testkit.Client, f testkit.Folder) (dto.AgentWelcomeFrame, string) {
	t.Helper()
	if f.Live == "" {
		f.Live = "welcome-test-process"
	}
	conn, resp := tok.DialFolder(f)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	defer conn.Close()
	raw := conn.Next(wsWait)
	require.Equal(t, dto.AgentFrameTypeWelcome, raw.Type)
	var w dto.AgentWelcomeFrame
	require.NoError(t, json.Unmarshal(raw.Raw, &w))
	return w, string(raw.Raw)
}

func handlesOf(refs []dto.IntelligenceRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Handle
	}
	return out
}

// POS-10, ORG-04 (design, "Welcome frame and channel notice"): the welcome frame tells a session its home
// organization, the position it holds and the identities that manage it, nearest first. The Worktrees
// worker that the AI Lead started is managed by Lead, then by the Director above Lead, then by the Admin.
// A v0.5 client, which reads none of that, still finds the host user as the owner.
func TestWelcomeTellsTheOrganizationPositionAndManagers(t *testing.T) {
	w := newPlacementWorld(t)
	f := placementFolder("worker-1")
	f.Parent, f.Position = "@lead", "Worktrees"
	welcome, raw := welcomeOf(t, w.agentToken(w.alice), f)

	require.Equal(t, &dto.ContactOrg{ID: w.acme.ID, Name: "Acme", Slug: "acme"}, welcome.Organization)
	require.Equal(t, "Worktrees", welcome.Position)
	require.Equal(t, []string{"lead", "bob", "alice"}, handlesOf(welcome.Managers), "nearest first: Lead, the Director above Lead, the Admin")
	kinds := func(refs []dto.IntelligenceRef) []string {
		out := make([]string, len(refs))
		for i, r := range refs {
			out[i] = fmt.Sprintf("%s/%s", r.Kind, r.Role)
		}
		return out
	}
	require.Equal(t, []string{"ai/ai", "human/user", "human/user"}, kinds(welcome.Managers), "a manager is an AI or a person, as its role says")
	require.Equal(t, []string{w.lead, w.bob.User.ID, w.alice.User.ID}, []string{welcome.Managers[0].ID, welcome.Managers[1].ID, welcome.Managers[2].ID})

	t.Run("a v0.5 client still reads the host user as the owner", func(t *testing.T) {
		var old welcomeV05
		require.NoError(t, json.Unmarshal([]byte(raw), &old))
		require.Equal(t, dto.AgentFrameTypeWelcome, old.Type)
		require.Equal(t, welcome.Agent.ID, old.Agent.ID)
		require.Equal(t, "alice", old.Agent.Owner, "the host user's username")
		require.Equal(t, "alice", old.Owner.Handle, "owner is the host user, not Lead, which started the AI")
		require.Equal(t, w.alice.User.ID, old.Owner.ID)
		require.Equal(t, "worker-1", old.Intelligence.Handle)
		require.Empty(t, old.Connections)
		require.Contains(t, raw, `"owner":{`, "the key stays")
	})
}

// POS-10: an AI that nobody manages gets an empty list, never null; one that holds no position says so;
// and a creator with no position is its manager, followed by whoever manages the creator.
func TestWelcomeOfAnAIWithoutManagersOrWithoutAPosition(t *testing.T) {
	t.Run("the root position has nobody above it", func(t *testing.T) {
		w := newPlacementWorld(t)
		f := placementFolder("root-holder")
		f.Org, f.Position = "acme", "Admin" // alice is an admin of Acme, so she may place it anywhere
		welcome, raw := welcomeOf(t, w.agentToken(w.alice), f)
		require.Equal(t, "Admin", welcome.Position)
		require.NotNil(t, welcome.Managers, "present and empty")
		require.Empty(t, welcome.Managers)
		require.Contains(t, raw, `"managers":[]`)
		require.Equal(t, w.acme.ID, welcome.Organization.ID)
	})

	t.Run("no position: the creator manages it, then whoever manages the creator", func(t *testing.T) {
		w := newPlacementWorld(t)
		f := placementFolder("no-position")
		welcome, raw := welcomeOf(t, w.agentToken(w.bob), f)
		require.Equal(t, "Acme", welcome.Organization.Name, "bob's default organization")
		require.Equal(t, "", welcome.Position)
		require.Contains(t, raw, `"position":""`, "always present")
		require.Equal(t, []string{"bob", "alice"}, handlesOf(welcome.Managers), "bob created it; alice is the Admin above bob's Director")
	})
}

// POS-10: management never crosses organizations, so the managers are the ones of the AI's home
// organization only, even when the AI belongs to a second one where somebody else is above it.
func TestWelcomeManagersAreTheHomeOrganizationsOnly(t *testing.T) {
	w := newPlacementWorld(t)
	ctx := context.Background()
	f := placementFolder("worker-two-orgs")
	f.Parent, f.Position = "@lead", "Worktrees"
	first, _ := welcomeOf(t, w.agentToken(w.alice), f)
	require.Equal(t, []string{"lead", "bob", "alice"}, handlesOf(first.Managers))

	// In Globex carol is a boss above the worker. She is nobody in Acme.
	globex, err := w.env.App.Authz.Chart(ctx, w.globex.ID)
	require.NoError(t, err)
	boss := w.env.AddPosition(w.globex.ID, "Boss", globex.Members[w.alice.User.ID].PositionID)
	staff := w.env.AddPosition(w.globex.ID, "Staff", boss)
	w.env.AddMember(w.globex.ID, w.carol.User.ID, orgs.PermissionMember, boss)
	w.env.AddMember(w.globex.ID, first.Agent.ID, orgs.PermissionMember, staff)
	manages, err := w.env.App.Authz.Manages(ctx, w.carol.User.ID, first.Agent.ID, w.globex.ID)
	require.NoError(t, err)
	require.True(t, manages, "fixture sanity: carol manages the worker in Globex")

	second, _ := welcomeOf(t, w.agentToken(w.alice), f)
	require.Equal(t, w.acme.ID, second.Organization.ID, "the home organization stays Acme")
	require.Equal(t, "Worktrees", second.Position, "and the position is the one held there")
	require.Equal(t, []string{"lead", "bob", "alice"}, handlesOf(second.Managers), "carol manages it only in Globex")
}

// The welcome lists at most ten managers, the nearest ten.
func TestWelcomeListsAtMostTenManagersTheNearestOnes(t *testing.T) {
	env := testkit.New(t)
	ctx := context.Background()
	admin := env.NewUser("boss")
	created, err := env.App.Orgs.Create(ctx, admin.User.ID, "Deep")
	require.NoError(t, err)
	// Admin ── level1 ── level2 ── ... ── level11 ── Worker: 12 managers above the worker.
	parent := created.PositionID
	var chain []string // handles, nearest to the worker first
	for i := 1; i <= 11; i++ {
		name := fmt.Sprintf("level%d", i)
		parent = env.AddPosition(created.ID, name, parent)
		holder := env.NewUser(name)
		env.AddMember(created.ID, holder.User.ID, orgs.PermissionMember, parent)
		chain = append([]string{name}, chain...)
	}
	env.AddPosition(created.ID, "Workers", parent)
	chain = append(chain, "boss") // the root holder is the 12th and farthest
	require.Len(t, chain, 12)

	// The admin starts the worker in Deep at the bottom of the chart.
	f := testkit.FolderOf("bottom")
	f.Org, f.Position = "deep", "Workers"
	welcome, _ := welcomeOf(t, admin.Token(dto.TokenKindAgent, "laptop"), f)
	require.Equal(t, "Workers", welcome.Position)
	require.Equal(t, chain[:10], handlesOf(welcome.Managers), "the ten nearest, nearest first; the farthest two are left out")
}

// An AI no upgrade placed (only a fixture is one) has no home organization: the frame names none and
// lists no managers, and the other fields are still there.
func TestWelcomeOfAnAIWithoutAHomeOrganization(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	env.NewLegacyAI(alice, "old")
	f := testkit.FolderOf("old")
	f.Adopt = "old"
	welcome, raw := welcomeOf(t, alice.Token(dto.TokenKindAgent, "laptop"), f)
	require.Equal(t, "old", welcome.Intelligence.Handle, "fixture sanity: the legacy AI was adopted")
	require.Nil(t, welcome.Organization)
	require.NotContains(t, raw, `"organization"`, "absent, not null")
	require.Equal(t, "", welcome.Position)
	require.Contains(t, raw, `"managers":[]`)
	require.Equal(t, "alice", welcome.Owner.Handle)
}

// GET /agent/whoami keeps answering who the token belongs to - the same fields at the top level - and
// gains the same three facts about that user's default organization, the one a new AI of theirs joins:
// the organization, the position they hold there and who manages them, nearest first.
func TestWhoAmICarriesTheOrganizationPositionAndManagers(t *testing.T) {
	w := newPlacementWorld(t)
	whoami := func(c *testkit.Client) (dto.AgentWhoAmI, string) {
		t.Helper()
		resp := w.agentToken(c).Get("/api/v1/agent/whoami").RequireStatus(http.StatusOK)
		return testkit.Decode[dto.AgentWhoAmI](resp), string(resp.Body)
	}

	t.Run("an admin who holds the root position", func(t *testing.T) {
		me, raw := whoami(w.alice)
		require.Equal(t, w.alice.User.ID, me.ID, "the token's user, as before")
		require.Equal(t, "alice", me.Handle)
		require.Equal(t, dto.IntelligenceRoleUser, me.Role)
		require.Equal(t, w.alicePersonal, me.Organization.ID, "her default organization is her own")
		require.Equal(t, "Admin", me.Position)
		require.Empty(t, me.Managers)
		require.Contains(t, raw, `"managers":[]`)
	})
	t.Run("a director", func(t *testing.T) {
		me, _ := whoami(w.bob)
		require.Equal(t, &dto.ContactOrg{ID: w.acme.ID, Name: "Acme", Slug: "acme"}, me.Organization, "Acme is bob's default")
		require.Equal(t, "Director", me.Position)
		require.Equal(t, []string{"alice"}, handlesOf(me.Managers))
	})
	t.Run("a member with no position and no creator", func(t *testing.T) {
		me, _ := whoami(w.erin)
		require.Equal(t, w.acme.ID, me.Organization.ID)
		require.Equal(t, "", me.Position)
		require.Empty(t, me.Managers, "nobody manages a positionless user")
	})
	t.Run("a guest is managed by its inviter", func(t *testing.T) {
		me, _ := whoami(w.gus)
		require.Equal(t, w.acme.ID, me.Organization.ID)
		require.Equal(t, []string{"alice"}, handlesOf(me.Managers))
	})
	t.Run("the default organization is the one the user chose", func(t *testing.T) {
		personal := w.bob.PersonalOrg().ID
		require.NoError(t, w.env.App.Orgs.SetDefault(context.Background(), w.bob.User.ID, personal))
		me, _ := whoami(w.bob)
		require.Equal(t, personal, me.Organization.ID)
		require.Equal(t, "Admin", me.Position, "the root position of his own organization")
		require.Empty(t, me.Managers)
	})
	t.Run("a user in no organization", func(t *testing.T) {
		dora := w.env.NewUser("dora")
		require.NoError(t, w.env.App.Orgs.Delete(context.Background(), dora.User.ID, dora.PersonalOrg().ID))
		me, raw := whoami(dora)
		require.Equal(t, dora.User.ID, me.ID)
		require.Nil(t, me.Organization)
		require.NotContains(t, raw, `"organization"`)
		require.Equal(t, "", me.Position)
		require.Contains(t, raw, `"managers":[]`)
	})
	t.Run("a v0.5 client reads the same answer", func(t *testing.T) {
		resp := w.agentToken(w.bob).Get("/api/v1/agent/whoami").RequireStatus(http.StatusOK)
		old := testkit.Decode[dto.IntelligenceRef](resp)
		require.Equal(t, dto.IntelligenceRef{ID: w.bob.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "bob", Handle: "bob"}, old)
	})
}
