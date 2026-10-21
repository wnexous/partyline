package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/wnexous/partyline/internal/handles"
)

// backfillIntelligences is the Go hook of 0015_intelligences (MIG-01, MIG-04): one
// intelligence per user and agent, one person (with its intelligence) per connector account
// seen in identities or as a chat sender, message author intelligences, the people seen in
// each chat and the counterpart of DM chats. Raw SQL only: it runs against the 0015 schema.
func backfillIntelligences(ctx context.Context, tx *sql.Tx) error {
	b := &backfill{tx: tx, taken: map[string]bool{}}
	steps := []func(context.Context) error{
		b.loadHandles, b.users, b.agents, b.people, b.authors, b.chats,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

type backfill struct {
	tx    *sql.Tx
	taken map[string]bool // lower-case handles in use
	// filled by people
	personOf   map[accountKey]string // (connector, sender or alt id) → person id
	chatPeople map[chatPerson]int64  // (chat, person) → latest message time
	dmLatest   map[string]string     // dm chat id → person of its latest message
}

type accountKey struct{ connector, id string }

type chatPerson struct{ chat, person string }

func (b *backfill) loadHandles(ctx context.Context) error {
	rows, err := b.tx.QueryContext(ctx, `SELECT handle FROM intelligences`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return err
		}
		b.taken[strings.ToLower(h)] = true
	}
	return rows.Err()
}

func (b *backfill) create(ctx context.Context, id, kind, display, base string, created int64) error {
	handle := handles.Allocate(func(h string) bool { return b.taken[h] }, base)
	b.taken[handle] = true
	_, err := b.tx.ExecContext(ctx, `INSERT INTO intelligences (id, kind, display_name, handle, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, id, kind, display, handle, created, created)
	if err != nil {
		return fmt.Errorf("intelligence %s: %w", id, err)
	}
	return nil
}

// users: display name = users.display_name, else the username; handle from the username.
func (b *backfill) users(ctx context.Context) error {
	type user struct {
		id, username, display string
		created               int64
	}
	var list []user
	err := queryRows(ctx, b.tx, `SELECT id, username, display_name, created_at FROM users ORDER BY created_at, id`,
		func(rows *sql.Rows) error {
			var u user
			if err := rows.Scan(&u.id, &u.username, &u.display, &u.created); err != nil {
				return err
			}
			list = append(list, u)
			return nil
		})
	if err != nil {
		return err
	}
	for _, u := range list {
		display := firstNonEmpty(u.display, u.username)
		if err := b.create(ctx, u.id, "human", display, handles.Default(handles.User, u.username), u.created); err != nil {
			return err
		}
	}
	return nil
}

// agents: display name = the owner's rename, else the temporary agent's readable name, else
// the name; handle from the name. Every v0.3 agent stays unlinked (empty folder).
func (b *backfill) agents(ctx context.Context) error {
	type agent struct {
		id, name, display, auto string
		created                 int64
	}
	var list []agent
	err := queryRows(ctx, b.tx, `SELECT id, name, display_name, auto_name, created_at FROM agents ORDER BY created_at, id`,
		func(rows *sql.Rows) error {
			var a agent
			if err := rows.Scan(&a.id, &a.name, &a.display, &a.auto, &a.created); err != nil {
				return err
			}
			list = append(list, a)
			return nil
		})
	if err != nil {
		return err
	}
	for _, a := range list {
		display := firstNonEmpty(a.display, a.auto, a.name)
		if err := b.create(ctx, a.id, "ai", display, handles.Default(handles.AI, a.name), a.created); err != nil {
			return err
		}
	}
	return nil
}

// sighting is one appearance of an external account: an identity or a chat message.
type sighting struct {
	connector, id, alt, name string
	at                       int64
	identity                 bool
	chat, chatKind           string // chat messages only
}

// person is the accounts of one connector folded into one person.
type person struct {
	connector          string
	ids                []string
	external, alt      string
	name, platformName string
	created, lastSeen  int64
	seen               bool   // seen as a chat sender
	identity           string // display of the linked identity
}

// people: one person per (connector, sender id); an alternative id seen together with
// another id folds both into one person. Name = latest non-empty sender name, else the
// identity's display, else "<Platform> contact".
func (b *backfill) people(ctx context.Context) error {
	kinds := map[string]string{}
	err := queryRows(ctx, b.tx, `SELECT id, kind FROM connectors`, func(rows *sql.Rows) error {
		var id, kind string
		if err := rows.Scan(&id, &kind); err != nil {
			return err
		}
		kinds[id] = kind
		return nil
	})
	if err != nil {
		return err
	}
	var seen []sighting
	err = queryRows(ctx, b.tx, `SELECT connector_id, external_id, alt_external_id, display, created_at FROM identities ORDER BY created_at, id`,
		func(rows *sql.Rows) error {
			s := sighting{identity: true}
			if err := rows.Scan(&s.connector, &s.id, &s.alt, &s.name, &s.at); err != nil {
				return err
			}
			seen = append(seen, s)
			return nil
		})
	if err != nil {
		return err
	}
	err = queryRows(ctx, b.tx, `SELECT c.connector_id, m.sender_id, m.sender_alt_id, m.sender_name, m.created_at, c.id, c.kind
		FROM messages m JOIN chats c ON c.id = m.author_id
		WHERE m.author_kind = 'chat' AND m.sender_id <> ''
		ORDER BY m.created_at, m.rowid`,
		func(rows *sql.Rows) error {
			var s sighting
			if err := rows.Scan(&s.connector, &s.id, &s.alt, &s.name, &s.at, &s.chat, &s.chatKind); err != nil {
				return err
			}
			seen = append(seen, s)
			return nil
		})
	if err != nil {
		return err
	}

	// Union the ids reported together (sender id with its alternative id).
	parent := map[accountKey]accountKey{}
	var find func(k accountKey) accountKey
	find = func(k accountKey) accountKey {
		p, ok := parent[k]
		if !ok {
			parent[k] = k
			return k
		}
		if p == k {
			return k
		}
		root := find(p)
		parent[k] = root
		return root
	}
	union := func(a, b accountKey) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	for _, s := range seen {
		find(accountKey{s.connector, s.id})
		if s.alt != "" && s.alt != s.id {
			union(accountKey{s.connector, s.id}, accountKey{s.connector, s.alt})
		}
	}

	groups := map[accountKey]*person{}
	for k := range parent {
		root := find(k)
		p := groups[root]
		if p == nil {
			p = &person{connector: k.connector}
			groups[root] = p
		}
		p.ids = append(p.ids, k.id)
	}
	// Sightings are in time order (identities, then messages): later ones win.
	for _, s := range seen {
		p := groups[find(accountKey{s.connector, s.id})]
		if p.created == 0 || s.at < p.created {
			p.created = s.at
		}
		if s.identity {
			if p.identity == "" || s.name != "" {
				p.identity = s.name
			}
			if p.external == "" {
				p.external, p.alt = s.id, s.alt
			}
			continue
		}
		p.seen = true
		p.external = s.id
		if s.alt != "" && s.alt != s.id {
			p.alt = s.alt
		}
		if s.name != "" {
			p.platformName = s.name
		}
		if s.at >= p.lastSeen {
			p.lastSeen = s.at
		}
	}

	list := make([]*person, 0, len(groups))
	for _, p := range groups {
		sort.Strings(p.ids)
		if p.alt == p.external {
			p.alt = ""
		}
		if p.alt == "" {
			for _, id := range p.ids {
				if id != p.external {
					p.alt = id
					break
				}
			}
		}
		if p.platformName == "" {
			p.platformName = p.identity
		}
		p.name = firstNonEmpty(p.platformName, platformLabel(kinds[p.connector])+" contact")
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].created != list[j].created {
			return list[i].created < list[j].created
		}
		if list[i].connector != list[j].connector {
			return list[i].connector < list[j].connector
		}
		return list[i].external < list[j].external
	})

	b.personOf = map[accountKey]string{}
	for _, p := range list {
		id := NewID()
		var lastSeen any
		if p.seen {
			lastSeen = p.lastSeen
		}
		if _, err := b.tx.ExecContext(ctx, `INSERT INTO people (id, connector_id, external_id, alt_external_id, platform_name, last_seen_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, id, p.connector, p.external, p.alt, p.platformName, lastSeen, p.created); err != nil {
			return fmt.Errorf("person: %w", err)
		}
		if err := b.create(ctx, id, "human", p.name, handles.Default(handles.Person, p.name), p.created); err != nil {
			return err
		}
		for _, ext := range p.ids {
			b.personOf[accountKey{p.connector, ext}] = id
		}
	}

	b.chatPeople = map[chatPerson]int64{}
	b.dmLatest = map[string]string{}
	for _, s := range seen {
		if s.identity {
			continue
		}
		pid := b.personOf[accountKey{s.connector, s.id}]
		key := chatPerson{s.chat, pid}
		if s.at >= b.chatPeople[key] {
			b.chatPeople[key] = s.at
		}
		if s.chatKind == "dm" {
			b.dmLatest[s.chat] = pid
		}
	}
	return nil
}

// authors sets messages.author_iid: the user or agent itself, the linked user of a chat
// message (PEOP-02), the person of a chat sender (MIG-04); empty for system messages, chat
// messages without a sender and authors that no longer exist.
func (b *backfill) authors(ctx context.Context) error {
	if _, err := b.tx.ExecContext(ctx, `UPDATE messages SET author_iid = author_id
		WHERE author_kind IN ('user', 'agent') AND author_id IN (SELECT id FROM intelligences)`); err != nil {
		return fmt.Errorf("user and agent authors: %w", err)
	}
	if _, err := b.tx.ExecContext(ctx, `UPDATE messages SET author_iid = author_user_id
		WHERE author_kind = 'chat' AND author_user_id <> '' AND author_user_id IN (SELECT id FROM intelligences)`); err != nil {
		return fmt.Errorf("linked chat authors: %w", err)
	}
	// One set-based UPDATE through a temporary (connector, sender id) → person table: per-row
	// updates are far too slow on large histories.
	if _, err := b.tx.ExecContext(ctx, `CREATE TEMP TABLE backfill_senders (
		connector_id TEXT NOT NULL, sender_id TEXT NOT NULL, person_id TEXT NOT NULL,
		PRIMARY KEY (connector_id, sender_id))`); err != nil {
		return err
	}
	keys := make([]accountKey, 0, len(b.personOf))
	for k := range b.personOf {
		keys = append(keys, k)
	}
	const chunk = 500
	for start := 0; start < len(keys); start += chunk {
		part := keys[start:min(start+chunk, len(keys))]
		args := make([]any, 0, 3*len(part))
		for _, k := range part {
			args = append(args, k.connector, k.id, b.personOf[k])
		}
		values := strings.TrimSuffix(strings.Repeat("(?, ?, ?), ", len(part)), ", ")
		query := `INSERT INTO backfill_senders VALUES ` + values // #nosec G202 -- only placeholders are concatenated
		if _, err := b.tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	if _, err := b.tx.ExecContext(ctx, `UPDATE messages SET author_iid = (
			SELECT s.person_id FROM backfill_senders s JOIN chats c ON c.connector_id = s.connector_id
			WHERE c.id = messages.author_id AND s.sender_id = messages.sender_id)
		WHERE author_kind = 'chat' AND author_user_id = '' AND sender_id <> ''
		  AND author_id IN (SELECT id FROM chats)`); err != nil {
		return fmt.Errorf("chat sender authors: %w", err)
	}
	_, err := b.tx.ExecContext(ctx, `DROP TABLE backfill_senders`)
	return err
}

// chats fills chat_people (GRP-02) and the counterpart of DM chats (the person of the
// chat's latest message).
func (b *backfill) chats(ctx context.Context) error {
	for k, at := range b.chatPeople {
		if _, err := b.tx.ExecContext(ctx, `INSERT INTO chat_people (chat_id, person_id, last_seen_at) VALUES (?, ?, ?)`,
			k.chat, k.person, at); err != nil {
			return fmt.Errorf("chat people: %w", err)
		}
	}
	for chat, pid := range b.dmLatest {
		if _, err := b.tx.ExecContext(ctx, `UPDATE chats SET person_id = ? WHERE id = ?`, pid, chat); err != nil {
			return fmt.Errorf("dm chat person: %w", err)
		}
	}
	return nil
}

func queryRows(ctx context.Context, tx *sql.Tx, query string, scan func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// platformLabel names a connector kind for people without a name ("Telegram contact").
func platformLabel(kind string) string {
	switch kind {
	case "whatsapp":
		return "WhatsApp"
	case "telegram":
		return "Telegram"
	case "discord":
		return "Discord"
	case "webhook":
		return "Webhook"
	}
	return handles.DisplayName(kind)
}
