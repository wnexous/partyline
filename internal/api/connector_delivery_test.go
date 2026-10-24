package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/testkit"
)

// queueText is a synthetic message text that must never show in a connector view.
const queueText = "needle-text-of-a-queued-message"

// queueItem queues a message of the platform chat for the AI on the connection and returns its
// id (the connection's chat is created when it is new).
func queueItem(t *testing.T, env *testkit.Env, connectorID, aiID, chat, external string) string {
	t.Helper()
	ctx := context.Background()
	c, err := connectors.UpsertChat(ctx, env.App.Store, env.App.Bus, connectorID, connectors.ChatInfo{ID: chat, Title: "Support", Kind: "group"}, time.Now(), time.Now())
	require.NoError(t, err)
	id, queued, err := env.App.Platform.Enqueue(ctx, platform.Item{
		AgentID: aiID, ConnectorID: connectorID, ChatID: c.ID, ExternalID: external, SenderID: "5511999990000", SenderName: "Pat", Text: queueText,
	})
	require.NoError(t, err)
	require.True(t, queued)
	return id
}

// CID-27: a new connection starts with deliver=all and groups_mentions_only on, whatever the
// create body says: the create request has no such fields, only PATCH changes them.
func TestNewConnectionStartsWithTheDeliveryDefaults(t *testing.T) {
	env, _ := fakeConnectorEnv(t)
	alice := env.NewUser("alice")

	body := map[string]any{
		"kind": connectortest.KindName, "name": "wa", "config": map[string]any{"token": "fake-token-not-real"}, "enabled": false,
		"deliver": "dms", "groups_mentions_only": false, "queued": 99,
	}
	created := testkit.Decode[dto.Connector](alice.Post("/api/v1/connectors", body).RequireStatus(http.StatusCreated))

	require.Equal(t, dto.ConnectorDeliverAll, created.Deliver)
	require.True(t, created.GroupsMentionsOnly)
	require.Zero(t, created.Queued, "nothing is queued for a new connection")
	got := testkit.Decode[dto.Connector](alice.Get("/api/v1/connectors/" + created.ID).RequireStatus(http.StatusOK))
	require.Equal(t, []any{dto.ConnectorDeliverAll, true}, []any{got.Deliver, got.GroupsMentionsOnly})
	list := testkit.Decode[dto.List[dto.Connector]](alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items
	require.Equal(t, []any{dto.ConnectorDeliverAll, true}, []any{list[0].Deliver, list[0].GroupsMentionsOnly})
}

// CID-23, CID-27: the owner changes both settings with PATCH; each is read back by the response,
// the connection and the list; one alone leaves the other, absent fields leave both, and the
// change is audited with the setting that changed.
func TestOwnerChangesTheDeliverySettings(t *testing.T) {
	f := newOperatorFixture(t)
	path := "/api/v1/connectors/" + f.conn.ID
	patch := func(req dto.UpdateConnectorRequest) dto.Connector {
		t.Helper()
		return testkit.Decode[dto.Connector](f.alice.Patch(path, req).RequireStatus(http.StatusOK))
	}
	read := func(c dto.Connector) []any { return []any{c.Deliver, c.GroupsMentionsOnly} }
	require.Equal(t, []any{dto.ConnectorDeliverAll, true}, read(f.connector(f.alice)))

	for _, d := range []dto.ConnectorDeliver{dto.ConnectorDeliverDMs, dto.ConnectorDeliverGroups, dto.ConnectorDeliverAll} {
		got := patch(dto.UpdateConnectorRequest{Deliver: &d})
		require.Equal(t, []any{d, true}, read(got), "deliver %s: the response, the mentions setting untouched", d)
		require.Equal(t, []any{d, true}, read(f.connector(f.alice)), "stored")
		require.Equal(t, []any{d, true}, read(f.connector(f.bob)), "a member of the organization sees it too")
	}
	off, on := false, true
	require.Equal(t, []any{dto.ConnectorDeliverAll, false}, read(patch(dto.UpdateConnectorRequest{GroupsMentionsOnly: &off})), "mentions-only off, deliver untouched")
	groups := dto.ConnectorDeliverGroups
	require.Equal(t, []any{dto.ConnectorDeliverGroups, false}, read(patch(dto.UpdateConnectorRequest{Deliver: &groups})), "deliver alone leaves mentions-only off")
	require.Equal(t, []any{dto.ConnectorDeliverGroups, true}, read(patch(dto.UpdateConnectorRequest{GroupsMentionsOnly: &on})))
	name := "renamed"
	require.Equal(t, []any{dto.ConnectorDeliverGroups, true}, read(patch(dto.UpdateConnectorRequest{Name: &name})), "other edits leave both")
	both := dto.ConnectorDeliverDMs
	require.Equal(t, []any{dto.ConnectorDeliverDMs, false}, read(patch(dto.UpdateConnectorRequest{Deliver: &both, GroupsMentionsOnly: &off})), "both in one request")
	list := testkit.Decode[dto.List[dto.Connector]](f.alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items
	require.Equal(t, []any{dto.ConnectorDeliverDMs, false}, read(list[0]))

	// The audit trail names what changed, and a request that changes nothing records nothing.
	var updates []map[string]any
	for _, e := range auditPage(f.admin, "").Items {
		if e.Action == "connector.update" {
			updates = append(updates, e.Detail)
		}
	}
	require.Equal(t, []any{"deliver", "groups_mentions_only"}, updates[0]["changed"], "the newest change: both")
	before := len(updates)
	patch(dto.UpdateConnectorRequest{Deliver: &both, GroupsMentionsOnly: &off})
	updates = updates[:0]
	for _, e := range auditPage(f.admin, "").Items {
		if e.Action == "connector.update" {
			updates = append(updates, e.Detail)
		}
	}
	require.Len(t, updates, before, "the same values change nothing")
}

// CID-27 (L-017, L-018): a deliver other than dms, groups or all is a 422 on deliver, in any case
// or spelling, and a refused request changes nothing, also next to valid edits.
func TestInvalidDeliverIsRefused(t *testing.T) {
	f := newOperatorFixture(t)
	path := "/api/v1/connectors/" + f.conn.ID
	for _, bad := range []string{"everyone", "", "DMs", "ALL", " all", "dm", "off", "groups,dms"} {
		d, renamed, off := dto.ConnectorDeliver(bad), "renamed by a request that is refused", false
		e := f.alice.Patch(path, dto.UpdateConnectorRequest{Deliver: &d, Name: &renamed, GroupsMentionsOnly: &off}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, map[string]string{"deliver": "must be dms, groups or all"}, e.Fields, "deliver %q", bad)
	}
	got := f.connector(f.alice)
	require.Equal(t, []any{"wa", dto.ConnectorDeliverAll, true}, []any{got.Name, got.Deliver, got.GroupsMentionsOnly}, "nothing changed")
}

// CID-23 (authorization matrix): only an admin of the connection's organization changes the delivery
// settings. A member of the organization may not manage the connection (403); a stranger and the admin
// of the instance get 404; anonymous callers 401; AI tokens, the connection's own operator included,
// 403. A refusal changes nothing.
func TestDeliverySettingsAuthorizationMatrix(t *testing.T) {
	f := newOperatorFixture(t)
	f.operate(t)
	groups, off := dto.ConnectorDeliverGroups, false
	req := dto.UpdateConnectorRequest{Deliver: &groups, GroupsMentionsOnly: &off}
	patch := func(c *testkit.Client) *testkit.Response { return c.Patch("/api/v1/connectors/"+f.conn.ID, req) }

	patch(f.bob).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	patch(f.carol).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	patch(f.admin).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	patch(f.env.Anon()).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	owner := f.alice.Token(dto.TokenKindAgent, "laptop")
	patch(owner).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	patch(owner.AsAgent("Atlas")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	patch(f.bob.Token(dto.TokenKindAgent, "laptop").AsAgent("Bobby")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	got := f.connector(f.alice)
	require.Equal(t, []any{dto.ConnectorDeliverAll, true}, []any{got.Deliver, got.GroupsMentionsOnly}, "no refusal changed a setting")

	// Reading is for the users of the connection only.
	f.connector(f.bob)
	f.carol.Get("/api/v1/connectors/"+f.conn.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, dto.ConnectorDeliverGroups, testkit.Decode[dto.Connector](patch(f.alice).RequireStatus(http.StatusOK)).Deliver, "the owner can")
}

// CID-23: queued is the number of the connection's unacknowledged items, shown to the users of
// the connection: acknowledged items, other connections' items and the items other AIs hold of
// this connection's earlier operators all follow the rule "unacked, of this connection".
func TestQueuedCountsTheConnectionsUnackedItems(t *testing.T) {
	f := newOperatorFixture(t)
	other := createFakeIdle(f.alice, "tg", true)
	require.Zero(t, f.connector(f.alice).Queued)

	first := queueItem(t, f.env, f.conn.ID, f.atlas, "chat-1", "m1")
	second := queueItem(t, f.env, f.conn.ID, f.atlas, "chat-1", "m2")
	queueItem(t, f.env, f.conn.ID, f.notes, "chat-2", "m3") // held by another AI of the owner
	queueItem(t, f.env, other.ID, f.atlas, "chat-9", "x1")  // another connection
	read := func(c *testkit.Client, id string) int64 {
		return testkit.Decode[dto.Connector](c.Get("/api/v1/connectors/" + id).RequireStatus(http.StatusOK)).Queued
	}
	require.Equal(t, int64(3), read(f.alice, f.conn.ID), "three unacknowledged items of this connection")
	require.Equal(t, int64(3), read(f.bob, f.conn.ID), "the users of the connection see the same count")
	require.Equal(t, int64(1), read(f.alice, other.ID), "each connection counts its own")

	require.NoError(t, f.env.App.Platform.Ack(context.Background(), f.atlas, first))
	require.Equal(t, int64(2), read(f.alice, f.conn.ID), "an acknowledged item no longer counts")
	require.NoError(t, f.env.App.Platform.Ack(context.Background(), f.notes, second)) // not Notes's item: nothing changes
	require.Equal(t, int64(2), read(f.alice, f.conn.ID))

	byID := map[string]int64{}
	for _, c := range testkit.Decode[dto.List[dto.Connector]](f.alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items {
		byID[c.ID] = c.Queued
	}
	require.Equal(t, map[string]int64{f.conn.ID: 2, other.ID: 1}, byID, "the list carries the counts")
	patched := testkit.Decode[dto.Connector](f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{Name: ptr("renamed")}).RequireStatus(http.StatusOK))
	require.Equal(t, int64(2), patched.Queued, "the update answer carries it too")
}

// CID-23: nothing a connector view returns carries the text of a queued message (nor does the
// chats listing of the connection): the owner sees how many items wait, never what they say.
func TestConnectorViewsCarryNoMessageText(t *testing.T) {
	f := newOperatorFixture(t)
	f.operate(t)
	queueItem(t, f.env, f.conn.ID, f.atlas, "chat-1", "m1")
	acked := queueItem(t, f.env, f.conn.ID, f.atlas, "chat-1", "m2")
	require.NoError(t, f.env.App.Platform.Ack(context.Background(), f.atlas, acked))

	bodies := map[string][]byte{
		"get":    f.alice.Get("/api/v1/connectors/" + f.conn.ID).RequireStatus(http.StatusOK).Body,
		"member": f.bob.Get("/api/v1/connectors/" + f.conn.ID).RequireStatus(http.StatusOK).Body,
		"list":   f.alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK).Body,
		"chats":  f.alice.Get("/api/v1/connectors/" + f.conn.ID + "/chats").RequireStatus(http.StatusOK).Body,
		"patch":  f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{Name: ptr("renamed")}).RequireStatus(http.StatusOK).Body,
	}
	for name, body := range bodies {
		require.False(t, strings.Contains(string(body), queueText), "%s must not carry the text of a queued message", name)
	}
	require.Contains(t, string(bodies["get"]), `"queued":1`, "the count is there")
}
