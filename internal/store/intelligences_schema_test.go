package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

type schemaEnv struct {
	t *testing.T
	s *Store
}

func newSchemaEnv(t *testing.T) *schemaEnv {
	t.Helper()
	return &schemaEnv{t: t, s: openMigrated(t)}
}

func (e *schemaEnv) exec(query string, args ...any) error {
	_, err := e.s.write.ExecContext(context.Background(), query, args...)
	return err
}

func (e *schemaEnv) must(query string, args ...any) {
	e.t.Helper()
	require.NoError(e.t, e.exec(query, args...), query)
}

func (e *schemaEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.s.write.QueryRowContext(context.Background(), query, args...).Scan(&n), query)
	return n
}

func (e *schemaEnv) intelligence(id, kind, handle string) error {
	return e.s.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateIntelligence(context.Background(), db.CreateIntelligenceParams{
			ID: id, Kind: kind, DisplayName: handle, Handle: handle, CreatedAt: 1, UpdatedAt: 1,
		})
	})
}

func (e *schemaEnv) dm(id, owner, a, b string) error {
	return e.s.WithTx(context.Background(), func(q *db.Queries) error {
		return q.CreateDMRoom(context.Background(), db.CreateDMRoomParams{
			ID: id, OwnerID: owner, Name: "", DmA: nullStr(a), DmB: nullStr(b), CreatedAt: 1, UpdatedAt: 1,
		})
	})
}

// PROF-01, PROF-02: handles are unique across the instance, case-insensitively.
func TestIntelligenceHandlesAreUniqueIgnoringCase(t *testing.T) {
	e := newSchemaEnv(t)
	require.NoError(t, e.intelligence("i1", "ai", "atlas"))
	require.Error(t, e.intelligence("i2", "human", "Atlas"), "Atlas collides with atlas")
	require.Error(t, e.intelligence("i3", "human", "ATLAS"))
	require.NoError(t, e.intelligence("i4", "human", "atlas-2"))

	n, err := e.s.R().CountIntelligenceHandle(context.Background(), "ATLAS")
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "a lookup by handle ignores case")
	byHandle, err := e.s.R().GetIntelligenceByHandle(context.Background(), "Atlas-2")
	require.NoError(t, err)
	require.Equal(t, "i4", byHandle.ID)
	list, err := e.s.R().ListIntelligencesByIDs(context.Background(), []string{"i1", "i4", "missing"})
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.ElementsMatch(t, []string{"i1", "i4"}, []string{list[0].ID, list[1].ID})
}

// DM-01 (data): one DM room per pair; groups have no pair. The kind CHECKs refuse unknown
// values.
func TestDMPairIsUniqueAndKindsAreChecked(t *testing.T) {
	e := newSchemaEnv(t)
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u1', 'alice', 'x', 'member', 1)`)
	require.NoError(t, e.intelligence("u1", "human", "alice"))
	require.NoError(t, e.intelligence("p1", "human", "maria"))
	require.NoError(t, e.intelligence("p2", "human", "joao"))

	require.NoError(t, e.dm("d1", "u1", "p1", "u1"))
	require.Error(t, e.dm("d2", "u1", "p1", "u1"), "a second DM for the same pair")
	require.NoError(t, e.dm("d3", "u1", "p2", "u1"), "another pair")
	e.must(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('g1', 'u1', 'ops', 1, 1), ('g2', 'u1', 'dev', 1, 1)`)
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM rooms WHERE kind = 'group'`), "rooms default to group")
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM rooms WHERE kind = 'dm'`))
	require.Error(t, e.exec(`INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at)
		VALUES ('d4', 'u1', '', 'dm', 'p1', 'missing', 1, 1)`), "DM participants must be intelligences")

	require.Error(t, e.exec(`UPDATE rooms SET kind = 'channel' WHERE id = 'g1'`))
	require.Error(t, e.intelligence("x1", "robot", "robot"))
}

// DM-08 edge case, PROF-01: deleting a user deletes its agents, their intelligences and the
// DM rooms of both; deleting a connector deletes its people, their intelligences and DMs.
func TestDeletesCascadeToIntelligencesAndDMs(t *testing.T) {
	e := newSchemaEnv(t)
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES
		('u1', 'alice', 'x', 'member', 1), ('u2', 'bob', 'x', 'member', 1)`)
	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES ('a1', 'u1', 'atlas', 1), ('a2', 'u2', 'notes', 1)`)
	e.must(`INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES ('k1', 'u2', 'telegram', 'tg', 1, 1)`)
	e.must(`INSERT INTO people (id, connector_id, external_id, created_at) VALUES ('p1', 'k1', '42', 1)`)
	for id, kind := range map[string]string{"u1": "human", "u2": "human", "a1": "ai", "a2": "ai", "p1": "human"} {
		require.NoError(t, e.intelligence(id, kind, "h-"+id))
	}
	// DMs owned by bob, so only the participant cascade can remove them.
	require.NoError(t, e.dm("d-alice-bob", "u2", "u1", "u2"))
	require.NoError(t, e.dm("d-atlas-bob", "u2", "a1", "u2"))
	require.NoError(t, e.dm("d-bob-notes", "u2", "a2", "u2"))
	require.NoError(t, e.dm("d-person", "u2", "p1", "u2"))
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, created_at) VALUES ('m1', 'd-atlas-bob', 1, 'agent', 'a1', 1)`)

	e.must(`DELETE FROM users WHERE id = 'u1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM agents WHERE id = 'a1'`), "alice's agent goes with her")
	require.Zero(t, e.count(`SELECT COUNT(*) FROM intelligences WHERE id IN ('u1', 'a1')`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM rooms WHERE id IN ('d-alice-bob', 'd-atlas-bob')`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM messages WHERE id = 'm1'`))
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM rooms WHERE id IN ('d-bob-notes', 'd-person')`), "other DMs stay")
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM intelligences WHERE id IN ('u2', 'a2', 'p1')`))

	e.must(`DELETE FROM connectors WHERE id = 'k1'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM people`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM intelligences WHERE id = 'p1'`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM rooms WHERE id = 'd-person'`))
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM rooms WHERE id = 'd-bob-notes'`))

	e.must(`DELETE FROM agents WHERE id = 'a2'`)
	require.Zero(t, e.count(`SELECT COUNT(*) FROM intelligences WHERE id = 'a2'`))
	require.Zero(t, e.count(`SELECT COUNT(*) FROM rooms`), "an AI's DMs go with it")
	require.Equal(t, 1, e.count(`SELECT COUNT(*) FROM intelligences WHERE id = 'u2'`))
}

// The view names an author by its intelligence (author_iid); the display name an agent member
// has in a room still wins in that room (GRP-04). A chat message without an intelligence is named
// by the sender, then the chat; a system message is Partyline. There are no fallbacks to the
// user or agent row: 0016 removed them with the legacy name columns, so an author without an
// intelligence has no name.
func TestMessageViewNamesAuthors(t *testing.T) {
	e := newSchemaEnv(t)
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES
		('u1', 'alice', 'x', 'member', 1), ('u2', 'bob', 'x', 'member', 1), ('u3', 'carol', 'x', 'member', 1)`)
	e.must(`INSERT INTO agents (id, owner_id, name, created_at) VALUES
		('a1', 'u1', 'atlas', 1), ('a2', 'u1', 'helper', 1), ('a4', 'u1', 'named', 1)`)
	e.must(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('r1', 'u1', 'ops', 1, 1), ('r2', 'u1', 'dev', 1, 1)`)
	e.must(`INSERT INTO room_members (id, room_id, kind, member_id, display_name, created_at) VALUES ('rm1', 'r1', 'agent', 'a4', 'Ops Bot', 1)`)
	e.must(`INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES ('k1', 'u1', 'telegram', 'tg', 1, 1)`)
	e.must(`INSERT INTO chats (id, connector_id, external_id, title, created_at) VALUES ('c1', 'k1', '42', 'Family', 1)`)
	e.must(`INSERT INTO people (id, connector_id, external_id, created_at) VALUES ('p1', 'k1', '7', 1)`)
	require.NoError(t, e.s.WithTx(context.Background(), func(q *db.Queries) error {
		for _, i := range []db.CreateIntelligenceParams{
			{ID: "u1", Kind: "human", DisplayName: "Alice A.", Handle: "alice"},
			{ID: "u3", Kind: "human", DisplayName: "Carol C.", Handle: "carol", AvatarSha256: "abc"},
			{ID: "a1", Kind: "ai", DisplayName: "Atlas", Handle: "atlas"},
			{ID: "a4", Kind: "ai", DisplayName: "Named AI", Handle: "named"},
			{ID: "p1", Kind: "human", DisplayName: "Maria", Handle: "maria"},
		} {
			if err := q.CreateIntelligence(context.Background(), i); err != nil {
				return err
			}
		}
		return nil
	}))
	e.must(`INSERT INTO messages (id, room_id, seq, author_kind, author_id, sender_id, sender_name, author_iid, created_at) VALUES
		('m1', 'r1', 1, 'user', 'u1', '', '', 'u1', 1),
		('m2', 'r1', 2, 'user', 'u2', '', '', '', 1),
		('m3', 'r1', 3, 'user', 'u3', '', '', 'u3', 1),
		('m4', 'r1', 4, 'agent', 'a1', '', '', 'a1', 1),
		('m5', 'r1', 5, 'agent', 'a2', '', '', '', 1),
		('m6', 'r1', 6, 'agent', 'a4', '', '', 'a4', 1),
		('m7', 'r2', 1, 'agent', 'a4', '', '', 'a4', 1),
		('m8', 'r1', 7, 'chat', 'c1', '9', 'Grandma', '', 1),
		('m9', 'r1', 8, 'chat', 'c1', '', '', '', 1),
		('m10', 'r1', 9, 'chat', 'c1', '7', 'Old Name', 'p1', 1),
		('m11', 'r1', 10, 'system', '', '', '', '', 1)`)

	type author struct{ Name, Handle, Kind, Avatar string }
	got := map[string]author{}
	rows, err := e.s.write.Query(`SELECT id, author_name, author_handle, author_ikind, author_avatar FROM message_views`)
	require.NoError(t, err)
	for rows.Next() {
		var id string
		var a author
		require.NoError(t, rows.Scan(&id, &a.Name, &a.Handle, &a.Kind, &a.Avatar))
		got[id] = a
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, map[string]author{
		"m1":  {Name: "Alice A.", Handle: "alice", Kind: "human"},
		"m2":  {Name: ""}, // no intelligence: no fallback to the username
		"m3":  {Name: "Carol C.", Handle: "carol", Kind: "human", Avatar: "abc"},
		"m4":  {Name: "Atlas", Handle: "atlas", Kind: "ai"},
		"m5":  {Name: ""}, // no intelligence: no fallback to the agent name
		"m6":  {Name: "Ops Bot", Handle: "named", Kind: "ai"},
		"m7":  {Name: "Named AI", Handle: "named", Kind: "ai"}, // the member name belongs to its room
		"m8":  {Name: "Grandma"},
		"m9":  {Name: "Family"},
		"m10": {Name: "Maria", Handle: "maria", Kind: "human"},
		"m11": {Name: "Partyline"},
	}, got)
}

// The CRUD queries with a rule of their own: read cursors and chat sightings only move
// forward; a person is found by external or alternative id, an external match first; a
// machine upsert keeps its label.
func TestIntelligenceQueryRules(t *testing.T) {
	ctx := context.Background()
	e := newSchemaEnv(t)
	e.must(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u1', 'alice', 'x', 'member', 1)`)
	e.must(`INSERT INTO rooms (id, owner_id, name, created_at, updated_at) VALUES ('r1', 'u1', 'ops', 1, 1)`)
	e.must(`INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES ('k1', 'u1', 'whatsapp', 'wa', 1, 1), ('k2', 'u1', 'whatsapp', 'wa2', 1, 1)`)
	e.must(`INSERT INTO chats (id, connector_id, external_id, created_at) VALUES ('c1', 'k1', 'g', 1)`)
	e.must(`INSERT INTO people (id, connector_id, external_id, alt_external_id, created_at) VALUES
		('p1', 'k1', '5511999990001@s.whatsapp.net', '100000000000001@lid', 1),
		('p2', 'k1', '100000000000001@lid', '', 1),
		('p3', 'k1', '5511999990003@s.whatsapp.net', '', 1),
		('p4', 'k2', '5511999990003@s.whatsapp.net', '', 1)`)

	require.NoError(t, e.s.WithTx(ctx, func(q *db.Queries) error {
		for _, seq := range []int64{5, 3, 7} {
			if err := q.UpsertReadCursor(ctx, db.UpsertReadCursorParams{UserID: "u1", RoomID: "r1", Seq: seq}); err != nil {
				return err
			}
		}
		for _, at := range []int64{50, 30} {
			if err := q.UpsertChatPerson(ctx, db.UpsertChatPersonParams{ChatID: "c1", PersonID: "p1", LastSeenAt: at}); err != nil {
				return err
			}
		}
		if err := q.UpsertMachine(ctx, db.UpsertMachineParams{OwnerID: "u1", Hostname: "devbox", Home: "/home/a", LastSeenAt: sql.NullInt64{Int64: 1, Valid: true}}); err != nil {
			return err
		}
		if err := q.SetMachineLabel(ctx, db.SetMachineLabelParams{Label: "Desk", OwnerID: "u1", Hostname: "devbox"}); err != nil {
			return err
		}
		return q.UpsertMachine(ctx, db.UpsertMachineParams{OwnerID: "u1", Hostname: "devbox", Home: "/home/b", LastSeenAt: sql.NullInt64{Int64: 2, Valid: true}})
	}))
	seq, err := e.s.R().GetReadCursor(ctx, db.GetReadCursorParams{UserID: "u1", RoomID: "r1"})
	require.NoError(t, err)
	require.Equal(t, int64(7), seq, "a read cursor only moves forward")
	require.Equal(t, 50, e.count(`SELECT last_seen_at FROM chat_people WHERE chat_id = 'c1' AND person_id = 'p1'`))
	m, err := e.s.R().GetMachine(ctx, db.GetMachineParams{OwnerID: "u1", Hostname: "devbox"})
	require.NoError(t, err)
	require.Equal(t, []any{"Desk", "/home/b", int64(2)}, []any{m.Label, m.Home, m.LastSeenAt.Int64})

	find := func(connector, sender string) string {
		row, err := e.s.R().GetPersonByExternalID(ctx, db.GetPersonByExternalIDParams{ConnectorID: connector, SenderID: sender})
		if err != nil {
			require.ErrorIs(t, err, sql.ErrNoRows)
			return ""
		}
		return row.Person.ID
	}
	require.Equal(t, "p1", find("k1", "5511999990001@s.whatsapp.net"))
	require.Equal(t, "p2", find("k1", "100000000000001@lid"), "an external match wins over another person's alt id")
	require.Equal(t, "p3", find("k1", "5511999990003@s.whatsapp.net"))
	require.Equal(t, "p4", find("k2", "5511999990003@s.whatsapp.net"), "people are per connector")
	require.Equal(t, "", find("k1", ""), "an empty sender id matches no empty alt id")
	e.must(`UPDATE people SET external_id = 'x' WHERE id = 'p2'`)
	require.Equal(t, "p1", find("k1", "100000000000001@lid"), "an alternative id matches")
}
