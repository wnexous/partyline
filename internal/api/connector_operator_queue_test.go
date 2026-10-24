package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/testkit"
)

// operatedPlatformWS is the platform fixture with its connection operated by Atlas: alice's AIs
// Atlas and Notes, the connection "wa" (disabled, so the fake connector does not run) with a group
// and a direct chat.
func operatedPlatformWS(t *testing.T) *platformWS {
	t.Helper()
	f := newPlatformWS(t)
	f.alice.OperateConnector(f.conn.ID, f.atlas)
	return f
}

func (f *platformWS) connection(t *testing.T) dto.Connector {
	t.Helper()
	return testkit.Decode[dto.Connector](f.alice.Get("/api/v1/connectors/" + f.conn.ID).RequireStatus(http.StatusOK))
}

func (f *platformWS) setOperator(c *testkit.Client, id string) *testkit.Response {
	t := f.env.T
	t.Helper()
	return c.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{OperatorID: &id})
}

// holds tells whether the item is among the AI's items, acknowledged or not.
func (f *platformWS) holds(agent, id string) bool {
	_, err := f.env.App.Platform.Get(context.Background(), agent, id)
	return err == nil
}

// operatorAudits returns the connector.operator entries of the connection, newest first.
func connectorOperatorAudits(admin *testkit.Client, connectorID string) []dto.AuditEntry {
	admin.T.Helper()
	var out []dto.AuditEntry
	for _, e := range auditPage(admin, "").Items {
		if e.Action == "connector.operator" && e.TargetID == connectorID {
			out = append(out, e)
		}
	}
	return out
}

// forceFailure runs sql on the app's database through a second connection, to plant a trigger
// that makes a step of a request fail, and returns a function that runs another statement (the
// trigger's removal).
func forceFailure(t *testing.T, env *testkit.Env, statement string) func(string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(env.App.Config.DataDir, app.DatabaseFile)+"?_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	_, err = raw.ExecContext(context.Background(), statement)
	require.NoError(t, err)
	return func(next string) {
		t.Helper()
		_, err := raw.ExecContext(context.Background(), next)
		require.NoError(t, err)
	}
}

// CID-05: when the owner changes the operator, every item of the connection, acknowledged or not,
// belongs to the new operator, whose live session receives the ones it has not acknowledged,
// oldest first, as soon as the change commits. The old operator's session gets nothing and its
// acknowledgement of a moved item changes nothing. One audit entry says how many items moved. The
// items of the AI's other connections stay where they are, and the connection's queued count
// does not change.
func TestOperatorChangeHandsTheQueueToTheNewOperator(t *testing.T) {
	f := operatedPlatformWS(t)
	admin := f.env.NewAdmin("root")
	second := createFakeIdle(f.alice, "wb", false)
	f.alice.OperateConnector(second.ID, f.atlas)
	elsewhereChat := f.chat(t, second, "42", "Lee", "dm")

	one := f.enqueue(t, f.atlas, f.group, "m1", "first text")
	two := f.enqueue(t, f.atlas, f.dm, "m2", "second text")
	three := f.enqueue(t, f.atlas, f.group, "m3", "third text")
	elsewhere := f.enqueue(t, f.atlas, elsewhereChat, "o1", "other connection")

	atlas := f.dial(t, "Atlas")
	require.Equal(t, []string{one, two, three, elsewhere}, platformIDs(t, atlas, 4))
	atlas.AckPlatform(two)
	f.acked(t, f.atlas, two)
	notes := f.dial(t, "Notes")
	notes.ExpectNone(300 * time.Millisecond)
	require.EqualValues(t, 2, f.connection(t).Queued)

	got := testkit.Decode[dto.Connector](f.setOperator(f.alice, f.notes).RequireStatus(http.StatusOK))
	require.Equal(t, f.notes, got.Operator.ID)
	require.EqualValues(t, 2, got.Queued, "the unacknowledged items of the connection are still queued")

	// The new operator's session receives what it has not acknowledged, oldest first, with the text.
	first, last := notes.Platform(wsWait).Event, notes.Platform(wsWait).Event
	require.Equal(t, []string{one, "first text", three, "third text"}, []string{first.ID, first.Text, last.ID, last.Text})
	notes.ExpectNone(300 * time.Millisecond)
	atlas.ExpectNone(300 * time.Millisecond)

	// Every item of the connection, the acknowledged one too (a reply to it keeps working), is Notes's.
	for _, id := range []string{one, two, three} {
		require.True(t, f.holds(f.notes, id), "item %s moved to Notes", id)
		require.False(t, f.holds(f.atlas, id), "item %s is no longer Atlas's", id)
	}
	done := f.item(t, f.notes, two)
	require.False(t, done.AckedAt.IsZero())
	require.Equal(t, []string{"", "m2"}, []string{done.Text, done.ExternalID}, "an acknowledged item keeps its acknowledgement and its platform id")
	require.True(t, f.holds(f.atlas, elsewhere), "the other connection's item stays with Atlas")
	require.False(t, f.holds(f.notes, elsewhere))

	// Atlas's session still holds the first item in flight and acknowledges it: nothing happens to
	// Notes's item. Acknowledgements are handled in order, so once the second one is recorded the
	// first was handled.
	atlas.AckPlatform(one)
	atlas.AckPlatform(elsewhere)
	f.acked(t, f.atlas, elsewhere)
	still := f.item(t, f.notes, one)
	require.True(t, still.AckedAt.IsZero(), "the old operator cannot acknowledge what moved")
	require.Equal(t, "first text", still.Text)
	require.EqualValues(t, 2, f.connection(t).Queued)

	// One audit entry for the change, with the operator before and after and what moved.
	entries := connectorOperatorAudits(admin, f.conn.ID)
	require.Len(t, entries, 2, "choosing Atlas, then the change to Notes")
	e := entries[0]
	require.Equal(t, []any{f.alice.User.ID, "connector", f.conn.ID}, []any{e.Actor.ID, e.TargetKind, e.TargetID})
	require.Equal(t, []any{f.atlas, f.notes, "wa", float64(3)}, []any{e.Detail["from"], e.Detail["to"], e.Detail["name"], e.Detail["moved"]})
	require.Equal(t, float64(0), entries[1].Detail["moved"], "nothing was queued when Atlas was chosen")
}

// CID-05, CID-08: a new operator that is offline gets the moved items when its session connects,
// oldest first, interleaved by the order they arrived in and not by who held them; the old
// operator's session does not.
func TestOperatorChangeDeliversToANewOperatorThatConnectsLater(t *testing.T) {
	f := operatedPlatformWS(t)
	one := f.enqueue(t, f.atlas, f.group, "m1", "first")
	two := f.enqueue(t, f.atlas, f.dm, "m2", "second")
	three := f.enqueue(t, f.atlas, f.group, "m3", "third")
	f.setOperator(f.alice, f.notes).RequireStatus(http.StatusOK)

	atlas := f.dial(t, "Atlas")
	atlas.ExpectNone(300 * time.Millisecond)
	notes := f.dial(t, "Notes")
	require.Equal(t, []string{one, two, three}, platformIDs(t, notes, 3))
	notes.ExpectNone(300 * time.Millisecond)
}

// CID-05: an item that arrives after the change is the new operator's, and the old operator keeps
// nothing of the connection: the connection has one operator at a time.
func TestOperatorChangeLeavesNoItemWithTheOldOperator(t *testing.T) {
	f := operatedPlatformWS(t)
	f.enqueue(t, f.atlas, f.group, "m1", "before the change")
	f.setOperator(f.alice, f.notes).RequireStatus(http.StatusOK)

	require.Empty(t, unackedOf(t, f, f.atlas), "nothing of the connection is left with Atlas")
	require.Len(t, unackedOf(t, f, f.notes), 1)
	f.enqueue(t, f.notes, f.group, "m2", "after the change")
	require.Len(t, unackedOf(t, f, f.notes), 2)
	require.EqualValues(t, 2, f.connection(t).Queued)
}

func unackedOf(t *testing.T, f *platformWS, agent string) []platform.Event {
	t.Helper()
	list, err := f.env.App.Platform.Unacked(context.Background(), agent, 0, 1000)
	require.NoError(t, err)
	return list
}

// CID-05: the change is one transaction. When any step fails, the operator, the items, the other
// edits of the same request and the audit trail are what they were; the same request works once
// the cause is gone. Each case fails a different step, so the rollback is shown from both sides:
// the move failing after the operator was written, and the audit entry failing after the move.
func TestOperatorChangeIsAllOrNothing(t *testing.T) {
	for _, c := range []struct{ name, trigger string }{
		{"the queue cannot be moved", "CREATE TRIGGER forced_failure BEFORE UPDATE ON platform_items BEGIN SELECT RAISE(ABORT, 'forced failure'); END"},
		{"the audit entry cannot be written", "CREATE TRIGGER forced_failure BEFORE INSERT ON audit_log WHEN NEW.action = 'connector.operator' BEGIN SELECT RAISE(ABORT, 'forced failure'); END"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := operatedPlatformWS(t)
			admin := f.env.NewAdmin("root")
			one := f.enqueue(t, f.atlas, f.group, "m1", "first text")
			two := f.enqueue(t, f.atlas, f.dm, "m2", "second text")
			require.NoError(t, f.env.App.Platform.Ack(context.Background(), f.atlas, two))
			drop := forceFailure(t, f.env, c.trigger)

			rename, to := "renamed together with the operator", f.notes
			f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{Name: &rename, OperatorID: &to}).
				RequireError(http.StatusInternalServerError, dto.ErrorCodeInternal)

			got := f.connection(t)
			require.Equal(t, []any{f.atlas, "wa", int64(1)}, []any{got.Operator.ID, got.Name, got.Queued}, "operator, name and queue are unchanged")
			require.True(t, f.holds(f.atlas, one) && f.holds(f.atlas, two), "both items are still Atlas's")
			require.False(t, f.holds(f.notes, one) || f.holds(f.notes, two), "nothing reached Notes")
			require.Equal(t, "first text", f.item(t, f.atlas, one).Text)
			require.Len(t, connectorOperatorAudits(admin, f.conn.ID), 1, "only the choice of Atlas is recorded")
			for _, e := range auditPage(admin, "").Items {
				require.NotEqual(t, "connector.update", e.Action, "the rename of the refused request was not recorded either")
			}
			f.enqueue(t, f.atlas, f.group, "m3", "a message that arrives meanwhile")

			drop("DROP TRIGGER forced_failure")
			f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{Name: &rename, OperatorID: &to}).RequireStatus(http.StatusOK)
			got = f.connection(t)
			require.Equal(t, []any{f.notes, rename, int64(2)}, []any{got.Operator.ID, got.Name, got.Queued})
			require.True(t, f.holds(f.notes, one) && f.holds(f.notes, two), "both moved")
			entries := connectorOperatorAudits(admin, f.conn.ID)
			require.Len(t, entries, 2)
			require.Equal(t, float64(3), entries[0].Detail["moved"], "the message that arrived meanwhile moved too")
		})
	}
}

// CID-05, CID-01 (authorization matrix of PATCH /connectors/{id}): only the admins of the connection's
// organization change the operator and move its queue. A member gets 403, a stranger, someone who sees the
// operator but not the connection, and the admin of the instance get 404, anonymous callers 401, and AI tokens,
// the operator's own and another user's included, 403. An operator that is not an AI of the organization is
// a 422. Every refusal leaves the items with the operator, the audit trail untouched, and the
// operator's session without a frame.
func TestRefusedOperatorChangesLeaveTheQueueAlone(t *testing.T) {
	f := newOperatorFixture(t)
	f.operate(t)
	one := queueItem(t, f.env, f.conn.ID, f.atlas, "120363000000000001@g.us", "m1")
	two := queueItem(t, f.env, f.conn.ID, f.atlas, "120363000000000001@g.us", "m2")
	require.NoError(t, f.env.App.Platform.Ack(context.Background(), f.atlas, two))
	atlas, resp := f.alice.Token(dto.TokenKindAgent, "laptop").DialPlatform("Atlas")
	require.NotNil(t, atlas, "upgrade refused: %d %s", resp.Status, resp.Body)
	atlas.Welcome(wsWait)
	require.Equal(t, one, atlas.Platform(wsWait).Event.ID)

	other := f.notes
	req := dto.UpdateConnectorRequest{OperatorID: &other}
	patch := func(c *testkit.Client) *testkit.Response { return c.Patch("/api/v1/connectors/"+f.conn.ID, req) }
	patch(f.bob).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden) // a member
	patch(f.carol).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound) // stranger
	patch(f.dave).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)  // sees Atlas, not the connection
	patch(f.admin).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound) // admins of the instance get no implicit access
	patch(f.env.Anon()).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	owner := f.alice.Token(dto.TokenKindAgent, "laptop")
	patch(owner).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	patch(owner.AsAgent("Atlas")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden) // the operator itself
	patch(owner.AsAgent("Notes")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden) // the AI that would receive it
	patch(f.bob.Token(dto.TokenKindAgent, "laptop").AsAgent("Bobby")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	for _, bad := range []string{f.bobby, f.alice.User.ID, ""} { // not an AI of the connection's organization
		invalid := bad
		e := f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{OperatorID: &invalid}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, map[string]string{"operator_id": "must be an AI of the connection's organization"}, e.Fields)
	}

	for _, id := range []string{one, two} {
		_, err := f.env.App.Platform.Get(context.Background(), f.atlas, id)
		require.NoError(t, err, "item %s is still Atlas's", id)
		_, err = f.env.App.Platform.Get(context.Background(), f.notes, id)
		require.ErrorIs(t, err, platform.ErrNotFound, "item %s did not reach Notes", id)
	}
	require.Len(t, f.operatorAudits(), 1, "only the choice of Atlas is recorded")
	require.EqualValues(t, 1, f.connector(f.alice).Queued)
	atlas.ExpectNone(300 * time.Millisecond)

	// The owner can, and the first choice moved nothing.
	f.setOperator(f.alice, f.notes).RequireStatus(http.StatusOK)
	audits := f.operatorAudits()
	require.Len(t, audits, 2)
	require.Equal(t, float64(2), audits[0].Detail["moved"])
	require.Equal(t, float64(0), audits[1].Detail["moved"])
}

// CID-05: a connection that is running keeps running when one AI replaces another: its session
// with the platform is not restarted, so nothing that arrives meanwhile is lost to a reconnect
// (nothing the connector runs depends on the operator; the pipeline reads it per message).
func TestOperatorChangeDoesNotRestartARunningConnection(t *testing.T) {
	f := newOperatorFixture(t)
	inst := f.operate(t)
	inst.Connected()

	f.setOperator(f.alice, f.notes).RequireStatus(http.StatusOK)

	require.Same(t, inst, f.fake.Lookup(f.conn.ID), "no new connector was started")
	got := f.connector(f.alice)
	require.Equal(t, []any{dto.ConnectorStatusConnected, f.notes}, []any{got.Status, got.Operator.ID},
		"still connected: a restart would report connecting until the new session is up")
}
