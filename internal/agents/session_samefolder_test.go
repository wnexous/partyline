package agents_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

// SESS-47: a parallel session in the AI's own folder (not a worktree) never receives a plain message live,
// the principal's own plain message or the AI's own message of any session; a message that addresses it
// reaches it, with the conversation it had not read as context (SN-03), and the principal follows the room
// so it receives the addressed message too (SN-04). A reconnect of the parallel session brings no principal
// backlog either.
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
	// pi-2 read none of the conversation, so the addressed message brings it as context (SN-03): alice's
	// plain message and the principal's own, whose every sibling sees as context (SN-06); pi-2's own is not
	// part of it.
	for _, want := range []struct{ id, authorLabel string }{{plain.ID, ""}, {own.ID, ""}} {
		context := second.Message(time.Minute)
		require.Equal(t, want.id, context.Message.ID)
		require.True(t, context.Context, "earlier conversation is marked as context")
		require.Equal(t, want.authorLabel, context.Message.AuthorLabel)
	}
	require.Equal(t, addressed.ID, second.Message(time.Minute).Message.ID)
	// The principal sees pi-2's own message as context, never as a notification (SN-06), and then the copy of
	// the message that addresses its sibling (SN-04).
	context := principal.Message(time.Minute)
	require.Equal(t, ownSecond.ID, context.Message.ID)
	require.True(t, context.Context)
	require.Equal(t, "pi-2", context.Message.AuthorLabel)
	principalFrame := principal.Message(time.Minute)
	require.Equal(t, addressed.ID, principalFrame.Message.ID, "the principal follows the room (SN-04)")
	require.False(t, principalFrame.Context)

	// The principal never acknowledged its plain message; pi-2 coming back as the same process gets no principal backlog.
	second.Close()
	f.waitSessionGone(t, "pi-2")
	again, resp := f.token.DialFolder(s)
	require.NotNil(t, again, "upgrade refused: %d %s", resp.Status, resp.Body)
	t.Cleanup(again.Close)
	require.Equal(t, "pi-2", again.Welcome(time.Minute).Session.Label)
	again.ExpectNone(600 * time.Millisecond)
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

	outside := f.post(t, "`@atlas:other` is gone, ask @atlas:pi-2")
	// The example message was never delivered live to pi-2 (the address was in code), so it is now context.
	context := second.Message(time.Minute)
	require.Equal(t, example.ID, context.Message.ID)
	require.True(t, context.Context)
	require.Equal(t, outside.ID, second.Message(time.Minute).Message.ID)
	require.Equal(t, outside.ID, principal.Message(time.Minute).Message.ID, "the principal follows the room (SN-04)")
}

// SESS-49: a labelled session that mentions its AI by the bare @handle reaches the principal once, as the
// AI's own message; the principal's own message and a plain message of the labelled session reach nobody.
func TestAParallelSessionReachesItsPrincipalWithTheBareHandle(t *testing.T) {
	f := newSessionFixture(t)
	principal, second, p, s := f.sameFolderPair(t)

	m := f.postFrom(t, s, "@atlas ping")
	require.Equal(t, "pi-2", m.AuthorLabel)
	frame := principal.Message(time.Minute)
	require.Equal(t, m.ID, frame.Message.ID)
	require.True(t, frame.Trusted, "the AI itself wrote it")
	require.Equal(t, "pi-2", frame.Message.AuthorLabel)
	require.Empty(t, frame.ForLabel)
	principal.Ack(m.ID)
	principal.ExpectNone(300 * time.Millisecond)
	second.ExpectNone(300 * time.Millisecond)

	// Nothing else a session writes is echoed: no mention, the principal's own mention, an address of its own
	// label, an address of a session that is not live, and a mention inside code.
	for _, c := range []struct {
		from testkit.Folder
		text string
	}{
		{s, "hello"},
		{p, "@atlas ping"},
		{s, "@atlas:pi-2 ping"},
		{s, "@atlas:gone ping"},
		{s, "write `@atlas` to reach me"},
	} {
		f.postFrom(t, c.from, c.text)
		principal.ExpectNone(300 * time.Millisecond)
		second.ExpectNone(300 * time.Millisecond)
	}

	// The principal acknowledged it: a reconnect brings it back neither as backlog nor as pending.
	principal.Close()
	f.waitPrincipalGone(t)
	again, resp := f.token.DialFolder(p)
	require.NotNil(t, again, "upgrade refused: %d %s", resp.Status, resp.Body)
	t.Cleanup(again.Close)
	again.Welcome(time.Minute)
	again.ExpectNone(500 * time.Millisecond)

	// Both addresses in one message: the sibling and the principal each receive it once.
	both := f.postFrom(t, s, "@atlas and @atlas:claude-2")
	require.Equal(t, both.ID, again.Message(time.Minute).Message.ID)
	again.ExpectNone(300 * time.Millisecond)
}

// SESS-49: with no live principal the mention is not delivered and not queued: a principal that connects later
// does not get it.
func TestAMentionOfAnOfflinePrincipalIsNotQueued(t *testing.T) {
	f := newSessionFixture(t)
	principal, _, p, s := f.sameFolderPair(t)
	principal.Close()
	f.waitPrincipalGone(t)

	f.postFrom(t, s, "@atlas ping")
	time.Sleep(500 * time.Millisecond) // the dispatcher runs after the post: let it find nobody
	again, resp := f.token.DialFolder(p)
	require.NotNil(t, again, "upgrade refused: %d %s", resp.Status, resp.Body)
	t.Cleanup(again.Close)
	again.Welcome(time.Minute)
	again.ExpectNone(500 * time.Millisecond)
}
