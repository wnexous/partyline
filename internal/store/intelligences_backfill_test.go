package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/handles"
)

// backfillFixture builds a database at 0014 with users, agents, identities and chat senders
// of every kind (synthetic values), then migrates it.
func backfillFixture(t *testing.T) *schemaEnv {
	t.Helper()
	e := &schemaEnv{t: t, s: openMigratedBefore(t, "0015")}
	for _, stmt := range []string{
		`INSERT INTO users (id, username, display_name, password_hash, role, created_at) VALUES
			('u-alice', 'alice', 'Alice A.', 'x', 'admin', 1),
			('u-bob', 'Bob.Smith', '', 'x', 'member', 2),
			('u-pl', 'partyline', '', 'x', 'member', 3),
			('u-alice2', 'alice_', '', 'x', 'member', 4)`,
		`INSERT INTO agents (id, owner_id, name, display_name, auto_name, kind, created_at) VALUES
			('a-atlas', 'u-alice', 'atlas', '', '', 'fixed', 10),
			('a-temp', 'u-alice', 'proj-claude', '', 'Proj · Claude', 'temporary', 11),
			('a-renamed', 'u-alice', 'helper', 'Helper Bot', '', 'fixed', 12),
			('a-bob-atlas', 'u-bob', 'atlas', '', '', 'fixed', 13),
			('a-num', 'u-bob', '2024-notes', '', '', 'fixed', 14),
			('a-alice', 'u-bob', 'alice', '', '', 'fixed', 15)`,
		`INSERT INTO connectors (id, owner_id, kind, name, created_at, updated_at) VALUES
			('k-tg', 'u-alice', 'telegram', 'tg', 1, 1), ('k-wa', 'u-alice', 'whatsapp', 'wa', 1, 1)`,
		`INSERT INTO chats (id, connector_id, external_id, title, kind, created_at) VALUES
			('c-tgdm', 'k-tg', '100', 'Maria', 'dm', 1),
			('c-tggrp', 'k-tg', '-200', 'Family', 'group', 1),
			('c-wagrp', 'k-wa', '120363000000000001@g.us', 'Team', 'group', 1)`,
		`INSERT INTO identities (id, user_id, connector_id, connector_kind, external_id, alt_external_id, display, created_at) VALUES
			('i-bob', 'u-bob', 'k-tg', 'telegram', '101', '', 'Bob TG', 5),
			('i-alice', 'u-alice', 'k-wa', 'whatsapp', '5511999990000@s.whatsapp.net', '100000000000001@lid', 'Alice WA', 6)`,
		`INSERT INTO rooms (id, owner_id, name, next_seq, created_at, updated_at) VALUES ('r1', 'u-alice', 'ops', 13, 1, 1)`,
		`INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_user_id, sender_id, sender_alt_id, sender_name, text, created_at) VALUES
			('m-user', 'r1', 1, 'user', 'u-alice', '', '', '', '', 'x', 10),
			('m-agent', 'r1', 2, 'agent', 'a-atlas', '', '', '', '', 'x', 11),
			('m-system', 'r1', 3, 'system', '', '', '', '', '', 'x', 12),
			('m-linked', 'r1', 4, 'chat', 'c-tggrp', 'u-bob', '101', '', 'Bobby', 'x', 15),
			('m-maria1', 'r1', 5, 'chat', 'c-tgdm', '', '100', '', 'Maria', 'x', 20),
			('m-maria-grp', 'r1', 6, 'chat', 'c-tggrp', '', '100', '', '', 'x', 25),
			('m-maria2', 'r1', 7, 'chat', 'c-tgdm', '', '100', '', 'Maria Silva', 'x', 30),
			('m-ze1', 'r1', 8, 'chat', 'c-wagrp', '', '100000000000002@lid', '5511999990002@s.whatsapp.net', 'Zé', 'x', 40),
			('m-ze2', 'r1', 9, 'chat', 'c-wagrp', '', '5511999990002@s.whatsapp.net', '', '', 'x', 41),
			('m-nosender', 'r1', 10, 'chat', 'c-tggrp', '', '', '', '', 'x', 42),
			('m-alice-wa', 'r1', 11, 'chat', 'c-wagrp', 'u-alice', '100000000000001@lid', '', '', 'x', 43),
			('m-wa100', 'r1', 12, 'chat', 'c-wagrp', '', '100', '', '', 'x', 44)`,
	} {
		e.must(stmt)
	}
	require.NoError(t, e.s.Migrate(context.Background()))
	return e
}

type intel struct{ Kind, Display, Handle string }

func (e *schemaEnv) intelligences() map[string]intel {
	e.t.Helper()
	out := map[string]intel{}
	rows, err := e.s.write.Query(`SELECT id, kind, display_name, handle FROM intelligences`)
	require.NoError(e.t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var i intel
		require.NoError(e.t, rows.Scan(&id, &i.Kind, &i.Display, &i.Handle))
		out[id] = i
	}
	require.NoError(e.t, rows.Err())
	return out
}

func (e *schemaEnv) str(query string, args ...any) string {
	e.t.Helper()
	var s sql.NullString
	require.NoError(e.t, e.s.write.QueryRow(query, args...).Scan(&s), query)
	return s.String
}

// personOf returns the person whose external or alternative id is ext on the connector.
func (e *schemaEnv) personOf(connector, ext string) string {
	e.t.Helper()
	return e.str(`SELECT id FROM people WHERE connector_id = ? AND (external_id = ? OR alt_external_id = ?)`, connector, ext, ext)
}

// MIG-01, PROF-01, PROF-02: every user and agent gets one intelligence with its v0.3 display
// name and a valid, unique handle allocated in creation order; agents stay unlinked.
func TestBackfillUsersAndAgents(t *testing.T) {
	e := backfillFixture(t)
	got := e.intelligences()
	want := map[string]intel{
		"u-alice":     {"human", "Alice A.", "alice"},
		"u-bob":       {"human", "Bob.Smith", "bob-smith"},
		"u-pl":        {"human", "partyline", "partyline-2"},
		"u-alice2":    {"human", "alice_", "alice-2"},
		"a-atlas":     {"ai", "atlas", "atlas"},
		"a-temp":      {"ai", "Proj · Claude", "proj-claude"},
		"a-renamed":   {"ai", "Helper Bot", "helper"},
		"a-bob-atlas": {"ai", "atlas", "atlas-2"},
		"a-num":       {"ai", "2024-notes", "ai-2024-notes"},
		"a-alice":     {"ai", "alice", "alice-3"},
	}
	for id, w := range want {
		require.Equal(t, w, got[id], id)
	}
	seen := map[string]bool{}
	for id, i := range got {
		h, err := handles.Validate(i.Handle)
		require.NoError(t, err, id)
		require.Equal(t, h, i.Handle, "stored lower-case")
		require.False(t, seen[h], "handle %s used twice", h)
		seen[h] = true
	}
	require.Equal(t, 6, e.count(`SELECT COUNT(*) FROM agents WHERE folder = ''`), "every v0.3 agent is unlinked")
	require.Equal(t, 4+6+5, len(got), "users, agents and people, one intelligence each")
}

// MIG-04: one person per (connector, sender id); an alternative id reported with a sender id
// folds into one person; the name is the latest non-empty sender name, else the identity's
// display, else "<Platform> contact".
func TestBackfillPeople(t *testing.T) {
	e := backfillFixture(t)
	require.Equal(t, 2, e.count(`SELECT COUNT(*) FROM people WHERE connector_id = 'k-tg'`))
	require.Equal(t, 3, e.count(`SELECT COUNT(*) FROM people WHERE connector_id = 'k-wa'`))

	maria := e.personOf("k-tg", "100")
	require.NotEmpty(t, maria)
	require.Equal(t, intel{"human", "Maria Silva", "maria-silva"}, e.intelligences()[maria])
	require.Equal(t, "Maria Silva", e.str(`SELECT platform_name FROM people WHERE id = ?`, maria))
	require.Equal(t, 30, e.count(`SELECT last_seen_at FROM people WHERE id = ?`, maria))

	ze := e.personOf("k-wa", "100000000000002@lid")
	require.NotEmpty(t, ze)
	require.Equal(t, ze, e.personOf("k-wa", "5511999990002@s.whatsapp.net"), "the alternative id folds into the same person")
	require.Equal(t, intel{"human", "Zé", "ze"}, e.intelligences()[ze])

	bob := e.personOf("k-tg", "101")
	require.Equal(t, "Bobby", e.intelligences()[bob].Display, "the linked sender's latest name")
	aliceWA := e.personOf("k-wa", "100000000000001@lid")
	require.Equal(t, aliceWA, e.personOf("k-wa", "5511999990000@s.whatsapp.net"))
	require.Equal(t, "Alice WA", e.intelligences()[aliceWA].Display, "no sender name: the identity's display")

	wa100 := e.personOf("k-wa", "100")
	require.NotEqual(t, maria, wa100, "the same id on another connector is another person")
	require.Equal(t, intel{"human", "WhatsApp contact", "whatsapp-contact"}, e.intelligences()[wa100])
}

// MIG-04: author_iid per author case; chat_people and the counterpart of DM chats.
func TestBackfillMessageAuthors(t *testing.T) {
	e := backfillFixture(t)
	maria, ze := e.personOf("k-tg", "100"), e.personOf("k-wa", "5511999990002@s.whatsapp.net")
	bob, aliceWA, wa100 := e.personOf("k-tg", "101"), e.personOf("k-wa", "100000000000001@lid"), e.personOf("k-wa", "100")
	authors := map[string]string{}
	rows, err := e.s.write.Query(`SELECT id, author_iid FROM messages`)
	require.NoError(t, err)
	for rows.Next() {
		var id, iid string
		require.NoError(t, rows.Scan(&id, &iid))
		authors[id] = iid
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Equal(t, map[string]string{
		"m-user":      "u-alice",
		"m-agent":     "a-atlas",
		"m-system":    "",
		"m-linked":    "u-bob",
		"m-maria1":    maria,
		"m-maria-grp": maria,
		"m-maria2":    maria,
		"m-ze1":       ze,
		"m-ze2":       ze,
		"m-nosender":  "",
		"m-alice-wa":  "u-alice",
		"m-wa100":     wa100,
	}, authors)

	type seen struct {
		Chat, Person string
		At           int64
	}
	var chatPeople []seen
	rows, err = e.s.write.Query(`SELECT chat_id, person_id, last_seen_at FROM chat_people`)
	require.NoError(t, err)
	for rows.Next() {
		var s seen
		require.NoError(t, rows.Scan(&s.Chat, &s.Person, &s.At))
		chatPeople = append(chatPeople, s)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.ElementsMatch(t, []seen{
		{"c-tggrp", bob, 15}, {"c-tgdm", maria, 30}, {"c-tggrp", maria, 25},
		{"c-wagrp", ze, 41}, {"c-wagrp", aliceWA, 43}, {"c-wagrp", wa100, 44},
	}, chatPeople)

	require.Equal(t, maria, e.str(`SELECT person_id FROM chats WHERE id = 'c-tgdm'`))
	require.Equal(t, "", e.str(`SELECT person_id FROM chats WHERE id = 'c-tggrp'`), "group chats have no counterpart")
}
