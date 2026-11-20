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

// welcomeV05 is the welcome frame as a v0.5 CLI reads it: no organization nor instructors, and the owner as
// the one who runs the AI.
type welcomeV05 struct {
	Type         dto.AgentFrameType    `json:"type"`
	Agent        dto.AgentInfo         `json:"agent"`
	Intelligence dto.IntelligenceRef   `json:"intelligence"`
	Owner        dto.IntelligenceRef   `json:"owner"`
	Connections  []dto.AgentConnection `json:"connections"`
}

// welcomeOf starts the session of folder f with the token and returns its welcome frame, decoded and
// raw, then closes the session. Every call is the same process (one live id), so dialing an AI again is a
// reconnect, never a second session.
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

// SR2-11, ORG-04: the welcome frame tells a session its home organization and who instructs it: its host
// (owner) and its instructors besides the host (instructors, oldest first). The deprecated managers field is the
// host followed by the instructors, for older CLIs. A worker the AI lead started with --parent is instructed by
// lead. A v0.5 client, which reads none of that, still finds the host user as the owner.
func TestSR211WelcomeTellsTheOrganizationAndTheInstructors(t *testing.T) {
	w := newPlacementWorld(t)
	f := placementFolder("worker-1")
	f.Parent = "@lead"
	welcome, raw := welcomeOf(t, w.agentToken(w.alice), f)

	require.Equal(t, &dto.ContactOrg{ID: w.acme.ID, Name: "Acme", Slug: "acme"}, welcome.Organization)
	require.Equal(t, "alice", welcome.Owner.Handle, "the host")
	require.Equal(t, []string{"lead"}, handlesOf(welcome.Instructors), "the parent instructs it")
	require.Equal(t, []string{"alice", "lead"}, handlesOf(welcome.Managers), "deprecated: the host, then the instructors")
	kinds := func(refs []dto.IntelligenceRef) []string {
		out := make([]string, len(refs))
		for i, r := range refs {
			out[i] = fmt.Sprintf("%s/%s", r.Kind, r.Role)
		}
		return out
	}
	require.Equal(t, []string{"human/user", "ai/ai"}, kinds(welcome.Managers), "an identity is an AI or a person, as its role says")
	require.NotContains(t, raw, `"position"`, "SR2-05: no position any more")

	t.Run("a v0.5 client still reads the host user as the owner", func(t *testing.T) {
		var old welcomeV05
		require.NoError(t, json.Unmarshal([]byte(raw), &old))
		require.Equal(t, dto.AgentFrameTypeWelcome, old.Type)
		require.Equal(t, welcome.Agent.ID, old.Agent.ID)
		require.Equal(t, "alice", old.Agent.Owner, "the host user's username")
		require.Equal(t, "alice", old.Owner.Handle, "owner is the host user, not lead, which started the AI")
		require.Equal(t, w.alice.User.ID, old.Owner.ID)
		require.Equal(t, "worker-1", old.Intelligence.Handle)
		require.Empty(t, old.Connections)
		require.Contains(t, raw, `"owner":{`, "the key stays")
	})
}

// SR2-11: an AI nobody but its host instructs has an empty list of instructors, never null, and its managers are
// its host alone.
func TestSR211WelcomeOfAnAIOnlyItsHostInstructs(t *testing.T) {
	w := newPlacementWorld(t)
	welcome, raw := welcomeOf(t, w.agentToken(w.bob), placementFolder("plain"))
	require.Equal(t, "Acme", welcome.Organization.Name, "bob's default organization")
	require.Contains(t, raw, `"instructors":[]`)
	require.Equal(t, []string{"bob"}, handlesOf(welcome.Managers))
}

// SR2-07, SR2-11: instructors cross organizations - a user outside the AI's organizations instructs it when
// the host lists them - while an admin of another organization the AI belongs to instructs nothing. They are
// listed oldest first.
func TestSR211WelcomeListsInstructorsWhateverTheOrganizations(t *testing.T) {
	w := newPlacementWorld(t)
	f := placementFolder("worker-two-orgs")
	f.Parent = "@lead"
	first, _ := welcomeOf(t, w.agentToken(w.alice), f)
	require.Equal(t, []string{"lead"}, handlesOf(first.Instructors))

	w.env.AddMember(w.globex.ID, first.Agent.ID, orgs.PermissionMember) // alice administers Globex
	w.env.AddInstructor(first.Agent.ID, w.carol.User.ID)                // carol is in no organization of the AI
	second, _ := welcomeOf(t, w.agentToken(w.alice), f)
	require.Equal(t, w.acme.ID, second.Organization.ID, "the home organization stays Acme")
	require.Equal(t, []string{"lead", "carol"}, handlesOf(second.Instructors), "oldest first")
	require.Equal(t, []string{"alice", "lead", "carol"}, handlesOf(second.Managers))
}

// An AI no upgrade placed (only a fixture is one) has no home organization: the frame names none, and its host
// still instructs it.
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
	require.Contains(t, raw, `"instructors":[]`)
	require.Equal(t, []string{"alice"}, handlesOf(welcome.Managers))
	require.Equal(t, "alice", welcome.Owner.Handle)
}

// GET /agent/whoami keeps answering who the token belongs to - the same fields at the top level - and the
// user's default organization, the one a new AI of theirs joins. Positions and managers are gone (SR2-05).
func TestWhoAmICarriesTheDefaultOrganization(t *testing.T) {
	w := newPlacementWorld(t)
	whoami := func(c *testkit.Client) (dto.AgentWhoAmI, string) {
		t.Helper()
		resp := w.agentToken(c).Get("/api/v1/agent/whoami").RequireStatus(http.StatusOK)
		return testkit.Decode[dto.AgentWhoAmI](resp), string(resp.Body)
	}

	t.Run("an admin", func(t *testing.T) {
		me, raw := whoami(w.alice)
		require.Equal(t, w.alice.User.ID, me.ID, "the token's user, as before")
		require.Equal(t, "alice", me.Handle)
		require.Equal(t, dto.IntelligenceRoleUser, me.Role)
		require.Equal(t, w.alicePersonal, me.Organization.ID, "her default organization is her own")
		require.NotContains(t, raw, `"position"`)
		require.NotContains(t, raw, `"managers"`)
	})
	t.Run("a member", func(t *testing.T) {
		me, _ := whoami(w.bob)
		require.Equal(t, &dto.ContactOrg{ID: w.acme.ID, Name: "Acme", Slug: "acme"}, me.Organization, "Acme is bob's default")
	})
	t.Run("the default organization is the one the user chose", func(t *testing.T) {
		personal := w.bob.PersonalOrg().ID
		require.NoError(t, w.env.App.Orgs.SetDefault(context.Background(), w.bob.User.ID, personal))
		me, _ := whoami(w.bob)
		require.Equal(t, personal, me.Organization.ID)
	})
	t.Run("a user in no organization", func(t *testing.T) {
		dora := w.env.NewUser("dora")
		require.NoError(t, w.env.App.Orgs.Delete(context.Background(), dora.User.ID, dora.PersonalOrg().ID))
		me, raw := whoami(dora)
		require.Equal(t, dora.User.ID, me.ID)
		require.Nil(t, me.Organization)
		require.NotContains(t, raw, `"organization"`)
	})
	t.Run("a v0.5 client reads the same answer", func(t *testing.T) {
		resp := w.agentToken(w.erin).Get("/api/v1/agent/whoami").RequireStatus(http.StatusOK)
		old := testkit.Decode[dto.IntelligenceRef](resp)
		require.Equal(t, dto.IntelligenceRef{ID: w.erin.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "erin", Handle: "erin"}, old)
	})
}
