package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// defaultOrgOf is the organization the user's new AIs join when none is asked for.
func defaultOrgOf(t *testing.T, env *testkit.Env, c *testkit.Client) string {
	t.Helper()
	u, err := env.App.Store.R().GetUser(context.Background(), c.User.ID)
	require.NoError(t, err)
	require.True(t, u.DefaultOrgID.Valid, "%s has a default organization", c.User.Username)
	return u.DefaultOrgID.String
}

// requireHome checks the organization and the creator an AI was created with (ORG-02).
func requireHome(t *testing.T, env *testkit.Env, agentID, org, creator string) {
	t.Helper()
	a, err := env.App.Store.R().GetAgent(context.Background(), agentID)
	require.NoError(t, err)
	require.Equal(t, org, a.OrgID.String, "the AI's home organization")
	require.Equal(t, creator, a.CreatorIid.String, "the AI's creator")
}

// requirePlaced checks where an AI was put when it was created (ORG-02, ORG-04): the organization is
// its home, the host its creator, and it is a member with no position and no inviter, whom the host
// manages. It returns its membership.
func requirePlaced(t *testing.T, env *testkit.Env, agentID, org, creator string) orgs.Member {
	t.Helper()
	ctx := context.Background()
	requireHome(t, env, agentID, org, creator)
	m, ok, err := env.App.Authz.Member(ctx, org, agentID)
	require.NoError(t, err)
	require.True(t, ok, "the AI is a member of its home organization")
	require.Equal(t, orgs.Member{IID: agentID, Permission: orgs.PermissionMember, CreatorIID: creator}, m,
		"a member (the host started it) with no position and no inviter")
	managed, err := env.App.Authz.Manages(ctx, creator, agentID, org)
	require.NoError(t, err)
	require.True(t, managed, "the host manages the AI it created (POS-03)")
	return m
}

func memberships(t *testing.T, env *testkit.Env, agentID string) int {
	t.Helper()
	return countRows(t, env, `SELECT COUNT(*) FROM org_members WHERE iid = ?`, agentID)
}

// ORG-04, ORG-02: the first session in a folder creates its AI in the host's default organization as a
// member with no position, created by the host - whether the session is a live WebSocket or a
// tools-only call (L-034: each in its own test case).
func TestFirstSessionPlacesTheAIInTheHostsDefaultOrganization(t *testing.T) {
	ctx := context.Background()
	for name, start := range map[string]func(tok *testkit.Client, f testkit.Folder) string{
		"a tools-only call": func(tok *testkit.Client, f testkit.Folder) string {
			return testkit.Decode[dto.AgentInfo](tok.AsFolder(f).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID
		},
		"a live session": func(tok *testkit.Client, f testkit.Folder) string {
			conn, resp := tok.DialFolder(f)
			require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
			return conn.Welcome(wsWait).Agent.ID
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := testkit.New(t)
			alice := env.NewUser("alice")
			tok := alice.Token(dto.TokenKindAgent, "laptop")
			personal := defaultOrgOf(t, env, alice)
			bus, unsubscribe := env.App.Bus.Subscribe(64)
			defer unsubscribe()

			id := start(tok, acme)

			requirePlaced(t, env, id, personal, alice.User.ID)
			require.Eventually(t, func() bool {
				for {
					select {
					case e := <-bus:
						if e == (events.OrgChanged{OrgID: personal}) {
							return true
						}
					default:
						return false
					}
				}
			}, wsWait, 10*time.Millisecond, "the organization is told it has a new member")
			entries, _, err := audit.New(env.App.Store, nil).List(ctx, "")
			require.NoError(t, err)
			var joined []string
			for _, e := range entries {
				if e.Action == "org.member_join" {
					var detail map[string]any
					require.NoError(t, json.Unmarshal([]byte(e.DetailJson), &detail))
					require.Equal(t, alice.User.ID, e.ActorID, "the host started it")
					require.Equal(t, map[string]any{"iid": id, "by": "start", "permission": "member"}, detail)
					joined = append(joined, e.TargetID)
				}
			}
			require.Equal(t, []string{personal}, joined, "the join is audited, once")
		})
	}
}

// ORG-04: a second session in the same folder - any harness, any path - resolves the same AI and adds no
// membership, keeping its first join.
func TestASecondSessionInTheSameFolderAddsNoMembership(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	personal := defaultOrgOf(t, env, alice)
	first := testkit.Decode[dto.AgentInfo](tok.AsFolder(acme).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID
	requirePlaced(t, env, first, personal, alice.User.ID)
	joined := countRows(t, env, `SELECT joined_at FROM org_members WHERE iid = ?`, first)

	again := testkit.Decode[dto.AgentInfo](tok.AsFolder(acme).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID
	require.Equal(t, first, again)
	conn, resp := tok.DialFolder(acme)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, first, conn.Welcome(wsWait).Agent.ID)
	require.NoError(t, conn.Conn.CloseNow())

	require.Equal(t, 1, memberships(t, env, first), "no second membership")
	require.Equal(t, joined, countRows(t, env, `SELECT joined_at FROM org_members WHERE iid = ?`, first), "and the first join stands")
	require.Equal(t, 1, countRows(t, env, `SELECT COUNT(*) FROM audit_log WHERE action = 'org.member_join'`))
	requirePlaced(t, env, first, personal, alice.User.ID)
}

// ORG-04: where a new AI goes follows the host's default organization: the one they chose, and, once they
// leave it, the organization they have administered the longest. AIs already created stay where they are.
func TestNewAIsFollowTheHostsDefaultOrganization(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	bob := env.NewUser("bob")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	personal := defaultOrgOf(t, env, alice)
	register := func(folder string) string {
		return testkit.Decode[dto.AgentInfo](tok.AsFolder(testkit.Folder{Machine: "devbox", Path: folder}).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID
	}

	first := register("/work/first")
	requirePlaced(t, env, first, personal, alice.User.ID)

	team, err := env.App.Orgs.Create(ctx, alice.User.ID, "Team")
	require.NoError(t, err)
	require.NoError(t, env.App.Orgs.SetDefault(ctx, alice.User.ID, team.ID))
	second := register("/work/second")
	requirePlaced(t, env, second, team.ID, alice.User.ID)
	requirePlaced(t, env, first, personal, alice.User.ID)

	// alice makes bob an admin of Team and leaves it: her default is cleared and falls back to the
	// organization she has administered the longest, her own.
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return orgs.AddMember(ctx, q, orgs.AddMemberInput{OrgID: team.ID, IID: bob.User.ID, Permission: orgs.PermissionAdmin, Now: time.Now()})
	}))
	require.NoError(t, env.App.Orgs.Leave(ctx, alice.User.ID, team.ID))
	third := register("/work/third")
	requirePlaced(t, env, third, personal, alice.User.ID)
	requireHome(t, env, second, team.ID, alice.User.ID) // it stays in Team, which alice left
}

// ORG-04: a host that belongs to no organization cannot start an AI - a live session and a tools-only call
// are each refused with 422 organization_required - and nothing is created.
func TestAHostWithoutAnOrganizationCannotStartAnAI(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	dora := env.NewUser("dora")
	tok := dora.Token(dto.TokenKindAgent, "laptop")
	personal := defaultOrgOf(t, env, dora)
	require.NoError(t, env.App.Orgs.Delete(ctx, dora.User.ID, personal), "her only organization is empty: she deletes it")
	mine, err := env.App.Orgs.ListMine(ctx, dora.User.ID)
	require.NoError(t, err)
	require.Empty(t, mine, "fixture sanity: dora belongs to no organization")

	conn, resp := tok.DialFolder(acme)
	require.Nil(t, conn, "a live session is refused")
	resp.RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeOrganizationRequired)
	tok.AsFolder(acme).Post("/api/v1/agent/register", nil).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeOrganizationRequired)
	tok.AsFolder(acme).Get("/api/v1/agent/rooms").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeOrganizationRequired)

	require.Zero(t, countRows(t, env, `SELECT COUNT(*) FROM agents`), "no AI row")
	require.Zero(t, countRows(t, env, `SELECT COUNT(*) FROM intelligences WHERE kind = 'ai'`), "no AI profile")
	require.Zero(t, countRows(t, env, `SELECT COUNT(*) FROM org_members`))

	// Once she has an organization again, the same folder starts.
	home, err := env.App.Orgs.Create(ctx, dora.User.ID, "Dora's")
	require.NoError(t, err)
	id := testkit.Decode[dto.AgentInfo](tok.AsFolder(acme).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID
	requirePlaced(t, env, id, home.ID, dora.User.ID)
}
