package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/testkit"
)

// aiEnv is alice with her AIs atlas and orion and an agent token, and bob with his AI sage and
// token; nothing is shared yet.
type aiEnv struct {
	env              *testkit.Env
	alice, bob       *testkit.Client
	atlas, orion     string
	sage             string
	aliceAI, bobAI   *testkit.Client // agent clients acting as atlas and as sage
	aliceTok, bobTok *testkit.Client // agent tokens, for other folders
}

func newAIEnv(t *testing.T, opts ...testkit.Option) *aiEnv {
	t.Helper()
	env := testkit.New(t, opts...)
	f := &aiEnv{env: env, alice: env.NewUser("alice"), bob: env.NewUser("bob")}
	f.atlas, f.orion = env.NewAI(f.alice, "atlas"), env.NewAI(f.alice, "orion")
	f.sage = env.NewAI(f.bob, "sage")
	f.aliceTok, f.bobTok = f.alice.Token(dto.TokenKindAgent, "laptop"), f.bob.Token(dto.TokenKindAgent, "desktop")
	f.aliceAI, f.bobAI = f.aliceTok.AsAgent("atlas"), f.bobTok.AsAgent("sage")
	return f
}

// sendDM calls POST /agent/dms as the AI behind c.
func sendDM(c *testkit.Client, to, text string) *testkit.Response {
	c.T.Helper()
	return c.Post("/api/v1/agent/dms", dto.AgentDMRequest{To: to, Text: text})
}

// agentGroup calls POST /agent/groups as the AI behind c.
func agentGroup(c *testkit.Client, name string, members ...string) *testkit.Response {
	c.T.Helper()
	return c.Post("/api/v1/agent/groups", dto.CreateGroupRequest{Name: name, Members: members})
}

// DM-04: an AI writes to its owner without ever having talked to them: the DM is created, the
// message is in it, and it sits at the top of the owner's conversation list, unread. The target
// can be named by @handle, handle or id; the next messages go to the same DM.
func TestAIOpensADMWithItsOwner(t *testing.T) {
	f := newAIEnv(t)
	aliceLive := dialLive(t, f.alice)

	first := testkit.Decode[dto.Message](sendDM(f.aliceAI, "@alice", "hi, the deploy finished").RequireStatus(http.StatusCreated))
	require.Equal(t, []any{f.atlas, "hi, the deploy finished"}, []any{first.Author.ID, first.Text})
	dm := first.RoomID

	list := conversationsOf(t, f.alice)
	require.Equal(t, []string{dm}, conversationIDs(list))
	c := list[0]
	require.Equal(t, dto.ConversationKindDM, c.Kind)
	require.Equal(t, "atlas", c.Title)
	require.Equal(t, []any{f.atlas, dto.IntelligenceRoleAI}, []any{c.Counterpart.ID, c.Counterpart.Role})
	require.Equal(t, dto.LevelPost, c.MyLevel)
	require.Equal(t, 1, c.Unread)
	require.Equal(t, "hi, the deploy finished", c.LastMessage.Text)
	room := testkit.Decode[dto.Room](f.alice.Get("/api/v1/rooms/" + dm).RequireStatus(http.StatusOK))
	require.Equal(t, alicePair(f), participantIDs(room), "the DM is between alice and atlas")
	require.Equal(t, f.alice.User.ID, room.Owner.ID, "an AI's DM belongs to its owner")

	// The owner's socket learns the DM and its first message.
	var seen dto.Room
	aliceLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, dm, seen.ID)
	var msg dto.Message
	aliceLive.Expect(dto.LiveEventTypeRoomMessage, &msg, liveWait)
	require.Equal(t, first.ID, msg.ID)

	// By plain handle, in any case, and by id: the same DM, no new conversation.
	for _, to := range []string{"alice", "ALICE", f.alice.User.ID} {
		again := testkit.Decode[dto.Message](sendDM(f.aliceAI, to, "again via "+to).RequireStatus(http.StatusCreated))
		require.Equal(t, dm, again.RoomID, to)
	}
	require.Equal(t, []string{dm}, conversationIDs(conversationsOf(t, f.alice)))
	require.Equal(t, 4, conversationIn(t, conversationsOf(t, f.alice), dm).Unread)

	// Alice answers in the DM; it is a conversation like any other.
	say(f.alice, dm, "thanks atlas")
	require.Len(t, timelineOf(t, f.alice, dm), 5)
}

func alicePair(f *aiEnv) []string {
	ids := []string{f.alice.User.ID, f.atlas}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	return ids
}

func participantIDs(r dto.Room) []string {
	out := make([]string, len(r.Participants))
	for i, p := range r.Participants {
		out[i] = p.ID
	}
	return out
}

// DM-04: an AI reaches the intelligences its owner can see, and nothing else, with one answer for
// an unknown target and an invisible one; and nothing is created or counted for a refused request.
func TestAIDMTargetsAreThoseItsOwnerSees(t *testing.T) {
	f := newAIEnv(t)
	connector, _ := storeChat(t, f.env, f.alice.User.ID, "chat")
	person := seedPerson(t, f.env, connector, "5511999990000", "Contact")

	// What an AI sees is what its owner sees: bob cannot see atlas yet, so neither can his AI.
	sendDM(f.bobAI, "@atlas", "bob's AI cannot see atlas").RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)

	// Visible: the owner's other AI and another user.
	sendDM(f.aliceAI, "@orion", "hello orion").RequireStatus(http.StatusCreated)
	sendDM(f.aliceAI, "@bob", "hello bob").RequireStatus(http.StatusCreated)

	// Invisible: bob's AI is not shared, an unknown handle and an unknown id are no better.
	hidden := sendDM(f.aliceAI, "@sage", "psst").RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	ghost := sendDM(f.aliceAI, "@ghost", "psst").RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	unknown := sendDM(f.aliceAI, store.NewID(), "psst").RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	sendDM(f.aliceAI, f.sage, "by id").RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	require.Equal(t, hidden, ghost, "an AI the owner cannot see answers like a handle that does not exist")
	require.Equal(t, hidden, unknown)

	// Refusals of the DM itself.
	sendDM(f.aliceAI, "@atlas", "me").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeDMSelf)
	sendDM(f.aliceAI, f.atlas, "me").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeDMSelf)
	sendDM(f.aliceAI, person, "hello").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson)

	// Bob shares sage: now alice's AI sees it; and what sage sees is bob's to decide.
	shareAI(f.bob, f.sage, f.alice)
	sendDM(f.aliceAI, "@sage", "now visible").RequireStatus(http.StatusCreated)
	// Atlas wrote to bob, so bob now shares a conversation with it and sees it: so does his AI.
	sendDM(f.bobAI, "@atlas", "now it can").RequireStatus(http.StatusCreated)

	// atlas's DMs are atlas's: alice, its owner, views them and lists none; bob is a participant
	// of the one atlas opened with him.
	require.Empty(t, conversationsOf(t, f.alice))
	require.Len(t, conversationsOf(t, f.bob), 1)
}

// DM-04: invalid requests create nothing and use none of the AI's allowance.
func TestAIDMValidation(t *testing.T) {
	f := newAIEnv(t)
	for name, tc := range map[string]struct {
		req   dto.AgentDMRequest
		field string
	}{
		"no target":  {dto.AgentDMRequest{Text: "hi"}, "to"},
		"just an @":  {dto.AgentDMRequest{To: "@", Text: "hi"}, "to"},
		"no text":    {dto.AgentDMRequest{To: "@alice"}, "text"},
		"blank text": {dto.AgentDMRequest{To: "@alice", Text: "  \n"}, "text"},
		"long text":  {dto.AgentDMRequest{To: "@alice", Text: strings.Repeat("x", dto.MaxTextLength+1)}, "text"},
	} {
		t.Run(name, func(t *testing.T) {
			c := *f.aliceAI
			c.T = t
			e := c.Post("/api/v1/agent/dms", tc.req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
			require.Contains(t, e.Fields, tc.field)
		})
	}
	c := *f.aliceAI
	c.Do(http.MethodPost, "/api/v1/agent/dms", map[string]any{"to": 7}).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)
	require.Empty(t, conversationsOf(t, f.alice), "nothing was created")

	// Invalid requests spend none of the 30 posts a minute: after more than that, a good one works.
	for range 35 {
		sendDM(f.aliceAI, "@alice", " ").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	}

	// A post the room refuses after the DM was created (a chat id that is no chat of a new DM)
	// leaves no empty DM behind.
	e := f.aliceAI.Post("/api/v1/agent/dms", dto.AgentDMRequest{To: "@alice", Text: "hi", ChatIDs: []string{"no-such-chat"}}).
		RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Contains(t, e.Fields, "recipients")
	require.Empty(t, conversationsOf(t, f.alice), "no empty DM was left")
	ok := testkit.Decode[dto.Message](f.aliceAI.Post("/api/v1/agent/dms", dto.AgentDMRequest{To: "@alice", Text: "fine"}).RequireStatus(http.StatusCreated))
	require.NotEmpty(t, ok.RoomID)
	require.Len(t, conversationsOf(t, f.alice), 1)
}

// DM-04 + DM-03: an AI writing to another AI that is connected reaches it as a DM from that AI,
// untrusted when the two have different owners, and the answer goes back into the same DM.
func TestAIDMReachesALiveAI(t *testing.T) {
	f := newAIEnv(t)
	shareAI(f.alice, f.atlas, f.bob)
	conn, resp := f.aliceTok.DialAgent("atlas")
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	conn.Welcome(wsWait)

	sent := testkit.Decode[dto.Message](sendDM(f.bobAI, "@atlas", "hello from sage").RequireStatus(http.StatusCreated))
	got := conn.Message(wsWait)
	require.Equal(t, dto.RoomRef{ID: sent.RoomID, Name: "sage", Kind: dto.ConversationKindDM}, got.Room)
	require.False(t, got.Trusted, "sage belongs to bob, atlas to alice")
	meta := channel.Format(got.AgentMessage).Meta
	require.Equal(t, []string{"dm", "sage", f.sage, "sage", "agent"}, []string{meta["conversation"], meta["author"], meta["author_id"], meta["author_handle"], meta["author_kind"]})

	reply := testkit.Decode[dto.Message](f.aliceAI.Post("/api/v1/agent/messages/"+sent.ID+"/reply", dto.ReplyRequest{Text: "hello sage"}).RequireStatus(http.StatusCreated))
	require.Equal(t, sent.RoomID, reply.RoomID)
	// Both owners view the DM of their AIs; neither is a participant.
	for _, c := range []*testkit.Client{f.alice, f.bob} {
		r := testkit.Decode[dto.Room](c.Get("/api/v1/rooms/" + sent.RoomID).RequireStatus(http.StatusOK))
		require.Equal(t, dto.LevelView, r.MyLevel)
	}
	require.Empty(t, conversationsOf(t, f.alice))
}

// AGRP-01: an AI creates a group with its owner and another AI its owner can use: the group belongs
// to the owner, the AI is a member and posts in it, and the members see it. Members are named by
// @handle, handle or id.
func TestAICreatesAGroup(t *testing.T) {
	f := newAIEnv(t)
	aliceLive := dialLive(t, f.alice)

	created := testkit.Decode[dto.CreatedGroup](agentGroup(f.aliceAI, "Ops", "@alice", f.orion).RequireStatus(http.StatusCreated))
	require.Equal(t, "Ops", created.Name)
	require.NotEmpty(t, created.Room)

	room := testkit.Decode[dto.Room](f.alice.Get("/api/v1/rooms/" + created.Room).RequireStatus(http.StatusOK))
	require.Equal(t, []any{dto.ConversationKindGroup, "Ops", f.alice.User.ID, dto.LevelManage, 3}, []any{room.Kind, room.Name, room.Owner.ID, room.MyLevel, room.MemberCount})
	members := membersOf(t, f.alice, created.Room)
	require.Equal(t, dto.MemberKindAgent, members[f.atlas].Kind, "the creating AI is a member")
	require.Equal(t, dto.MemberKindAgent, members[f.orion].Kind)
	require.Equal(t, dto.MemberKindUser, members[f.alice.User.ID].Kind)
	require.Equal(t, []string{created.Room}, conversationIDs(conversationsOf(t, f.alice)))

	var seen dto.Room
	aliceLive.Expect(dto.LiveEventTypeRoomChanged, &seen, liveWait)
	require.Equal(t, created.Room, seen.ID)

	// The AI posts in its group, and lists it with its other rooms.
	posted := testkit.Decode[dto.Message](f.aliceAI.Post("/api/v1/agent/rooms/"+created.Room+"/messages", dto.PostMessageRequest{Text: "group is ready"}).RequireStatus(http.StatusCreated))
	require.Equal(t, created.Room, posted.RoomID)
	rooms := testkit.Decode[dto.List[dto.AgentRoom]](f.aliceAI.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK))
	require.Len(t, rooms.Items, 1)
	require.Equal(t, []any{created.Room, dto.ConversationKindGroup, "Ops"}, []any{rooms.Items[0].ID, rooms.Items[0].Kind, rooms.Items[0].Name})

	// By plain handle and id, with the AI listing itself, a description-less group of one.
	again := testkit.Decode[dto.CreatedGroup](agentGroup(f.aliceAI, "Pair", "alice", f.atlas).RequireStatus(http.StatusCreated))
	require.Equal(t, 2, testkit.Decode[dto.Room](f.alice.Get("/api/v1/rooms/"+again.Room).RequireStatus(http.StatusOK)).MemberCount)
	alone := testkit.Decode[dto.CreatedGroup](agentGroup(f.aliceAI, "Alone").RequireStatus(http.StatusCreated))
	require.Equal(t, 1, testkit.Decode[dto.Room](f.alice.Get("/api/v1/rooms/"+alone.Room).RequireStatus(http.StatusOK)).MemberCount)
}

// AGRP-02: a member the owner may not add refuses the AI's group: 422 members_refused listing every
// refused member as the AI named it, nothing created, and the request costs no allowance.
func TestAIGroupRefusesAndCreatesNothing(t *testing.T) {
	f := newAIEnv(t)
	connector, _ := storeChat(t, f.env, f.alice.User.ID, "chat")
	person := seedPerson(t, f.env, connector, "5511999990000", "Contact")
	personHandle := profileOf(t, f.alice, person).Handle

	e := agentGroup(f.aliceAI, "Ops", "@alice", "@sage", "@"+personHandle, "@ghost", f.orion).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
	require.Equal(t, []dto.RefusedMember{
		{ID: "@sage", Reason: dto.RefusedReasonNotFound},
		{ID: "@" + personHandle, Reason: dto.RefusedReasonPerson},
		{ID: "@ghost", Reason: dto.RefusedReasonNotFound},
	}, e.Refused, "as the AI named them, in order, valid members left out")
	require.Empty(t, conversationsOf(t, f.alice), "nothing was created")
	require.Empty(t, testkit.Decode[dto.List[dto.AgentRoom]](f.aliceAI.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)).Items)

	// A person of a connector bob's AI cannot reach is as unknown to it as a ghost.
	hiddenPerson := agentGroup(f.bobAI, "Ops", "@"+personHandle).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
	require.Equal(t, []dto.RefusedMember{{ID: "@" + personHandle, Reason: dto.RefusedReasonNotFound}}, hiddenPerson.Refused)

	// Once bob shares sage the same request works for it.
	shareAI(f.bob, f.sage, f.alice)
	ok := agentGroup(f.aliceAI, "Ops", "@alice", "@sage", f.orion).RequireStatus(http.StatusCreated)
	require.Equal(t, 4, testkit.Decode[dto.Room](f.alice.Get("/api/v1/rooms/"+testkit.Decode[dto.CreatedGroup](ok).Room).RequireStatus(http.StatusOK)).MemberCount)

	// Invalid requests are validation errors.
	for name, req := range map[string]dto.CreateGroupRequest{
		"no name":     {Members: []string{"@alice"}},
		"blank entry": {Name: "x", Members: []string{"@alice", " "}},
	} {
		t.Run(name, func(t *testing.T) {
			c := *f.aliceAI
			c.T = t
			c.Post("/api/v1/agent/groups", req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		})
	}
}

// DM-04, AGRP-01: an AI creates at most 10 new conversations per hour, DMs and groups together; a
// reopened DM, a refused request and another AI do not count; an hour later it may create again.
func TestAIConversationRateLimit(t *testing.T) {
	clock := testkit.NewClock(time.Now().UTC().Truncate(time.Second))
	f := newAIEnv(t, testkit.WithClock(clock.Now))
	people := make([]*testkit.Client, 12)
	for i := range people {
		people[i] = f.env.NewUser(fmt.Sprintf("friend%02d", i))
	}

	// Nine DMs and a group are the ten.
	for _, p := range people[:9] {
		sendDM(f.aliceAI, "@"+handleOf(t, p), "hi").RequireStatus(http.StatusCreated)
		clock.Advance(time.Second)
	}
	agentGroup(f.aliceAI, "Ten", "@alice").RequireStatus(http.StatusCreated)

	// The eleventh new conversation, a DM or a group, is refused and creates nothing.
	e := sendDM(f.aliceAI, "@"+handleOf(t, people[9]), "one too many").RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	require.NotEmpty(t, e.Message)
	agentGroup(f.aliceAI, "Eleven", "@alice").RequireError(http.StatusTooManyRequests, dto.ErrorCodeRateLimited)
	require.Empty(t, conversationsOf(t, people[9]))
	require.Len(t, conversationsOf(t, f.alice), 1, "only the group of the ten")

	// Reopening a DM is free, and so is anything that creates nothing.
	sendDM(f.aliceAI, "@"+handleOf(t, people[0]), "back again").RequireStatus(http.StatusCreated)
	sendDM(f.aliceAI, "@ghost", "no one").RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)
	agentGroup(f.aliceAI, "Refused", "@sage").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeMembersRefused)
	require.Len(t, timelineOf(t, people[0], conversationsOf(t, people[0])[0].ID), 2)

	// Another AI of the same owner has its own allowance.
	orion := f.aliceTok.AsAgent("orion")
	sendDM(orion, "@"+handleOf(t, people[9]), "orion can").RequireStatus(http.StatusCreated)

	// An hour after the first ones the window moves on.
	clock.Advance(time.Hour)
	sendDM(f.aliceAI, "@"+handleOf(t, people[10]), "an hour later").RequireStatus(http.StatusCreated)
}

func handleOf(t *testing.T, c *testkit.Client) string {
	t.Helper()
	return profileOf(t, c, c.User.ID).Handle
}

// Authorization of the AI routes: only an agent token acting as an AI reaches them; users with a
// session or a user token, anonymous callers and sessions that name no folder do not.
func TestAIConversationRoutesAuthorization(t *testing.T) {
	f := newAIEnv(t)
	userToken := f.alice.Token(dto.TokenKindUser, "script")
	for name, c := range map[string]*testkit.Client{"session": f.alice, "user token": userToken} {
		t.Run(name, func(t *testing.T) {
			c := *c
			c.T = t
			sendDM(&c, "@bob", "hi").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			agentGroup(&c, "Ops", "@bob").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		})
	}
	t.Run("anonymous", func(t *testing.T) {
		anon := f.env.Anon()
		anon.T = t
		sendDM(anon, "@bob", "hi").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
		agentGroup(anon, "Ops").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	})
	t.Run("no folder", func(t *testing.T) {
		tok := *f.aliceTok
		tok.T = t
		sendDM(&tok, "@bob", "hi").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeFolderRequired)
		agentGroup(&tok, "Ops").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeFolderRequired)
	})
	require.Empty(t, conversationsOf(t, f.bob), "nothing of that reached bob")
}
