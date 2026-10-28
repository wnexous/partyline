package agents_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// sameFolderPair dials the principal (harness claude) and a second session of the same folder in
// another harness, which the server labels pi-2: a parallel session that is not a worktree.
func (f *sessionFixture) sameFolderPair(t *testing.T) (principal, second *testkit.AgentConn, p, s testkit.Folder) {
	t.Helper()
	p = testkit.FolderOf("atlas")
	p.Live = testkit.NewLiveID()
	var resp *testkit.Response
	principal, resp = f.token.DialFolder(p)
	require.NotNil(t, principal, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Empty(t, principal.Welcome(time.Minute).Session.Label)
	t.Cleanup(principal.Close)
	f.settle(t, principal)
	s = testkit.FolderOf("atlas")
	s.Harness = "pi"
	s.Live = testkit.NewLiveID()
	second, resp = f.token.DialFolder(s)
	require.NotNil(t, second, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, "pi-2", second.Welcome(time.Minute).Session.Label)
	t.Cleanup(second.Close)
	f.settle(t, second)
	return principal, second, p, s
}

// SESS-47: a parallel session in the AI's own folder (not a worktree) never receives a plain message,
// the principal's own plain message or the AI's own message of any session; a message that addresses
// it reaches it alone. A reconnect of the parallel session brings no principal backlog either.
func TestAParallelSessionOfTheSameFolderReceivesOnlyWhatAddressesIt(t *testing.T) {
	f := newSessionFixture(t)
	principal, second, p, s := f.sameFolderPair(t)

	plain := f.post(t, "plain from alice")
	require.Equal(t, plain.ID, principal.Message(time.Minute).Message.ID)
	second.ExpectNone(400 * time.Millisecond)

	own := f.postFrom(t, p, "plain from the principal")
	require.Empty(t, own.AuthorLabel)
	principal.ExpectNone(300 * time.Millisecond)
	second.ExpectNone(400 * time.Millisecond)

	ownSecond := f.postFrom(t, s, "plain from pi-2")
	require.Equal(t, "pi-2", ownSecond.AuthorLabel)
	principal.ExpectNone(300 * time.Millisecond)
	second.ExpectNone(400 * time.Millisecond)

	addressed := f.post(t, "@atlas:pi-2 only you")
	require.Equal(t, addressed.ID, second.Message(time.Minute).Message.ID)
	principal.ExpectNone(300 * time.Millisecond)

	// The principal never acknowledged its plain message; pi-2 coming back as the same process gets no principal backlog.
	second.Close()
	f.waitSessionGone(t, "pi-2")
	again, resp := f.token.DialFolder(s)
	require.NotNil(t, again, "upgrade refused: %d %s", resp.Status, resp.Body)
	t.Cleanup(again.Close)
	require.Equal(t, "pi-2", again.Welcome(time.Minute).Session.Label)
	again.ExpectNone(600 * time.Millisecond)
}

// SESS-46: the tools half of pi and OpenCode holds no socket; its messages are pushed by the listen
// process. When the principal is not live but a parallel session is, it must not read (and thereby
// take) the principal's queue: a plain message stays for the principal. Codex and Gemini, which have no
// listen process, keep reading it. With the live id of the parallel session it is that session: empty.
func TestTheToolsHalfOfAListenHarnessNeverTakesThePrincipalsQueue(t *testing.T) {
	f := newSessionFixture(t)
	principal, _, _, s := f.sameFolderPair(t)
	principal.Close()
	f.waitPrincipalGone(t)

	plain := f.post(t, "plain for the principal")

	tools := s
	tools.Live = ""
	r := f.token.AsFolder(tools).Get("/api/v1/agent/pending")
	require.Equal(t, http.StatusConflict, r.Status, r.Body)
	cursor, err := f.cursor(t)
	require.Zero(t, cursor, "nothing was taken from the principal's queue")
	_ = err

	asSession := f.token.AsFolder(s).Get("/api/v1/agent/pending")
	require.Equal(t, http.StatusOK, asSession.Status, asSession.Body)
	require.Empty(t, testkit.Decode[dto.List[dto.AgentMessage]](asSession).Items)

	codex := tools
	codex.Harness = "codex"
	r = f.token.AsFolder(codex).Get("/api/v1/agent/pending")
	require.Equal(t, http.StatusOK, r.Status, r.Body)
	items := testkit.Decode[dto.List[dto.AgentMessage]](r).Items
	require.Len(t, items, 1)
	require.Equal(t, plain.ID, items[0].Message.ID)
}

// SESS-48: an address written as an example in code reaches the principal as a plain message, with no
// note, and no parallel session; the same address written outside code reaches its session.
func TestAnAddressInCodeIsNotDeliveredToItsSession(t *testing.T) {
	f := newSessionFixture(t)
	principal, second, _, _ := f.sameFolderPair(t)

	example := f.post(t, "to reach a session write `@atlas:pi-2` or\n```\n@atlas:pi-2 hello\n```")
	frame := principal.Message(time.Minute)
	require.Equal(t, example.ID, frame.Message.ID)
	require.Empty(t, frame.ForLabel, "no label was addressed: no note")
	second.ExpectNone(400 * time.Millisecond)
	require.Empty(t, f.rows(t, "pi-2"))

	real := f.post(t, "`@atlas:other` is gone, ask @atlas:pi-2")
	require.Equal(t, real.ID, second.Message(time.Minute).Message.ID)
	principal.ExpectNone(300 * time.Millisecond)
}
