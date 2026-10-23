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
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// teamChat is the platform chat id the queue helpers use for a group.
const teamChat = "120363000000000001@g.us"

// countRows runs a query that selects one number on a read-only connection to the app's
// database, so that tests can look at rows no API shows (acknowledged items, files).
func countRows(t *testing.T, env *testkit.Env, query string, args ...any) int {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(env.App.Config.DataDir, app.DatabaseFile)+"?mode=ro&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	var n int
	require.NoError(t, raw.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

// itemHeldBy tells whether the item is among the AI's queue items, acknowledged or not.
func itemHeldBy(env *testkit.Env, agent, id string) bool {
	_, err := env.App.Platform.Get(context.Background(), agent, id)
	return err == nil
}

// queueFile queues a message with one stored file for the AI on the connection and returns its id.
func queueFile(t *testing.T, env *testkit.Env, connectorID, aiID, external string) string {
	t.Helper()
	ctx := context.Background()
	c, err := connectors.UpsertChat(ctx, env.App.Store, env.App.Bus, connectorID, connectors.ChatInfo{ID: teamChat, Title: "Support", Kind: "group"}, time.Now(), time.Now())
	require.NoError(t, err)
	id, queued, err := env.App.Platform.Enqueue(ctx, platform.Item{
		AgentID: aiID, ConnectorID: connectorID, ChatID: c.ID, ExternalID: external, SenderName: "Pat", Text: "see the file",
		Attachments: []platform.Attachment{{Name: "photo.png", Mime: "image/png", Size: 3, SHA256: "ab" + external, Status: dto.AttachmentStatusStored}},
	})
	require.NoError(t, err)
	require.True(t, queued)
	return id
}

// addDropped adds n to the AI's count of dropped platform messages, as the bound and the 7-day
// expiry do.
func addDropped(t *testing.T, env *testkit.Env, agent string, n int64) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.AddPlatformDropped(ctx, db.AddPlatformDroppedParams{N: n, AgentID: agent})
	}))
}

func droppedOf(t *testing.T, env *testkit.Env, agent string) int64 {
	t.Helper()
	n, err := env.App.Store.R().GetPlatformDropped(context.Background(), agent)
	require.NoError(t, err)
	return n
}

// CID-24, CID-02: when the owner deletes the AI that operates a connection, the AI's queue goes
// with it (acknowledged items and files too), the connection has no operator, stops, and shows
// what to do, on the live socket as well; the owner's other connections keep their operators,
// their sessions and their queues. The owner can choose another operator afterwards, and the
// connection runs again with no stale detail.
func TestDeletingTheOperatorRemovesItsQueueAndStopsItsConnection(t *testing.T) {
	f := newOperatorFixture(t)
	inst := f.operate(t)
	inst.Connected()
	tg := createFakeIdle(f.alice, "tg", true)
	f.alice.OperateConnector(tg.ID, f.notes)
	tgInst := f.fake.Instance(t, tg.ID)
	tgInst.WaitRun(t)
	tgInst.Connected()
	live, _ := f.alice.DialLive("")
	ctx := context.Background()

	one := queueItem(t, f.env, f.conn.ID, f.atlas, teamChat, "m1")
	two := queueItem(t, f.env, f.conn.ID, f.atlas, teamChat, "m2")
	require.NoError(t, f.env.App.Platform.Ack(ctx, f.atlas, two))
	queueFile(t, f.env, f.conn.ID, f.atlas, "m3")
	kept := queueItem(t, f.env, tg.ID, f.notes, "chat-9", "n1")
	rows := "SELECT COUNT(*) FROM platform_items WHERE connector_id = ?"
	require.Equal(t, 3, countRows(t, f.env, rows, f.conn.ID))
	require.Equal(t, 1, countRows(t, f.env, "SELECT COUNT(*) FROM platform_attachments"))
	require.EqualValues(t, 2, f.connector(f.alice).Queued)

	f.alice.Delete("/api/v1/agents/" + f.atlas).RequireStatus(http.StatusNoContent)

	inst.WaitStop(t)
	var st dto.ConnectorStatusData
	live.Expect(dto.LiveEventTypeConnectorStatus, &st, connectortest.Timeout)
	require.Equal(t, dto.ConnectorStatusData{ConnectorID: f.conn.ID, Status: dto.ConnectorStatusError, LastError: noOperatorDetail}, st)
	got := f.connector(f.alice)
	require.Nil(t, got.Operator, "the connection has no operator")
	require.Equal(t, []any{dto.ConnectorStatusError, noOperatorDetail, true, int64(0)}, []any{got.Status, got.LastError, got.Enabled, got.Queued})
	require.Equal(t, noOperatorDetail, f.connector(f.bob).LastError, "a use grantee sees the same")
	require.Zero(t, countRows(t, f.env, rows, f.conn.ID), "every item of the connection is gone, acknowledged ones included")
	require.Zero(t, countRows(t, f.env, "SELECT COUNT(*) FROM platform_attachments"), "and their files")
	for _, id := range []string{one, two} {
		require.False(t, itemHeldBy(f.env, f.atlas, id))
	}

	// The other connection is untouched.
	require.Same(t, tgInst, f.fake.Lookup(tg.ID), "no new connector was started")
	other := testkit.Decode[dto.Connector](f.alice.Get("/api/v1/connectors/" + tg.ID).RequireStatus(http.StatusOK))
	require.Equal(t, []any{dto.ConnectorStatusConnected, f.notes, int64(1)}, []any{other.Status, other.Operator.ID, other.Queued})
	require.True(t, itemHeldBy(f.env, f.notes, kept))

	// Choosing an operator again starts the connection, and the detail goes away.
	f.alice.OperateConnector(f.conn.ID, f.notes)
	again := f.fake.Instance(t, f.conn.ID)
	require.NotSame(t, inst, again)
	again.WaitRun(t)
	require.Empty(t, f.connector(f.alice).LastError)
	require.Equal(t, f.notes, f.connector(f.alice).Operator.ID)
}

// CID-24: deleting an AI that operates nothing leaves every connection alone: nothing is applied
// again, so no running connection loses its session with the platform.
func TestDeletingAnAIThatOperatesNothingTouchesNoConnection(t *testing.T) {
	f := newOperatorFixture(t)
	inst := f.operate(t)
	inst.Connected()
	bystander := f.alice.NewAI("Bystander")

	f.alice.Delete("/api/v1/agents/" + bystander).RequireStatus(http.StatusNoContent)

	require.Never(t, func() bool {
		return f.fake.Lookup(f.conn.ID) != inst || f.connector(f.alice).Status != dto.ConnectorStatusConnected
	}, 300*time.Millisecond, 20*time.Millisecond, "the connection was applied again")
	require.Equal(t, f.atlas, f.connector(f.alice).Operator.ID)
}

// CID-24 (authorization matrix of DELETE /agents/{id}): only whoever manages the AI deletes it, so only
// they orphan its connection. Holders of a use grant, which gives nothing since AD-016, a stranger and
// the admin get 404, anonymous callers 401, and AI tokens, the operator itself and another user's AI
// included, 403. Every
// refusal leaves the operator, the connection's session, its status and its queue as they were.
func TestRefusedAgentDeletesLeaveTheOperatorsConnectionAlone(t *testing.T) {
	f := newOperatorFixture(t)
	inst := f.operate(t)
	inst.Connected()
	kept := queueItem(t, f.env, f.conn.ID, f.atlas, teamChat, "m1")

	del := func(c *testkit.Client) *testkit.Response { return c.Delete("/api/v1/agents/" + f.atlas) }
	del(f.bob).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)  // may use the connection; a use grant on Atlas gives nothing
	del(f.dave).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound) // holds a use grant on Atlas only
	del(f.carol).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	del(f.admin).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	del(f.env.Anon()).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	owner := f.alice.Token(dto.TokenKindAgent, "laptop")
	del(owner).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	del(owner.AsAgent("Atlas")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden) // the operator deleting itself
	del(owner.AsAgent("Notes")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	del(f.bob.Token(dto.TokenKindAgent, "laptop").AsAgent("Bobby")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	require.Never(t, func() bool {
		return f.fake.Lookup(f.conn.ID) != inst || f.connector(f.alice).Status != dto.ConnectorStatusConnected
	}, 300*time.Millisecond, 20*time.Millisecond, "a refusal applied the connection again")
	got := f.connector(f.alice)
	require.Equal(t, []any{f.atlas, "", int64(1)}, []any{got.Operator.ID, got.LastError, got.Queued})
	require.True(t, itemHeldBy(f.env, f.atlas, kept), "the queue is intact")

	del(f.alice).RequireStatus(http.StatusNoContent)
	inst.WaitStop(t)
	// Apply stops the connector before it persists the CID-02 detail, so a loaded machine can read
	// the row in between; wait for the write instead of racing it.
	require.Eventually(t, func() bool {
		got := f.connector(f.alice)
		return got.Status == dto.ConnectorStatusError && got.LastError == noOperatorDetail
	}, connectortest.Timeout, 20*time.Millisecond, "the connection shows the CID-02 detail")
}

// mergedWorld is alice's merge world with three connections: "wa" operated by the AI that is
// merged away (old), "tg" by another AI (orion) and "dc" by the survivor (atlas). Items arrive in
// the order first (dc, atlas), m1, m2, m3 (wa, old; m3 acknowledged), o1 (tg, orion), last (dc,
// atlas).
type mergedWorld struct {
	*mergeWorld
	orion             string
	wa, tg, dc        dto.Connector
	first, m1, m2, m3 string
	o1, last          string
}

func newMergedWorld(t *testing.T) *mergedWorld {
	t.Helper()
	w := &mergedWorld{mergeWorld: newMergeWorld(t)}
	w.orion = w.alice.NewAI("orion")
	w.wa, w.tg, w.dc = createFakeIdle(w.alice, "wa", true), createFakeIdle(w.alice, "tg", true), createFakeIdle(w.alice, "dc", true)
	w.alice.OperateConnector(w.wa.ID, w.old)
	w.alice.OperateConnector(w.tg.ID, w.orion)
	w.alice.OperateConnector(w.dc.ID, w.atlas)
	w.first = queueItem(t, w.env, w.dc.ID, w.atlas, "room-1", "a1")
	w.m1 = queueItem(t, w.env, w.wa.ID, w.old, teamChat, "m1")
	w.m2 = queueItem(t, w.env, w.wa.ID, w.old, teamChat, "m2")
	w.m3 = queueItem(t, w.env, w.wa.ID, w.old, teamChat, "m3")
	require.NoError(t, w.env.App.Platform.Ack(context.Background(), w.old, w.m3))
	w.o1 = queueItem(t, w.env, w.tg.ID, w.orion, "chat-9", "o1")
	w.last = queueItem(t, w.env, w.dc.ID, w.atlas, "room-1", "a2")
	addDropped(t, w.env, w.old, 2)
	addDropped(t, w.env, w.atlas, 1)
	return w
}

// CID-05, MRG-01: merging the AI that operates a connection into another hands the connection's
// queue to the survivor, acknowledged items included, ordered with what the survivor already held
// by arrival; another AI's connection and the survivor's own items stay; the AI's count of dropped
// messages joins the survivor's, so the survivor is told about all of them on its next
// connection; and the merge audit entry says how many items moved.
func TestMergeHandsTheQueueOfItsConnectionsToTheSurvivor(t *testing.T) {
	w := newMergedWorld(t)

	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)

	for _, id := range []string{w.m1, w.m2, w.m3} {
		require.True(t, itemHeldBy(w.env, w.atlas, id), "item %s belongs to the survivor", id)
	}
	wa := testkit.Decode[dto.Connector](w.alice.Get("/api/v1/connectors/" + w.wa.ID).RequireStatus(http.StatusOK))
	require.Equal(t, []any{w.atlas, int64(2)}, []any{wa.Operator.ID, wa.Queued}, "the connection is the survivor's with its two unacknowledged items")
	e, err := w.env.App.Platform.Get(context.Background(), w.atlas, w.m3)
	require.NoError(t, err)
	require.False(t, e.AckedAt.IsZero(), "an acknowledged item keeps its state (a reply to it keeps working)")
	require.True(t, itemHeldBy(w.env, w.orion, w.o1), "another AI's connection is untouched")
	require.True(t, itemHeldBy(w.env, w.atlas, w.first) && itemHeldBy(w.env, w.atlas, w.last), "the survivor's own items stay")
	require.Zero(t, countRows(t, w.env, "SELECT COUNT(*) FROM platform_items WHERE agent_id = ?", w.old), "nothing is left with the merged AI")

	// What the survivor is told when its session connects: everything dropped, then its queue by arrival.
	conn, resp := w.alice.Token(dto.TokenKindAgent, "laptop").DialPlatform("atlas")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(connectortest.Timeout)
	require.Equal(t, "3 platform messages were dropped (older than 7 days or over 5,000 queued)", conn.Notice(connectortest.Timeout).Text,
		"the 2 of the merged AI and the survivor's own 1")
	require.Equal(t, []string{w.first, w.m1, w.m2, w.last}, platformIDs(t, conn, 4))
	conn.ExpectNone(300 * time.Millisecond)

	entry := findAudit(t, auditPage(w.admin, "").Items, "agent.merge")
	require.Equal(t, []any{[]any{w.wa.ID}, float64(3)}, []any{entry.Detail["connectors"], entry.Detail["platform_items"]})
}

// CID-05, MRG-01: a survivor that is online when the merge commits receives the items it was
// handed without reconnecting, oldest first, behind what it already held.
func TestMergeWakesTheSurvivorsSession(t *testing.T) {
	w := newMergedWorld(t)
	conn, resp := w.alice.Token(dto.TokenKindAgent, "laptop").DialPlatform("atlas")
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(connectortest.Timeout)
	require.Equal(t, "1 platform messages were dropped (older than 7 days or over 5,000 queued)", conn.Notice(connectortest.Timeout).Text)
	require.Equal(t, []string{w.first, w.last}, platformIDs(t, conn, 2))
	conn.ExpectNone(300 * time.Millisecond)

	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)

	require.Equal(t, []string{w.m1, w.m2}, platformIDs(t, conn, 2), "the unacknowledged items it was handed, oldest first")
	conn.ExpectNone(300 * time.Millisecond)
}

// CID-05: the merge is one transaction, queue included: when its last step fails the items, the
// dropped counts and the connection's operator are what they were.
func TestMergeLeavesTheQueueAloneWhenItFails(t *testing.T) {
	w := newMergedWorld(t)
	drop := forceFailure(t, w.env, "CREATE TRIGGER forced_failure BEFORE DELETE ON agents WHEN OLD.name = 'old' BEGIN SELECT RAISE(ABORT, 'forced failure'); END")

	merge(w.alice, w.old, w.atlas).RequireError(http.StatusInternalServerError, dto.ErrorCodeInternal)

	for _, id := range []string{w.m1, w.m2, w.m3} {
		require.True(t, itemHeldBy(w.env, w.old, id), "item %s is still the merged AI's", id)
		require.False(t, itemHeldBy(w.env, w.atlas, id), "item %s did not reach the survivor", id)
	}
	require.Equal(t, []int64{2, 1}, []int64{droppedOf(t, w.env, w.old), droppedOf(t, w.env, w.atlas)}, "the counts did not move")
	require.Equal(t, w.old, testkit.Decode[dto.Connector](w.alice.Get("/api/v1/connectors/"+w.wa.ID).RequireStatus(http.StatusOK)).Operator.ID)
	require.NotContains(t, auditActions(t, w.admin), "agent.merge")

	drop("DROP TRIGGER forced_failure")
	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)
	require.True(t, itemHeldBy(w.env, w.atlas, w.m1))
	require.EqualValues(t, 3, droppedOf(t, w.env, w.atlas))
}

// CID-05 (authorization matrix of POST /agents/{id}/merge): only whoever manages both AIs merges. The
// holder of a use grant on the merged AI, a stranger and the admin get 404, anonymous callers 401 and AI
// tokens 403. A refusal leaves the queue with the merged AI, the dropped counts and the connection
// as they were; a merge the owner makes moves the queue.
func TestRefusedMergesLeaveTheQueueAlone(t *testing.T) {
	w := newMergedWorld(t)
	shareAI(w.alice, w.old, w.bob)

	merge(w.bob, w.old, w.atlas).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound) // a use grant gives nothing
	merge(w.carol, w.old, w.atlas).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	merge(w.admin, w.old, w.atlas).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	merge(w.env.Anon(), w.old, w.atlas).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	merge(w.alice.Token(dto.TokenKindAgent, "laptop"), w.old, w.atlas).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	merge(w.alice.Token(dto.TokenKindAgent, "laptop").AsAgent("old"), w.old, w.atlas).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	merge(w.alice.Token(dto.TokenKindAgent, "laptop").AsAgent("atlas"), w.old, w.atlas).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	for _, id := range []string{w.m1, w.m2, w.m3} {
		require.True(t, itemHeldBy(w.env, w.old, id), "item %s is still the merged AI's", id)
	}
	require.Equal(t, []int64{2, 1}, []int64{droppedOf(t, w.env, w.old), droppedOf(t, w.env, w.atlas)})
	require.Equal(t, w.old, testkit.Decode[dto.Connector](w.alice.Get("/api/v1/connectors/"+w.wa.ID).RequireStatus(http.StatusOK)).Operator.ID)

	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)
	require.True(t, itemHeldBy(w.env, w.atlas, w.m1))
}
