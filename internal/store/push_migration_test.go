package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// pushUser inserts an account and the intelligence it shares its id with (PROF-01).
func (e *schemaEnv) pushUser(id, username string) {
	e.t.Helper()
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, 'x', 'member', 1)`,
		id, username)
	require.NoError(e.t, e.intelligence(id, "human", username))
}

// pushSession inserts a login session of an account.
func (e *schemaEnv) pushSession(id, userID string) {
	e.t.Helper()
	e.must(`INSERT INTO sessions (id, token_hash, user_id, created_at, expires_at, last_seen_at)
		VALUES (?, ?, ?, 1, 2, 1)`, id, "hash-"+id, userID)
}

// pushSubscription inserts a browser subscription, without a session when sessionID is empty.
func (e *schemaEnv) pushSubscription(id, userID, sessionID, endpoint, p256dh, auth string) {
	e.t.Helper()
	var session any
	if sessionID != "" {
		session = sessionID
	}
	e.must(`INSERT INTO push_subscriptions (id, user_id, session_id, endpoint, p256dh, auth, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 1)`, id, userID, session, endpoint, p256dh, auth)
}

// PUSH-03, PUSH-06, PUSH-07, PUSH-09 (data): migration 0025 adds the subscription table with its three
// indexes and the group setting to accounts that existed before it, defaulting to mentions.
func TestMigration0025AddsPushSubscriptionsAndTheSetting(t *testing.T) {
	ctx := context.Background()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0025")}
	require.Zero(t, e.count(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'push_groups'`),
		"fixture sanity: an account before 0025 has no group setting")
	e.pushUser("u-alice", "alice")

	require.NoError(t, e.s.applyMigration(ctx, "0025_push_subscriptions.sql"))
	require.Contains(t, versions(t, e.s), "0025_push_subscriptions")

	require.Equal(t, "mentions", e.str(`SELECT push_groups FROM users WHERE id = 'u-alice'`),
		"an account that existed before 0025 starts with mentions")
	require.Equal(t, []string{"id", "user_id", "session_id", "endpoint", "p256dh", "auth", "created_at", "last_ok_at"},
		e.columnsOf("push_subscriptions"))
	for _, index := range []string{"push_subscriptions_endpoint", "push_subscriptions_user", "push_subscriptions_session"} {
		require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index), index)
	}

	e.must(`INSERT INTO push_subscriptions (id, user_id, session_id, endpoint, p256dh, auth, created_at)
		VALUES ('s1', 'u-alice', NULL, 'https://push.example.test/one', 'key', 'auth', 1)`)
	require.Error(t, e.exec(`INSERT INTO push_subscriptions (id, user_id, session_id, endpoint, p256dh, auth, created_at)
		VALUES ('s2', 'u-alice', NULL, 'https://push.example.test/one', 'other', 'auth', 2)`),
		"an endpoint belongs to one row")

	require.Error(t, e.exec(`UPDATE users SET push_groups = 'none' WHERE id = 'u-alice'`), "unknown setting")
	for _, setting := range []string{"all", "off"} {
		require.NoError(t, e.exec(`UPDATE users SET push_groups = ? WHERE id = 'u-alice'`, setting), setting)
	}

	// A fresh database gets the same table and the same default.
	fresh := newSchemaEnv(t)
	fresh.pushUser("u-new", "new")
	require.Equal(t, "mentions", fresh.str(`SELECT push_groups FROM users WHERE id = 'u-new'`))
	fresh.pushSubscription("s1", "u-new", "", "https://push.example.test/two", "key", "auth")
	require.Equal(t, 1, fresh.count(`SELECT COUNT(*) FROM push_subscriptions`))
}

// PUSH-03: the endpoint is unique; a second registration moves that row to the latest account and session
// with the new keys, keeps the first created_at and resets last_ok_at.
func TestPushSubscriptionUpsertMovesTheEndpointToTheLatestUser(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	e.pushUser("u1", "alice")
	e.pushUser("u2", "bob")
	e.pushSession("s1", "u1")
	e.pushSession("s2", "u2")

	var first db.PushSubscription
	require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
		var err error
		first, err = q.UpsertPushSubscription(ctx, db.UpsertPushSubscriptionParams{
			ID: "p1", UserID: "u1", SessionID: sql.NullString{String: "s1", Valid: true},
			Endpoint: "https://push.example.test/one", P256dh: "old-key", Auth: "old-auth", CreatedAt: 100,
		})
		return err
	}))
	require.Equal(t, "p1", first.ID)
	require.Equal(t, "u1", first.UserID)
	require.Equal(t, int64(100), first.CreatedAt)
	require.False(t, first.LastOkAt.Valid)
	e.must(`UPDATE push_subscriptions SET last_ok_at = 50 WHERE id = 'p1'`)

	var second db.PushSubscription
	require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
		var err error
		second, err = q.UpsertPushSubscription(ctx, db.UpsertPushSubscriptionParams{
			ID: "p2", UserID: "u2", SessionID: sql.NullString{String: "s2", Valid: true},
			Endpoint: "https://push.example.test/one", P256dh: "new-key", Auth: "new-auth", CreatedAt: 200,
		})
		return err
	}))
	require.Equal(t, "p1", second.ID, "the same endpoint keeps the row")
	require.Equal(t, "u2", second.UserID, "the row moves to the latest account")
	require.Equal(t, "s2", second.SessionID.String)
	require.Equal(t, "new-key", second.P256dh)
	require.Equal(t, "new-auth", second.Auth)
	require.Equal(t, int64(100), second.CreatedAt, "the first registration time is kept")
	require.False(t, second.LastOkAt.Valid, "a new registration resets last_ok_at")

	rows, err := e.s.R().ListPushSubscriptionsByUser(ctx, "u2")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "p1", rows[0].ID)
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM push_subscriptions`))
}

// PUSH-07: a subscription goes with the session that registered it and with its account.
func TestPushSubscriptionsGoWithTheirSessionAndUser(t *testing.T) {
	e := newSchemaEnv(t)
	e.pushUser("u1", "alice")
	e.pushSession("s1", "u1")
	e.pushSubscription("p1", "u1", "s1", "https://push.example.test/one", "key", "auth")

	e.must(`DELETE FROM sessions WHERE id = 's1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM push_subscriptions`), "the session takes its subscription")

	e.pushSession("s1", "u1")
	e.pushSubscription("p2", "u1", "s1", "https://push.example.test/two", "key", "auth")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM push_subscriptions`))

	e.must(`DELETE FROM users WHERE id = 'u1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM push_subscriptions`), "the account takes every subscription")
}

// PUSH-08, PUSH-09: the target rows of a group are its enabled user members with a subscription, one per
// device; unread follows the message seq, each user's read cursor and the room baseline. The DM query
// returns its user participants the same way.
func TestGroupAndDMPushTargetsFollowTheReadCursor(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	e.pushUser("u1", "alice")
	e.pushUser("u2", "bob")
	require.NoError(t, e.intelligence("a1", "ai", "atlas"))
	e.must(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('g1', 'u1', 'launch', 1, 1)`)
	e.must(`INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES
		('rm-u1', 'g1', 'user', 'u1', 1), ('rm-u2', 'g1', 'user', 'u2', 2), ('rm-a1', 'g1', 'agent', 'a1', 3)`)
	e.pushSubscription("p-u1", "u1", "", "https://push.example.test/u1", "key-u1", "auth-u1")
	e.pushSubscription("p-u2", "u2", "", "https://push.example.test/u2", "key-u2", "auth-u2")
	e.must(`INSERT INTO read_cursors (user_id, room_id, seq) VALUES ('u1', 'g1', 2), ('u2', 'g1', 3)`)

	rows, err := e.s.R().ListGroupPushTargets(ctx, db.ListGroupPushTargetsParams{RoomID: "g1", Seq: 3})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	unread := map[string]int64{}
	handles := map[string]string{}
	for _, row := range rows {
		unread[row.UserID] = row.Unread
		handles[row.UserID] = row.Handle
		require.Equal(t, "mentions", row.PushGroups)
	}
	require.Equal(t, map[string]int64{"u1": 1, "u2": 0}, unread, "each cursor decides its own unread")
	require.Equal(t, map[string]string{"u1": "alice", "u2": "bob"}, handles)

	e.must(`DELETE FROM push_subscriptions WHERE user_id = 'u2'`)
	rows, err = e.s.R().ListGroupPushTargets(ctx, db.ListGroupPushTargetsParams{RoomID: "g1", Seq: 3})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "u1", rows[0].UserID, "a user without a subscription has no target row")

	e.pushSubscription("p-u2", "u2", "", "https://push.example.test/u2", "key-u2", "auth-u2")
	e.must(`UPDATE users SET disabled_at = 99 WHERE id = 'u2'`)
	rows, err = e.s.R().ListGroupPushTargets(ctx, db.ListGroupPushTargetsParams{RoomID: "g1", Seq: 3})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "u1", rows[0].UserID, "a disabled user has no target row")

	e.pushUser("u3", "carol")
	require.NoError(t, e.dm("d1", "u1", "u1", "u3"))
	e.pushSubscription("p-u3", "u3", "", "https://push.example.test/u3", "key-u3", "auth-u3")
	e.must(`INSERT INTO read_cursors (user_id, room_id, seq) VALUES ('u1', 'd1', 2), ('u3', 'd1', 3)`)
	dmRows, err := e.s.R().ListDMPushTargets(ctx, db.ListDMPushTargetsParams{RoomID: "d1", Seq: 3})
	require.NoError(t, err)
	require.Len(t, dmRows, 2)
	dmUnread := map[string]int64{}
	dmHandles := map[string]string{}
	for _, row := range dmRows {
		dmUnread[row.UserID] = row.Unread
		dmHandles[row.UserID] = row.Handle
		require.Equal(t, "mentions", row.PushGroups)
	}
	require.Equal(t, map[string]int64{"u1": 1, "u3": 0}, dmUnread)
	require.Equal(t, map[string]string{"u1": "alice", "u3": "carol"}, dmHandles)
}

// PUSH-06, PUSH-08: both target queries skip a member without a subscription and return the account's
// group setting on every row; SetUserPushGroups changes it.
func TestPushTargetsSkipUsersWithoutASubscription(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	e.pushUser("u1", "alice")
	e.pushUser("u2", "bob")
	e.must(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('g1', 'u1', 'launch', 1, 1)`)
	e.must(`INSERT INTO room_members (id, room_id, kind, member_id, created_at) VALUES
		('rm-u1', 'g1', 'user', 'u1', 1), ('rm-u2', 'g1', 'user', 'u2', 2)`)
	e.pushSubscription("p-u1", "u1", "", "https://push.example.test/u1", "key-u1", "auth-u1")

	rows, err := e.s.R().ListGroupPushTargets(ctx, db.ListGroupPushTargetsParams{RoomID: "g1", Seq: 1})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "u1", rows[0].UserID)
	require.Equal(t, "mentions", rows[0].PushGroups)

	require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
		return q.SetUserPushGroups(ctx, db.SetUserPushGroupsParams{PushGroups: "all", ID: "u1"})
	}))
	rows, err = e.s.R().ListGroupPushTargets(ctx, db.ListGroupPushTargetsParams{RoomID: "g1", Seq: 1})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "all", rows[0].PushGroups)

	require.NoError(t, e.dm("d1", "u1", "u1", "u2"))
	dmRows, err := e.s.R().ListDMPushTargets(ctx, db.ListDMPushTargetsParams{RoomID: "d1", Seq: 1})
	require.NoError(t, err)
	require.Len(t, dmRows, 1)
	require.Equal(t, "u1", dmRows[0].UserID)
	require.Equal(t, "all", dmRows[0].PushGroups)
}
