package rooms

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

func agentPrincipal(owner authn.Principal, agentID string) authn.Principal {
	return authn.Principal{Kind: authn.KindAgent, UserID: owner.UserID, AgentID: agentID}
}

// addMember adds the member through the service. A chat is refused as a member (CID-19), so a chat
// is planted the way a room built before the connection proxy holds it: the tests that use one
// cover the history those rooms still hold.
func (f *fixture) addMember(t *testing.T, owner authn.Principal, roomID string, kind dto.MemberKind, id string, outbound bool) Member {
	t.Helper()
	if kind == dto.MemberKindChat {
		return f.seedChatMember(t, owner, roomID, id, outbound)
	}
	m, err := f.svc.AddMember(context.Background(), owner, roomID, MemberInput{Kind: kind, MemberID: id, Outbound: &outbound})
	require.NoError(t, err)
	return m
}

// seedChatMember plants a chat member row and returns the member as the room lists it.
func (f *fixture) seedChatMember(t *testing.T, owner authn.Principal, roomID, chatID string, outbound bool) Member {
	t.Helper()
	ctx := context.Background()
	rowID := store.NewID()
	require.NoError(t, f.st.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateRoomMember(ctx, db.CreateRoomMemberParams{
			ID: rowID, RoomID: roomID, Kind: string(dto.MemberKindChat), MemberID: chatID,
			Outbound: outbound, AddedBy: owner.UserID, CreatedAt: t0.UnixMilli(),
		})
	}))
	m, err := f.svc.member(ctx, roomID, rowID)
	require.NoError(t, err)
	return m
}

func (f *fixture) post(t *testing.T, p authn.Principal, roomID, text string) Message {
	t.Helper()
	m, err := f.svc.Post(context.Background(), p, roomID, PostInput{Text: text})
	require.NoError(t, err)
	return m
}

// drain returns the events published so far.
func drain(ch <-chan events.Event) []events.Event {
	var out []events.Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

// MSG-01: 50 concurrent posts get the sequence numbers 1..50 without gaps or duplicates.
func TestConcurrentPostsGetGaplessSequence(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "busy")

	const n = 50
	seqs := make([]int64, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			m, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "concurrent"})
			seqs[i], errs[i] = m.Seq, err
		}(i)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, s := range seqs {
		require.Equal(t, int64(i+1), s)
	}
	require.Equal(t, n, f.count(t, "SELECT COUNT(DISTINCT seq) FROM messages WHERE room_id = ? AND seq BETWEEN 1 AND 50", r.ID))
	got, err := f.svc.Get(ctx, alice, r.ID)
	require.NoError(t, err)
	require.Equal(t, int64(n), got.LastSeq())
}

// MSG-02: empty text without attachments and text over 16,000 characters are rejected,
// without storing anything or using a sequence number.
func TestPostValidation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")

	for _, text := range []string{"", "  \n\t", strings.Repeat("é", dto.MaxTextLength+1)} {
		_, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: text})
		var verr *validation.Error
		require.ErrorAs(t, err, &verr)
		require.Contains(t, verr.Fields, "text")
	}
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM messages"))

	m := f.post(t, alice, r.ID, strings.Repeat("é", dto.MaxTextLength))
	require.Equal(t, int64(1), m.Seq, "rejected posts do not consume sequence numbers")
	withFile, err := f.svc.Post(ctx, alice, r.ID, PostInput{Files: []File{{SHA256: strings.Repeat("a", 64), Name: "a.png", Mime: "image/png", Size: 3}}})
	require.NoError(t, err)
	require.Equal(t, int64(2), withFile.Seq)
	require.Len(t, withFile.Attachments, 1)
	require.Equal(t, "a.png", withFile.Attachments[0].Name)
	require.Equal(t, string(dto.AttachmentStatusStored), withFile.Attachments[0].Status)
}

// ACL-03/ACL-04 for posting: view-only is forbidden, strangers and non-member agents see nothing.
func TestPostAuthorization(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	viewer := f.user(t, "viewer", dto.RoleMember)
	poster := f.user(t, "poster", dto.RoleMember)
	stranger := f.user(t, "stranger", dto.RoleMember)
	member, outsider := f.agent(t, alice, "member"), f.agent(t, alice, "outsider")
	r := f.room(t, alice, "ops")
	f.grant(t, access.KindRoom, r.ID, viewer.UserID, dto.LevelView)
	f.grant(t, access.KindRoom, r.ID, poster.UserID, dto.LevelPost)
	f.addMember(t, alice, r.ID, dto.MemberKindAgent, member, true)

	_, err := f.svc.Post(ctx, viewer, r.ID, PostInput{Text: "hi"})
	require.ErrorIs(t, err, access.ErrForbidden)
	_, err = f.svc.Post(ctx, stranger, r.ID, PostInput{Text: "hi"})
	require.ErrorIs(t, err, access.ErrNotFound)
	_, err = f.svc.Post(ctx, agentPrincipal(alice, outsider), r.ID, PostInput{Text: "hi"})
	require.ErrorIs(t, err, access.ErrNotFound)

	m := f.post(t, poster, r.ID, "hi")
	require.Equal(t, string(dto.AuthorKindUser), m.AuthorKind)
	require.Equal(t, poster.UserID, m.AuthorID)
	m = f.post(t, agentPrincipal(alice, member), r.ID, "hi")
	require.Equal(t, string(dto.AuthorKindAgent), m.AuthorKind)
	require.Equal(t, member, m.AuthorID)

	_, _, err = f.svc.Messages(ctx, stranger, r.ID, Page{})
	require.ErrorIs(t, err, access.ErrNotFound)
	list, _, err := f.svc.Messages(ctx, viewer, r.ID, Page{})
	require.NoError(t, err)
	require.Len(t, list, 2)
}

// CID-22: a room sends nothing to a chat. Whoever posts (a user, an agent, a chat), whatever they
// reply to or react to, a chat member of an old room gets no outbox row.
func TestPostsPlanNoDeliveryForChatMembers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	a1 := f.agent(t, alice, "a1")
	_, open := f.chat(t, alice, "open")
	_, quiet := f.chat(t, alice, "quiet")
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindAgent, a1, true)
	f.addMember(t, alice, r.ID, dto.MemberKindChat, open, true)
	f.addMember(t, alice, r.ID, dto.MemberKindChat, quiet, false)

	fromChat, err := f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: open, SenderID: "5511999990000", Text: "can you help?"})
	require.NoError(t, err)
	posts := []Message{
		f.post(t, alice, r.ID, "from the user"),
		f.post(t, agentPrincipal(alice, a1), r.ID, "from the agent"),
		fromChat,
	}
	for _, p := range []authn.Principal{alice, agentPrincipal(alice, a1)} {
		reply, err := f.svc.Post(ctx, p, r.ID, PostInput{Text: "answer", ReplyTo: fromChat.ID})
		require.NoError(t, err)
		reaction, err := f.svc.Post(ctx, p, r.ID, PostInput{Text: "👍", ReplyTo: fromChat.ID, Kind: dto.MessageKindReaction})
		require.NoError(t, err)
		posts = append(posts, reply, reaction)
	}
	for _, m := range posts {
		require.Empty(t, m.Recipients(), "no chat is named")
	}
	require.Zero(t, f.count(t, "SELECT COUNT(*) FROM outbox"), "no delivery is planned for a chat")
	require.Equal(t, len(posts), f.count(t, "SELECT COUNT(*) FROM messages WHERE room_id = ?", r.ID), "every post is stored")
}

// DLV-06 input: author_user_id is the user, the agent's owner, the verified sender's user,
// or empty; author_name is filled per author kind.
func TestAuthorResolution(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	agent := f.agent(t, alice, "builder")
	_, chat := f.chat(t, alice, "family")
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindAgent, agent, true)
	f.addMember(t, alice, r.ID, dto.MemberKindChat, chat, true)

	m := f.post(t, alice, r.ID, "user")
	require.Equal(t, alice.UserID, m.AuthorUserID)
	require.Equal(t, "Alice", m.AuthorName)
	m = f.post(t, agentPrincipal(alice, agent), r.ID, "agent")
	require.Equal(t, alice.UserID, m.AuthorUserID)
	require.Equal(t, "builder", m.AuthorName)
	m, err := f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: chat, SenderID: "s1", SenderName: "Bob P", SenderUserID: bob.UserID, Text: "verified"})
	require.NoError(t, err)
	require.Equal(t, bob.UserID, m.AuthorUserID)
	require.Equal(t, "Bob P", m.AuthorName)
	require.Equal(t, "s1", m.SenderID)
	m, err = f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: chat, SenderID: "s2", Text: "unverified"})
	require.NoError(t, err)
	require.Empty(t, m.AuthorUserID)
	require.Equal(t, "family", m.AuthorName, "an unnamed sender shows the chat title")
	require.Equal(t, string(dto.MessageKindText), m.Kind)
}

// CON-10: a repeated external id from the same chat returns the stored message and
// publishes nothing.
func TestDuplicateExternalIDReturnsExistingMessage(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	_, c1 := f.chat(t, alice, "c1")
	_, c2 := f.chat(t, alice, "c2")
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindChat, c1, true)
	f.addMember(t, alice, r.ID, dto.MemberKindChat, c2, true)
	sub, cancel := f.bus.Subscribe(16)
	defer cancel()

	first, err := f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: c1, ExternalID: "wamid-1", Text: "once"})
	require.NoError(t, err)
	require.Equal(t, []events.Event{events.MessageCreated{RoomID: r.ID, MessageID: first.ID, Seq: 1}}, drain(sub))

	again, err := f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: c1, ExternalID: "wamid-1", Text: "once"})
	require.NoError(t, err)
	require.Equal(t, first.ID, again.ID)
	require.Empty(t, drain(sub), "no event for a duplicate")
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM messages"))

	other, err := f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: c2, ExternalID: "wamid-1", Text: "other chat"})
	require.NoError(t, err)
	require.NotEqual(t, first.ID, other.ID)
	require.Equal(t, int64(2), other.Seq, "the duplicate did not use a sequence number")
}

// DLV-08/09/10: 25 consecutive agent messages pause agent delivery with a system message;
// user and chat messages reset the streak; a manager resumes.
func TestLoopGuardPauseAndResume(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	poster := f.user(t, "poster", dto.RoleMember)
	a1, a2 := f.agent(t, alice, "a1"), f.agent(t, alice, "a2")
	_, chat := f.chat(t, alice, "c1")
	r := f.room(t, alice, "ops")
	f.grant(t, access.KindRoom, r.ID, poster.UserID, dto.LevelPost)
	f.addMember(t, alice, r.ID, dto.MemberKindAgent, a1, true)
	f.addMember(t, alice, r.ID, dto.MemberKindAgent, a2, true)
	f.addMember(t, alice, r.ID, dto.MemberKindChat, chat, true)
	streak := func() int { return f.count(t, "SELECT agent_streak FROM rooms WHERE id = ?", r.ID) }
	agents := []authn.Principal{agentPrincipal(alice, a1), agentPrincipal(alice, a2)}

	for i := 0; i < 24; i++ {
		f.post(t, agents[i%2], r.ID, "ping")
	}
	require.Equal(t, 24, streak())
	f.post(t, alice, r.ID, "user interrupts") // DLV-10
	require.Equal(t, 0, streak())
	for i := 0; i < 24; i++ {
		f.post(t, agents[i%2], r.ID, "ping")
	}
	_, err := f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: chat, Text: "chat interrupts"}) // DLV-10
	require.NoError(t, err)
	require.Equal(t, 0, streak())

	for i := 0; i < 24; i++ {
		f.post(t, agents[i%2], r.ID, "ping")
	}
	got, err := f.svc.Get(ctx, alice, r.ID)
	require.NoError(t, err)
	require.False(t, got.AgentsPaused)

	sub, cancel := f.bus.Subscribe(16)
	defer cancel()
	last := f.post(t, agents[0], r.ID, "25th")
	got, err = f.svc.Get(ctx, alice, r.ID)
	require.NoError(t, err)
	require.True(t, got.AgentsPaused)
	require.Equal(t, last.Seq+1, got.LastSeq(), "the pause notice follows the 25th message")

	notice, _, err := f.svc.Messages(ctx, alice, r.ID, Page{Limit: 1})
	require.NoError(t, err)
	require.Len(t, notice, 1)
	require.Equal(t, string(dto.AuthorKindSystem), notice[0].AuthorKind)
	require.Equal(t, string(dto.MessageKindSystem), notice[0].Kind)
	require.Equal(t, PauseNotice, notice[0].Text)
	require.Equal(t, []events.Event{
		events.MessageCreated{RoomID: r.ID, MessageID: last.ID, Seq: last.Seq},
		events.MessageCreated{RoomID: r.ID, MessageID: notice[0].ID, Seq: last.Seq + 1},
		events.RoomChanged{RoomID: r.ID},
	}, drain(sub))

	// Only one notice, even when agents keep posting.
	f.post(t, agents[1], r.ID, "26th")
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM messages WHERE room_id = ? AND author_kind = 'system'", r.ID))

	// DLV-09: a user without manage cannot resume; a manager can.
	_, err = f.svc.Resume(ctx, poster, r.ID)
	require.ErrorIs(t, err, access.ErrForbidden)
	_, err = f.svc.Resume(ctx, f.user(t, "stranger", dto.RoleMember), r.ID)
	require.ErrorIs(t, err, access.ErrNotFound)
	f.post(t, alice, r.ID, "users do not resume")
	got, err = f.svc.Get(ctx, alice, r.ID)
	require.NoError(t, err)
	require.True(t, got.AgentsPaused)

	got, err = f.svc.Resume(ctx, alice, r.ID)
	require.NoError(t, err)
	require.False(t, got.AgentsPaused)
	require.Equal(t, 0, streak())
}

// Pagination: newest first by default and with before_seq, oldest first with after_seq.
func TestListPages(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r := f.room(t, alice, "ops")
	for i := 0; i < 7; i++ {
		f.post(t, alice, r.ID, "m")
	}
	seqsOf := func(pg Page) ([]int64, bool) {
		list, more, err := f.svc.Messages(ctx, alice, r.ID, pg)
		require.NoError(t, err)
		out := []int64{}
		for _, m := range list {
			out = append(out, m.Seq)
		}
		return out, more
	}
	cases := []struct {
		pg   Page
		want []int64
		more bool
	}{
		{Page{Limit: 3}, []int64{7, 6, 5}, true},
		{Page{Limit: 3, BeforeSeq: 5}, []int64{4, 3, 2}, true},
		{Page{Limit: 3, BeforeSeq: 2}, []int64{1}, false},
		{Page{Limit: 3, AfterSeq: 5}, []int64{6, 7}, false},
		{Page{Limit: 2, AfterSeq: 1}, []int64{2, 3}, true},
		{Page{}, []int64{7, 6, 5, 4, 3, 2, 1}, false},
	}
	for _, tc := range cases {
		got, more := seqsOf(tc.pg)
		require.Equal(t, tc.want, got, "%+v", tc.pg)
		require.Equal(t, tc.more, more, "%+v", tc.pg)
	}
}

// reply_to must name a message of the same room.
func TestReplyTo(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	r, other := f.room(t, alice, "ops"), f.room(t, alice, "other")
	orig := f.post(t, alice, r.ID, "question")
	elsewhere := f.post(t, alice, other.ID, "elsewhere")

	m, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "answer", ReplyTo: orig.ID})
	require.NoError(t, err)
	require.Equal(t, orig.ID, m.ReplyTo)
	_, err = f.svc.Post(ctx, alice, r.ID, PostInput{Text: "answer", ReplyTo: elsewhere.ID})
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "reply_to")
}

// ROUTE-07: while a connector routes new chats to a room, broadcast and bridge_chats cannot
// be turned on (422 on the field, nothing applied), for the owner and for another manager;
// turning them off, and other settings, still work.
func TestCatchAllRoomRefusesBroadcastAndBridge(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	bob := f.user(t, "bob", dto.RoleMember)
	r := f.room(t, alice, "inbox")
	f.grant(t, access.KindRoom, r.ID, bob.UserID, dto.LevelManage)
	conn, _ := f.chat(t, alice, "c1")
	f.exec(t, "UPDATE connectors SET auto_route_scope = 'dms', auto_route_room_id = ? WHERE id = ?", r.ID, conn)
	f.exec(t, "UPDATE rooms SET broadcast = 0, bridge_chats = 0 WHERE id = ?", r.ID)

	got, err := f.svc.Get(ctx, alice, r.ID)
	require.NoError(t, err)
	require.True(t, got.CatchAll)
	for _, p := range []authn.Principal{alice, bob} {
		var verr *validation.Error
		_, err = f.svc.Update(ctx, p, r.ID, UpdateInput{Broadcast: ptr(true), Name: ptr("renamed")})
		require.ErrorAs(t, err, &verr)
		require.Equal(t, map[string]string{"broadcast": "must stay off while the room is a catch-all room"}, verr.Fields)
		_, err = f.svc.Update(ctx, p, r.ID, UpdateInput{BridgeChats: ptr(true)})
		require.ErrorAs(t, err, &verr)
		require.Equal(t, map[string]string{"bridge_chats": "must stay off while the room is a catch-all room"}, verr.Fields)
	}
	got, err = f.svc.Get(ctx, alice, r.ID)
	require.NoError(t, err)
	require.Equal(t, "inbox", got.Name, "a refused update applies nothing")
	require.False(t, got.Broadcast)
	require.False(t, got.BridgeChats)
	_, err = f.svc.Update(ctx, bob, r.ID, UpdateInput{Broadcast: ptr(false), BridgeChats: ptr(false), Name: ptr("support inbox")})
	require.NoError(t, err)

	f.exec(t, "UPDATE connectors SET auto_route_scope = 'off', auto_route_room_id = NULL WHERE id = ?", conn)
	got, err = f.svc.Update(ctx, alice, r.ID, UpdateInput{Broadcast: ptr(true)})
	require.NoError(t, err, "no longer a catch-all room")
	require.True(t, got.Broadcast)
	require.False(t, got.CatchAll)
}
