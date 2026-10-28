package api_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// session returns f as a process that belongs to the harness session id: its own live id (a process of
// its own, as a restarted `claude --resume` is) and the harness session id it sends.
func session(f testkit.Folder, harnessSession string) testkit.Folder {
	f.Live = testkit.NewLiveID()
	f.HarnessSession = harnessSession
	return f
}

// liveLabels is the labels of the AI's live sessions, the principal first, as its profile lists them.
func liveLabels(t *testing.T, l *liveEnv, id string) []string {
	t.Helper()
	p := profileOf(t, l.alice, id)
	require.NotNil(t, p.AI)
	labels := make([]string, 0, len(p.AI.Sessions))
	for _, s := range p.AI.Sessions {
		labels = append(labels, s.Label)
	}
	return labels
}

func requireLabels(t *testing.T, l *liveEnv, id string, want ...string) {
	t.Helper()
	require.Eventually(t, func() bool {
		got := liveLabels(t, l, id)
		return len(got) == len(want) && strings.Join(got, "|") == strings.Join(want, "|")
	}, wsWait, 20*time.Millisecond, "the live sessions should be %q, they are %q", want, liveLabels(t, l, id))
}

func pinnedOf(t *testing.T, l *liveEnv, id string) string {
	t.Helper()
	a, err := l.env.App.Store.R().GetAgent(t.Context(), id)
	require.NoError(t, err)
	return a.PrincipalSession
}

// SESS-52, SESS-53, SESS-54 (the production incident): the principal A has Claude session "sess-a" and a
// parallel session B joined as :claude-2. A's process dies. B stays :claude-2, never receives what is for
// the principal, and the messages queue. A comes back as a new process with the same harness session id,
// after B reconnected first, and gets the principal back and the queue.
func TestAParallelSessionNeverTakesThePrincipalWhenItLeaves(t *testing.T) {
	l := newLiveEnv(t)
	aFolder := session(acme, "sess-a-0000-0000")
	aConn, aWelcome := dialAs(t, l, aFolder)
	require.Empty(t, aWelcome.Session.Label)
	id := aWelcome.Agent.ID
	l.addMember(t, id)

	bFolder := session(acme, "sess-b-0000-0000")
	bConn, bWelcome := dialAs(t, l, bFolder)
	require.Equal(t, "claude-2", bWelcome.Session.Label)
	requireLabels(t, l, id, "", "claude-2")
	require.Equal(t, "sess-a-0000-0000", pinnedOf(t, l, id), "the principal that held the role at the first connection is pinned")

	// A's process dies.
	l.clock.Advance(time.Minute)
	aConn.Close()
	requireLabels(t, l, id, "claude-2")
	left := l.start.Add(time.Minute)
	p := profileOf(t, l.alice, id)
	require.True(t, p.AI.Online, "the AI is online while any session is live")
	require.NotNil(t, p.AI.PrincipalOfflineSince, "but its principal is offline")
	require.True(t, p.AI.PrincipalOfflineSince.Equal(left), "since the moment it left: %v", p.AI.PrincipalOfflineSince)

	// What is for the principal waits: B is not the principal and receives none of it.
	queued := l.post(t, "for the principal")
	bConn.ExpectNone(300 * time.Millisecond)

	// B's process restarts (claude --resume): a new process with the same harness session id, while the
	// principal is still away. It is :claude-2 again, not the principal.
	bConn.Close()
	requireLabels(t, l, id)
	bConn, bWelcome = dialAs(t, l, session(acme, "sess-b-0000-0000"))
	require.Equal(t, "claude-2", bWelcome.Session.Label, "a parallel session never becomes the principal by itself")
	requireLabels(t, l, id, "claude-2")
	bConn.ExpectNone(300 * time.Millisecond)
	require.False(t, principalLive(t, l), "no principal session is live")

	// A comes back after B, as a new process of the same harness session: it is the principal and drains
	// the queue.
	aConn, aWelcome = dialAs(t, l, session(acme, "sess-a-0000-0000"))
	require.Empty(t, aWelcome.Session.Label, "the pinned session gets the principal role back")
	require.Equal(t, queued.ID, aConn.Message(wsWait).Message.ID, "and the queue")
	requireLabels(t, l, id, "", "claude-2")
	bConn.ExpectNone(300 * time.Millisecond)
	p = profileOf(t, l.alice, id)
	require.Nil(t, p.AI.PrincipalOfflineSince, "the principal is back")
}

// principalLive tells whether the AI of the acme folder has a live principal session, by GET /agent/session.
func principalLive(t *testing.T, l *liveEnv) bool {
	t.Helper()
	for _, s := range l.session(t).Sessions {
		if s.Label == "" {
			return true
		}
	}
	return false
}

// SESS-54: a parallel session's label is tied to its harness session id: it keeps the label across
// reconnects while the label is free, even when another label is lower.
func TestAParallelSessionKeepsItsLabelAcrossReconnects(t *testing.T) {
	l := newLiveEnv(t)
	_, aWelcome := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	id := aWelcome.Agent.ID
	l.addMember(t, id)
	bConn, bWelcome := dialAs(t, l, session(acme, "sess-b-0000-0000"))
	cConn, cWelcome := dialAs(t, l, session(acme, "sess-c-0000-0000"))
	require.Equal(t, "claude-2", bWelcome.Session.Label)
	require.Equal(t, "claude-3", cWelcome.Session.Label)

	bConn.Close()
	cConn.Close()
	requireLabels(t, l, id, "")
	// C returns first: the lowest free label is claude-2, but C is :claude-3.
	cConn, cWelcome = dialAs(t, l, session(acme, "sess-c-0000-0000"))
	require.Equal(t, "claude-3", cWelcome.Session.Label)
	_, bWelcome = dialAs(t, l, session(acme, "sess-b-0000-0000"))
	require.Equal(t, "claude-2", bWelcome.Session.Label)
	requireLabels(t, l, id, "", "claude-2", "claude-3")

	// A label taken by another session is not stolen: D arrives while B is away and takes claude-2; B is
	// then the next free one.
	cConn.Close()
	requireLabels(t, l, id, "", "claude-2")
}

// SESS-55: a session whose harness session id is not the pinned one joins as a parallel session even
// when the principal label is free and it is the only session there is.
func TestANewSessionThatIsNotThePinnedOneIsParallel(t *testing.T) {
	l := newLiveEnv(t)
	aConn, aWelcome := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	id := aWelcome.Agent.ID
	aConn.Close()
	requireLabels(t, l, id)

	_, other := dialAs(t, l, session(acme, "sess-other-0000"))
	require.Equal(t, "claude-2", other.Session.Label, "a different conversation does not take the role")
	_, plain := dialAs(t, l, session(acme, ""))
	require.Equal(t, "claude-3", plain.Session.Label, "neither does a session with no id")
	requireLabels(t, l, id, "claude-2", "claude-3")
	require.Equal(t, "sess-a-0000-0000", pinnedOf(t, l, id))
}

// SESS-56: with nothing pinned (a harness with no session id, or an AI from before the upgrade whose
// principal sent none) the folder's first live session is the principal, as before, and a session with
// an id does not take it from a live one.
func TestWithoutAPinnedSessionTheFirstLiveSessionIsThePrincipal(t *testing.T) {
	l := newLiveEnv(t)
	_, first := dialAs(t, l, session(acme, ""))
	require.Empty(t, first.Session.Label)
	id := first.Agent.ID
	require.Empty(t, pinnedOf(t, l, id), "a session with no id pins nothing")
	_, second := dialAs(t, l, session(acme, "sess-b-0000-0000"))
	require.Equal(t, "claude-2", second.Session.Label)
	require.Empty(t, pinnedOf(t, l, id), "a parallel session does not pin itself either")
}

// SESS-52: the principal that holds the role the first time a session with an id connects is the one
// pinned, even when it connected before the upgrade without one: the first session with an id pins itself
// when the label is free, and the holder's id when it is the holder who has one.
func TestTheFirstSessionWithAnIDOnAFreeLabelIsPinned(t *testing.T) {
	l := newLiveEnv(t)
	conn, w := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	require.Empty(t, w.Session.Label)
	require.Equal(t, "sess-a-0000-0000", pinnedOf(t, l, w.Agent.ID))
	conn.Close()
	requireLabels(t, l, w.Agent.ID)
	_, again := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	require.Empty(t, again.Session.Label)
}

// SESS-53: two live processes of one harness session: the newer one replaces the older (the same
// conversation resumed while the old process is still up), which is closed as taken over.
func TestTheSameHarnessSessionInANewProcessReplacesTheOldOne(t *testing.T) {
	l := newLiveEnv(t)
	old, w := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	id := w.Agent.ID
	_, fresh := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	require.Empty(t, fresh.Session.Label)
	var ce websocket.CloseError
	require.True(t, errors.As(old.Closed(wsWait), &ce))
	require.Equal(t, websocket.StatusCode(4002), ce.Code)
	requireLabels(t, l, id, "")
}

// SESS-60: a malformed harness session id is refused with 422 and the field, creating nothing; a Claude
// Code UUID and the other shapes the harnesses use are accepted.
func TestTheHarnessSessionHeaderIsValidated(t *testing.T) {
	l := newLiveEnv(t)
	for name, bad := range map[string]string{
		"a space":        "sess a",
		"a slash":        "../etc/passwd",
		"non ascii":      "sessão-1",
		"too long":       strings.Repeat("a", 129),
		"leading hyphen": "-sess",
	} {
		t.Run(name, func(t *testing.T) {
			f := session(acme, bad)
			conn, resp := l.tok.DialFolder(f)
			if conn != nil {
				conn.Close()
			}
			require.Nil(t, conn, "refused")
			require.Equal(t, http.StatusUnprocessableEntity, resp.Status, string(resp.Body))
			require.Contains(t, string(resp.Body), "harness_session")
		})
	}
	require.Empty(t, ownedAIs(t, l.alice), "a refused connection creates no AI")
	for _, good := range []string{
		"15cf4c1e-3a52-4a8e-9f6b-0123456789ab", "ses_5c1a9b2d4e", "a", strings.Repeat("a", 128), "pi.session:1_2-3",
	} {
		conn, resp := l.tok.DialFolder(session(acme, good))
		require.NotNil(t, conn, "%q refused: %d %s", good, resp.Status, resp.Body)
		conn.Welcome(wsWait)
		conn.Close()
	}
}

// SESS-57: `--principal` makes the session the principal at once and pins its id, whether the principal
// is live (it is taken over, 4002) or away; the former principal is parallel when it comes back.
func TestThePrincipalFlagMakesTheSessionThePrincipal(t *testing.T) {
	l := newLiveEnv(t)
	aConn, aWelcome := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	id := aWelcome.Agent.ID
	l.addMember(t, id)

	c := session(acme, "sess-c-0000-0000")
	c.Principal = true
	cConn, cWelcome := dialAs(t, l, c)
	require.Empty(t, cWelcome.Session.Label, "the flag gives the role while the principal is live")
	var ce websocket.CloseError
	require.True(t, errors.As(aConn.Closed(wsWait), &ce))
	require.Equal(t, websocket.StatusCode(4002), ce.Code, "the former principal is taken over")
	require.Equal(t, "sess-c-0000-0000", pinnedOf(t, l, id))

	// The former principal opens again: it is a parallel session now.
	_, back := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	require.Equal(t, "claude-2", back.Session.Label)

	// A reconnect of C without the flag (a dropped socket) keeps the role.
	cConn.Close()
	requireLabels(t, l, id, "claude-2")
	_, again := dialAs(t, l, session(acme, "sess-c-0000-0000"))
	require.Empty(t, again.Session.Label)

	// With the principal away the flag gives the role as well.
	d := session(acme, "sess-d-0000-0000")
	d.Principal = true
	dConn, dWelcome := dialAs(t, l, d)
	_ = dConn
	require.Empty(t, dWelcome.Session.Label)
	require.Equal(t, "sess-d-0000-0000", pinnedOf(t, l, id))
}

// SESS-58: --take-over replaces the live principal and pins the session that does it; with no principal
// live there is nothing to take over and the session joins as a parallel one unless it is the pinned one.
func TestTakeOverReplacesTheLivePrincipalAndPinsTheSession(t *testing.T) {
	l := newLiveEnv(t)
	aConn, aWelcome := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	id := aWelcome.Agent.ID

	b := session(acme, "sess-b-0000-0000")
	b.Takeover = true
	_, bWelcome := dialAs(t, l, b)
	require.Empty(t, bWelcome.Session.Label)
	var ce websocket.CloseError
	require.True(t, errors.As(aConn.Closed(wsWait), &ce))
	require.Equal(t, websocket.StatusCode(4002), ce.Code)
	require.Equal(t, "sess-b-0000-0000", pinnedOf(t, l, id))

	// The principal is B and live: a take-over by a session without an id replaces it and unpins.
	c := session(acme, "")
	c.Takeover = true
	_, cWelcome := dialAs(t, l, c)
	require.Empty(t, cWelcome.Session.Label)
	require.Empty(t, pinnedOf(t, l, id), "a harness with no id cannot be pinned: the role follows the folder again")
}

// SESS-59: the make-principal action of the web. Whoever manages the AI pins one of its live sessions that
// has a harness session id; both the former principal and the new one reconnect by themselves (4003) and
// the roles swap in either order. Anyone else is refused, and a session that is not live or has no id is a
// conflict.
func TestMakingASessionThePrincipalFromTheWeb(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	ai := r.TargetAI
	host := r.Manager.Token(dto.TokenKindAgent, "laptop")
	folder := testkit.FolderOf("acme-ai")
	dial := func(id string) (*testkit.AgentConn, dto.AgentWelcomeFrame) {
		conn, resp := host.DialFolder(session(folder, id))
		require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
		return conn, conn.Welcome(wsWait)
	}
	aConn, aWelcome := dial("sess-a-0000-0000")
	require.Empty(t, aWelcome.Session.Label)
	bConn, bWelcome := dial("sess-b-0000-0000")
	require.Equal(t, "claude-2", bWelcome.Session.Label)
	_, plain := dial("")
	require.Equal(t, "claude-3", plain.Session.Label)

	profile := testkit.Decode[dto.IntelligenceProfile](r.Manager.Get("/api/v1/intelligences/" + ai).RequireStatus(http.StatusOK))
	require.Len(t, profile.AI.Sessions, 3)
	require.True(t, profile.AI.Sessions[0].CanBePrincipal, "a session with an id can be made the principal")
	require.True(t, profile.AI.Sessions[1].CanBePrincipal)
	require.False(t, profile.AI.Sessions[2].CanBePrincipal, "one with no id cannot")

	path := "/api/v1/agents/" + ai + "/principal"
	body := dto.MakePrincipalRequest{Label: "claude-2"}
	requireRefusal(t, r.Peer.Post(path, body), http.StatusForbidden)
	requireRefusal(t, r.Outsider.Post(path, body), http.StatusNotFound)
	r.Manager.Post(path, dto.MakePrincipalRequest{Label: "ghost"}).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	r.Manager.Post(path, dto.MakePrincipalRequest{Label: "claude-3"}).RequireError(http.StatusConflict, dto.ErrorCodeConflict)
	require.NoError(t, bConn.Closed(200*time.Millisecond), "a refusal ends nothing")

	r.Manager.Post(path, body).RequireStatus(http.StatusNoContent)
	for _, c := range []*testkit.AgentConn{aConn, bConn} {
		var ce websocket.CloseError
		require.True(t, errors.As(c.Closed(wsWait), &ce))
		require.Equal(t, websocket.StatusCode(4004), ce.Code, "the roles change: reconnect")
	}
	a, err := env.App.Store.R().GetAgent(t.Context(), ai)
	require.NoError(t, err)
	require.Equal(t, "sess-b-0000-0000", a.PrincipalSession)

	// The former principal reconnects first: it is parallel now; then the new principal gets the role.
	_, aBack := dial("sess-a-0000-0000")
	require.Equal(t, "claude-2", aBack.Session.Label)
	_, bBack := dial("sess-b-0000-0000")
	require.Empty(t, bBack.Session.Label)
}

// SESS-59: making the principal the principal again is idempotent and pins it.
func TestMakingThePrincipalThePrincipalPinsItAndChangesNothing(t *testing.T) {
	env := testkit.New(t)
	r := env.OrgRoles("acme")
	ai := r.TargetAI
	host := r.Manager.Token(dto.TokenKindAgent, "laptop")
	conn, resp := host.DialFolder(session(testkit.FolderOf("acme-ai"), "sess-a-0000-0000"))
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(wsWait)
	r.Manager.Post("/api/v1/agents/"+ai+"/principal", dto.MakePrincipalRequest{}).RequireStatus(http.StatusNoContent)
	require.NoError(t, conn.Closed(300*time.Millisecond), "the principal stays connected")
}
