package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// historyColumns are the scanned columns that keep the deleted id on purpose, because they say who did something
// (history), not what belongs to whom. The author and actor columns (messages.author_*, audit_log.actor_id,
// room_members.added_by, rooms.updated_by, org_invitations.invited_by) are provenance too and are not scanned at
// all: messages stay readable after their author is gone (AD-015). `grants` is the inert table 0020 drops.
var historyColumns = []string{"organizations.created_by", "grants.created_by"}

// idColumns are the columns of the schema that can hold a user's or an intelligence's id as a reference.
var idColumns = []string{
	"user_id", "owner_id", "iid", "member_id", "agent_id", "creator_iid", "inviter_iid", "invitee_iid",
	"operator_iid", "dm_a", "dm_b", "used_by", "created_by",
}

// referencesTo counts, for every table and column of the app's database that can hold an id, the rows that hold
// id, as "table.column". The tables are read from the schema, so a table a later migration adds is covered
// without anyone remembering it. users.id and intelligences.id count too.
func referencesTo(t *testing.T, env *testkit.Env, id string) map[string]int {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(env.App.Config.DataDir, app.DatabaseFile)+"?mode=ro&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	rows, err := raw.Query(`SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p WHERE m.type = 'table'`)
	require.NoError(t, err)
	type column struct{ table, name string }
	var columns []column
	for rows.Next() {
		var c column
		require.NoError(t, rows.Scan(&c.table, &c.name))
		if slices.Contains(idColumns, c.name) || (c.name == "id" && (c.table == "users" || c.table == "intelligences")) {
			columns = append(columns, c)
		}
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	out := map[string]int{}
	for _, c := range columns {
		var n int
		require.NoError(t, raw.QueryRow(`SELECT COUNT(*) FROM "`+c.table+`" WHERE "`+c.name+`" = ?`, id).Scan(&n))
		if n > 0 {
			out[c.table+"."+c.name] = n
		}
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// deletionWorld is a user with a life in several organizations:
//
//	Team  ── alice is its only admin; bob is a member; carol is alice's guest; notes holds "Dev"
//	Pair  ── alice and bob are both admins
//	Alice's own organization ── she is its only admin; her AI atlas lives there (and works in Team too)
//
// alice hosts the AIs atlas (live: it has an open session) and notes, owns the group Support and the
// connection "wa" of Team, which notes operates, talks to bob, has a pending invitation and the usual
// credentials. The first admin, root, is the oldest active instance admin: the heir.
type deletionWorld struct {
	env                     *testkit.Env
	root, alice, bob, carol *testkit.Client
	userToken, agentToken   *testkit.Client
	team, pair, personal    db.Organization
	atlas, notes            string
	support                 dto.Room
	dm                      dto.Room
	conn                    dto.Connector
	atlasSocket             *testkit.AgentConn
	aliceLive               *testkit.LiveConn
	devPosition             string
}

func newDeletionWorld(t *testing.T) *deletionWorld {
	t.Helper()
	ctx := context.Background()
	env, _ := fakeConnectorEnv(t)
	w := &deletionWorld{env: env}
	w.root = env.SetupAdmin()
	w.alice, w.bob, w.carol = env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	w.personal = w.alice.PersonalOrg()
	team, err := env.App.Orgs.Create(ctx, w.alice.User.ID, "Team")
	require.NoError(t, err)
	w.team = team.Organization
	pair, err := env.App.Orgs.Create(ctx, w.alice.User.ID, "Pair")
	require.NoError(t, err)
	w.pair = pair.Organization
	env.AddMember(w.team.ID, w.bob.User.ID, orgs.PermissionMember, "")
	env.AddMember(w.pair.ID, w.bob.User.ID, orgs.PermissionAdmin, "")
	env.AddGuest(w.team.ID, w.carol.User.ID, w.alice.User.ID, "")
	w.devPosition = env.AddPosition(w.team.ID, "Dev", team.PositionID)

	w.atlas = env.NewAI(w.alice, "atlas")
	w.notes = env.NewAIIn(w.alice, w.team.ID, "notes")
	_, err = env.App.Orgs.SetPosition(ctx, w.alice.User.ID, w.team.ID, w.notes, w.devPosition)
	require.NoError(t, err)

	w.userToken = w.alice.Token(dto.TokenKindUser, "ci")
	w.agentToken = w.alice.Token(dto.TokenKindAgent, "laptop")
	conn, resp := w.agentToken.DialAgent("atlas")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(wsWait)
	w.atlasSocket = conn
	w.aliceLive, resp = w.alice.DialLive("")
	require.NotNil(t, w.aliceLive, "live refused: %d %s", resp.Status, resp.Body)

	w.support = createRoom(w.alice, "Support")
	execSQL(t, env, `UPDATE rooms SET org_id = ?, creator_iid = ? WHERE id = ?`, w.team.ID, w.alice.User.ID, w.support.ID)
	env.AddMember(w.team.ID, w.atlas, orgs.PermissionMember, "") // an AI joins a group of an organization it belongs to
	for _, m := range []dto.AddMemberRequest{
		{Kind: dto.MemberKindUser, MemberID: w.bob.User.ID}, {Kind: dto.MemberKindAgent, MemberID: w.atlas},
	} {
		w.alice.Post("/api/v1/rooms/"+w.support.ID+"/members", m).RequireStatus(http.StatusCreated)
	}
	w.alice.Post("/api/v1/rooms/"+w.support.ID+"/messages", dto.PostMessageRequest{Text: "welcome to support"}).RequireStatus(http.StatusCreated)
	w.dm = testkit.Decode[dto.Room](w.alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: w.bob.User.ID}).RequireStatus(http.StatusCreated))
	w.alice.Post("/api/v1/rooms/"+w.dm.ID+"/messages", dto.PostMessageRequest{Text: "hi bob"}).RequireStatus(http.StatusCreated)

	w.conn = createFakeIdle(w.alice, "wa", true)
	execSQL(t, env, `UPDATE connectors SET org_id = ? WHERE id = ?`, w.team.ID, w.conn.ID)
	w.alice.OperateConnector(w.conn.ID, w.notes) // ORG-07: the operator is an AI of the connection's organization

	globex, err := env.App.Orgs.Create(ctx, w.root.User.ID, "Globex")
	require.NoError(t, err)
	_, err = env.App.Orgs.Invite(ctx, w.root.User.ID, globex.ID, "@alice", orgs.PermissionGuest, "")
	require.NoError(t, err)
	return w
}

func (w *deletionWorld) deleteMe(c *testkit.Client, password string) *testkit.Response {
	w.env.T.Helper()
	return c.Do(http.MethodDelete, "/api/v1/me", dto.DeleteAccountRequest{Password: password})
}

func (w *deletionWorld) member(t *testing.T, org, iid string) (orgs.Member, bool) {
	t.Helper()
	m, ok, err := w.env.App.Authz.Member(context.Background(), org, iid)
	require.NoError(t, err)
	return m, ok
}

// POS-06: a user may delete their own account with their password. Anything else about the request is a refusal
// that changes nothing: a wrong, empty or missing password is 422 on the field password, no credentials is 401 and
// an agent token cannot reach the route at all.
func TestDeleteMeRefusalsChangeNothing(t *testing.T) {
	w := newDeletionWorld(t)
	before := referencesTo(t, w.env, w.alice.User.ID)

	for name, password := range map[string]string{"a wrong password": "not-the-password", "an empty password": ""} {
		t.Run(name, func(t *testing.T) {
			e := w.deleteMe(w.alice, password).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
			require.Contains(t, e.Fields, "password")
		})
	}
	t.Run("a password that is almost right", func(t *testing.T) {
		e := w.deleteMe(w.alice, testkit.Password+"-x").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "password")
	})
	t.Run("a body that is not JSON", func(*testing.T) {
		w.alice.Delete("/api/v1/me").RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	})
	t.Run("no credentials", func(*testing.T) {
		w.deleteMe(w.env.Anon(), testkit.Password).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	})
	t.Run("an agent token", func(*testing.T) {
		w.deleteMe(w.agentToken, testkit.Password).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})
	t.Run("a user token with a wrong password reaches the check", func(t *testing.T) {
		e := w.deleteMe(w.userToken, "not-the-password").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "password")
	})

	w.alice.Get("/api/v1/me").RequireStatus(http.StatusOK)
	require.Equal(t, before, referencesTo(t, w.env, w.alice.User.ID), "nothing changed")
	require.NoError(t, w.atlasSocket.Closed(200*time.Millisecond), "nobody was disconnected")
}

// POS-06, OUPG-02, AD-019: deleting the account ends it - and only it. The sessions and tokens stop at once, the
// live sockets close (the AIs' with 4001), no row names the account any more; the organizations keep what was
// theirs - the group, the connection, the AIs with their positions - and the heir becomes admin where the user
// was the last; the heir's own sessions never adopt the AIs left unlinked.
func TestDeletingYourAccountLeavesTheOrganizationsTheirThings(t *testing.T) {
	w := newDeletionWorld(t)
	alice := w.alice.User.ID
	before := referencesTo(t, w.env, alice)
	require.GreaterOrEqual(t, len(before), 14, "the fixture gives the account rows in many tables: %v", sortedKeys(before))

	resp := w.deleteMe(w.alice, testkit.Password).RequireStatus(http.StatusNoContent)
	cleared := resp.Cookie(api.SessionCookie)
	require.NotNil(t, cleared, "the session cookie is cleared")
	require.Negative(t, cleared.MaxAge)

	// the credentials are dead
	w.alice.Get("/api/v1/me").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	w.userToken.Get("/api/v1/me").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	w.agentToken.AsAgent("atlas").Get("/api/v1/agent/whoami").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	w.env.Anon().Post("/api/v1/auth/login", dto.LoginRequest{Username: "alice", Password: testkit.Password}).
		RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	closed := w.atlasSocket.Closed(wsWait)
	require.Equal(t, websocket.StatusCode(4001), websocket.CloseStatus(closed), "the AI's session is ended as a deleted AI's is: %v", closed)
	require.Error(t, w.aliceLive.Closed(wsWait), "her live socket closes")

	// no row names the account except history
	left := referencesTo(t, w.env, alice)
	for _, h := range historyColumns {
		delete(left, h)
	}
	require.Empty(t, left, "rows that still name the deleted account")

	// the organizations keep what is theirs: the group and its history, the connection, the AIs
	var group dto.Room
	w.bob.Get("/api/v1/rooms/" + w.support.ID).RequireStatus(http.StatusOK).Decode(&group)
	require.Equal(t, "Support", group.Name)
	require.Equal(t, 1, countRows(t, w.env, `SELECT COUNT(*) FROM rooms WHERE id = ? AND org_id = ? AND owner_id = ?`, w.support.ID, w.team.ID, w.root.User.ID), "the heir owns the anchor of the group, which stays in Team")
	msgs := testkit.Decode[dto.Page[dto.Message]](w.bob.Get("/api/v1/rooms/" + w.support.ID + "/messages").RequireStatus(http.StatusOK)).Items
	require.Len(t, msgs, 1)
	require.Equal(t, "welcome to support", msgs[0].Text, "what she wrote in the group stays")
	require.Equal(t, 1, countRows(t, w.env, `SELECT COUNT(*) FROM connectors WHERE id = ? AND org_id = ? AND owner_id = ? AND operator_iid = ?`, w.conn.ID, w.team.ID, w.root.User.ID, w.notes), "the connection stays in Team with its operator")
	require.Zero(t, countRows(t, w.env, `SELECT COUNT(*) FROM rooms WHERE id = ?`, w.dm.ID), "her DM went with the account")
	w.bob.Get("/api/v1/rooms/" + w.dm.ID).RequireStatus(http.StatusNotFound)

	for _, c := range []struct {
		name, id, org string
		position      string
	}{{"atlas", w.atlas, w.personal.ID, ""}, {"notes", w.notes, w.team.ID, w.devPosition}} {
		m, ok := w.member(t, c.org, c.id)
		require.True(t, ok, "%s is still a member of its organization", c.name)
		require.Equal(t, c.position, m.PositionID, "%s keeps its position", c.name)
		require.Empty(t, m.CreatorIID, "%s: nobody created it any more", c.name)
		require.Equal(t, 1, countRows(t, w.env, `SELECT COUNT(*) FROM agents WHERE id = ? AND owner_id = ? AND folder = '' AND org_id = ?`, c.id, w.root.User.ID, c.org), "%s is unlinked and owned by the heir", c.name)
		require.False(t, w.env.App.Agents.Online(c.id), "%s is offline", c.name)
	}

	// organizations: the heir is admin where she was the last admin, and nowhere else
	for _, org := range []db.Organization{w.team, w.personal} {
		m, ok := w.member(t, org.ID, w.root.User.ID)
		require.True(t, ok, "the heir joined %s", org.Name)
		require.Equal(t, orgs.PermissionAdmin, m.Permission)
		require.Empty(t, m.PositionID)
	}
	_, ok := w.member(t, w.pair.ID, w.root.User.ID)
	require.False(t, ok, "Pair has another admin: it is unchanged")
	m, _ := w.member(t, w.pair.ID, w.bob.User.ID)
	require.Equal(t, orgs.PermissionAdmin, m.Permission)
	m, ok = w.member(t, w.team.ID, w.carol.User.ID)
	require.True(t, ok, "her guest stays")
	require.Equal(t, orgs.PermissionGuest, m.Permission)
	require.Empty(t, m.InviterIID, "but nobody is responsible for it")
	_, ok = w.member(t, w.team.ID, w.bob.User.ID)
	require.True(t, ok)

	// the heir's own sessions never adopt the AIs left unlinked (MIG-02), by folder or by --as
	rootAgent := w.root.Token(dto.TokenKindAgent, "laptop")
	for name, f := range map[string]testkit.Folder{
		"by the folder's name":    testkit.FolderOf("atlas"),
		"asked for with --as":     {Machine: "heirbox", Path: "/work/elsewhere", Adopt: "notes"},
		"by --as and the machine": {Machine: testkit.Machine, Path: "/work/notes", Adopt: "atlas"},
	} {
		t.Run(name, func(t *testing.T) {
			id := testkit.Decode[dto.AgentInfo](rootAgent.AsFolder(f).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID
			require.NotContains(t, []string{w.atlas, w.notes}, id, "a new AI, not the heir's pick of someone else's")
			require.Equal(t, 2, countRows(t, w.env, `SELECT COUNT(*) FROM agents WHERE id IN (?, ?) AND folder = ''`, w.atlas, w.notes), "the old ones are still unlinked")
		})
	}
	require.Equal(t, 2, countRows(t, w.env, `SELECT COUNT(*) FROM agents WHERE folder = '' AND owner_id = ?`, w.root.User.ID), "atlas and notes, still unlinked")

	// the audit trail
	entries := testkit.Decode[dto.Page[dto.AuditEntry]](w.root.Get("/api/v1/admin/audit").RequireStatus(http.StatusOK)).Items
	var deleted int
	var inherited []string
	for _, e := range entries {
		switch e.Action {
		case "user.delete":
			deleted++
			require.Equal(t, alice, e.TargetID)
		case "org.admin_inherited":
			inherited = append(inherited, e.TargetID)
		}
	}
	require.Equal(t, 1, deleted)
	require.ElementsMatch(t, []string{w.team.ID, w.personal.ID}, inherited)
}

// OUPG-02: the last active instance admin cannot delete their account - 409 last_instance_admin, with the right
// password, and nothing changes - because an admin must remain to inherit what they leave. With a second admin
// the first may, and the second is then the last.
func TestTheLastInstanceAdminCannotDeleteTheirAccount(t *testing.T) {
	env := testkit.New(t)
	root := env.SetupAdmin()
	alice := env.NewUser("alice")
	before := referencesTo(t, env, root.User.ID)

	root.Do(http.MethodDelete, "/api/v1/me", dto.DeleteAccountRequest{Password: testkit.Password}).
		RequireError(http.StatusConflict, dto.ErrorCodeLastInstanceAdmin)
	root.Get("/api/v1/me").RequireStatus(http.StatusOK)
	require.Equal(t, before, referencesTo(t, env, root.User.ID), "nothing changed")

	// a wrong password is told first, even to the last admin
	e := root.Do(http.MethodDelete, "/api/v1/me", dto.DeleteAccountRequest{Password: "not-the-password"}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "password")

	// a member may leave whatever the admins are
	alice.Do(http.MethodDelete, "/api/v1/me", dto.DeleteAccountRequest{Password: testkit.Password}).RequireStatus(http.StatusNoContent)

	second := env.NewAdmin("second")
	root.Do(http.MethodDelete, "/api/v1/me", dto.DeleteAccountRequest{Password: testkit.Password}).RequireStatus(http.StatusNoContent)
	second.Do(http.MethodDelete, "/api/v1/me", dto.DeleteAccountRequest{Password: testkit.Password}).
		RequireError(http.StatusConflict, dto.ErrorCodeLastInstanceAdmin)
	second.Get("/api/v1/me").RequireStatus(http.StatusOK)
}
