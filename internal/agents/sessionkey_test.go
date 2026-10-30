package agents

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// addAIMember makes the AI a member of the room.
func (f *fixture) addAIMember(t *testing.T, roomID, agentID string) {
	t.Helper()
	f.exec(t, `INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES (?, ?, 'agent', ?, ?)`,
		"member-"+roomID, roomID, agentID, t0.UnixMilli())
}

// state reads the state row of a session in a room.
func (f *fixture) state(t *testing.T, agentID, sessionKey, roomID string) db.SessionRoomState {
	t.Helper()
	state, err := f.st.R().GetSessionRoomState(context.Background(), db.GetSessionRoomStateParams{
		AgentID: agentID, SessionKey: sessionKey, RoomID: roomID,
	})
	require.NoError(t, err)
	return state
}

// SN-01: a session is keyed by the harness session id it sends, else by its label, and the principal by its
// pinned id or "principal" when nothing is pinned.
func TestSessionKey(t *testing.T) {
	require.Equal(t, "0f2a-session", SessionKey("0f2a-session", "", "pinned"), "the harness session id outlives the label")
	require.Equal(t, "0f2a-session", SessionKey("0f2a-session", "claude-2", "pinned"), "a labelled session with an id is keyed by the id")
	require.Equal(t, "label:claude-2", SessionKey("", "claude-2", "pinned"), "a session without an id lives with its label")
	require.Equal(t, "label:wt", SessionKey("", "wt", ""))
	require.Equal(t, "pinned", SessionKey("", "", "pinned"), "the principal without an id is the pinned session")
	require.Equal(t, "principal", SessionKey("", "", ""), "an AI that pins nothing has a principal key that is not empty")
}

// SN-02: the principal is notified of everything its delivery mode wants, a parallel session only of the
// messages that mention it, and no other setting exists.
func TestDefaultNotify(t *testing.T) {
	require.Equal(t, NotifyOn, DefaultNotify(true))
	require.Equal(t, NotifyMentions, DefaultNotify(false))
	require.True(t, ValidNotify(NotifyOn))
	require.True(t, ValidNotify(NotifyMentions))
	require.False(t, ValidNotify("all"))
	require.False(t, ValidNotify(""))
}

// SN-01: an attach resolves the key of the session it made, and the presence resolves the same key by label: the
// harness session id the session sent, else its label, and the principal's pinned id.
func TestSessionKeysOfLiveSessions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	agent := mustAgent(t, f, f.upsert(t, alice, "builder"))
	attach := func(req AttachRequest) (Live, string) {
		t.Helper()
		live, key, err := f.reg.AttachSession(ctx, agent.Agent, &fakeSession{}, req)
		require.NoError(t, err)
		require.Equal(t, key, keyByLabel(t, f, agent.ID, live.Label), "the attach and the presence agree on the key")
		return live, key
	}

	live, key := attach(AttachRequest{Harness: "claude", LiveID: "process-principal", SessionID: "harness-principal"})
	require.Equal(t, "", live.Label)
	require.Equal(t, "harness-principal", key)
	require.Equal(t, "harness-principal", mustAgent(t, f, agent.ID).PrincipalSession, "the first identified session is pinned")

	live, key = attach(AttachRequest{Harness: "claude", LiveID: "process-parallel", Preferred: "claude-2"})
	require.Equal(t, "claude-2", live.Label)
	require.Equal(t, "label:claude-2", key, "a session without an id lives with its label")

	live, key = attach(AttachRequest{
		Harness: "claude", LiveID: "process-identified", Preferred: "claude-3", SessionID: "harness-parallel",
	})
	require.Equal(t, "claude-3", live.Label)
	require.Equal(t, "harness-parallel", key, "an id outlives the label it attached with")

	// The principal without an id (an AI that pins nothing) is keyed by the role, not by a label it does not have.
	other := newFixture(t)
	bob := other.user(t, "bob")
	plain := mustAgent(t, other, other.upsert(t, bob, "builder"))
	_, key, err := other.reg.AttachSession(ctx, plain.Agent, &fakeSession{}, AttachRequest{Harness: "opencode", LiveID: "process-plain"})
	require.NoError(t, err)
	require.Equal(t, keyPrincipal, key)

	_, ok := f.presence.SessionKeyOf(agent.ID, "claude-9", "harness-principal")
	require.False(t, ok, "a label that is not live has no key")
}

// keyByLabel reads the key of a live session the way the gateway's routing does.
func keyByLabel(t *testing.T, f *fixture, agentID, label string) string {
	t.Helper()
	pinned := mustAgent(t, f, agentID).PrincipalSession
	key, ok := f.presence.SessionKeyOf(agentID, label, pinned)
	require.True(t, ok, "the session is live")
	return key
}

// SN-01, SN-02: a session joins a room caught up, at the highest sequence the room assigned, with the default
// of its role; joining again later keeps its own position and setting instead of jumping to the join point.
func TestEnsureRoomStateJoinsAtTheHighSequence(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	agentID := f.upsert(t, alice, "builder")
	room := f.room(t, alice)
	f.addAIMember(t, room, agentID)
	f.exec(t, `UPDATE rooms SET next_seq = 11 WHERE id = ?`, room) // ten messages so far

	require.NoError(t, f.reg.EnsureRoomState(ctx, agentID, "harness-principal", room, true))
	require.NoError(t, f.reg.EnsureRoomState(ctx, agentID, "label:wt", room, false))
	require.Equal(t, db.SessionRoomState{
		AgentID: agentID, SessionKey: "harness-principal", RoomID: room, ReadSeq: 10, Notify: NotifyOn, UpdatedAt: t0.UnixMilli(),
	}, f.state(t, agentID, "harness-principal", room))
	require.Equal(t, db.SessionRoomState{
		AgentID: agentID, SessionKey: "label:wt", RoomID: room, ReadSeq: 10, Notify: NotifyMentions, UpdatedAt: t0.UnixMilli(),
	}, f.state(t, agentID, "label:wt", room))

	// The session reads up to 12 while the room grows to 15 and comes back (a new process): its position stays.
	require.NoError(t, f.reg.AdvanceSessionReadSeq(ctx, agentID, "harness-principal", room, 12, true))
	f.exec(t, `UPDATE rooms SET next_seq = 16 WHERE id = ?`, room)
	require.NoError(t, f.reg.EnsureRoomState(ctx, agentID, "harness-principal", room, true))
	require.Equal(t, int64(12), f.state(t, agentID, "harness-principal", room).ReadSeq, "the join point is only the first position")

	// An AI or a room with no row: nothing to create.
	require.NoError(t, f.reg.EnsureRoomState(ctx, agentID, "", room, true), "a session with no key is not a session")
	require.Error(t, f.reg.EnsureRoomState(ctx, agentID, "harness-principal", "missing", true), "a room that does not exist")
}

// SN-01: a read position only moves forward, and a session that has none gets one with the default of its role.
func TestAdvanceSessionReadSeqOnlyMovesForward(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	agentID := f.upsert(t, alice, "builder")
	room := f.room(t, alice)
	f.addAIMember(t, room, agentID)
	f.exec(t, `UPDATE rooms SET next_seq = 11 WHERE id = ?`, room)

	require.NoError(t, f.reg.AdvanceSessionReadSeq(ctx, agentID, "label:wt", room, 4, false))
	require.Equal(t, db.SessionRoomState{
		AgentID: agentID, SessionKey: "label:wt", RoomID: room, ReadSeq: 4, Notify: NotifyMentions,
	}, withoutUpdatedAt(f.state(t, agentID, "label:wt", room)), "a session with no row gets one")
	require.NoError(t, f.reg.AdvanceSessionReadSeq(ctx, agentID, "label:wt", room, 2, false))
	require.Equal(t, int64(4), f.state(t, agentID, "label:wt", room).ReadSeq, "a late read cannot unread")
	require.NoError(t, f.reg.AdvanceSessionReadSeq(ctx, agentID, "label:wt", room, 9, false))
	require.Equal(t, int64(9), f.state(t, agentID, "label:wt", room).ReadSeq)
	require.NoError(t, f.reg.AdvanceSessionReadSeq(ctx, agentID, "", room, 9, false), "a session with no key is not a session")
}

// SN-02: a session turns notifications on for one room; its state row is created at its join point first, so
// the setting never turns the whole history unread, and only that session's row changes.
func TestSetSessionNotify(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.user(t, "alice")
	agentID := f.upsert(t, alice, "builder")
	room := f.room(t, alice)
	f.addAIMember(t, room, agentID)
	f.exec(t, `UPDATE rooms SET next_seq = 31 WHERE id = ?`, room)

	require.NoError(t, f.reg.SetSessionNotify(ctx, agentID, "label:wt", room, false, NotifyOn))
	require.Equal(t, db.SessionRoomState{
		AgentID: agentID, SessionKey: "label:wt", RoomID: room, ReadSeq: 30, Notify: NotifyOn,
	}, withoutUpdatedAt(f.state(t, agentID, "label:wt", room)))

	require.NoError(t, f.reg.SetSessionNotify(ctx, agentID, "label:wt", room, false, NotifyMentions))
	require.Equal(t, int64(30), f.state(t, agentID, "label:wt", room).ReadSeq, "the setting keeps the position")
	require.NoError(t, f.reg.SetSessionNotify(ctx, agentID, "label:other", room, false, NotifyOn))
	require.Equal(t, NotifyMentions, f.state(t, agentID, "label:wt", room).Notify, "another session has its own setting")

	require.Error(t, f.reg.SetSessionNotify(ctx, agentID, "label:wt", room, false, "all"), "there are two settings")
	_, err := f.st.R().GetSessionRoomState(ctx, db.GetSessionRoomStateParams{
		AgentID: agentID, SessionKey: "label:refused", RoomID: room,
	})
	require.ErrorIs(t, err, sql.ErrNoRows, "a refused setting creates nothing")
}

// withoutUpdatedAt drops the time from a state row, which the assertions do not pin.
func withoutUpdatedAt(s db.SessionRoomState) db.SessionRoomState {
	s.UpdatedAt = 0
	return s
}
