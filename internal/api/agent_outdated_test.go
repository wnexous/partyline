package api_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// sessionsOf returns the AI's live sessions as the profile lists them.
func sessionsOf(t *testing.T, l *liveEnv, id string) []dto.SessionInfo {
	t.Helper()
	p := profileOf(t, l.alice, id)
	require.NotNil(t, p.AI)
	return p.AI.Sessions
}

func outdatedOf(sessions []dto.SessionInfo, label string) bool {
	for _, s := range sessions {
		if s.Label == label {
			return s.Outdated
		}
	}
	return false
}

// SESS-63: a live session records the client version it sent, and is outdated when it is a Claude Code or
// pi session whose client sends no harness session id or is older than v0.11.0 (the first one that sends
// one); a missing version header counts as old. A harness without a session id to send (OpenCode) is never
// outdated. The profile and the member card carry the flag.
func TestAnOutdatedClientIsFlaggedOnItsSession(t *testing.T) {
	l := newLiveEnv(t)
	_, first := dialAs(t, l, session(acme, ""))
	require.True(t, first.Session.Outdated, "a client without the version header and without a session id is old")
	id := first.Agent.ID
	l.addMember(t, id)

	_, noVersion := dialAs(t, l, session(acme, "sess-b-0000-0000"))
	require.True(t, noVersion.Session.Outdated, "a session with an id but no version header is from a client older than it")

	current := session(acme, "sess-c-0000-0000")
	current.ClientVersion = "v0.11.0"
	_, upToDate := dialAs(t, l, current)
	require.False(t, upToDate.Session.Outdated, "v0.11.0 is the first client that sends the id")

	sessions := sessionsOf(t, l, id)
	require.Len(t, sessions, 3)
	require.True(t, outdatedOf(sessions, ""), "the principal's session is outdated")
	require.True(t, outdatedOf(sessions, "claude-2"), "the session with no version header is outdated")
	require.False(t, outdatedOf(sessions, "claude-3"), "a v0.11.0 client is current")

	member := membersOf(t, l.alice, l.room.ID)[id]
	require.Len(t, member.Sessions, 3, "the member card carries the same sessions")
	require.True(t, outdatedOf(member.Sessions, "claude-2"))

	// pi has a session id to send; OpenCode has none to send, so it is never outdated.
	pi := testkit.Folder{Machine: testkit.Machine, Path: "/work/pi-ai", Harness: "pi"}
	_, piWelcome := dialAs(t, l, pi)
	require.True(t, piWelcome.Session.Outdated)
	opencode := testkit.Folder{Machine: testkit.Machine, Path: "/work/opencode-ai", Harness: "opencode"}
	_, opencodeWelcome := dialAs(t, l, opencode)
	require.False(t, opencodeWelcome.Session.Outdated, "OpenCode sends no session id, so it cannot be out of date about one")
}

// SESS-64: a session without a harness session id never takes the principal role while the AI has one
// pinned. A connection with no id joins as a parallel session and the queued messages keep waiting for
// the pinned session.
func TestASessionWithoutAnIDNeverTakesThePinnedPrincipal(t *testing.T) {
	l := newLiveEnv(t)
	aConn, aWelcome := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	id := aWelcome.Agent.ID
	l.addMember(t, id)
	require.Empty(t, aWelcome.Session.Label, "the session with an id is the principal")
	require.Equal(t, "sess-a-0000-0000", pinnedOf(t, l, id))

	aConn.Close()
	noID, welcome := dialAs(t, l, session(acme, ""))
	require.Equal(t, "claude-2", welcome.Session.Label, "an old client waits as a parallel session")
	require.Equal(t, "sess-a-0000-0000", pinnedOf(t, l, id), "and pins nothing")
	require.False(t, principalLive(t, l), "the principal role stays away with its conversation")

	queued := l.post(t, "for the principal")
	noID.ExpectNone(300 * time.Millisecond)

	backConn, back := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	require.Empty(t, back.Session.Label, "the pinned conversation is the principal again")
	requireLabels(t, l, id, "", "claude-2")
	require.Equal(t, queued.ID, backConn.Message(wsWait).Message.ID, "and it drains the queue")
}

// SESS-64: the AI has had an identified session even when its pin is empty (a stored parallel label):
// a client that sends no id joins as a parallel session instead of taking the free principal label. An AI
// that never had a session id keeps the legacy behaviour (SESS-56).
func TestASessionWithoutAnIDWaitsWhenTheAIHadAnIdentifiedSession(t *testing.T) {
	l := newLiveEnv(t)
	_, aWelcome := dialAs(t, l, session(acme, "sess-a-0000-0000"))
	id := aWelcome.Agent.ID
	_, bWelcome := dialAs(t, l, session(acme, "sess-b-0000-0000"))
	require.Equal(t, "claude-2", bWelcome.Session.Label)

	// A take-over by a client without an id is explicit and replaces the principal, clearing the pin; the
	// label of a session with an id is still remembered in the database.
	old := session(acme, "")
	old.Takeover = true
	old.Principal = true
	oldConn, oldWelcome := dialAs(t, l, old)
	require.Empty(t, oldWelcome.Session.Label)
	require.Empty(t, pinnedOf(t, l, id), "an id-less take-over unpins")
	oldConn.Close()
	requireLabels(t, l, id, "claude-2")

	_, newcomer := dialAs(t, l, session(acme, ""))
	require.NotEmpty(t, newcomer.Session.Label, "an old client does not take the free principal label")
	require.Empty(t, pinnedOf(t, l, id), "and pins nothing")

	// An AI that never sent an id at all keeps the legacy first-session-is-principal behaviour.
	legacy := testkit.Folder{Machine: testkit.Machine, Path: "/work/legacy-ai", Harness: "claude"}
	_, legacyWelcome := dialAs(t, l, legacy)
	require.Empty(t, legacyWelcome.Session.Label)
}
