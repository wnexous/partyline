package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// SI-01, SI-02, SI-03: two AIs of the same host see each other although they share no organization: each opens
// a DM with the other, by handle and by id, and the message arrives untrusted, because seeing is not managing.
// Adding the other one to a group still needs membership of the group's organization, and an AI of another host
// in the second organization stays out of reach.
func TestAIsOfTheSameHostSeeEachOther(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	globex := env.OrgRoles("globex")
	env.AddMember(globex.Org.ID, alice.User.ID, orgs.PermissionMember, "")
	atlas := env.NewAI(alice, "atlas")                    // home: alice's personal organization
	helper := env.NewAIIn(alice, globex.Org.ID, "helper") // home: globex, which atlas does not belong to
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	atlasAI, helperAI := tok.AsAgent("atlas"), tok.AsAgent("helper")
	atlasHandle, helperHandle := "@"+profileOf(t, alice, atlas).Handle, "@"+profileOf(t, alice, helper).Handle

	live, resp := tok.DialAgent("helper")
	require.NotNil(t, live, resp)
	defer live.Close()
	live.Welcome(wsWait)

	// SI-01: each reaches the other, by handle and by id.
	sent := testkit.Decode[dto.Message](sendDM(atlasAI, helperHandle, "the build is green").RequireStatus(http.StatusCreated))
	sendDM(atlasAI, helper, "by id").RequireStatus(http.StatusCreated)
	back := testkit.Decode[dto.Message](sendDM(helperAI, atlasHandle, "thanks").RequireStatus(http.StatusCreated))
	require.Equal(t, sent.RoomID, back.RoomID, "one DM between the two")

	// SI-03: seeing is not trust - the message is not marked trusted for helper.
	got := live.Message(wsWait)
	require.Equal(t, sent.ID, got.Message.ID)
	require.False(t, got.Trusted, "an AI of the same host is not a manager")

	// SI-02: a group of atlas's organization still refuses helper, which is not a member of it.
	e := agentGroup(atlasAI, "Ops", helper).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
	require.Equal(t, []dto.RefusedMember{{ID: helper, Reason: dto.RefusedReasonNotOrgMember}}, e.Refused)

	// The same-host rule reaches no further: globex's AI of another host stays hidden from atlas.
	sendDM(atlasAI, globex.TargetAI, "psst").RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
}

// SI-04, SI-07, SI-08: a session started in a subfolder of an AI's folder connects as a parallel session of that
// AI, labelled after its own folder like a linked worktree session, and GET /agent/session from the subfolder
// names that AI; with X-Partyline-New-AI the subfolder gets an AI of its own instead.
func TestASessionInASubfolderIsAParallelSessionOfTheAIAbove(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	folder := func(path string) testkit.Folder {
		return testkit.Folder{Machine: testkit.Machine, Path: path, Home: "/home/alice", Harness: "claude"}
	}
	main, resp := tok.DialFolder(folder("/home/alice/projects/atlas"))
	require.NotNil(t, main, resp)
	defer main.Close()
	atlas := main.Welcome(wsWait)
	require.Empty(t, atlas.Session.Label, "the AI's own folder is its principal session")

	sub := folder("/home/alice/projects/atlas/server")
	state := testkit.Decode[dto.SessionState](tok.AsFolder(sub).Get("/api/v1/agent/session").RequireStatus(http.StatusOK))
	require.True(t, state.Exists, "SI-08: the lookup finds the AI above")
	require.Equal(t, atlas.Intelligence.ID, state.Intelligence.ID)

	conn, resp := tok.DialFolder(sub)
	require.NotNil(t, conn, resp)
	defer conn.Close()
	w := conn.Welcome(wsWait)
	require.Equal(t, atlas.Intelligence.ID, w.Intelligence.ID, "SI-04: the subfolder is a session of the AI above")
	require.Equal(t, "server", w.Session.Label, "a parallel session named after its folder")

	// SI-07: --new-ai makes the subfolder its own AI.
	own := folder("/home/alice/projects/atlas/tools")
	own.NewAI = true
	state = testkit.Decode[dto.SessionState](tok.AsFolder(own).Get("/api/v1/agent/session").RequireStatus(http.StatusOK))
	require.False(t, state.Exists, "SI-07: a folder that asks for its own AI has none yet")
	fresh, resp := tok.DialFolder(own)
	require.NotNil(t, fresh, resp)
	defer fresh.Close()
	fw := fresh.Welcome(wsWait)
	require.NotEqual(t, atlas.Intelligence.ID, fw.Intelligence.ID)
	require.Empty(t, fw.Session.Label, "the principal session of its own AI")
}
