package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

func conversationsOf(t *testing.T, c *testkit.Client) []dto.Conversation {
	t.Helper()
	return testkit.Decode[dto.List[dto.Conversation]](c.Get("/api/v1/conversations").RequireStatus(http.StatusOK)).Items
}

// conversationIn finds the conversation of a room, failing when the list lacks it.
func conversationIn(t *testing.T, list []dto.Conversation, roomID string) dto.Conversation {
	t.Helper()
	for _, c := range list {
		if c.ID == roomID {
			return c
		}
	}
	require.Failf(t, "conversation missing", "room %s is not in the list", roomID)
	return dto.Conversation{}
}

func conversationIDs(list []dto.Conversation) []string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.ID
	}
	return out
}

func say(c *testkit.Client, roomID, text string) dto.Message {
	c.T.Helper()
	return testkit.Decode[dto.Message](c.Post("/api/v1/rooms/"+roomID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

func markRead(c *testkit.Client, roomID string, seq *int64) *testkit.Response {
	c.T.Helper()
	return c.Put("/api/v1/rooms/"+roomID+"/read", dto.MarkReadRequest{Seq: seq})
}

func seqPtr(n int64) *int64 { return &n }

// clockEnv starts the app on a manual clock, so that message times are known.
func clockEnv(t *testing.T) (*testkit.Env, *testkit.Clock) {
	t.Helper()
	clock := testkit.NewClock(time.Now().UTC().Truncate(time.Second))
	return testkit.New(t, testkit.WithClock(clock.Now)), clock
}

// DM-06, LAY-01: the list holds the groups a user created, administers or is a member of and the DMs
// they take part in, newest activity first; a conversation without messages sorts by its creation;
// each carries its kind, title (the group name, or the other participant's display name), the
// counterpart of a DM, the user's level and the member names.
func TestConversationListOrdersByNewestActivity(t *testing.T) {
	env, clock := clockEnv(t)
	alice, bob, carol := env.NewUser("alice"), env.NewUser("bob"), env.NewUser("carol")
	patchProfile(bob, bob.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Bob B."), Handle: ptr("bobby")}).RequireStatus(http.StatusOK)

	clock.Advance(time.Minute)
	owned := createRoom(alice, "Owned") // created first, never written to
	clock.Advance(time.Minute)
	// carol makes a group in alice's organization, which alice administers without being in the group.
	env.AddMember(alice.PersonalOrg().ID, carol.User.ID, orgs.PermissionMember, "")
	granted := groupIn(t, env, carol, alice.PersonalOrg().ID, "Granted")
	clock.Advance(time.Minute)
	member := createRoom(bob, "Member of")
	addToRoom(t, env, bob, member.ID, alice)
	clock.Advance(time.Minute)
	dm := dmWith(alice, bob.User.ID, http.StatusCreated)
	hidden := createRoom(carol, "Not for alice")
	carolDM := dmWith(carol, bob.User.ID, http.StatusCreated) // alice is no participant

	// Activity: granted (newest), then the DM, then the group alice is a member of.
	clock.Advance(time.Minute)
	say(bob, member.ID, "first in member")
	clock.Advance(time.Minute)
	say(bob, dm.ID, "hi alice")
	clock.Advance(time.Minute)
	last := say(carol, granted.ID, "latest in granted")
	say(carol, carolDM.ID, "not for alice")

	list := conversationsOf(t, alice)
	require.Equal(t, []string{granted.ID, dm.ID, member.ID, owned.ID}, conversationIDs(list))
	require.NotContains(t, conversationIDs(list), hidden.ID)
	require.NotContains(t, conversationIDs(list), carolDM.ID, "a DM of two other users")

	g := conversationIn(t, list, granted.ID)
	require.Equal(t, dto.ConversationKindGroup, g.Kind)
	require.Equal(t, "Granted", g.Title)
	require.Nil(t, g.Counterpart)
	require.Equal(t, dto.LevelManage, g.MyLevel, "an admin of the group's organization manages it")
	require.True(t, g.ActivityAt.Equal(last.CreatedAt), "activity is the time of the last message")

	d := conversationIn(t, list, dm.ID)
	require.Equal(t, dto.ConversationKindDM, d.Kind)
	require.Equal(t, "Bob B.", d.Title, "a DM is titled by the other participant")
	require.NotNil(t, d.Counterpart)
	require.Equal(t, []any{bob.User.ID, "bobby", dto.IntelligenceRoleUser}, []any{d.Counterpart.ID, d.Counterpart.Handle, d.Counterpart.Role})
	require.Equal(t, dto.LevelPost, d.MyLevel)
	require.Equal(t, []string{"Bob B. @bobby"}, d.MemberNames)

	m := conversationIn(t, list, member.ID)
	require.Equal(t, dto.LevelPost, m.MyLevel, "a user member posts (AD-011)")
	require.Equal(t, []string{"alice @alice"}, m.MemberNames, "the member's name and handle, for search")

	o := conversationIn(t, list, owned.ID)
	require.Equal(t, dto.LevelManage, o.MyLevel)
	require.Nil(t, o.LastMessage, "no message yet")
	require.Zero(t, o.Unread)
	require.True(t, o.ActivityAt.Equal(owned.CreatedAt), "a room without messages sorts by its creation")
	require.NotNil(t, o.MemberNames, "an empty list, not null")
	require.Empty(t, o.MemberNames)

	// A new message moves its conversation to the top.
	clock.Advance(time.Minute)
	say(alice, owned.ID, "now the newest")
	require.Equal(t, owned.ID, conversationsOf(t, alice)[0].ID)
}

// DM-06: the list does not hold the DMs a user only views through an AI, and a stranger's list is
// empty; strangers' conversations never leak into a list.
func TestConversationListHoldsOwnConversationsOnly(t *testing.T) {
	env := testkit.New(t)
	owner, alice, stranger := env.NewUser("owner"), env.NewUser("alice"), env.NewUser("stranger")
	atlas := env.NewAI(owner, "atlas")
	shareAI(owner, atlas, alice)
	hers := dmWith(alice, atlas, http.StatusCreated)
	mine := dmWith(owner, atlas, http.StatusCreated)
	say(alice, hers.ID, "private to alice and atlas")

	require.Equal(t, []string{hers.ID}, conversationIDs(conversationsOf(t, alice)))
	require.Equal(t, []string{mine.ID}, conversationIDs(conversationsOf(t, owner)), "the owner views alice's DM by id, it is not listed")
	owner.Get("/api/v1/rooms/" + hers.ID).RequireStatus(http.StatusOK)
	require.Empty(t, conversationsOf(t, stranger))
	require.NotNil(t, testkit.Decode[map[string]any](stranger.Get("/api/v1/conversations").RequireStatus(http.StatusOK))["items"], "an empty list, not null")

	// The AI is the DM's counterpart, with its role.
	d := conversationIn(t, conversationsOf(t, alice), hers.ID)
	require.Equal(t, []any{atlas, dto.IntelligenceRoleAI, dto.IntelligenceKindAI}, []any{d.Counterpart.ID, d.Counterpart.Role, d.Counterpart.Kind})
	require.Equal(t, "atlas", d.Title)
}

// DM-06: unread is per user and per conversation: the messages after the user's read position,
// the pre-upgrade baseline, whichever is later, without the user's own messages.
func TestUnreadCountsPerUser(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	atlas := env.NewAI(alice, "atlas")
	dm := dmWith(alice, bob.User.ID, http.StatusCreated)
	group := createRoom(alice, "Ops")
	addToRoom(t, env, alice, group.ID, bob)
	alice.Post("/api/v1/rooms/"+group.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: atlas}).RequireStatus(http.StatusCreated)

	unread := func(c *testkit.Client, roomID string) int {
		t.Helper()
		return conversationIn(t, conversationsOf(t, c), roomID).Unread
	}
	for i := range 3 {
		say(bob, dm.ID, fmt.Sprintf("from bob %d", i))
	}
	say(alice, dm.ID, "from alice")
	require.Equal(t, 3, unread(alice, dm.ID), "bob's three; her own message is not unread")
	require.Equal(t, 1, unread(bob, dm.ID), "alice's one; his own three are not unread")

	// Messages of every kind of author count for a user who did not write them: users, AIs and the
	// system's notices.
	say(alice, group.ID, "by alice")
	_, err := env.App.Rooms.Post(context.Background(), authn.Principal{Kind: authn.KindAgent, UserID: alice.User.ID, AgentID: atlas}, group.ID, rooms.PostInput{Text: "by atlas"})
	require.NoError(t, err)
	require.Equal(t, 1, unread(alice, group.ID), "the AI's message; hers is her own")
	require.Equal(t, 2, unread(bob, group.ID), "alice's and the AI's")

	// The baseline hides history older than the upgrade; a cursor behind it changes nothing, one
	// ahead of it wins.
	for range 3 {
		say(alice, group.ID, "history")
	}
	require.Equal(t, 5, unread(bob, group.ID))
	setBaseline(t, env, group.ID, 3)
	require.Equal(t, 2, unread(bob, group.ID), "only messages 4 and 5 are after the baseline")
	markRead(bob, group.ID, seqPtr(1)).RequireStatus(http.StatusOK)
	require.Equal(t, 2, unread(bob, group.ID), "a read position behind the baseline changes nothing")
	markRead(bob, group.ID, seqPtr(4)).RequireStatus(http.StatusOK)
	require.Equal(t, 1, unread(bob, group.ID), "ahead of the baseline it wins")
	require.Zero(t, unread(alice, group.ID), "after the baseline she wrote the rest herself")
	say(bob, group.ID, "late")
	require.Equal(t, 1, unread(alice, group.ID), "bob's late message; his read position is not hers")
	require.Equal(t, 1, unread(bob, group.ID), "message 5 is after his position, his own late one is not unread")
}

func setBaseline(t *testing.T, env *testkit.Env, roomID string, seq int64) {
	t.Helper()
	require.NoError(t, env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.SetRoomReadBaseline(context.Background(), db.SetRoomReadBaselineParams{ReadBaseline: seq, ID: roomID})
	}))
}

// DM-07: marking a conversation read zeroes the unread count of that user only, never moves
// backwards, clamps to the newest message and, without a seq, reads everything.
func TestMarkReadIsMonotonicAndPerUser(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := createRoom(alice, "Ops")
	addToRoom(t, env, alice, room.ID, bob)
	unread := func(c *testkit.Client) int {
		t.Helper()
		return conversationIn(t, conversationsOf(t, c), room.ID).Unread
	}
	for i := range 5 {
		say(bob, room.ID, fmt.Sprintf("m%d", i+1))
	}
	require.Equal(t, 5, unread(alice))

	got := testkit.Decode[dto.ReadState](markRead(alice, room.ID, seqPtr(3)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ReadState{RoomID: room.ID, Seq: 3, Unread: 2}, got)
	require.Equal(t, 2, unread(alice))
	require.Zero(t, unread(bob), "bob wrote them all")
	say(alice, room.ID, "alice answers")
	require.Equal(t, 1, unread(bob), "bob's own position is untouched by alice's")

	// Never backwards.
	back := testkit.Decode[dto.ReadState](markRead(alice, room.ID, seqPtr(1)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ReadState{RoomID: room.ID, Seq: 3, Unread: 2}, back, "the answer is the position that holds")
	require.Equal(t, 2, unread(alice))
	markRead(alice, room.ID, seqPtr(0)).RequireStatus(http.StatusOK)
	require.Equal(t, 2, unread(alice))

	// Past the newest message clamps: a later message is still unread.
	far := testkit.Decode[dto.ReadState](markRead(alice, room.ID, seqPtr(9999)).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ReadState{RoomID: room.ID, Seq: 6, Unread: 0}, far)
	say(bob, room.ID, "after")
	require.Equal(t, 1, unread(alice), "the position was clamped to the newest message, not stored as 9999")

	// No seq: up to the newest message.
	all := testkit.Decode[dto.ReadState](markRead(alice, room.ID, nil).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ReadState{RoomID: room.ID, Seq: 7, Unread: 0}, all)
	require.Zero(t, unread(alice))

	// A negative position is invalid, and nothing changes.
	e := markRead(alice, room.ID, seqPtr(-1)).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "seq")
	alice.Do(http.MethodPut, "/api/v1/rooms/"+room.ID+"/read", map[string]any{"seq": "x"}).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	require.Zero(t, unread(alice))

	// An empty room can be marked read.
	empty := createRoom(alice, "Empty")
	zero := testkit.Decode[dto.ReadState](markRead(alice, empty.ID, nil).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ReadState{RoomID: empty.ID}, zero)
}

// DM-07 authorization: reading needs view - the creator, a member of the room or an admin of its
// organization (the viewer role here) - and others get 404; the manager of an AI may mark the DM it
// views; an agent token and an anonymous caller are refused.
func TestMarkReadAuthorization(t *testing.T) {
	m := newMatrix(t)
	owner, viewer, poster := m.as(t, roleOwner), m.as(t, roleViewer), m.as(t, rolePoster)
	room := createRoom(owner, "Ops")
	joinOrgOf(t, m.env, room.ID, viewer, orgs.PermissionAdmin)
	addToRoom(t, m.env, owner, room.ID, poster)
	say(owner, room.ID, "hello")

	for role, want := range map[matrixRole]int{roleOwner: 200, roleViewer: 200, rolePoster: 200, roleStranger: 404, roleAdmin: 404} {
		t.Run(string(role), func(t *testing.T) {
			resp := markRead(m.as(t, role), room.ID, nil)
			if want == http.StatusNotFound {
				resp.RequireError(want, dto.ErrorCodeNotFound)
				return
			}
			resp.RequireStatus(want)
		})
	}
	t.Run("a DM viewed through an AI", func(t *testing.T) {
		atlas := m.env.NewAI(owner, "atlas")
		shareAI(owner, atlas, poster)
		dm := dmWith(poster, atlas, http.StatusCreated)
		say(poster, dm.ID, "hi atlas")
		markRead(owner, dm.ID, nil).RequireStatus(http.StatusOK)
		markRead(m.as(t, roleStranger), dm.ID, nil).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	})
	t.Run("agent token", func(t *testing.T) {
		agent := owner.Token(dto.TokenKindAgent, "laptop")
		agent.T = t
		markRead(agent, room.ID, nil).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		agent.Get("/api/v1/conversations").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	})
	t.Run("anonymous", func(t *testing.T) {
		anon := m.env.Anon()
		anon.T = t
		markRead(anon, room.ID, nil).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
		anon.Get("/api/v1/conversations").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	})
}

// DM-07, LIVE-02: marking read emits room.read with the position that holds to every socket of the
// reading user, also the ones of other tabs, and to nobody else; the message frames still reach
// everybody who can view the room.
func TestMarkReadEmitsRoomReadToTheReaderOnly(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := createRoom(alice, "Ops")
	addToRoom(t, env, alice, room.ID, bob)
	say(bob, room.ID, "one")
	say(bob, room.ID, "two")
	aliceTab1, aliceTab2, bobLive := dialLive(t, alice), dialLive(t, alice), dialLive(t, bob)

	markRead(alice, room.ID, seqPtr(2)).RequireStatus(http.StatusOK)
	for _, tab := range []*testkit.LiveConn{aliceTab1, aliceTab2} {
		var got dto.ReadData
		tab.Expect(dto.LiveEventTypeRoomRead, &got, liveWait)
		require.Equal(t, dto.ReadData{RoomID: room.ID, Seq: 2}, got)
	}
	bobLive.ExpectNone(quiet)

	// A lower position still answers with the position that holds, never a regression.
	markRead(alice, room.ID, seqPtr(1)).RequireStatus(http.StatusOK)
	var held dto.ReadData
	aliceTab1.Expect(dto.LiveEventTypeRoomRead, &held, liveWait)
	require.Equal(t, int64(2), held.Seq)
	bobLive.ExpectNone(quiet)
}

// LAY-01 data: the preview is the first 80 code points of the last message, with the sender's name
// in groups; an attachment without text previews as [file name]; text wins over the attachment.
func TestConversationPreview(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	patchProfile(bob, bob.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Bob B.")}).RequireStatus(http.StatusOK)
	dm := dmWith(alice, bob.User.ID, http.StatusCreated)
	group := createRoom(alice, "Ops")
	addToRoom(t, env, alice, group.ID, bob)

	preview := func(c *testkit.Client, roomID string) dto.MessagePreview {
		t.Helper()
		lm := conversationIn(t, conversationsOf(t, c), roomID).LastMessage
		require.NotNil(t, lm)
		return *lm
	}

	// 80 code points: multi-byte text counts code points, not bytes.
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"79", strings.Repeat("é", 79), strings.Repeat("é", 79)},
		{"80", strings.Repeat("é", 80), strings.Repeat("é", 80)},
		{"81", strings.Repeat("é", 81), strings.Repeat("é", 80)},
		{"long", strings.Repeat("ab", 200), strings.Repeat("ab", 40)},
		{"emoji", strings.Repeat("😀", 100), strings.Repeat("😀", 80)},
		{"short", "ok", "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := say(bob, group.ID, tc.text)
			p := preview(alice, group.ID)
			require.Equal(t, tc.want, p.Text)
			require.Equal(t, []any{sent.Seq, bob.User.ID, "Bob B."}, []any{p.Seq, p.AuthorID, p.AuthorName}, "the sender's name in a group")
			require.True(t, p.CreatedAt.Equal(sent.CreatedAt))
		})
	}

	// The same text in a DM: the author is identified, the name prefix is a group matter.
	say(bob, dm.ID, "hello in the dm")
	p := preview(alice, dm.ID)
	require.Equal(t, []any{"hello in the dm", bob.User.ID, ""}, []any{p.Text, p.AuthorID, p.AuthorName})

	// An attachment without text previews as [file name]; with text, the text.
	postMultipart(bob, group.ID, dto.PostMessageRequest{}, upload{name: "report.pdf", mime: "application/pdf", content: "pdf"}).RequireStatus(http.StatusCreated)
	require.Equal(t, "[report.pdf]", preview(alice, group.ID).Text)
	postMultipart(bob, group.ID, dto.PostMessageRequest{Text: "see the file"}, upload{name: "notes.txt", mime: "text/plain", content: "x"}).RequireStatus(http.StatusCreated)
	require.Equal(t, "see the file", preview(alice, group.ID).Text)
	postMultipart(bob, group.ID, dto.PostMessageRequest{Text: "   "}, upload{name: "a.txt", mime: "text/plain", content: "x"}, upload{name: "b.txt", mime: "text/plain", content: "y"}).RequireStatus(http.StatusCreated)
	require.Equal(t, "[a.txt]", preview(alice, group.ID).Text, "blank text and several files: the first file")
}

// LAY-01: member names are "Name @handle", up to 20, in the order the members were added, for the
// sidebar search; AIs and users alike.
func TestConversationMemberNames(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "Big")
	want := []string{}
	for i := range 25 {
		u := env.NewUser(fmt.Sprintf("user%02d", i))
		addToRoom(t, env, alice, room.ID, u)
		want = append(want, fmt.Sprintf("user%02d @user%02d", i, i))
	}
	got := conversationIn(t, conversationsOf(t, alice), room.ID).MemberNames
	require.Equal(t, want[:20], got)

	atlas := env.NewAI(alice, "atlas")
	small := createRoom(alice, "Small")
	alice.Post("/api/v1/rooms/"+small.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: atlas}).RequireStatus(http.StatusCreated)
	require.Equal(t, []string{"atlas @atlas"}, conversationIn(t, conversationsOf(t, alice), small.ID).MemberNames)
}

// LAY-01 with retention (OPS-02): the preview is the newest message that still exists; when the
// retention job purged them all the conversation has no preview, no unread and sorts by its
// creation.
func TestConversationListAfterRetentionPurgesMessages(t *testing.T) {
	env, clock := clockEnv(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := testkit.Decode[dto.Room](alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Short lived", RetentionDays: ptr(1)}).RequireStatus(http.StatusCreated))
	addToRoom(t, env, alice, room.ID, bob)
	say(bob, room.ID, "old")
	clock.Advance(30 * time.Hour)
	say(bob, room.ID, "recent")

	_, err := env.App.Rooms.Cleanup(context.Background())
	require.NoError(t, err)
	c := conversationIn(t, conversationsOf(t, alice), room.ID)
	require.NotNil(t, c.LastMessage)
	require.Equal(t, "recent", c.LastMessage.Text, "the old message was purged, the recent one is the preview")
	require.Equal(t, 1, c.Unread)

	clock.Advance(30 * time.Hour)
	_, err = env.App.Rooms.Cleanup(context.Background())
	require.NoError(t, err)
	c = conversationIn(t, conversationsOf(t, alice), room.ID)
	require.Nil(t, c.LastMessage)
	require.Zero(t, c.Unread)
	require.True(t, c.ActivityAt.Equal(room.CreatedAt))
}

// ORG-02, OMEM-03 (T56, gap G1): a conversation says which organizations it belongs to. A group belongs to its
// organization; a DM to the organizations both of its participants are members of, so one with a colleague in two
// of them lists both; a DM whose participants share none is listed under the default organization of whoever lists
// it; and the tags follow the memberships: once the colleague left one organization, the DM lists the other.
func TestConversationsCarryTheirOrganizations(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	ana, bea, cid := env.NewUser("ana"), env.NewUser("bea"), env.NewUser("cid")
	anas, beas, cids := ana.PersonalOrg().ID, bea.PersonalOrg().ID, cid.PersonalOrg().ID
	// ana and bea belong to each other's personal organization, so they share two; cid belongs to neither.
	env.AddMember(anas, bea.User.ID, orgs.PermissionMember, "")
	env.AddMember(beas, ana.User.ID, orgs.PermissionMember, "")
	// ana's default is bea's organization, not the first one she has: a default is not "the first organization".
	require.NoError(t, env.App.Orgs.SetDefault(ctx, ana.User.ID, beas))

	group := groupIn(t, env, ana, anas, "Crew")
	withBea := dmWith(ana, bea.User.ID, http.StatusCreated)
	withCid := dmWith(ana, cid.User.ID, http.StatusCreated)
	scout := env.NewAIIn(ana, anas, "scout")
	withScout := dmWith(ana, scout, http.StatusCreated)
	tags := func(c *testkit.Client, roomID string) []string {
		t.Helper()
		got := conversationIn(t, conversationsOf(t, c), roomID).OrgIDs
		require.NotNil(t, got, "no tag reads [], never null")
		return got
	}

	require.Equal(t, []string{anas}, tags(ana, group.ID), "a group lists its organization")
	require.ElementsMatch(t, []string{anas, beas}, tags(ana, withBea.ID), "a DM with a colleague in two shared organizations lists both")
	require.ElementsMatch(t, []string{anas, beas}, tags(bea, withBea.ID), "from either side")
	require.Equal(t, []string{anas}, tags(ana, withScout.ID), "an AI is a member of its organization like a user")
	require.Equal(t, []string{beas}, tags(ana, withCid.ID), "no shared organization: the default organization of who lists the DM")
	require.Equal(t, []string{cids}, tags(cid, withCid.ID), "each side lists it under their own")

	// GET /rooms/{id} and GET /rooms (and the live room.changed, below) carry the same tags.
	room := func(c *testkit.Client, id string) dto.Room {
		return testkit.Decode[dto.Room](c.Get("/api/v1/rooms/" + id).RequireStatus(http.StatusOK))
	}
	require.Equal(t, []string{anas}, room(ana, group.ID).OrgIDs)
	require.ElementsMatch(t, []string{anas, beas}, room(bea, withBea.ID).OrgIDs)
	require.Equal(t, []string{cids}, room(cid, withCid.ID).OrgIDs)
	for _, r := range testkit.Decode[dto.List[dto.Room]](ana.Get("/api/v1/rooms").RequireStatus(http.StatusOK)).Items {
		if r.ID == withCid.ID {
			require.Equal(t, []string{beas}, r.OrgIDs)
		}
	}

	require.NoError(t, env.App.Orgs.Leave(ctx, bea.User.ID, anas))
	require.Equal(t, []string{beas}, tags(ana, withBea.ID), "after bea left one organization the DM lists the one they still share")
	require.Equal(t, []string{beas}, tags(bea, withBea.ID))
	require.Equal(t, []string{anas}, tags(ana, group.ID), "a group keeps its organization")
}

// ORG-02 (T56): the live room.changed frame is rendered for the socket's user and carries the same tags as the list,
// so the sidebar can file a conversation the moment it appears.
func TestRoomChangedFrameCarriesTheOrganizations(t *testing.T) {
	env := testkit.New(t)
	ana, bea, cid := env.NewUser("ana"), env.NewUser("bea"), env.NewUser("cid")
	anas, beas, cids := ana.PersonalOrg().ID, bea.PersonalOrg().ID, cid.PersonalOrg().ID
	env.AddMember(anas, bea.User.ID, orgs.PermissionMember, "")
	env.AddMember(beas, ana.User.ID, orgs.PermissionMember, "")
	beaLive, cidLive := dialLive(t, bea), dialLive(t, cid)

	room := dmWith(ana, bea.User.ID, http.StatusCreated)
	var seen dto.Room
	beaLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, room.ID, seen.ID)
	require.ElementsMatch(t, []string{anas, beas}, seen.OrgIDs)

	room = dmWith(ana, cid.User.ID, http.StatusCreated)
	cidLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, room.ID, seen.ID)
	require.Equal(t, []string{cids}, seen.OrgIDs, "the frame is rendered for cid: no organization is shared, so cid's default")

	group := groupIn(t, env, ana, anas, "Crew")
	ana.Post("/api/v1/rooms/"+group.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindUser, MemberID: bea.User.ID}).RequireStatus(http.StatusCreated)
	beaLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, group.ID, seen.ID)
	require.Equal(t, []string{anas}, seen.OrgIDs)
}
