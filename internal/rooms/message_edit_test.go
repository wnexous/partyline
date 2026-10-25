package rooms

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/validation"
)

// Edit and delete messages (messenger-polish, EDT-01..EDT-05, DLT-01..DLT-05, AD-025). The service is
// tested here with the rooms fixture; the routes, the live frames and the AI tools are tested over HTTP
// (internal/api/message_edit_test.go) and in package channel.

// plant stores a message the service cannot create (a system notice, a message of a chat) and returns its
// id. The room's sequence number moves on, as a post moves it.
func (f *fixture) plant(t *testing.T, roomID, authorKind, authorID, authorIID, kind, text string) string {
	t.Helper()
	id := store.NewID()
	var seq int
	require.NoError(t, f.raw.QueryRowContext(context.Background(), "UPDATE rooms SET next_seq = next_seq + 1 WHERE id = ? RETURNING next_seq - 1", roomID).Scan(&seq))
	f.exec(t, `INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_iid, kind, text, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, roomID, seq, authorKind, authorID, authorIID, kind, text, t0.UnixMilli())
	return id
}

// editedAt is the edited_at column of a message; ok is false for NULL.
func (f *fixture) editedAt(t *testing.T, id string) (ms int64, ok bool) {
	t.Helper()
	var v *int64
	require.NoError(t, f.raw.QueryRowContext(context.Background(), "SELECT edited_at FROM messages WHERE id = ?", id).Scan(&v))
	if v == nil {
		return 0, false
	}
	return *v, true
}

// EDT-01: an edit replaces the text and sets edited_at; the message keeps its id, sequence number, author,
// time and place, whenever it is edited (no time limit), and the bus carries the text it had.
func TestEditMessageKeepsItsPlaceAndSetsEditedAt(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")
	f.post(t, alice, r.ID, "first")
	m := f.post(t, alice, r.ID, "helo wrld")
	f.post(t, alice, r.ID, "third")
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	f.clock.now = t0.Add(400 * 24 * time.Hour) // there is no window
	got, err := f.svc.EditMessage(ctx, alice, r.ID, m.ID, "hello world")
	require.NoError(t, err)
	require.Equal(t, m.ID, got.ID)
	require.Equal(t, m.Seq, got.Seq)
	require.Equal(t, m.CreatedAt, got.CreatedAt)
	require.Equal(t, m.AuthorIid, got.AuthorIid)
	require.Equal(t, "hello world", got.Text)
	require.True(t, got.EditedAt.Valid)
	require.Equal(t, f.clock.now.UnixMilli(), got.EditedAt.Int64)
	require.False(t, got.DeletedAt.Valid)
	require.Equal(t, []events.Event{events.MessageUpdated{RoomID: r.ID, MessageID: m.ID, Seq: m.Seq, PrevText: "helo wrld"}}, drain(sub))

	list, _, err := f.svc.Messages(ctx, alice, r.ID, Page{})
	require.NoError(t, err)
	var order []string
	for _, x := range list {
		order = append(order, x.Text)
	}
	require.Equal(t, []string{"third", "hello world", "first"}, order, "newest first, the edited message in its place")

	f.clock.now = f.clock.now.Add(time.Hour)
	again, err := f.svc.EditMessage(ctx, alice, r.ID, m.ID, "hello, world")
	require.NoError(t, err)
	require.Equal(t, f.clock.now.UnixMilli(), again.EditedAt.Int64, "edited_at is the time of the last edit")
	require.Equal(t, []events.Event{events.MessageUpdated{RoomID: r.ID, MessageID: m.ID, Seq: m.Seq, PrevText: "hello world"}}, drain(sub))
}

// EDT-04: the same text changes nothing: no edited_at, no event.
func TestEditToTheSameTextChangesNothing(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")
	m := f.post(t, alice, r.ID, "same")
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	f.clock.now = t0.Add(time.Hour)
	got, err := f.svc.EditMessage(ctx, alice, r.ID, m.ID, "same")
	require.NoError(t, err)
	require.Equal(t, "same", got.Text)
	require.False(t, got.EditedAt.Valid)
	_, edited := f.editedAt(t, m.ID)
	require.False(t, edited)
	require.Empty(t, drain(sub))
}

// EDT-02: only the author edits - an AI as itself, never its host - and the answers follow the room's
// access: someone who cannot view the room, or a message that is not in it, is not found.
func TestEditNeedsTheAuthor(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionMember)
	admin := f.colleague(t, f.orgOf(t, alice), "carol", orgs.PermissionAdmin)
	stranger := f.user(t, "stranger", dto.RoleMember)
	atlas := f.agent(t, alice, "atlas")
	as := agentPrincipal(alice, atlas)
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindUser, bob.UserID)
	f.addMember(t, alice, r.ID, dto.MemberKindAgent, atlas)
	other := f.room(t, alice, "elsewhere")
	byAlice := f.post(t, alice, r.ID, "alice wrote this")
	byAtlas, err := f.svc.Post(ctx, as, r.ID, PostInput{Text: "atlas wrote this"})
	require.NoError(t, err)
	inOther := f.post(t, alice, other.ID, "in another room")

	for name, p := range map[string]authn.Principal{"a member": bob, "an admin of the organization": admin, "the AI": as} {
		_, err := f.svc.EditMessage(ctx, p, r.ID, byAlice.ID, "mine now")
		require.ErrorIs(t, err, access.ErrForbidden, name)
	}
	_, err = f.svc.EditMessage(ctx, alice, r.ID, byAtlas.ID, "mine now")
	require.ErrorIs(t, err, access.ErrForbidden, "the host of an AI is not its author")
	_, err = f.svc.EditMessage(ctx, stranger, r.ID, byAlice.ID, "mine now")
	require.ErrorIs(t, err, access.ErrNotFound, "no view of the room")
	_, err = f.svc.EditMessage(ctx, alice, r.ID, inOther.ID, "mine now")
	require.ErrorIs(t, err, access.ErrNotFound, "a message of another room")
	_, err = f.svc.EditMessage(ctx, alice, r.ID, "no-such-message", "mine now")
	require.ErrorIs(t, err, access.ErrNotFound)
	require.Equal(t, "alice wrote this", f.messageText(t, byAlice.ID), "nothing changed")

	got, err := f.svc.EditMessage(ctx, as, r.ID, byAtlas.ID, "atlas fixed this")
	require.NoError(t, err)
	require.Equal(t, "atlas fixed this", got.Text, "an AI edits its own message")

	// An AI that was removed from the room cannot edit there any more.
	f.exec(t, "DELETE FROM room_members WHERE room_id = ? AND member_id = ?", r.ID, atlas)
	_, err = f.svc.EditMessage(ctx, as, r.ID, byAtlas.ID, "too late")
	require.ErrorIs(t, err, access.ErrNotFound)
}

// messageText is the stored text of a message.
func (f *fixture) messageText(t *testing.T, id string) string {
	t.Helper()
	var text string
	require.NoError(t, f.raw.QueryRowContext(context.Background(), "SELECT text FROM messages WHERE id = ?", id).Scan(&text))
	return text
}

// EDT-03: what is not text a person or an AI wrote is not edited: a reaction of the caller's is 422, the
// notice of the system and what a chat wrote - however the sender is linked - have no author to edit them,
// and a deleted message is a conflict.
func TestEditRefusesWhatIsNotAText(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	atlas := f.agent(t, alice, "atlas")
	as := agentPrincipal(alice, atlas)
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindAgent, atlas)
	reaction, err := f.svc.Post(ctx, as, r.ID, PostInput{Text: "👍", Kind: dto.MessageKindReaction})
	require.NoError(t, err)
	notice := f.plant(t, r.ID, "system", "", "", "system", PauseNotice)
	linked := f.plant(t, r.ID, "chat", "a-chat", alice.UserID, "text", "written on a platform, linked to alice")
	gone := f.post(t, alice, r.ID, "soon gone")
	_, err = f.svc.DeleteMessage(ctx, alice, r.ID, gone.ID)
	require.NoError(t, err)

	_, err = f.svc.EditMessage(ctx, as, r.ID, reaction.ID, "👎")
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "message")
	for name, id := range map[string]string{"a system notice": notice, "a message a chat wrote": linked} {
		_, err := f.svc.EditMessage(ctx, alice, r.ID, id, "rewritten")
		require.ErrorIs(t, err, access.ErrForbidden, name)
	}
	_, err = f.svc.EditMessage(ctx, alice, r.ID, gone.ID, "back again")
	require.ErrorIs(t, err, ErrMessageDeleted)
	require.Equal(t, "👍", f.messageText(t, reaction.ID))
	require.Equal(t, PauseNotice, f.messageText(t, notice))
	require.Equal(t, "", f.messageText(t, gone.ID), "a deleted message stays deleted")
}

// EDT-04: the new text follows the rules of a post (MSG-02), except that a message with files may have none.
func TestEditValidatesTheText(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")
	plain := f.post(t, alice, r.ID, "plain")
	withFile, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "caption", Files: []File{{SHA256: strings.Repeat("a", 64), Name: "a.txt", Mime: "text/plain", Size: 3}}})
	require.NoError(t, err)

	for name, text := range map[string]string{"empty": "", "blank": " \n\t ", "too long": strings.Repeat("x", dto.MaxTextLength+1)} {
		_, err := f.svc.EditMessage(ctx, alice, r.ID, plain.ID, text)
		var verr *validation.Error
		require.ErrorAs(t, err, &verr, name)
		require.Contains(t, verr.Fields, "text", name)
	}
	require.Equal(t, "plain", f.messageText(t, plain.ID))

	got, err := f.svc.EditMessage(ctx, alice, r.ID, plain.ID, strings.Repeat("é", dto.MaxTextLength))
	require.NoError(t, err, "16,000 characters, counted as characters")
	require.Len(t, []rune(got.Text), dto.MaxTextLength)

	got, err = f.svc.EditMessage(ctx, alice, r.ID, withFile.ID, "")
	require.NoError(t, err, "the files stay, so the caption may go")
	require.Empty(t, got.Text)
	require.Len(t, got.Attachments, 1, "an edit keeps the attachments")
	require.True(t, got.EditedAt.Valid)
}

// EDT-05, DLT-03: a DM that holds an external person is history; a DM whose other participant is a disabled
// user refuses an edit like a post and allows a delete.
func TestEditAndDeleteInHistoryAndDisabledDMs(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	dm := f.dm(t, alice, bob)
	m := f.post(t, alice, dm, "before")
	f.exec(t, "UPDATE users SET disabled_at = ? WHERE id = ?", t0.UnixMilli(), bob.UserID)

	_, err := f.svc.EditMessage(ctx, alice, dm, m.ID, "after")
	require.ErrorIs(t, err, ErrCounterpartDisabled)
	require.Equal(t, "before", f.messageText(t, m.ID))
	gone, err := f.svc.DeleteMessage(ctx, alice, dm, m.ID)
	require.NoError(t, err, "taking something back is not posting")
	require.True(t, gone.DeletedAt.Valid)

	// The DM the connection proxy left between an AI and a person is history.
	atlas := f.agent(t, alice, "atlas")
	as := agentPrincipal(alice, atlas)
	conn, _ := f.chat(t, alice, "Pat")
	person := store.NewID()
	f.intelligence(t, person, "human", "Pat", "pat")
	f.exec(t, "INSERT INTO people (id, connector_id, external_id, created_at) VALUES (?, ?, 'pat-phone', ?)", person, conn, t0.UnixMilli())
	x, y := atlas, person
	if x > y {
		x, y = y, x
	}
	history := store.NewID()
	f.exec(t, "INSERT INTO rooms (id, owner_id, name, kind, dm_a, dm_b, created_at, updated_at) VALUES (?, ?, '', 'dm', ?, ?, ?, ?)",
		history, alice.UserID, x, y, t0.UnixMilli(), t0.UnixMilli())
	f.exec(t, "INSERT INTO room_members (id, room_id, kind, member_id, added_by, created_at) VALUES (?, ?, 'agent', ?, ?, ?)", store.NewID(), history, atlas, alice.UserID, t0.UnixMilli())
	mine := f.plant(t, history, "agent", atlas, atlas, "text", "an answer from before the proxy")

	_, err = f.svc.EditMessage(ctx, as, history, mine, "rewritten")
	require.ErrorIs(t, err, access.ErrHistoryOnly)
	_, err = f.svc.DeleteMessage(ctx, as, history, mine)
	require.ErrorIs(t, err, access.ErrHistoryOnly)
	require.Equal(t, "an answer from before the proxy", f.messageText(t, mine))
}

// DLT-01: a delete keeps the row as a placeholder - id, sequence number, author, time and reply_to - without
// text or attachments, clears edited_at, and tells the bus what the text was. A reply to it keeps pointing
// to it.
func TestDeleteLeavesAPlaceholder(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.colleague(t, f.orgOf(t, alice), "bob", orgs.PermissionMember)
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindUser, bob.UserID)
	question := f.post(t, bob, r.ID, "ready?")
	m, err := f.svc.Post(ctx, alice, r.ID, PostInput{
		Text: "typo here", ReplyTo: question.ID,
		Files: []File{{SHA256: strings.Repeat("b", 64), Name: "b.txt", Mime: "text/plain", Size: 3}},
	})
	require.NoError(t, err)
	reply, err := f.svc.Post(ctx, bob, r.ID, PostInput{Text: "I answer this", ReplyTo: m.ID})
	require.NoError(t, err)
	_, err = f.svc.EditMessage(ctx, alice, r.ID, m.ID, "typo fixed")
	require.NoError(t, err)
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	f.clock.now = t0.Add(time.Hour)
	got, err := f.svc.DeleteMessage(ctx, alice, r.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, m.ID, got.ID)
	require.Equal(t, m.Seq, got.Seq)
	require.Equal(t, m.CreatedAt, got.CreatedAt)
	require.Equal(t, m.AuthorIid, got.AuthorIid)
	require.Equal(t, question.ID, got.ReplyTo, "the placeholder keeps what it replied to")
	require.Empty(t, got.Text)
	require.Empty(t, got.Attachments)
	require.True(t, got.DeletedAt.Valid)
	require.Equal(t, f.clock.now.UnixMilli(), got.DeletedAt.Int64)
	require.False(t, got.EditedAt.Valid, "a placeholder is not an edited message")
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM attachments WHERE message_id = ?", m.ID))
	require.Equal(t, []events.Event{events.MessageUpdated{RoomID: r.ID, MessageID: m.ID, Seq: m.Seq, Deleted: true, PrevText: "typo fixed"}}, drain(sub))

	stored, err := f.svc.GetMessage(ctx, bob, r.ID, reply.ID)
	require.NoError(t, err)
	require.Equal(t, m.ID, stored.ReplyTo, "the reply still points to the deleted message")
	page, _, err := f.svc.Messages(ctx, bob, r.ID, Page{})
	require.NoError(t, err)
	require.Len(t, page, 3, "the conversation keeps all its messages")
	require.Equal(t, m.ID, page[1].ID)
	require.True(t, page[1].DeletedAt.Valid)
	require.Empty(t, page[1].Text)
}

// DLT-01: deleting a deleted message answers with the placeholder again and publishes nothing.
func TestDeleteIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")
	m := f.post(t, alice, r.ID, "oops")
	first, err := f.svc.DeleteMessage(ctx, alice, r.ID, m.ID)
	require.NoError(t, err)
	sub, cancel := f.bus.Subscribe(8)
	defer cancel()

	f.clock.now = t0.Add(time.Hour)
	again, err := f.svc.DeleteMessage(ctx, alice, r.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, first.DeletedAt, again.DeletedAt, "the time of the first delete stands")
	require.Empty(t, drain(sub))
}

// DLT-02, DLT-03: the author deletes; in a group so do its creator and the admins of its organization; in a
// DM only the author; a system notice is never deleted. Others who see the room are forbidden, the rest not
// found.
func TestDeleteWho(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	org := f.orgOf(t, alice)
	bob := f.colleague(t, org, "bob", orgs.PermissionMember)
	carol := f.colleague(t, org, "carol", orgs.PermissionMember)
	admin := f.colleague(t, org, "dana", orgs.PermissionAdmin)
	stranger := f.user(t, "stranger", dto.RoleMember)
	r := f.room(t, alice, "ops") // alice created it: she manages it
	f.addMember(t, alice, r.ID, dto.MemberKindUser, bob.UserID)
	f.addMember(t, alice, r.ID, dto.MemberKindUser, carol.UserID)

	by := func(p authn.Principal, text string) Message { return f.post(t, p, r.ID, text) }
	type try struct {
		who  authn.Principal
		name string
		want error
	}
	for _, tc := range []try{
		{bob, "its author", nil},
		{alice, "the creator of the group", nil},
		{admin, "an admin of the organization", nil},
		{carol, "a member who is not its author", access.ErrForbidden},
		{stranger, "someone who cannot view the room", access.ErrNotFound},
	} {
		m := by(bob, "bob's message for "+tc.name)
		_, err := f.svc.DeleteMessage(ctx, tc.who, r.ID, m.ID)
		if tc.want == nil {
			require.NoError(t, err, tc.name)
			require.True(t, f.deleted(t, m.ID), tc.name)
			continue
		}
		require.ErrorIs(t, err, tc.want, tc.name)
		require.False(t, f.deleted(t, m.ID), tc.name)
	}

	notice := f.plant(t, r.ID, "system", "", "", "system", PauseNotice)
	_, err := f.svc.DeleteMessage(ctx, alice, r.ID, notice)
	var verr *validation.Error
	require.ErrorAs(t, err, &verr, "not even a manager deletes a system notice")
	require.Contains(t, verr.Fields, "message")
	_, err = f.svc.DeleteMessage(ctx, carol, r.ID, notice)
	require.ErrorIs(t, err, access.ErrForbidden)

	// What a chat wrote before the connection proxy is history a manager may remove.
	chat := f.plant(t, r.ID, "chat", "a-chat", "", "text", "from a platform")
	_, err = f.svc.DeleteMessage(ctx, carol, r.ID, chat)
	require.ErrorIs(t, err, access.ErrForbidden)
	_, err = f.svc.DeleteMessage(ctx, alice, r.ID, chat)
	require.NoError(t, err)

	// Nobody manages a DM: only the author deletes, and whoever only views it as the manager of its AI cannot.
	atlas := f.agent(t, bob, "atlas") // bob manages Atlas, which sits in a DM with carol
	dm := f.dmAI(t, carol, atlas)
	mine := f.post(t, carol, dm, "carol to atlas")
	_, err = f.svc.DeleteMessage(ctx, bob, dm, mine.ID)
	require.ErrorIs(t, err, access.ErrForbidden, "the manager of the AI sees the DM and does not moderate it")
	_, err = f.svc.EditMessage(ctx, bob, dm, mine.ID, "bob rewrites carol")
	require.ErrorIs(t, err, access.ErrForbidden)
	_, err = f.svc.DeleteMessage(ctx, stranger, dm, mine.ID)
	require.ErrorIs(t, err, access.ErrNotFound)
	_, err = f.svc.DeleteMessage(ctx, carol, dm, mine.ID)
	require.NoError(t, err)

	own := f.dm(t, alice, bob)
	hers := f.post(t, alice, own, "alice to bob")
	_, err = f.svc.DeleteMessage(ctx, bob, own, hers.ID)
	require.ErrorIs(t, err, access.ErrForbidden, "the other participant of a DM cannot delete what the author wrote")
	_, err = f.svc.DeleteMessage(ctx, admin, own, hers.ID)
	require.ErrorIs(t, err, access.ErrNotFound, "an admin of the organization has no access to a DM")
}

// deleted tells whether the message is a placeholder.
func (f *fixture) deleted(t *testing.T, id string) bool {
	t.Helper()
	var n int
	require.NoError(t, f.raw.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM messages WHERE id = ? AND deleted_at IS NOT NULL", id).Scan(&n))
	return n == 1
}

// DLT-04: a delete releases the blobs its attachments were the last reference to, as deleting a room does -
// at once when they were written more than a minute ago, by the retention sweep when they are newer - and
// keeps those another message uses.
func TestDeleteReleasesTheBlobsNothingElseReferences(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	blobDir := f.dir + "/blobs"
	blobs, err := attachments.Open(blobDir)
	require.NoError(t, err)
	f.svc = New(Deps{Store: f.st, Access: f.az, Bus: f.bus, Now: f.clock.Now, Blobs: blobs})
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")

	old := t0.Add(-time.Hour)
	only := putBlob(t, blobs, blobDir, "only-here", old)
	twice := putBlob(t, blobs, blobDir, "twice-here", old)
	shared := putBlob(t, blobs, blobDir, "shared", old)
	fresh := putBlob(t, blobs, blobDir, "fresh", t0.Add(-59*time.Second))
	justOld := putBlob(t, blobs, blobDir, "just-old", t0.Add(-61*time.Second))
	doomed, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "files", Files: []File{only, twice, twice, shared, fresh, justOld}})
	require.NoError(t, err)
	_, err = f.svc.Post(ctx, alice, r.ID, PostInput{Text: "keeps one", Files: []File{shared}})
	require.NoError(t, err)

	_, err = f.svc.DeleteMessage(ctx, alice, r.ID, doomed.ID)
	require.NoError(t, err)
	require.False(t, blobExists(blobDir, only.SHA256), "a blob only the deleted message used is removed at once")
	require.False(t, blobExists(blobDir, twice.SHA256), "so is a blob it used twice")
	require.True(t, blobExists(blobDir, shared.SHA256), "a blob another message references stays")
	require.True(t, blobExists(blobDir, fresh.SHA256), "a blob written 59 s ago is left to the sweep")
	require.False(t, blobExists(blobDir, justOld.SHA256), "a blob written 61 s ago is removed")

	f.clock.now = t0.Add(2 * time.Hour)
	res, err := f.svc.Cleanup(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, res.Blobs, "the sweep is the backstop for what the delete left")
	require.False(t, blobExists(blobDir, fresh.SHA256))
	require.True(t, blobExists(blobDir, shared.SHA256), "and keeps what is referenced")
}

// DLT-05: the placeholder is what every read returns in the message's place: a page, one message, the feed
// of the latest messages.
func TestReadsReturnThePlaceholder(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")
	f.post(t, alice, r.ID, "one")
	m, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "secret", Files: []File{{SHA256: strings.Repeat("c", 64), Name: "c.txt", Mime: "text/plain", Size: 3}}})
	require.NoError(t, err)
	f.post(t, alice, r.ID, "three")
	file := m.Attachments[0].ID
	_, err = f.svc.Attachment(ctx, alice, file)
	require.NoError(t, err, "the file is there before the delete")
	_, err = f.svc.DeleteMessage(ctx, alice, r.ID, m.ID)
	require.NoError(t, err)

	one, err := f.svc.GetMessage(ctx, alice, r.ID, m.ID)
	require.NoError(t, err)
	require.True(t, one.DeletedAt.Valid)
	require.Empty(t, one.Text)
	require.Empty(t, one.Attachments)

	feed, err := f.svc.Activity(ctx, alice, 10)
	require.NoError(t, err)
	require.Len(t, feed, 3)
	require.True(t, feed[1].Message.DeletedAt.Valid, "the feed shows the placeholder")
	require.Empty(t, feed[1].Message.Text)
	after, _, err := f.svc.Messages(ctx, alice, r.ID, Page{AfterSeq: 1})
	require.NoError(t, err)
	require.True(t, after[0].DeletedAt.Valid, "a page after a sequence number does too")

	_, err = f.svc.Attachment(ctx, alice, file)
	require.ErrorIs(t, err, access.ErrNotFound, "the file of a deleted message cannot be fetched")
}
