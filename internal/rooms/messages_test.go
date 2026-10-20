package rooms

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/validation"
)

func agentPrincipal(owner authn.Principal, agentID string) authn.Principal {
	return authn.Principal{Kind: authn.KindAgent, UserID: owner.UserID, AgentID: agentID}
}

func (f *fixture) addMember(t *testing.T, owner authn.Principal, roomID string, kind dto.MemberKind, id string, outbound bool) Member {
	t.Helper()
	m, err := f.svc.AddMember(context.Background(), owner, roomID, MemberInput{Kind: kind, MemberID: id, Outbound: &outbound})
	require.NoError(t, err)
	return m
}

func (f *fixture) post(t *testing.T, p authn.Principal, roomID, text string) Message {
	t.Helper()
	m, err := f.svc.Post(context.Background(), p, roomID, PostInput{Text: text})
	require.NoError(t, err)
	return m
}

func outboxChats(m Message) []string {
	out := []string{}
	for _, o := range m.Outbox {
		out = append(out, o.ChatID)
	}
	sort.Strings(out)
	return out
}

func sorted(ids ...string) []string {
	out := append([]string{}, ids...)
	sort.Strings(out)
	return out
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

// DLV-03/04/05, ROOM-01: outbox entries follow the delivery plan.
func TestOutboxFollowsDeliveryPlan(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	a1, a2 := f.agent(t, alice, "a1"), f.agent(t, alice, "a2")
	_, c1 := f.chat(t, alice, "c1")
	_, c2 := f.chat(t, alice, "c2")
	_, c3 := f.chat(t, alice, "c3")
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindAgent, a1, true)
	f.addMember(t, alice, r.ID, dto.MemberKindAgent, a2, true)
	m1 := f.addMember(t, alice, r.ID, dto.MemberKindChat, c1, true)
	f.addMember(t, alice, r.ID, dto.MemberKindChat, c2, false)
	f.addMember(t, alice, r.ID, dto.MemberKindChat, c3, true)

	// DLV-03: users and agents without recipients reach the chats with outbound on.
	m := f.post(t, alice, r.ID, "to all")
	require.Equal(t, sorted(c1, c3), outboxChats(m))
	for _, o := range m.Outbox {
		require.Equal(t, string(dto.OutboxStatusPending), o.Status)
		require.Zero(t, o.Attempts)
		require.Equal(t, t0.UnixMilli(), o.NextAttemptAt)
	}
	require.Equal(t, sorted(c1, c3), outboxChats(f.post(t, agentPrincipal(alice, a1), r.ID, "agent to all")))

	// DLV-04: explicit recipients reach only those chats; a listen-only chat (outbound off)
	// receives nothing, so naming it is rejected with 422 recipients=listen-only.
	m, err := f.svc.Post(ctx, alice, r.ID, PostInput{Text: "to c3", Recipients: []string{c3}})
	require.NoError(t, err)
	require.Equal(t, []string{c3}, outboxChats(m))
	require.Equal(t, []string{c3}, m.Recipients())
	_, err = f.svc.Post(ctx, alice, r.ID, PostInput{Text: "to c2", Recipients: []string{c3, c2}})
	var verr *validation.Error
	require.ErrorAs(t, err, &verr)
	require.Equal(t, "listen-only", verr.Fields["recipients"])
	_, err = f.svc.Post(ctx, alice, r.ID, PostInput{Text: "bad", Recipients: []string{store.NewID()}})
	require.ErrorAs(t, err, &verr)
	require.Contains(t, verr.Fields, "recipients")

	// DLV-05: chat messages reach the other outbound chats only when the room bridges chats;
	// c2 has outbound off and receives nothing.
	m, err = f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: c1, SenderID: "5511999990000", Text: "from c1"})
	require.NoError(t, err)
	require.Empty(t, outboxChats(m))
	_, err = f.svc.Update(ctx, alice, r.ID, UpdateInput{BridgeChats: ptr(true)})
	require.NoError(t, err)
	m, err = f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: c1, SenderID: "5511999990000", Text: "bridged"})
	require.NoError(t, err)
	require.Equal(t, []string{c3}, outboxChats(m))

	// ROOM-01: a removed chat gets nothing afterwards.
	require.NoError(t, f.svc.RemoveMember(ctx, alice, r.ID, m1.ID))
	require.Equal(t, []string{c3}, outboxChats(f.post(t, alice, r.ID, "after removal")))
	_, err = f.svc.PostFromChat(ctx, r.ID, ChatMessage{ChatID: c1, Text: "still there?"})
	require.ErrorIs(t, err, access.ErrNotFound, "a removed chat cannot post either")
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
	require.Empty(t, notice[0].Outbox, "the notice is not sent to chats")
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
	_, chat := f.chat(t, alice, "c1")
	r := f.room(t, alice, "ops")
	f.addMember(t, alice, r.ID, dto.MemberKindChat, chat, true)
	for i := 0; i < 7; i++ {
		f.post(t, alice, r.ID, "m")
	}
	seqsOf := func(pg Page) ([]int64, bool) {
		list, more, err := f.svc.Messages(ctx, alice, r.ID, pg)
		require.NoError(t, err)
		out := []int64{}
		for _, m := range list {
			out = append(out, m.Seq)
			require.Len(t, m.Outbox, 1, "outbox entries come with each message")
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

// recordingSender records every delivery the outbox worker attempts.
type recordingSender struct {
	mu    sync.Mutex
	calls []delivery.Delivery
}

func (s *recordingSender) Send(_ context.Context, d delivery.Delivery) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, d)
	return "ext-" + d.OutboxID, nil
}

// ROOM-01: removing a chat member cancels its not-yet-sent deliveries in that room, so a
// message queued before the removal is never sent; sent history and other rooms stay.
func TestRemoveMemberCancelsQueuedDeliveries(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	alice := f.user(t, "alice", dto.RoleMember)
	_, chat := f.chat(t, alice, "c1")
	_, kept := f.chat(t, alice, "c2")
	r := f.room(t, alice, "ops")
	other := f.room(t, alice, "other")
	m := f.addMember(t, alice, r.ID, dto.MemberKindChat, chat, true)
	f.addMember(t, alice, r.ID, dto.MemberKindChat, kept, true)
	f.addMember(t, alice, other.ID, dto.MemberKindChat, chat, true)

	sent := f.post(t, alice, r.ID, "already sent")
	f.exec(t, "UPDATE outbox SET status = 'sent' WHERE message_id = ?", sent.ID)
	f.post(t, alice, r.ID, "queued before removal")
	backoff := f.post(t, alice, r.ID, "waiting for retry")
	f.exec(t, "UPDATE outbox SET attempts = 2, next_attempt_at = ? WHERE message_id = ?", t0.Add(time.Minute).UnixMilli(), backoff.ID)
	f.post(t, alice, other.ID, "other room")

	require.NoError(t, f.svc.RemoveMember(ctx, alice, r.ID, m.ID))

	require.Equal(t, 0, f.count(t, "SELECT COUNT(*) FROM outbox o JOIN messages m ON m.id = o.message_id WHERE m.room_id = ? AND o.chat_id = ? AND o.status = 'pending'", r.ID, chat))
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM outbox WHERE message_id = ? AND chat_id = ? AND status = 'sent'", sent.ID, chat), "sent history stays")
	require.Equal(t, 2, f.count(t, "SELECT COUNT(*) FROM outbox o JOIN messages m ON m.id = o.message_id WHERE m.room_id = ? AND o.chat_id = ? AND o.status = 'pending'", r.ID, kept))
	require.Equal(t, 1, f.count(t, "SELECT COUNT(*) FROM outbox o JOIN messages m ON m.id = o.message_id WHERE m.room_id = ? AND o.chat_id = ? AND o.status = 'pending'", other.ID, chat))

	f.clock.mu.Lock()
	f.clock.now = t0.Add(time.Hour)
	f.clock.mu.Unlock()
	s := &recordingSender{}
	w := delivery.NewOutboxWorker(delivery.OutboxOptions{Store: f.st, Bus: f.bus, Sender: s, Now: f.clock.Now})
	require.NoError(t, w.Flush(ctx))
	for _, d := range s.calls {
		require.False(t, d.RoomID == r.ID && d.ChatID == chat, "ROOM-01: removed chat received a delivery queued before its removal")
	}
	require.Len(t, s.calls, 3, "the other chat (2) and the other room (1) still get theirs")
}
