package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// alertTarget is one row of ListConnectorAlertTargets, reduced to the account and the device.
type alertTarget struct {
	user   string
	device string
}

// alertConnector inserts a connection; an empty org or operator writes NULL.
func (e *schemaEnv) alertConnector(id, org, operator string) {
	e.t.Helper()
	var orgID, operatorIID any
	if org != "" {
		orgID = org
	}
	if operator != "" {
		operatorIID = operator
	}
	e.must(`INSERT INTO connectors (id, owner_id, kind, name, org_id, operator_iid, created_at, updated_at)
		VALUES (?, 'u1', 'whatsapp', ?, ?, ?, 1, 1)`, id, "conn-"+id, orgID, operatorIID)
}

// alertAgent inserts an AI with its intelligence row, so it can hold a membership and operate a
// connection.
func (e *schemaEnv) alertAgent(id, ownerID string) {
	e.t.Helper()
	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES (?, ?, ?, 1)`, id, ownerID, "agent-"+id)
	require.NoError(e.t, e.intelligence(id, "ai", id))
}

// alertDevice registers a device of an account with an explicit registration order.
func (e *schemaEnv) alertDevice(id, userID string, created int64) {
	e.t.Helper()
	e.pushSubscription(id, userID, "", "https://push.example.test/"+id, "p256dh-"+id, "auth-"+id)
	e.must(`UPDATE push_subscriptions SET created_at = ? WHERE id = ?`, created, id)
}

// alertFixture builds the organization of ALERT-02: two admins (one with two devices, one without
// any), a disabled admin, a member, a guest, an AI admin and a second AI hosted by a member that
// operates the connection k1.
func (e *schemaEnv) alertFixture() {
	e.t.Helper()
	require.NoError(e.t, e.org("o1", "acme"))
	e.user("u1", "alice")
	e.user("u2", "alex")
	e.user("u3", "bea")
	e.user("u4", "gus")
	e.user("u5", "carol")
	e.user("u6", "dan")

	require.NoError(e.t, e.member("o1", "u1", "admin"))
	require.NoError(e.t, e.member("o1", "u2", "admin"))
	require.NoError(e.t, e.member("o1", "u6", "admin"))
	require.NoError(e.t, e.member("o1", "u3", "member"))
	require.NoError(e.t, e.member("o1", "u4", "guest"))
	e.alertAgent("a1", "u2")
	require.NoError(e.t, e.member("o1", "a1", "admin"))
	e.alertAgent("a2", "u5")

	e.alertConnector("k1", "o1", "a2")

	e.alertDevice("p1", "u1", 1)
	e.alertDevice("p2", "u1", 2)
	e.alertDevice("p3", "u3", 1)
	e.alertDevice("p4", "u4", 1)
	e.alertDevice("p5", "u5", 1)
	e.alertDevice("p6", "u6", 1)
	e.must(`UPDATE users SET disabled_at = 1 WHERE id = 'u6'`)
}

// alertTargets reads the target rows of a connection as (account, device) pairs, in query order.
func (e *schemaEnv) alertTargets(connectorID string) []alertTarget {
	e.t.Helper()
	rows, err := e.s.R().ListConnectorAlertTargets(context.Background(), connectorID)
	require.NoError(e.t, err)
	out := make([]alertTarget, 0, len(rows))
	for _, row := range rows {
		out = append(out, alertTarget{user: row.UserID, device: row.SubscriptionID})
	}
	return out
}

// ALERT-02, ALERT-13: one row per (responsible enabled account, device) — the admins of the
// connection's organization and the host of its operator AI — and the group setting is ignored.
func TestConnectorAlertTargetsAreTheAdminsAndTheOperatorsHost(t *testing.T) {
	e := newSchemaEnv(t)
	e.alertFixture()

	// u2 (an admin), u3 (a member), u4 (a guest) and a1 (an AI admin) have no row: only u6 has a
	// device among them, and its disabled account keeps it out of the audience.
	expected := []alertTarget{{user: "u1", device: "p1"}, {user: "u1", device: "p2"}, {user: "u5", device: "p5"}}
	require.Equal(t, expected, e.alertTargets("k1"))

	rows, err := e.s.R().ListConnectorAlertTargets(context.Background(), "k1")
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, "u1", rows[0].UserID)
	require.Equal(t, "https://push.example.test/p1", rows[0].Endpoint)
	require.Equal(t, "p256dh-p1", rows[0].P256dh)
	require.Equal(t, "auth-p1", rows[0].Auth)

	// ALERT-13: the alert follows the devices, never the account's group setting.
	e.must(`UPDATE users SET push_groups = 'off' WHERE id = 'u1'`)
	require.Equal(t, expected, e.alertTargets("k1"))
}

// ALERT-02: an admin who also hosts the operator AI is one responsible account, so the UNION
// dedupes the account and keeps every device.
func TestConnectorAlertTargetsNotifyAnAdminWhoAlsoHostsTheOperatorOnce(t *testing.T) {
	e := newSchemaEnv(t)
	e.alertFixture()
	e.must(`UPDATE agents SET owner_id = 'u1' WHERE id = 'a2'`)

	require.Equal(t, []alertTarget{{user: "u1", device: "p1"}, {user: "u1", device: "p2"}}, e.alertTargets("k1"))
}

// ALERT-02: a connection without an organization still alerts its operator's host, one without an
// operator still alerts the admins, and one with neither alerts nobody.
func TestConnectorAlertTargetsWithoutAnOrganizationOrOperator(t *testing.T) {
	e := newSchemaEnv(t)
	e.alertFixture()
	e.alertConnector("k2", "", "a2")
	e.alertConnector("k3", "o1", "")
	e.alertConnector("k4", "", "")

	require.Equal(t, []alertTarget{{user: "u5", device: "p5"}}, e.alertTargets("k2"))
	require.Equal(t, []alertTarget{{user: "u1", device: "p1"}, {user: "u1", device: "p2"}}, e.alertTargets("k3"))
	require.Empty(t, e.alertTargets("k4"))
}

// ALERT-10: the seed reads every stored status, stopped included, in id order.
func TestListConnectorStatusesReturnsEveryStoredStatus(t *testing.T) {
	e := newSchemaEnv(t)
	e.user("u1", "alice")
	e.alertConnector("k1", "", "")
	e.alertConnector("k2", "", "")
	e.alertConnector("k3", "", "")
	e.must(`UPDATE connectors SET status = 'error' WHERE id = 'k1'`)
	e.must(`UPDATE connectors SET status = 'stopped' WHERE id = 'k2'`)
	e.must(`UPDATE connectors SET status = 'connected' WHERE id = 'k3'`)

	rows, err := e.s.R().ListConnectorStatuses(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, "k1", rows[0].ID)
	require.Equal(t, "error", rows[0].Status)
	require.Equal(t, "k2", rows[1].ID)
	require.Equal(t, "stopped", rows[1].Status)
	require.Equal(t, "k3", rows[2].ID)
	require.Equal(t, "connected", rows[2].Status)

	e.must(`DELETE FROM connectors WHERE id = 'k2'`)
	rows, err = e.s.R().ListConnectorStatuses(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "k1", rows[0].ID)
	require.Equal(t, "k3", rows[1].ID)
}

// ALERT-12: the events of a deleted connection find no target, so nothing is sent.
func TestConnectorAlertTargetsOfADeletedConnector(t *testing.T) {
	e := newSchemaEnv(t)
	e.alertFixture()
	e.must(`DELETE FROM connectors WHERE id = 'k1'`)

	require.Empty(t, e.alertTargets("k1"))
}
